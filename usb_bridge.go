package doorphoneserver

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	usbSerialPath    = "/dev/esp32"
	usbBaudRate      = 115200
	usbPingInterval  = 5 * time.Second
	usbRetryDelay    = 2 * time.Second
	usbRetryLogEvery = 15
	usbSendBufSize   = 32
	usbEvtBufSize    = 64
)

const cardLogMax = 50

// ESP32CardLog registra un evento tessera ricevuto dall'ESP32-S3.
type ESP32CardLog struct {
	Time   time.Time `json:"time"`
	Result string    `json:"result"`
}

type esp32State struct {
	mu        sync.Mutex
	connected bool
	pins      map[string]int
	cardLog   []ESP32CardLog
}

func newESP32State() *esp32State {
	return &esp32State{pins: make(map[string]int)}
}

func (s *esp32State) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

func (s *esp32State) isConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *esp32State) updatePin(pin string, value int) {
	s.mu.Lock()
	s.pins[pin] = value
	s.mu.Unlock()
}

func (s *esp32State) resetPins() {
	s.mu.Lock()
	s.pins = make(map[string]int)
	s.mu.Unlock()
}

func (s *esp32State) addCard(entry ESP32CardLog) {
	s.mu.Lock()
	s.cardLog = append(s.cardLog, entry)
	if len(s.cardLog) > cardLogMax {
		s.cardLog = s.cardLog[len(s.cardLog)-cardLogMax:]
	}
	s.mu.Unlock()
}

func (s *esp32State) clearCards() {
	s.mu.Lock()
	s.cardLog = nil
	s.mu.Unlock()
}

func (s *esp32State) snapshot() (connected bool, pins map[string]int, logs []ESP32CardLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	connected = s.connected
	pins = make(map[string]int, len(s.pins))
	for k, v := range s.pins {
		pins[k] = v
	}
	logs = make([]ESP32CardLog, len(s.cardLog))
	copy(logs, s.cardLog)
	return
}

// GPIOEvent rappresenta un cambio di stato su un pin GPIO dell'ESP32-S3.
type GPIOEvent struct {
	Pin   string
	Value int
}

// CardEvent rappresenta il risultato di un'autenticazione DESFire EV3.
type CardEvent struct {
	OK bool
}

// NfcEvent rappresenta la lettura di un tag NFC dall'ESP32-S3.
type NfcEvent struct {
	TagID string
}

// tagListResult raccoglie il risultato di una richiesta TAG-LIST.
type tagListResult struct {
	tags map[string]string
}

const usbLogMax = 200

// usbLogBuf è il ring buffer globale del log USB (← e →).
var (
	usbLogMu  sync.Mutex
	usbLogBuf []string
)

// usbLogAppend aggiunge una riga al ring buffer del log USB.
func usbLogAppend(line string) {
	usbLogMu.Lock()
	usbLogBuf = append(usbLogBuf, line)
	if len(usbLogBuf) > usbLogMax {
		usbLogBuf = usbLogBuf[len(usbLogBuf)-usbLogMax:]
	}
	usbLogMu.Unlock()
}

// USBLogSnapshot ritorna una copia del ring buffer attuale.
func USBLogSnapshot() []string {
	usbLogMu.Lock()
	defer usbLogMu.Unlock()
	out := make([]string, len(usbLogBuf))
	copy(out, usbLogBuf)
	return out
}

// USBLogClear svuota il ring buffer.
func USBLogClear() {
	usbLogMu.Lock()
	usbLogBuf = nil
	usbLogMu.Unlock()
}

// USBBridge gestisce la connessione seriale con l'ESP32-S3.
// Thread-safe. I canali GpioEvt, CardEvt e NfcEvt sono read-only per i consumer.
type USBBridge struct {
	GpioEvt <-chan GPIOEvent
	CardEvt <-chan CardEvent
	NfcEvt  <-chan NfcEvent
	State   *esp32State

	gpioCh chan GPIOEvent
	cardCh chan CardEvent
	nfcCh  chan NfcEvent
	sendCh chan string

	nfcMgr *NFCWhitelistManager

	// pending: risposte attese da un singolo comando (ACK, TAG-DEL)
	pendingMu  sync.Mutex
	pendingMap map[string]chan string

	// TAG-LIST: raccolta multi-riga
	tagListMu  sync.Mutex
	tagListCh  chan tagListResult
	tagListBuf map[string]string
}

// NewUSBBridge crea il bridge e avvia la connessione in background.
func NewUSBBridge(ctx context.Context) *USBBridge {
	b := &USBBridge{
		gpioCh:     make(chan GPIOEvent, usbEvtBufSize),
		cardCh:     make(chan CardEvent, usbEvtBufSize),
		nfcCh:      make(chan NfcEvent, usbEvtBufSize),
		sendCh:     make(chan string, usbSendBufSize),
		State:      newESP32State(),
		pendingMap: make(map[string]chan string),
	}
	b.GpioEvt = b.gpioCh
	b.CardEvt = b.cardCh
	b.NfcEvt = b.nfcCh
	go b.connectLoop(ctx)
	return b
}

// SetNFCManager collega il bridge al gestore whitelist NFC.
func (b *USBBridge) SetNFCManager(m *NFCWhitelistManager) {
	b.nfcMgr = m
}

// Send accoda un comando verso l'ESP32-S3. Non bloccante.
func (b *USBBridge) Send(msg string) {
	select {
	case b.sendCh <- msg:
	default:
		log.Printf("[USB] warn: send buffer pieno, scartato: %q", msg)
	}
}

// registerPending registra un canale per attendere una risposta con chiave key.
// Ritorna (channel, true) se registrato, (nil, false) se già presente.
func (b *USBBridge) registerPending(key string) (chan string, bool) {
	ch := make(chan string, 1)
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	if _, exists := b.pendingMap[key]; exists {
		return nil, false
	}
	b.pendingMap[key] = ch
	return ch, true
}

// clearPending rimuove una registrazione pending (cleanup in caso di timeout).
func (b *USBBridge) clearPending(key string) {
	b.pendingMu.Lock()
	delete(b.pendingMap, key)
	b.pendingMu.Unlock()
}

// signalPending invia un valore al canale pending registrato per key, se esiste.
func (b *USBBridge) signalPending(key, value string) {
	b.pendingMu.Lock()
	ch, ok := b.pendingMap[key]
	if ok {
		delete(b.pendingMap, key)
	}
	b.pendingMu.Unlock()
	if ok {
		select {
		case ch <- value:
		default:
		}
	}
}

// SendAndWait invia cmd e attende la risposta identificata da ackKey (timeout t).
// Ritorna la risposta e true, oppure "", false in caso di timeout o chiave già occupata.
func (b *USBBridge) SendAndWait(cmd, ackKey string, t time.Duration) (string, bool) {
	ch, ok := b.registerPending(ackKey)
	if !ok {
		log.Printf("[USB] warn: pending già registrato per %q, operazione rifiutata", ackKey)
		return "", false
	}
	b.Send(cmd)
	select {
	case resp := <-ch:
		return resp, true
	case <-time.After(t):
		b.clearPending(ackKey)
		return "", false
	}
}

// SendTagList invia TAG-LIST e raccoglie le risposte fino a TAG-LIST-END (timeout t).
// Ritorna mappa uid→"" o errore.
func (b *USBBridge) SendTagList(t time.Duration) (map[string]string, error) {
	ch := make(chan tagListResult, 1)

	b.tagListMu.Lock()
	if b.tagListCh != nil {
		b.tagListMu.Unlock()
		return nil, fmt.Errorf("TAG-LIST già in corso")
	}
	b.tagListCh = ch
	b.tagListBuf = make(map[string]string)
	b.tagListMu.Unlock()

	b.Send("TAG-LIST\n")

	select {
	case result := <-ch:
		return result.tags, nil
	case <-time.After(t):
		b.tagListMu.Lock()
		b.tagListCh = nil
		b.tagListBuf = nil
		b.tagListMu.Unlock()
		return nil, fmt.Errorf("timeout TAG-LIST")
	}
}

// ── Connessione ───────────────────────────────────────────────────────────────

func (b *USBBridge) connectLoop(ctx context.Context) {
	failCount := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("[USB] bridge fermato")
			return
		default:
		}

		port, err := serial.Open(usbSerialPath, &serial.Mode{
			BaudRate: usbBaudRate,
			DataBits: 8,
			Parity:   serial.NoParity,
			StopBits: serial.OneStopBit,
		})
		if err != nil {
			failCount++
			if failCount == 1 || failCount%usbRetryLogEvery == 0 {
				log.Printf("[USB] ESP32-S3 non disponibile (tentativo %d): %v", failCount, err)
			}
			select {
			case <-ctx.Done():
				log.Printf("[USB] bridge fermato")
				return
			case <-time.After(usbRetryDelay):
			}
			continue
		}

		failCount = 0
		log.Printf("[USB] ESP32-S3 connesso su %s", usbSerialPath)
		b.State.setConnected(true)
		b.drainSendCh()
		b.runSession(ctx, port)
		b.State.setConnected(false)
		b.State.resetPins()
		log.Printf("[USB] connessione persa — riconnessione in %v", usbRetryDelay)

		select {
		case <-ctx.Done():
			log.Printf("[USB] bridge fermato")
			return
		case <-time.After(usbRetryDelay):
		}
	}
}

func (b *USBBridge) runSession(ctx context.Context, port serial.Port) {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var closeOnce sync.Once
	closePort := func() {
		closeOnce.Do(func() { port.Close() })
	}
	defer closePort()

	go b.writeLoop(sessionCtx, port, closePort)
	go b.pingLoop(sessionCtx)
	b.readLoop(sessionCtx, port)
}

func (b *USBBridge) readLoop(ctx context.Context, port serial.Port) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[USB] panic in readLoop: %v", r)
		}
	}()

	scanner := bufio.NewScanner(port)
	for {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				log.Printf("[USB] errore lettura: %v", err)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		b.dispatch(strings.TrimSpace(scanner.Text()))
	}
}

// ── Dispatch ──────────────────────────────────────────────────────────────────

func (b *USBBridge) dispatch(line string) {
	if line == "" {
		return
	}
	log.Printf("[USB] ← %s", line)
	usbLogAppend("← " + line)

	switch {
	case line == "UID-OK":
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: line})
		b.drainOrSendCard(CardEvent{OK: true})
		if b.nfcMgr != nil {
			b.nfcMgr.HandleUIDOK()
			// Flusso E: il Pi apre il portone dopo auth NFC OK
			b.Send("SET unlockdoor pulse\n")
		}

	case line == "UID-KO":
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: line})
		b.drainOrSendCard(CardEvent{OK: false})
		if b.nfcMgr != nil {
			b.nfcMgr.HandleUIDKO()
		}

	case line == "PONG":
		// watchdog ok

	case strings.HasPrefix(line, "EVT "):
		b.parseAndSendGPIO(line)

	case strings.HasPrefix(line, "ACK TAG-ENROLL PENDING"):
		b.signalPending("ACK-ENROLL", "OK")

	case strings.HasPrefix(line, "ACK TAG-FORMAT PENDING"):
		b.signalPending("ACK-FORMAT", "OK")

	case strings.HasPrefix(line, "ACK TAG-CLEAR"):
		b.signalPending("ACK-CLEAR", "OK")

	case strings.HasPrefix(line, "TAG-ENROLLED "):
		b.parseTagEnrolled(line)

	case strings.HasPrefix(line, "TAG-ENROLL-FAIL"):
		if b.nfcMgr != nil {
			b.nfcMgr.HandleEnrollFail(line)
		}

	case strings.HasPrefix(line, "TAG-FORMAT-OK "):
		b.parseTagFormatOK(line)

	case strings.HasPrefix(line, "TAG-FORMAT-FAIL NOT-DESFIRE"):
		if b.nfcMgr != nil {
			b.nfcMgr.HandleFormatFail("not-desfire")
		}

	case strings.HasPrefix(line, "TAG-FORMAT-FAIL"):
		if b.nfcMgr != nil {
			b.nfcMgr.HandleFormatFail("format-fail")
		}

	case line == "TAG-DEL-OK":
		b.signalPending("TAG-DEL", "OK")

	case strings.HasPrefix(line, "TAG-DEL-FAIL"):
		// "TAG-DEL-FAIL NOT-FOUND" o "TAG-DEL-FAIL BAD-UID"
		after := strings.TrimPrefix(line, "TAG-DEL-FAIL")
		b.signalPending("TAG-DEL", "FAIL"+strings.TrimSpace(after))

	case line == "TAG-LIST-START":
		b.tagListMu.Lock()
		if b.tagListBuf != nil {
			b.tagListBuf = make(map[string]string) // reset buffer
		}
		b.tagListMu.Unlock()

	case strings.HasPrefix(line, "TAG-ENTRY "):
		// "TAG-ENTRY <n> <uid>"
		b.handleTagEntry(line)

	case strings.HasPrefix(line, "TAG-LIST-END"):
		b.handleTagListEnd()

	case strings.HasPrefix(line, "ACK "):
		// conferma output GPIO eseguito

	case strings.HasPrefix(line, "ERR "):
		log.Printf("[USB] errore ESP32-S3: %s", line)

	default:
		log.Printf("[USB] riga non riconosciuta: %q", line)
	}
}

// parseAndSendGPIO parsa "EVT <pin> <0|1>" o "EVT nfc <uid>".
func (b *USBBridge) parseAndSendGPIO(line string) {
	parts := strings.Fields(line)
	if len(parts) != 3 {
		log.Printf("[USB] EVT malformato: %q", line)
		return
	}
	if strings.ToLower(parts[1]) == "nfc" {
		uid := strings.ToUpper(parts[2])
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: line})
		if b.nfcMgr != nil {
			b.nfcMgr.HandleNFCEvent(uid)
		}
		select {
		case b.nfcCh <- NfcEvent{TagID: uid}:
		default:
			log.Printf("[USB] warn: NFC channel pieno, EVT scartato")
		}
		return
	}
	val, err := strconv.Atoi(parts[2])
	if err != nil {
		log.Printf("[USB] EVT valore non numerico: %q", line)
		return
	}
	evt := GPIOEvent{Pin: parts[1], Value: val}
	b.State.updatePin(evt.Pin, evt.Value)
	select {
	case b.gpioCh <- evt:
	default:
		log.Printf("[USB] warn: GPIO channel pieno, EVT scartato: %+v", evt)
	}
}

// parseTagEnrolled processa "TAG-ENROLLED <uid> <PLAIN|DESFIRE>"
// [MODIFICATO] — il nome non fa più parte del protocollo ESP32.
func (b *USBBridge) parseTagEnrolled(line string) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		log.Printf("[USB] TAG-ENROLLED malformato (attesi 3 campi): %q", line)
		return
	}
	uid := strings.ToUpper(parts[1])
	tagType := strings.ToUpper(parts[2])

	log.Printf("[USB] tag enrolled: uid=%s tipo=%s", uid, tagType)
	if b.nfcMgr != nil {
		b.nfcMgr.HandleTagEnrolled(uid, tagType)
	}
}

// parseTagFormatOK processa "TAG-FORMAT-OK <uid>".
// REGOLA CRITICA: invia TAG-ENROLL PRIMA di notificare SSE (HandleTagFormatOK).
func (b *USBBridge) parseTagFormatOK(line string) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		log.Printf("[USB] TAG-FORMAT-OK malformato: %q", line)
		return
	}
	uid := strings.ToUpper(parts[1])
	log.Printf("[USB] tag format OK: uid=%s", uid)

	if b.nfcMgr != nil {
		// CRITICO: invia TAG-ENROLL prima di notificare SSE
		b.Send("TAG-ENROLL\n")
		b.nfcMgr.HandleTagFormatOK(uid)
	}
}

// handleTagEntry raccoglie "TAG-ENTRY <n> <uid>" nella mappa TAG-LIST.
func (b *USBBridge) handleTagEntry(line string) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		log.Printf("[USB] TAG-ENTRY malformato: %q", line)
		return
	}
	uid := strings.ToUpper(parts[2])
	b.tagListMu.Lock()
	if b.tagListBuf != nil {
		b.tagListBuf[uid] = ""
	}
	b.tagListMu.Unlock()
}

// handleTagListEnd finalizza la raccolta TAG-LIST e segnala il canale.
func (b *USBBridge) handleTagListEnd() {
	b.tagListMu.Lock()
	ch := b.tagListCh
	buf := b.tagListBuf
	b.tagListCh = nil
	b.tagListBuf = nil
	b.tagListMu.Unlock()

	if ch != nil {
		select {
		case ch <- tagListResult{tags: buf}:
		default:
		}
	}
}

// drainOrSendCard invia un CardEvent, scartando eventi non ancora consumati.
func (b *USBBridge) drainOrSendCard(evt CardEvent) {
	for {
		select {
		case <-b.cardCh:
			log.Printf("[USB] warn: CardEvent precedente non consumato scartato")
		default:
			b.cardCh <- evt
			return
		}
	}
}

// drainSendCh scarta comandi stale accumulati durante la disconnessione.
func (b *USBBridge) drainSendCh() {
	drained := 0
	for {
		select {
		case <-b.sendCh:
			drained++
		default:
			if drained > 0 {
				log.Printf("[USB] warn: %d comandi stale scartati alla riconnessione", drained)
			}
			return
		}
	}
}

func (b *USBBridge) writeLoop(ctx context.Context, port serial.Port, closePort func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[USB] panic in writeLoop: %v", r)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			closePort()
			return
		case msg := <-b.sendCh:
			if _, err := port.Write([]byte(msg)); err != nil {
				log.Printf("[USB] errore scrittura: %v — disconnessione", err)
				closePort()
				return
			}
			log.Printf("[USB] → %s", strings.TrimSpace(msg))
			usbLogAppend("→ " + strings.TrimSpace(msg))
		}
	}
}

func (b *USBBridge) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(usbPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.Send("PING\n")
		}
	}
}

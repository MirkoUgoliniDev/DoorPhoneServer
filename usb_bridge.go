package doorphoneserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const floorsFile = "preferences/floors.json"

// floorsJSON è il formato di serializzazione su disco e nelle API.
type floorsJSON struct {
	P1 [4]string `json:"p1"`
	P2 [4]string `json:"p2"`
	P3 [4]string `json:"p3"`
}

const (
	usbBaudRate      = 115200
	usbPingInterval  = 5 * time.Second
	usbRetryDelay    = 2 * time.Second
	usbRetryLogEvery = 15
	usbSendBufSize   = 32
	usbEvtBufSize    = 64
	// usbProbeTimeout limita l'attesa di HELLO per ogni porta durante la discovery.
	// Tenuto basso perché HELLO arriva subito dopo GET-ROLE e il probe gira sotto
	// discoveryMu, bloccando l'altro bridge per la durata della scansione.
	usbProbeTimeout = 800 * time.Millisecond
)

const cardLogMax = 50

// ESP32CardLog registra un evento tessera ricevuto dall'ESP32-S3.
type ESP32CardLog struct {
	Time   time.Time `json:"time"`
	Result string    `json:"result"`
}

type esp32State struct {
	mu         sync.Mutex
	connected  bool
	pins       map[string]int
	cardLog    []ESP32CardLog
	ringFlash  map[string]time.Time
	tabletOn   bool
	fanPct     int
	floors     [3][4]string // [piano 0-2][slot 0-3]
}

func newESP32State() *esp32State {
	return &esp32State{pins: make(map[string]int), ringFlash: make(map[string]time.Time)}
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

func (s *esp32State) setTablet(on bool) {
	s.mu.Lock()
	s.tabletOn = on
	s.mu.Unlock()
}

func (s *esp32State) getTablet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tabletOn
}

// setFloorSlots imposta tutti e 4 gli slot di un piano in un colpo solo.
func (s *esp32State) setFloorSlots(idx int, slots [4]string) {
	if idx < 0 || idx > 2 {
		return
	}
	s.mu.Lock()
	s.floors[idx] = slots
	floors := s.floors
	s.mu.Unlock()
	saveFloorsToDisk(floors)
}

func (s *esp32State) getFloors() [3][4]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.floors
}

func (s *esp32State) loadFloorsFromDisk() {
	data, err := os.ReadFile(floorsFile)
	if err != nil {
		return
	}
	var fj floorsJSON
	if err := json.Unmarshal(data, &fj); err != nil {
		log.Printf("[USB] warn: floors.json malformato: %v", err)
		return
	}
	s.mu.Lock()
	s.floors[0], s.floors[1], s.floors[2] = fj.P1, fj.P2, fj.P3
	s.mu.Unlock()
	log.Printf("[USB] floors caricati da disco: p1=%v p2=%v p3=%v", fj.P1, fj.P2, fj.P3)
}

func saveFloorsToDisk(floors [3][4]string) {
	fj := floorsJSON{P1: floors[0], P2: floors[1], P3: floors[2]}
	data, err := json.MarshalIndent(fj, "", "  ")
	if err != nil {
		log.Printf("[USB] errore marshal floors: %v", err)
		return
	}
	if err := os.WriteFile(floorsFile, data, 0644); err != nil {
		log.Printf("[USB] errore salvataggio floors.json: %v", err)
	}
}

func (s *esp32State) setFanPct(pct int) {
	s.mu.Lock()
	s.fanPct = pct
	s.mu.Unlock()
}

func (s *esp32State) getFanPct() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fanPct
}

func (s *esp32State) setRingFlash(floor string) {
	s.mu.Lock()
	s.ringFlash[floor] = time.Now()
	s.mu.Unlock()
}

func (s *esp32State) getRingFlash() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.ringFlash))
	for k, v := range s.ringFlash {
		out[k] = v.UnixMilli()
	}
	return out
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

// normalizeUID porta un UID hex al formato standard 14 caratteri (7 byte).
// I tag a 4 byte (8 hex) vengono paddati con zeri: "1A09C601" → "1A09C601000000".
// UIDs già a 14 char vengono restituiti invariati.
func normalizeUID(uid string) string {
	uid = strings.ToUpper(uid)
	if len(uid) < 14 && len(uid)%2 == 0 {
		uid = uid + strings.Repeat("0", 14-len(uid))
	}
	return uid
}

const usbLogMax = 200

// USBBridge gestisce la connessione seriale con l'ESP32-S3.
// Thread-safe. I canali GpioEvt, CardEvt e NfcEvt sono read-only per i consumer.
type USBBridge struct {
	GpioEvt <-chan GPIOEvent
	CardEvt <-chan CardEvent
	NfcEvt  <-chan NfcEvent
	State   *esp32State

	// OnDoorUnlock è chiamato quando un EVT nfc <uid> di una tessera DESFire
	// crittograficamente valida risulta autorizzato nella whitelist server.
	// Permette di inviare "SET unlockdoor pulse" al bridge relay senza accoppiamento diretto.
	OnDoorUnlock func()

	path   string
	logTag string
	logMu  sync.Mutex
	logBuf []string

	gpioCh chan GPIOEvent
	cardCh chan CardEvent
	nfcCh  chan NfcEvent
	sendCh chan string

	nfcMgr *NFCWhitelistManager

	// pending: risposte attese da un singolo comando (ACK, KEY-STATUS, ...)
	pendingMu  sync.Mutex
	pendingMap map[string]chan string
}

// NewUSBBridge crea il bridge per il ruolo indicato ("RFID" o "RELAY").
// La porta USB viene scoperta automaticamente tramite il protocollo GET-ROLE/HELLO.
func NewUSBBridge(ctx context.Context, role string) *USBBridge {
	state := newESP32State()
	state.loadFloorsFromDisk()
	b := &USBBridge{
		logTag:     role,
		gpioCh:     make(chan GPIOEvent, usbEvtBufSize),
		cardCh:     make(chan CardEvent, usbEvtBufSize),
		nfcCh:      make(chan NfcEvent, usbEvtBufSize),
		sendCh:     make(chan string, usbSendBufSize),
		State:      state,
		pendingMap: make(map[string]chan string),
	}
	b.GpioEvt = b.gpioCh
	b.CardEvt = b.cardCh
	b.NfcEvt = b.nfcCh
	go b.connectLoop(ctx)
	return b
}

// logAppend aggiunge una riga al ring buffer del log di questo bridge.
func (b *USBBridge) logAppend(line string) {
	b.logMu.Lock()
	b.logBuf = append(b.logBuf, "["+b.logTag+"] "+line)
	if len(b.logBuf) > usbLogMax {
		b.logBuf = b.logBuf[len(b.logBuf)-usbLogMax:]
	}
	b.logMu.Unlock()
}

// LogSnapshot ritorna una copia del ring buffer log di questo bridge.
func (b *USBBridge) LogSnapshot() []string {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	out := make([]string, len(b.logBuf))
	copy(out, b.logBuf)
	return out
}

// LogClear svuota il ring buffer log di questo bridge.
func (b *USBBridge) LogClear() {
	b.logMu.Lock()
	b.logBuf = nil
	b.logMu.Unlock()
}

// setPath registra la porta seriale corrente del bridge (thread-safe).
// Riusa logMu: path e log hanno lo stesso ciclo di vita (sessione di connessione).
func (b *USBBridge) setPath(p string) {
	b.logMu.Lock()
	b.path = p
	b.logMu.Unlock()
}

// Path ritorna la porta seriale su cui il bridge è connesso ("" se disconnesso).
func (b *USBBridge) Path() string {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	return b.path
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

// ── Connessione ───────────────────────────────────────────────────────────────

func (b *USBBridge) connectLoop(ctx context.Context) {
	tag := "[USB-" + b.logTag + "]"
	failCount := 0
	for {
		select {
		case <-ctx.Done():
			log.Printf("%s bridge fermato", tag)
			return
		default:
		}

		// Scoperta dinamica della porta per questo ruolo. Se trovata, la porta
		// resta riservata (portsInUse) finché non la rilasciamo esplicitamente.
		path := findPortForRole(b.logTag, usbProbeTimeout)
		if path == "" {
			failCount++
			if failCount == 1 || failCount%usbRetryLogEvery == 0 {
				log.Printf("%s nessun device con ruolo %s trovato (tentativo %d)", tag, b.logTag, failCount)
			}
			select {
			case <-ctx.Done():
				log.Printf("%s bridge fermato", tag)
				return
			case <-time.After(usbRetryDelay):
			}
			continue
		}

		port, err := serial.Open(path, &serial.Mode{
			BaudRate: usbBaudRate,
			DataBits: 8,
			Parity:   serial.NoParity,
			StopBits: serial.OneStopBit,
		})
		if err != nil {
			log.Printf("%s impossibile aprire %s: %v", tag, path, err)
			unmarkPortInUse(path)
			select {
			case <-ctx.Done():
				return
			case <-time.After(usbRetryDelay):
			}
			continue
		}

		failCount = 0
		b.setPath(path)
		log.Printf("%s ESP32-%s connesso su %s", tag, b.logTag, path)
		b.State.setConnected(true)
		b.drainSendCh()
		b.Send("GET-STATE\n")
		// FLOOR-GET riguarda solo il display occupanti sull'ESP32 RFID.
		if b.logTag == "RFID" {
			b.Send("FLOOR-GET\n")
		}
		b.runSession(ctx, port)
		b.State.setConnected(false)
		b.State.resetPins()
		b.setPath("")
		unmarkPortInUse(path)
		log.Printf("%s connessione persa — riconnessione in %v", tag, usbRetryDelay)

		select {
		case <-ctx.Done():
			log.Printf("%s bridge fermato", tag)
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

	// Chiude la porta appena il contesto di sessione viene cancellato: sblocca
	// la Read di readLoop indipendentemente da writeLoop, evitando hang se
	// quest'ultimo terminasse senza chiudere la porta.
	go func() {
		<-sessionCtx.Done()
		closePort()
	}()

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
	log.Printf("[USB-%s] ← %s", b.logTag, line)
	b.logAppend("← " + line)

	switch {
	case line == "UID-OK":
		// Solo diagnostica: l'esito auth a bordo. L'apertura è decisa su EVT nfc.
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: line})
		b.drainOrSendCard(CardEvent{OK: true})
		if b.nfcMgr != nil {
			b.nfcMgr.HandleUIDOK()
		}

	case line == "UID-KO":
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: line})
		b.drainOrSendCard(CardEvent{OK: false})
		if b.nfcMgr != nil {
			b.nfcMgr.HandleUIDKO()
		}

	case line == "PONG":
		// watchdog ok

	case strings.HasPrefix(line, "RING-P"):
		floor := strings.ToLower(strings.TrimPrefix(line, "RING-"))
		b.State.setRingFlash(floor)

	case strings.HasPrefix(line, "STATE "):
		// "STATE FAN:75 TABLET:ON"
		for _, part := range strings.Fields(line)[1:] {
			if strings.HasPrefix(part, "FAN:") {
				if pct, err := strconv.Atoi(strings.TrimPrefix(part, "FAN:")); err == nil {
					b.State.setFanPct(pct)
				}
			} else if strings.HasPrefix(part, "TABLET:") {
				on := strings.TrimPrefix(part, "TABLET:") == "ON"
				b.State.setTablet(on)
				// Allinea lo stato software del tablet a quello reale dell'ESP32.
				SyncPowerTabletStateFromESP32(on)
			}
		}

	case strings.HasPrefix(line, "EVT "):
		b.parseAndSendGPIO(line)

	case strings.HasPrefix(line, "ACK FAN-"):
		if pct, err := strconv.Atoi(strings.TrimPrefix(line, "ACK FAN-")); err == nil {
			b.State.setFanPct(pct)
		}

	case strings.HasPrefix(line, "ACK TAG-SCAN PENDING"):
		b.signalPending("ACK-SCAN", "OK")

	case strings.HasPrefix(line, "ACK TAG-ENROLL PENDING"):
		b.signalPending("ACK-ENROLL", "OK")

	case strings.HasPrefix(line, "ACK TAG-FORMAT PENDING"):
		b.signalPending("ACK-FORMAT", "OK")

	case strings.HasPrefix(line, "ACK TAG-INFO PENDING"):
		b.signalPending("ACK-INFO", "OK")

	case strings.HasPrefix(line, "TAG-INFO "):
		b.parseTagInfo(line)

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

	case strings.HasPrefix(line, "TAG-FORMAT-FAIL NO-KEY"):
		if b.nfcMgr != nil {
			b.nfcMgr.HandleFormatFail("no-key")
		}

	case strings.HasPrefix(line, "TAG-FORMAT-FAIL"):
		if b.nfcMgr != nil {
			b.nfcMgr.HandleFormatFail("format-fail")
		}

	case strings.HasPrefix(line, "FLOOR-P"):
		b.parseFloor(line)

	case strings.HasPrefix(line, "ACK FLOOR-SET "):
		parts := strings.Fields(line)
		if len(parts) == 3 {
			b.signalPending("ACK-FLOOR-"+parts[2], "OK")
		}

	case strings.HasPrefix(line, "KEY-STATUS"):
		b.signalPending("KEY-STATUS", line)

	case strings.HasPrefix(line, "KEY-GEN"):
		b.signalPending("KEY-GEN", line)

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
		uid := normalizeUID(parts[2])
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: line})
		if b.nfcMgr != nil {
			// EVT nfc arriva solo per tessere DESFire crittograficamente valide.
			// Il server decide l'apertura in base alla whitelist.
			if b.nfcMgr.HandleNFCEvent(uid) && b.OnDoorUnlock != nil {
				b.OnDoorUnlock()
			}
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
// Il nome non fa parte del protocollo ESP32; l'UID viene normalizzato a 14 char.
func (b *USBBridge) parseTagEnrolled(line string) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		log.Printf("[USB] TAG-ENROLLED malformato (attesi 3 campi): %q", line)
		return
	}
	uid := normalizeUID(parts[1])
	tagType := strings.ToUpper(parts[2])

	log.Printf("[USB] tag enrolled: uid=%s tipo=%s", uid, tagType)
	if b.nfcMgr != nil {
		b.nfcMgr.HandleTagEnrolled(uid, tagType)
	}
}

// parseTagInfo processa "TAG-INFO <uid> <PLAIN|DESFIRE-CONFIGURED|DESFIRE-NEW>"
// inviato dall'ESP32 in modalità auto-scan appena il tag viene identificato.
func (b *USBBridge) parseTagInfo(line string) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		log.Printf("[USB] TAG-INFO malformato (attesi 3 campi): %q", line)
		return
	}
	uid := normalizeUID(parts[1])
	tagType := strings.ToUpper(parts[2])

	log.Printf("[USB] tag info: uid=%s tipo=%s", uid, tagType)
	if b.nfcMgr != nil {
		b.nfcMgr.HandleTagInfo(uid, tagType)
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
	uid := normalizeUID(parts[1])
	log.Printf("[USB] tag format OK: uid=%s", uid)

	if b.nfcMgr != nil {
		// CRITICO: invia TAG-ENROLL prima di notificare SSE
		b.Send("TAG-ENROLL\n")
		b.nfcMgr.HandleTagFormatOK(uid)
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
			log.Printf("[USB-%s] panic in writeLoop: %v", b.logTag, r)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			closePort()
			return
		case msg := <-b.sendCh:
			if _, err := port.Write([]byte(msg)); err != nil {
				log.Printf("[USB-%s] errore scrittura: %v — disconnessione", b.logTag, err)
				closePort()
				return
			}
			log.Printf("[USB-%s] → %s", b.logTag, strings.TrimSpace(msg))
			b.logAppend("→ " + strings.TrimSpace(msg))
		}
	}
}

// parseFloor processa "FLOOR-P1 slot1|slot2|slot3|slot4" (pipe-separated, 4 slot).
func (b *USBBridge) parseFloor(line string) {
	sp := strings.SplitN(line, " ", 2)
	var idx int
	switch sp[0] {
	case "FLOOR-P1":
		idx = 0
	case "FLOOR-P2":
		idx = 1
	case "FLOOR-P3":
		idx = 2
	default:
		return
	}
	var slots [4]string
	if len(sp) == 2 {
		parts := strings.SplitN(strings.TrimSpace(sp[1]), "|", 4)
		for i := 0; i < 4 && i < len(parts); i++ {
			slots[i] = parts[i]
		}
	}
	b.State.setFloorSlots(idx, slots)
	log.Printf("[USB] floor aggiornato: P%d → %v", idx+1, slots)
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

// KeyStatus interroga lo stato della chiave AES-128 DESFire sull'ESP32-S3.
// Ritorna (false, "", nil) se EMPTY, (true, "<hex8>", nil) se PRESENT.
func (b *USBBridge) KeyStatus() (present bool, fp string, err error) {
	resp, ok := b.SendAndWait("KEY-STATUS\n", "KEY-STATUS", 3*time.Second)
	if !ok {
		return false, "", fmt.Errorf("KEY-STATUS: timeout o ESP32 non disponibile")
	}
	if resp == "KEY-STATUS EMPTY" {
		return false, "", nil
	}
	if strings.HasPrefix(resp, "KEY-STATUS PRESENT FP:") {
		return true, strings.TrimPrefix(resp, "KEY-STATUS PRESENT FP:"), nil
	}
	return false, "", fmt.Errorf("KEY-STATUS: risposta inattesa %q", resp)
}

// GenKey genera la chiave AES-128 DESFire sull'ESP32-S3.
// force=true invia KEY-GEN FORCE (rigenera sempre, invalida tessere esistenti).
// Ritorna il fingerprint (8 hex char = SHA-256[:4] della chiave) sia per KEY-GEN-OK che KEY-GEN-EXISTS.
func (b *USBBridge) GenKey(force bool) (string, error) {
	cmd := "KEY-GEN\n"
	if force {
		cmd = "KEY-GEN FORCE\n"
	}
	resp, ok := b.SendAndWait(cmd, "KEY-GEN", 3*time.Second)
	if !ok {
		return "", fmt.Errorf("KEY-GEN: timeout o ESP32 non disponibile")
	}
	if strings.HasPrefix(resp, "KEY-GEN-OK FP:") {
		return strings.TrimPrefix(resp, "KEY-GEN-OK FP:"), nil
	}
	if strings.HasPrefix(resp, "KEY-GEN-EXISTS FP:") {
		return strings.TrimPrefix(resp, "KEY-GEN-EXISTS FP:"), nil
	}
	return "", fmt.Errorf("KEY-GEN: risposta inattesa %q", resp)
}

// EnsureKey riconcilia lo stato della chiave AES all'avvio:
// genera se assente, confronta il FP salvato e segnala re-enroll se la chiave è cambiata.
func (b *USBBridge) EnsureKey() (string, error) {
	present, fp, err := b.KeyStatus()
	if err != nil {
		return "", err
	}

	if !present {
		fp, err = b.GenKey(false)
		if err != nil {
			return "", err
		}
		if err := SaveKeyFP(fp); err != nil {
			log.Printf("[USB] EnsureKey: errore salvataggio FP: %v", err)
		}
		if err := SetReEnrollNeeded(true); err != nil {
			log.Printf("[USB] EnsureKey: errore SetReEnrollNeeded: %v", err)
		}
		log.Printf("[USB] EnsureKey: chiave generata, FP=%s — re-enroll necessario", fp)
		return fp, nil
	}

	saved, ok := GetKeyFP()
	switch {
	case !ok:
		if err := SaveKeyFP(fp); err != nil {
			log.Printf("[USB] EnsureKey: errore salvataggio FP: %v", err)
		}
		log.Printf("[USB] EnsureKey: primo avvio con chiave presente, FP=%s salvato", fp)
	case saved != fp:
		if err := SaveKeyFP(fp); err != nil {
			log.Printf("[USB] EnsureKey: errore salvataggio FP: %v", err)
		}
		if err := SetReEnrollNeeded(true); err != nil {
			log.Printf("[USB] EnsureKey: errore SetReEnrollNeeded: %v", err)
		}
		log.Printf("[USB] EnsureKey: FP cambiato (%s→%s) — re-enroll necessario", saved, fp)
	default:
		log.Printf("[USB] EnsureKey: chiave OK, FP=%s invariato", fp)
	}
	return fp, nil
}

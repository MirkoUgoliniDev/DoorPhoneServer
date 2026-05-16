package doorphoneserver

import (
	"bufio"
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	usbSerialPath    = "/dev/gpio-esp32"
	usbBaudRate      = 115200
	usbPingInterval  = 5 * time.Second
	usbRetryDelay    = 2 * time.Second // breve per rilevare hot-plug rapidamente
	usbRetryLogEvery = 15              // log ogni N tentativi falliti (~30s)
	usbSendBufSize   = 32
	usbEvtBufSize    = 64
)

const cardLogMax = 50

// ESP32CardLog registra un evento tessera ricevuto dall'ESP32-S3.
type ESP32CardLog struct {
	Time   time.Time `json:"time"`
	Result string    `json:"result"` // "OK" o "KO"
}

// esp32State mantiene lo stato corrente del bridge (connessione, pin, log tessere).
// Tutti i metodi sono thread-safe.
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

// resetPins azzera lo stato dei pin alla disconnessione,
// in modo che i LED nel pannello tornino spenti.
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
	Pin   string // "p1", "p2", "p3", "on_off"
	Value int    // 0 = premuto (active-low), 1 = rilasciato
}

// CardEvent rappresenta il risultato di un'autenticazione DESFire EV3.
type CardEvent struct {
	OK bool
}

// USBBridge gestisce la connessione seriale con l'ESP32-S3.
// Si riconnette automaticamente con supporto hot-plug. Thread-safe.
// I canali GpioEvt e CardEvt sono read-only per i consumer.
type USBBridge struct {
	GpioEvt <-chan GPIOEvent
	CardEvt <-chan CardEvent
	State   *esp32State

	gpioCh chan GPIOEvent
	cardCh chan CardEvent
	sendCh chan string
}

// NewUSBBridge crea il bridge e avvia la connessione in background.
// Ritorna subito — la connessione è asincrona con retry automatico.
func NewUSBBridge(ctx context.Context) *USBBridge {
	b := &USBBridge{
		gpioCh: make(chan GPIOEvent, usbEvtBufSize),
		cardCh: make(chan CardEvent, usbEvtBufSize),
		sendCh: make(chan string, usbSendBufSize),
		State:  newESP32State(),
	}
	b.GpioEvt = b.gpioCh
	b.CardEvt = b.cardCh
	go b.connectLoop(ctx)
	return b
}

// Send accoda un comando verso l'ESP32-S3. Non bloccante:
// se il buffer è pieno il messaggio viene scartato con un warning.
func (b *USBBridge) Send(msg string) {
	select {
	case b.sendCh <- msg:
	default:
		log.Printf("[USB] warn: send buffer pieno, scartato: %q", msg)
	}
}

// connectLoop è il loop principale di riconnessione.
// Supporta hot-plug: tenta ogni usbRetryDelay con log throttled.
// Esce solo quando il contesto viene cancellato.
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
			// log solo al primo tentativo e poi ogni usbRetryLogEvery per evitare spam
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
		b.drainSendCh() // scarta comandi stale accumulati durante la disconnessione
		b.runSession(ctx, port)
		b.State.setConnected(false)
		b.State.resetPins() // azzera LED pannello alla disconnessione
		log.Printf("[USB] connessione persa — riconnessione in %v", usbRetryDelay)

		select {
		case <-ctx.Done():
			log.Printf("[USB] bridge fermato")
			return
		case <-time.After(usbRetryDelay):
		}
	}
}

// runSession gestisce una singola sessione connessa.
// La porta viene chiusa esattamente una volta (sync.Once) indipendentemente
// da quale goroutine esce per prima, risolvendo il problema del double-close.
// Blocca fino a disconnessione o cancellazione del contesto.
func (b *USBBridge) runSession(ctx context.Context, port serial.Port) {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// closePort è sicuro da chiamare più volte: esegue Close una sola volta.
	var closeOnce sync.Once
	closePort := func() {
		closeOnce.Do(func() { port.Close() })
	}
	// Garantisce chiusura porta anche se readLoop esce prima di writeLoop.
	defer closePort()

	go b.writeLoop(sessionCtx, port, closePort)
	go b.pingLoop(sessionCtx)
	b.readLoop(sessionCtx, port)
}

// readLoop legge righe dalla porta seriale e dispatcha gli eventi.
// Esce su errore di lettura (disconnessione fisica o porta chiusa da writeLoop).
func (b *USBBridge) readLoop(ctx context.Context, port serial.Port) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[USB] panic in readLoop: %v", r)
		}
	}()

	scanner := bufio.NewScanner(port)
	for {
		// Scan() è bloccante. Viene sbloccato quando writeLoop chiude la porta
		// (sia su errore di scrittura che su ctx.Done), garantendo exit pulito.
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

// dispatch parsa una riga e la instrada sul canale corretto.
func (b *USBBridge) dispatch(line string) {
	if line == "" {
		return
	}
	log.Printf("[USB] ← %s", line)

	switch {
	case line == "UID-OK":
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: "OK"})
		b.drainOrSendCard(CardEvent{OK: true})

	case line == "UID-KO":
		b.State.addCard(ESP32CardLog{Time: time.Now(), Result: "KO"})
		b.drainOrSendCard(CardEvent{OK: false})

	case line == "PONG":
		// watchdog ok — nessuna azione

	case strings.HasPrefix(line, "EVT "):
		b.parseAndSendGPIO(line)

	case strings.HasPrefix(line, "ACK "):
		// conferma output eseguito — già loggato sopra

	case strings.HasPrefix(line, "ERR "):
		log.Printf("[USB] errore ESP32-S3: %s", line)

	default:
		log.Printf("[USB] riga non riconosciuta: %q", line)
	}
}

// parseAndSendGPIO parsa "EVT <pin> <0|1>" e manda sul canale GPIO.
func (b *USBBridge) parseAndSendGPIO(line string) {
	parts := strings.Fields(line)
	if len(parts) != 3 {
		log.Printf("[USB] EVT malformato: %q", line)
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

// drainOrSendCard invia un CardEvent, scartando eventi non ancora consumati.
// Chiamato solo da dispatch (singola goroutine per sessione) — nessuna race.
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

// drainSendCh scarta tutti i comandi accodati durante la disconnessione.
// Evita che comandi stale vengano inviati al device appena riconnesso.
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

// writeLoop invia i messaggi accodati sulla porta seriale.
// Chiude la porta (tramite closePort) sia su errore di scrittura che su
// ctx.Done, garantendo che readLoop venga sempre sbloccato.
func (b *USBBridge) writeLoop(ctx context.Context, port serial.Port, closePort func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[USB] panic in writeLoop: %v", r)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			closePort() // sblocca readLoop per uno shutdown pulito
			return
		case msg := <-b.sendCh:
			if _, err := port.Write([]byte(msg)); err != nil {
				log.Printf("[USB] errore scrittura: %v — disconnessione", err)
				closePort() // sblocca readLoop e triggera riconnessione
				return
			}
			log.Printf("[USB] → %s", strings.TrimSpace(msg))
		}
	}
}

// pingLoop invia PING ogni usbPingInterval per mantenere il watchdog ESP32-S3.
// Rileva connessioni silenziosamente morte tramite l'errore di scrittura in writeLoop.
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

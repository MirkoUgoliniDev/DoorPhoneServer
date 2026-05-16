package doorphoneserver

import (
	"bufio"
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

const (
	usbSerialPath     = "/dev/gpio-esp32"
	usbBaudRate       = 115200
	usbPingInterval   = 5 * time.Second
	usbReconnectDelay = 10 * time.Second
	usbSendBufSize    = 32
	usbEvtBufSize     = 64
)

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
// Si riconnette automaticamente. Thread-safe.
// I canali GpioEvt e CardEvt sono read-only per i consumer.
type USBBridge struct {
	GpioEvt <-chan GPIOEvent
	CardEvt <-chan CardEvent

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
// Esce solo quando il contesto viene cancellato.
func (b *USBBridge) connectLoop(ctx context.Context) {
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
			log.Printf("[USB] ESP32-S3 non disponibile: %v — riprovo in %v",
				err, usbReconnectDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(usbReconnectDelay):
			}
			continue
		}

		log.Printf("[USB] ESP32-S3 connesso su %s", usbSerialPath)
		b.runSession(ctx, port)
		port.Close()

		select {
		case <-ctx.Done():
			return
		default:
			log.Printf("[USB] connessione persa — riconnessione in %v", usbReconnectDelay)
			time.Sleep(usbReconnectDelay)
		}
	}
}

// runSession gestisce una singola sessione connessa.
// Blocca fino a disconnessione o cancellazione del contesto.
func (b *USBBridge) runSession(ctx context.Context, port serial.Port) {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go b.writeLoop(sessionCtx, port)
	go b.pingLoop(sessionCtx)
	b.readLoop(sessionCtx, port)
}

// readLoop legge righe dalla porta seriale e dispatcha gli eventi.
// Esce su errore di lettura (disconnessione) o cancellazione del contesto.
func (b *USBBridge) readLoop(ctx context.Context, port serial.Port) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[USB] panic in readLoop: %v", r)
		}
	}()

	scanner := bufio.NewScanner(port)
	for {
		// bufio.Scanner.Scan() è bloccante — non può essere interrotto da ctx
		// direttamente. La cancellazione del contesto chiuderà la porta dal
		// lato writeLoop (che esce), causando a sua volta l'uscita di Scan().
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
		b.drainOrSendCard(CardEvent{OK: true})

	case line == "UID-KO":
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
	// formato atteso: "EVT <pin> <valore>"
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
	select {
	case b.gpioCh <- evt:
	default:
		log.Printf("[USB] warn: GPIO channel pieno, EVT scartato: %+v", evt)
	}
}

// drainOrSendCard invia un CardEvent, scartando eventuali eventi precedenti
// non ancora consumati (evita accodamento di richieste duplicate).
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

// writeLoop invia i messaggi accodati sulla porta seriale.
// Esce su errore di scrittura o cancellazione del contesto.
func (b *USBBridge) writeLoop(ctx context.Context, port serial.Port) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[USB] panic in writeLoop: %v", r)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-b.sendCh:
			if _, err := port.Write([]byte(msg)); err != nil {
				log.Printf("[USB] errore scrittura: %v — disconnessione", err)
				// Chiude la porta per sbloccare readLoop e triggera riconnessione
				port.Close()
				return
			}
			log.Printf("[USB] → %s", strings.TrimSpace(msg))
		}
	}
}

// pingLoop invia PING ogni usbPingInterval per mantenere il watchdog ESP32-S3.
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

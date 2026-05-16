package doorphoneserver

import (
	"context"
	"log"
	"strings"
)

// GPIOUsb consuma eventi GPIO dall'ESP32-S3 e gestisce i comandi di output.
// In modalità debug opera in parallelo a gpio.go — logga ma non esegue azioni.
// In modalità produzione sostituisce gpio.go come backend primario.
type GPIOUsb struct {
	bridge    *USBBridge
	server    *DoorPhoneServer
	debugMode bool
}

// NewGPIOUsb crea il gestore GPIO USB.
// debugMode=true: gli eventi vengono solo loggati (coesistenza con gpio.go).
// debugMode=false: gli eventi eseguono le azioni (modalità produzione).
func NewGPIOUsb(bridge *USBBridge, server *DoorPhoneServer, debugMode bool) *GPIOUsb {
	return &GPIOUsb{
		bridge:    bridge,
		server:    server,
		debugMode: debugMode,
	}
}

// Run avvia il loop di ricezione eventi. Blocca fino alla cancellazione del contesto.
func (g *GPIOUsb) Run(ctx context.Context) {
	log.Printf("[GPIO-USB] avviato (debugMode=%v)", g.debugMode)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[GPIO-USB] fermato")
			return
		case evt := <-g.bridge.GpioEvt:
			g.handleEvent(evt)
		}
	}
}

// handleEvent gestisce un singolo GPIOEvent.
func (g *GPIOUsb) handleEvent(evt GPIOEvent) {
	// Value==0: fronte discendente (active-low → pulsante premuto)
	// Value==1: fronte ascendente (pulsante rilasciato)
	pressed := evt.Value == 0

	if g.debugMode {
		action := "rilasciato"
		if pressed {
			action = "premuto"
		}
		log.Printf("[GPIO-USB] debug: pin=%s %s", evt.Pin, action)
		return
	}

	// Modalità produzione: esegui le azioni
	if !pressed {
		return // ignora il rilascio
	}

	switch strings.ToLower(evt.Pin) {
	case "p1":
		log.Printf("[GPIO-USB] P1 premuto — chiamata piano 1")
		g.server.cmdRingPiano("P1")
	case "p2":
		log.Printf("[GPIO-USB] P2 premuto — chiamata piano 2")
		g.server.cmdRingPiano("P2")
	case "p3":
		log.Printf("[GPIO-USB] P3 premuto — chiamata piano 3")
		g.server.cmdRingPiano("P3")
	case "on_off":
		log.Printf("[GPIO-USB] On/Off premuto")
		// gestione accensione/spegnimento — da implementare
	default:
		log.Printf("[GPIO-USB] pin sconosciuto: %s", evt.Pin)
	}
}

// SetPin invia un comando SET all'ESP32-S3.
// name: nome del pin (es. "heartbeat", "unlockdoor")
// state: "on", "off" o "pulse"
func (g *GPIOUsb) SetPin(name, state string) {
	g.bridge.Send("SET " + name + " " + state + "\n")
}

// SetPWM invia un comando PWM all'ESP32-S3.
// name: nome del pin (es. "fan")
// duty: 0–100
func (g *GPIOUsb) SetPWM(name string, duty int) {
	if duty < 0 {
		duty = 0
	}
	if duty > 100 {
		duty = 100
	}
	g.bridge.Send("PWM " + name + " " + itoa(duty) + "\n")
}

// Pulse invia un comando pulse all'ESP32-S3.
// Usato principalmente per il relè portone.
func (g *GPIOUsb) Pulse(name string) {
	g.SetPin(name, "pulse")
}

// itoa converte int in stringa senza import fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte(n%10) + '0'
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

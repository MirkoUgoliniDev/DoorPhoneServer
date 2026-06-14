package doorphoneserver

import (
	"context"
	"log"
	"strings"
)

// GPIOUsb consuma eventi GPIO dall'ESP32-S3 ed espone i comandi di output
// (SetPin/SetPWM/Pulse). Gli eventi di input vengono tradotti in azioni
// (es. pressione pulsante piano → cmdRingPiano) solo quando il backend IO
// configurato è l'ESP32 (vedi ioUseESP32); altrimenti restano solo loggati.
type GPIOUsb struct {
	bridge *USBBridge
	srv    *DoorPhoneServer
}

// ioUSB è l'handle globale al backend IO su ESP32, usato dalle funzioni di
// output package-level (GPIOOutPin/GPIOOutAll) quando backend="esp32".
var ioUSB *GPIOUsb

// NewGPIOUsb crea il gestore GPIO USB collegato al server per le azioni di input.
func NewGPIOUsb(bridge *USBBridge, srv *DoorPhoneServer) *GPIOUsb {
	return &GPIOUsb{bridge: bridge, srv: srv}
}

// Run avvia il loop di ricezione eventi. Blocca fino alla cancellazione del contesto.
func (g *GPIOUsb) Run(ctx context.Context) {
	log.Printf("[GPIO-USB] avviato")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[GPIO-USB] fermato")
			return
		case evt := <-g.bridge.GpioEvt:
			pressed := evt.Value == 0 // active-low: 0 = premuto
			action := "rilasciato"
			if pressed {
				action = "premuto"
			}
			log.Printf("[GPIO-USB] pin=%s %s", evt.Pin, action)

			// Solo con backend ESP32 gli eventi pulsante generano azioni,
			// replicando il comportamento del loop GPIO del Raspberry.
			if pressed && g.srv != nil && ioUseESP32() && IsConnected.Load() {
				switch strings.ToLower(evt.Pin) {
				case "p1", "p2", "p3":
					g.srv.cmdRingPiano(strings.ToUpper(evt.Pin))
				}
			}
		}
	}
}

// SetPin invia un comando SET all'ESP32-S3 (on / off / pulse).
func (g *GPIOUsb) SetPin(name, state string) {
	g.bridge.Send("SET " + name + " " + state + "\n")
}

// SetPWM invia un comando PWM all'ESP32-S3 (duty 0–100).
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
func (g *GPIOUsb) Pulse(name string) {
	g.SetPin(name, "pulse")
}

// TabletPower invia TABLET-ON o TABLET-OFF all'ESP32-S3.
func (g *GPIOUsb) TabletPower(on bool) {
	if on {
		g.bridge.Send("TABLET-ON\n")
	} else {
		g.bridge.Send("TABLET-OFF\n")
	}
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

package doorphoneserver

import (
	"context"
	"log"
	"strings"
)

// GPIOUsb consuma eventi GPIO dall'ESP32-S3 RFID ed espone i comandi di output
// con routing automatico: unlockdoor/power_tablet/fan → relay bridge; il resto → rfid bridge.
type GPIOUsb struct {
	rfid  *USBBridge // ESP32-A: NFC + pulsanti campanelli
	relay *USBBridge // ESP32-B: relè porta + relè tablet + fan PWM
	srv   *DoorPhoneServer
}

// ioUSB è l'handle globale al backend IO su ESP32, usato dalle funzioni di
// output package-level (GPIOOutPin/GPIOOutAll) quando backend="esp32".
var ioUSB *GPIOUsb

// NewGPIOUsb crea il gestore GPIO USB con routing su due bridge.
// rfid gestisce NFC e input pulsanti; relay gestisce relè porta, tablet e fan.
func NewGPIOUsb(rfid, relay *USBBridge, srv *DoorPhoneServer) *GPIOUsb {
	return &GPIOUsb{rfid: rfid, relay: relay, srv: srv}
}

// Run avvia il loop di ricezione eventi dal bridge RFID. Blocca fino alla cancellazione del contesto.
func (g *GPIOUsb) Run(ctx context.Context) {
	log.Printf("[GPIO-USB] avviato")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[GPIO-USB] fermato")
			return
		case evt := <-g.rfid.GpioEvt:
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

// relayNames sono i pin fisicamente sul bridge relay (ESP32-B).
var relayNames = map[string]bool{
	"unlockdoor":   true,
	"power_tablet": true,
}

// bridgeFor restituisce il bridge corretto per il nome del pin.
func (g *GPIOUsb) bridgeFor(name string) *USBBridge {
	if relayNames[name] {
		return g.relay
	}
	return g.rfid
}

// SetPin invia un comando SET al bridge corretto per quel pin (on / off / pulse).
func (g *GPIOUsb) SetPin(name, state string) {
	g.bridgeFor(name).Send("SET " + name + " " + state + "\n")
}

// SetPWM invia un comando PWM al relay bridge (fan è su ESP32-B, duty 0–100).
func (g *GPIOUsb) SetPWM(name string, duty int) {
	if duty < 0 {
		duty = 0
	}
	if duty > 100 {
		duty = 100
	}
	g.relay.Send("PWM " + name + " " + itoa(duty) + "\n")
}

// Pulse invia un comando pulse al bridge corretto per quel pin.
func (g *GPIOUsb) Pulse(name string) {
	g.SetPin(name, "pulse")
}

// TabletPower invia TABLET-ON o TABLET-OFF al relay bridge (ESP32-B).
func (g *GPIOUsb) TabletPower(on bool) {
	if on {
		g.relay.Send("TABLET-ON\n")
	} else {
		g.relay.Send("TABLET-OFF\n")
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

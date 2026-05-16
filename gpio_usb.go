package doorphoneserver

import (
	"context"
	"log"
)

// GPIOUsb consuma eventi GPIO dall'ESP32-S3.
// Fase debug: logga tutti gli eventi senza eseguire azioni.
// Espone SetPin/SetPWM/Pulse per i comandi di output (pronti per la produzione).
type GPIOUsb struct {
	bridge *USBBridge
}

// NewGPIOUsb crea il gestore GPIO USB.
func NewGPIOUsb(bridge *USBBridge) *GPIOUsb {
	return &GPIOUsb{bridge: bridge}
}

// Run avvia il loop di ricezione eventi. Blocca fino alla cancellazione del contesto.
func (g *GPIOUsb) Run(ctx context.Context) {
	log.Printf("[GPIO-USB] avviato — modalità debug (solo log)")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[GPIO-USB] fermato")
			return
		case evt := <-g.bridge.GpioEvt:
			action := "rilasciato"
			if evt.Value == 0 {
				action = "premuto" // active-low: 0 = premuto
			}
			log.Printf("[GPIO-USB] pin=%s %s", evt.Pin, action)
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

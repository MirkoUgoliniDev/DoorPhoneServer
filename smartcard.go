package doorphoneserver

import (
	"context"
	"log"
)

// Smartcard consuma CardEvent dall'ESP32-S3.
// Fase debug: logga il risultato senza eseguire azioni sul portone.
// La logica operativa (apertura portone, LED, log accessi) verrà abilitata
// nella fase di produzione.
type Smartcard struct {
	bridge *USBBridge
}

// NewSmartcard crea il gestore tessere.
func NewSmartcard(bridge *USBBridge) *Smartcard {
	return &Smartcard{bridge: bridge}
}

// Run avvia il loop di ricezione eventi tessera.
// Blocca fino alla cancellazione del contesto.
func (s *Smartcard) Run(ctx context.Context) {
	log.Printf("[SMARTCARD] avviato — modalità debug (solo log)")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[SMARTCARD] fermato")
			return
		case evt := <-s.bridge.CardEvt:
			if evt.OK {
				log.Printf("[SMARTCARD] UID-OK — tessera DESFire EV3 autenticata")
			} else {
				log.Printf("[SMARTCARD] UID-KO — tessera rifiutata o errore auth")
			}
		}
	}
}

package doorphoneserver

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// smartcardEntry descrive una tessera autorizzata.
type smartcardEntry struct {
	Name    string   `json:"name"`
	Floors  []string `json:"floors"`
	Enabled bool     `json:"enabled"`
	Note    string   `json:"note,omitempty"`
}

// accessLogEntry è un singolo record nel log accessi.
type accessLogEntry struct {
	Timestamp string `json:"ts"`
	Result    string `json:"result"`  // "OK" o "KO"
	Action    string `json:"action,omitempty"`
}

// Smartcard consuma CardEvent dall'ESP32-S3 e gestisce l'apertura del portone.
// La validazione crittografica è già avvenuta nell'ESP32-S3:
// UID-OK significa tessera autentica, il Pi decide solo se aprire.
type Smartcard struct {
	bridge  *USBBridge
	gpioUsb *GPIOUsb
	server  *DoorPhoneServer
	mu      sync.Mutex
	lastOK  time.Time // anti-spam: ignora UID-OK multipli ravvicinati
}

// NewSmartcard crea il gestore tessere.
func NewSmartcard(bridge *USBBridge, gpioUsb *GPIOUsb, server *DoorPhoneServer) *Smartcard {
	return &Smartcard{
		bridge:  bridge,
		gpioUsb: gpioUsb,
		server:  server,
	}
}

// Run avvia il loop di ricezione eventi tessera.
// Blocca fino alla cancellazione del contesto.
func (s *Smartcard) Run(ctx context.Context) {
	log.Printf("[SMARTCARD] avviato")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[SMARTCARD] fermato")
			return
		case evt := <-s.bridge.CardEvt:
			s.handleEvent(evt)
		}
	}
}

// handleEvent gestisce un singolo CardEvent.
func (s *Smartcard) handleEvent(evt CardEvent) {
	if evt.OK {
		s.onAccessGranted()
	} else {
		s.onAccessDenied()
	}
}

// onAccessGranted gestisce l'autenticazione riuscita.
func (s *Smartcard) onAccessGranted() {
	// Anti-spam: ignora eventi ravvicinati (< 3s)
	s.mu.Lock()
	if time.Since(s.lastOK) < 3*time.Second {
		s.mu.Unlock()
		log.Printf("[SMARTCARD] UID-OK ignorato (troppo ravvicinato al precedente)")
		return
	}
	s.lastOK = time.Now()
	s.mu.Unlock()

	log.Printf("[SMARTCARD] accesso concesso — apertura portone")

	// Apri il portone sull'ESP32-S3 (relè)
	s.gpioUsb.Pulse("unlockdoor")

	// Feedback visivo LED verde sull'ESP32-S3
	s.bridge.Send("SET led_ok on\n")
	go func() {
		time.Sleep(3 * time.Second)
		s.bridge.Send("SET led_ok off\n")
	}()

	s.logAccess("OK", "unlockdoor")
}

// onAccessDenied gestisce il rifiuto dell'autenticazione.
func (s *Smartcard) onAccessDenied() {
	log.Printf("[SMARTCARD] accesso negato")

	// Feedback visivo LED rosso sull'ESP32-S3
	s.bridge.Send("SET led_ko on\n")
	go func() {
		time.Sleep(2 * time.Second)
		s.bridge.Send("SET led_ko off\n")
	}()

	s.logAccess("KO", "")
}

// logAccess scrive un record nel file di log accessi.
// Il file è in formato JSONL (una riga JSON per evento).
// Errori di scrittura vengono loggati ma non fanno crashare l'app.
func (s *Smartcard) logAccess(result, action string) {
	entry := accessLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Result:    result,
		Action:    action,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[SMARTCARD] errore marshal log: %v", err)
		return
	}

	logPath := s.accessLogPath()
	if logPath == "" {
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		log.Printf("[SMARTCARD] errore apertura log %s: %v", logPath, err)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		log.Printf("[SMARTCARD] errore scrittura log: %v", err)
	}
}

// accessLogPath restituisce il percorso del file di log accessi.
// Usa la home dell'utente di sistema TK_USER.
func (s *Smartcard) accessLogPath() string {
	home, _ := os.UserHomeDir()
	prefsDir := filepath.Join(home, "preferences")
	if err := os.MkdirAll(prefsDir, 0750); err != nil {
		log.Printf("[SMARTCARD] errore creazione preferences/: %v", err)
		return ""
	}
	return filepath.Join(prefsDir, "access_log.jsonl")
}

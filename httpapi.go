// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// DeviceStatus rappresenta lo stato di un dispositivo GPIO restituito dall'API HTTP.
type DeviceStatus struct {
	// Device è il nome del dispositivo GPIO
	Device string `json:"device"`
	// Status è lo stato del pin GPIO: 0=basso/off, 1=alto/on
	Status int    `json:"status"`
}

// StatusResponse incapsula un DeviceStatus come risposta JSON dell'API.
type StatusResponse struct {
	// Status contiene le informazioni di stato del dispositivo
	Status DeviceStatus `json:"status"`
}

// simpleLimiter implementa un rate limiter basato su token bucket per le richieste HTTP.
type simpleLimiter struct {
	mu       sync.Mutex
	last     time.Time
	rate     time.Duration
	burst    int
	tokens   int
	timeFunc func() time.Time
}

// newSimpleLimiter crea un nuovo rate limiter con il tasso e il burst specificati.
// @param r intervallo minimo tra richieste consecutive
// @param b numero massimo di richieste in burst
// @return puntatore al nuovo simpleLimiter
func newSimpleLimiter(r time.Duration, b int) *simpleLimiter {
	return &simpleLimiter{
		rate:     r,
		burst:    b,
		tokens:   b,
		timeFunc: time.Now,
	}
}

// allow verifica se una nuova richiesta è consentita dal rate limiter.
// @return true se la richiesta è permessa, false se è necessario limitare il traffico
func (l *simpleLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.timeFunc()
	elapsed := now.Sub(l.last)
	l.last = now

	l.tokens += int(elapsed / l.rate)
	if l.tokens > l.burst {
		l.tokens = l.burst
	}

	if l.tokens <= 0 {
		return false
	}

	l.tokens--
	return true
}

var (
	// validCommands mappa dei comandi API HTTP validi accettati dall'endpoint
	validCommands = make(map[string]bool)
	// rateLimiter limita le richieste all'API HTTP a 10 al secondo con burst di 10
	rateLimiter   = newSimpleLimiter(time.Second/10, 10) // 10 requests per second
	//mutex_httpapi sync.Mutex
)

// init registra i comandi API HTTP validi nella mappa validCommands al caricamento del pacchetto.
func init() {
	commands := []string{
		"takesnapshot",
		"mute",
		"unmute",
		"ring",
		"unlockdoor",  // NUOVO: sintassi corretta
		"powertablet_on",
		"powertablet_off",
		"sendpushovermessage",
		"reboot_server",
		"getrspitemp",  // Get Raspberry Pi CPU temperature
		"listapi",
		"audiochannelstatus",  // NUOVO: verifica stato canale audio
	}
	for _, cmd := range commands {
		validCommands[cmd] = true
	}
}

// httpAPI è il gestore principale dell'API HTTP che esegue i comandi ricevuti via query string.
// Supporta rate limiting, timeout di 25 secondi e reindirizza la root al pannello web.
// Il parametro obbligatorio è "command" nell'URL query string.
func (b *DoorPhoneServer) httpAPI(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered in httpAPI: %v", r)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	// redirect root to web panel
	if r.URL.Path == "/" && r.URL.RawQuery == "" {
		http.Redirect(w, r, "/panel", http.StatusFound)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")

	if !rateLimiter.allow() {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	//log.Printf("Received request: %s from %s", r.URL.String(), r.RemoteAddr)

	funcs := map[string]interface{}{
		"takesnapshot":        b.cmdTakeSnapshot,
		"mute":                b.cmdMuteUnmute,
		"unmute":              b.cmdMuteUnmute,
		"unlockdoor":          b.cmdUnlockDoor, // NUOVO: sintassi corretta
		"powertablet_on":      b.cmdPowertablet_on,
		"powertablet_off":     b.cmdPowertablet_off,
		"sendpushovermessage": b.cmdSendPushowerMessage,
		"reboot_server":       b.cmdRebootServer,
		"getrspitemp":         b.cmdGetRspiTemp, // Get Raspberry Pi CPU temperature
		"listapi":             listAPI,
		"audiochannelstatus":  b.cmdAudioChannelStatus, // NUOVO: verifica stato canale audio
	}

	APICommands, ok := r.URL.Query()["command"]
	if !ok || len(APICommands[0]) < 1 {
		http.Error(w, "error: URL Param 'command' is missing", http.StatusBadRequest)
		return
	}

	APICommand := strings.ToLower(APICommands[0])

	if !validCommands[APICommand] {
		http.Error(w, fmt.Sprintf("404 error: API Command %v Not A Valid Defined Command", APICommand), http.StatusNotFound)
		return
	}

	// Handle ?command=ring&P1 / &P2 / &P3 / &P4
	if APICommand == "ring" {
		validPianos := map[string]string{
			"p1": "P1",
			"p2": "P2",
			"p3": "P3",
			"p4": "P4",
		}
		found := false
		for key := range r.URL.Query() {
			if piano, ok := validPianos[strings.ToLower(key)]; ok {
				b.cmdRingPiano(piano)
				fmt.Fprintf(w, "200 OK: ring %s OK\n", strings.ToUpper(key))
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "error: ring requires a target parameter P1, P2, P3 or P4", http.StatusBadRequest)
		}
		return
	}


	for _, apicommand := range Config.Global.Software.RemoteControl.HTTP.Command {

		if apicommand.Action == APICommand {

			if len(apicommand.Funcparamname) == 0 {
				_, err := b.Call(funcs, apicommand.Action)
				if err != nil {
					log.Printf("Error calling function %s: %v", apicommand.Action, err)
					http.Error(w, "error: Wrong Parameters to Call Function", http.StatusInternalServerError)
					return
				}
				fmt.Fprintf(w, "200 OK: http command %v OK \n", APICommand)
				return
			}

			switch APICommand {
			case "unlockdoor": // unlockdoor=NUOVO
				P := r.URL.Query().Get("P")
				if P == "" {
					http.Error(w, "Missing required parameter P", http.StatusBadRequest)
					return
				}
				result, err := b.cmdUnlockDoor(P)
				if err != nil {
					log.Printf("Error calling cmdUnlockDoor: %v", err)
					http.Error(w, "Error calling cmdUnlockDoor", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(result)); err != nil {
					log.Printf("Error writing JSON response: %v", err)
				}
	
			case "getrspitemp": // Get Raspberry Pi CPU temperature
				result, err := b.cmdGetRspiTemp()
				if err != nil {
					log.Printf("Error calling cmdGetRspiTemp: %v", err)
					http.Error(w, fmt.Sprintf("Error getting temperature: %v", err), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(result)); err != nil {
					log.Printf("Error writing JSON response: %v", err)
				}
	
			default:
				_, err := b.Call(funcs, apicommand.Action, apicommand.Funcparamname)
				if err != nil {
					log.Printf("Error calling function %s: %v", apicommand.Action, err)
					http.Error(w, "error: Wrong Parameters to Call Function", http.StatusInternalServerError)
					return
				}
				fmt.Fprintf(w, "200 OK: http command %v For %v Control\n", apicommand.Action, apicommand.Message)
			}
			return
		}
	}
}

// Call invoca dinamicamente una funzione dalla mappa per nome usando reflection.
// @param m mappa delle funzioni disponibili indicizzate per nome
// @param name nome della funzione da invocare
// @param params parametri da passare alla funzione
// @return slice di valori di ritorno o errore se la funzione non esiste o i parametri non corrispondono
func (b *DoorPhoneServer) Call(m map[string]interface{}, name string, params ...interface{}) (result []reflect.Value, err error) {
	f, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("function %s not found", name)
	}

	fv := reflect.ValueOf(f)
	if len(params) != fv.Type().NumIn() {
		return nil, errors.New("the number of params is not adapted")
	}

	in := make([]reflect.Value, len(params))
	for k, param := range params {
		in[k] = reflect.ValueOf(param)
	}

	return fv.Call(in), nil
}

// listAPI registra nel log tutti i comandi API HTTP abilitati nella configurazione XML.
func listAPI() {
	for _, apicommand := range Config.Global.Software.RemoteControl.HTTP.Command {
		if apicommand.Enabled {
			log.Printf("Info: API Command %v for %v Control Available\n", apicommand.Action, apicommand.Message)
		}
	}
}

// cmdAudioChannelStatus ritorna lo stato corrente del canale audio bidirezionale.
// Fornisce informazioni su RX (ricezione da client Android) e TX (trasmissione verso client Android).
func (b *DoorPhoneServer) cmdAudioChannelStatus() {
	AudioChannelMonitor.mu.RLock()
	defer AudioChannelMonitor.mu.RUnlock()
	
	status := AudioChannelMonitor.GetChannelStatus()
	
	log.Printf("info: [AUDIO-CHANNEL-API] Status richiesto via HTTP API")
	log.Printf("info: [AUDIO-CHANNEL-API] Status: %s", status)
	
	if AudioChannelMonitor.RxActive {
		rxDuration := time.Since(AudioChannelMonitor.RxStartTime)
		rxBitrate := float64(AudioChannelMonitor.RxByteCount*8) / rxDuration.Seconds() / 1000
		log.Printf("info: [AUDIO-CHANNEL-API] RX: Client=%s Packets=%d Bitrate=%.1f kbps Level=%.0f%% Duration=%v",
			AudioChannelMonitor.RxFrom, AudioChannelMonitor.RxPacketCount, rxBitrate,
			AudioChannelMonitor.RxAvgLevel, rxDuration.Round(time.Second))
	} else {
		log.Printf("info: [AUDIO-CHANNEL-API] RX: Inattivo")
	}
	
	if AudioChannelMonitor.TxActive {
		txDuration := time.Since(AudioChannelMonitor.TxStartTime)
		txBitrate := float64(AudioChannelMonitor.TxByteCount*8) / txDuration.Seconds() / 1000
		packetLoss := 0.0
		if total := AudioChannelMonitor.TxPacketCount + AudioChannelMonitor.TxDroppedCount; total > 0 {
			packetLoss = float64(AudioChannelMonitor.TxDroppedCount) / float64(total) * 100
		}
		log.Printf("info: [AUDIO-CHANNEL-API] TX: Packets=%d Dropped=%d (%.1f%%) Bitrate=%.1f kbps Duration=%v",
			AudioChannelMonitor.TxPacketCount, AudioChannelMonitor.TxDroppedCount,
			packetLoss, txBitrate, txDuration.Round(time.Second))
	} else {
		log.Printf("info: [AUDIO-CHANNEL-API] TX: Inattivo")
	}
}

// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// activeCallUser tiene traccia dell'utente a cui è stato inviato il RING corrente.
// Solo quell'utente può inviare accept-call e close-call.
var activeCallUser string
var activeCallMu sync.Mutex

// PowerTabletState tracks the state and history of power tablet commands
type PowerTabletState struct {
	mu              sync.RWMutex
	isOn            bool
	lastChangeTime  time.Time
	minDebounceTime time.Duration
	changeHistory   []PowerTabletChange
}

// PowerTabletChange records a power tablet state change event
type PowerTabletChange struct {
	Timestamp time.Time
	Action    string // "on" or "off"
	Source    string // "http", "mqtt", "manual", "command"
	Success   bool
	Reason    string // if failed
}

var (
	powerTabletState = PowerTabletState{
		minDebounceTime: 10 * time.Second,
		changeHistory:   make([]PowerTabletChange, 0, 50),
		isOn:            false, // Assume off at startup
	}
	maxPowerTabletHistory = 50
)

const (
	// unlockDoorHoldTime è il tempo per cui il relè di sblocco porta rimane attivato
	unlockDoorHoldTime = 5 * time.Second
	// httpClientTimeout è il timeout per le richieste HTTP verso telecamere e dispositivi
	httpClientTimeout = 30 * time.Second
)

// configuredSnapshotMethod restituisce il metodo configurato per catturare snapshot dalla telecamera.
// @return "ffmpeg" se configurato, altrimenti "http"
func configuredSnapshotMethod() string {
	method := strings.ToLower(strings.TrimSpace(Config.Global.Software.Camera.Snapshot.Method))
	if method == "ffmpeg" {
		return "ffmpeg"
	}
	return "http"
}

// cmdTakeSnapshot scatta uno snapshot dalla telecamera configurata.
// Salva l'immagine nella directory configurata e gestisce la rotazione dei file.
func (b *DoorPhoneServer) cmdTakeSnapshot() {
	snapshotDir := Config.Global.Software.Camera.Snapshot.Dir
	if snapshotDir == "" {
		log.Println("error: snapshot dir not configured in XML")
		return
	}
	maxSnapshots := Config.Global.Software.Camera.Snapshot.MaxSnapshots
	if maxSnapshots <= 0 {
		maxSnapshots = 20
	}

	if err := os.MkdirAll(snapshotDir, 0750); err != nil {
		log.Println("Error creating snapshot directory:", err)
		return
	}

	fileName := filepath.Join(snapshotDir, "snapshot_"+time.Now().Format("20060102_150405")+".jpg")

	switch configuredSnapshotMethod() {
	case "ffmpeg":
		b.takeSnapshotFFmpeg(fileName)
	default:
		b.takeSnapshotHTTP(fileName)
	}

	// Rotate: remove oldest snapshots exceeding maxSnapshots
	retentionDays := Config.Global.Software.Camera.Snapshot.RetentionDays
	rotateSnapshots(snapshotDir, maxSnapshots, retentionDays)
}

// takeSnapshotHTTP cattura uno snapshot dalla telecamera tramite richiesta HTTP.
// @param fileName percorso completo del file dove salvare lo snapshot
func (b *DoorPhoneServer) takeSnapshotHTTP(fileName string) {
	snapshotURL := Config.Global.Software.Camera.Snapshot.Endpoint
	if snapshotURL == "" {
		log.Println("error: snapshot endpoint not configured in XML")
		return
	}
	if u, p := Config.Global.Software.Camera.Username, Config.Global.Software.Camera.Password; u != "" {
		sep := "&"
		if !strings.Contains(snapshotURL, "?") {
			sep = "?"
		}
		snapshotURL += sep + "user=" + u + "&password=" + p
	}

	file, err := os.Create(fileName)
	if err != nil {
		log.Println("Error creating snapshot file:", err)
		return
	}
	defer file.Close()

	client := http.Client{
		Timeout: httpClientTimeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			r.URL.Opaque = r.URL.Path
			return nil
		},
	}

	resp, err := client.Get(snapshotURL)
	if err != nil {
		log.Println("Error during HTTP request:", err)
		os.Remove(fileName)
		return
	}
	defer resp.Body.Close()

	size, err := io.Copy(file, resp.Body)
	if err != nil {
		log.Println("Error copying content to file:", err)
		os.Remove(fileName)
		return
	}

	log.Printf("Snapshot saved %s (%d bytes)\n", fileName, size)
}

// takeSnapshotFFmpeg cattura uno snapshot dalla telecamera tramite flusso RTSP usando ffmpeg.
// @param fileName percorso completo del file dove salvare lo snapshot
func (b *DoorPhoneServer) takeSnapshotFFmpeg(fileName string) {
	rtspURL := Config.Global.Software.Camera.Video.Endpoint
	if rtspURL == "" {
		log.Println("Error: no RTSP video endpoint configured")
		return
	}
	if u, p := Config.Global.Software.Camera.Username, Config.Global.Software.Camera.Password; u != "" {
		rtspURL = strings.Replace(rtspURL, "rtsp://", "rtsp://"+u+":"+p+"@", 1)
	}

	cmd := exec.Command("ffmpeg", "-y", "-rtsp_transport", "tcp", "-i", rtspURL, "-frames:v", "1", "-update", "1", fileName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error taking snapshot via ffmpeg: %v\n%s\n", err, string(output))
		os.Remove(fileName)
		return
	}

	info, err := os.Stat(fileName)
	if err != nil {
		log.Println("Error: snapshot file not found after ffmpeg:", err)
		return
	}

	log.Printf("Snapshot saved %s (%d bytes)\n", fileName, info.Size())
}

// isSnapshotFile verifica se un nome file corrisponde a un file snapshot gestito dal sistema.
// @param name nome del file da verificare
// @return true se il file è uno snapshot valido
func isSnapshotFile(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".jpg") && !strings.HasSuffix(lower, ".jpeg") {
		return false
	}
	if strings.HasPrefix(name, "snapshot_") {
		return true
	}
	for _, p := range []string{"P1_", "P2_", "P3_", "P4_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// rotateSnapshots gestisce la rotazione dei file snapshot, eliminando i più vecchi
// quando il numero supera il massimo e quelli scaduti rispetto ai giorni di retention.
// @param dir directory contenente gli snapshot
// @param max numero massimo di snapshot da mantenere
// @param retentionDays numero di giorni di conservazione degli snapshot (0 = disabilitato)
func rotateSnapshots(dir string, max int, retentionDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var snapshots []string
	for _, e := range entries {
		if !e.IsDir() && isSnapshotFile(e.Name()) {
			snapshots = append(snapshots, e.Name())
		}
	}
	sort.Strings(snapshots)
	for len(snapshots) > max {
		os.Remove(filepath.Join(dir, snapshots[0]))
		log.Printf("Rotated old snapshot: %s\n", snapshots[0])
		snapshots = snapshots[1:]
	}
	if retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -retentionDays)
		for _, name := range snapshots {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(dir, name))
				log.Printf("Expired snapshot removed: %s\n", name)
			}
		}
	}
}

// cmdRingPiano gestisce il suono del campanello per un piano specifico.
// Abilita l'audio, riproduce il suono di chiamata e invia una notifica push con snapshot.
// @param piano identificatore del piano (es. "P1", "P2", "P3", "P4")
func (b *DoorPhoneServer) cmdRingPiano(piano string) {
	log.Printf("info: RING %s - sending ring to %s", piano, piano)
	activeCallMu.Lock()
	activeCallUser = piano
	activeCallMu.Unlock()
	GlobalSpeakingLog.Open(piano)
	b.cmdMuteUnmute("unmute")
	TTSEvent("ring")
	b.SendMessageToUser(piano, "ring")
	PushoverSendPushNotificationWithAttach(piano)
	go b.SpotlightTrigger(60)
}

// cmdUnlockDoor sblocca la porta attivando il relè GPIO per un tempo configurato.
// Invia una notifica push e restituisce un JSON con la conferma dell'operazione.
// @param P identificatore del piano/utente che ha richiesto lo sblocco
// @return stringa JSON con l'esito dell'operazione o errore
func (b *DoorPhoneServer) cmdUnlockDoor(P string) (string, error) {

	// Ritorna {"unlock_door": P}

	log.Println("info: UNLOCK Requested from: " + P)

	// Avvia una goroutine per gestire lo sblocco della porta in background
	go func() {
		GPIOOutPin("unlockdoor", "on")
		time.Sleep(unlockDoorHoldTime)
		GPIOOutPin("unlockdoor", "off")
		PushoverSendPushNotification("Unlock from: " + P)
	}()

	// Creare la struttura JSON
	response := struct {
		UnlockDoor string `json:"unlock_door"`
	}{
		UnlockDoor: P,
	}

	// Convertire la struttura in JSON
	jsonResponse, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("errore nella creazione del JSON: %v", err)
	}

	return string(jsonResponse), nil
}

// GetGPIO restituisce il numero di pin GPIO associato al nome specificato nella configurazione.
// @param name nome del pin GPIO da cercare nella configurazione XML
// @return numero del pin GPIO, o -1 se non trovato
func GetGPIO(name string) int {
	for _, pin := range Config.Global.Hardware.IO.Pins.Pin {
		if pin.Name == name {
			return int(pin.PinNo)
		}
	}
	return -1
}

// InitPowerTabletState legge lo stato fisico del GPIO power_tablet e sincronizza
// la variabile software isOn con la realtà hardware. Da chiamare all'avvio.
func InitPowerTabletState() {
	gpioNumber := GetGPIO("power_tablet")
	if gpioNumber == -1 {
		return
	}
	state, err := GetGPIOState(gpioNumber)
	if err != nil {
		log.Printf("warn: InitPowerTabletState: cannot read GPIO state: %v\n", err)
		return
	}
	// Logica invertita: GPIO LOW (0) = relay attivo = tablet ON
	powerTabletState.mu.Lock()
	powerTabletState.isOn = (state == 0)
	powerTabletState.mu.Unlock()
	log.Printf("info: POWER TABLET initial state from GPIO: isOn=%v (gpio=%d)\n", powerTabletState.isOn, state)
}

// cmdPowertablet_on attiva l'alimentazione del tablet impostando il pin GPIO "power_tablet" su off (logica invertita).
func (b *DoorPhoneServer) cmdPowertablet_on() {
	if err := b.cmdPowertabletWithSource("on", "command"); err != nil {
		log.Printf("error: cmdPowertablet_on failed: %v\n", err)
	}
}

// cmdPowertablet_off disattiva l'alimentazione del tablet impostando il pin GPIO "power_tablet" su on (logica invertita).
func (b *DoorPhoneServer) cmdPowertablet_off() {
	if err := b.cmdPowertabletWithSource("off", "command"); err != nil {
		log.Printf("error: cmdPowertablet_off failed: %v\n", err)
	}
}

// cmdPowertabletWithSource executes power tablet command with debouncing and state tracking
func (b *DoorPhoneServer) cmdPowertabletWithSource(action string, source string) error {
	powerTabletState.mu.Lock()
	defer powerTabletState.mu.Unlock()

	appendHistory := func(success bool, reason string) {
		powerTabletState.changeHistory = append(powerTabletState.changeHistory, PowerTabletChange{
			Timestamp: time.Now(),
			Action:    action,
			Source:    source,
			Success:   success,
			Reason:    reason,
		})
		if len(powerTabletState.changeHistory) > maxPowerTabletHistory {
			powerTabletState.changeHistory = powerTabletState.changeHistory[1:]
		}
	}

	// 1. Check debouncing
	timeSinceLastChange := time.Since(powerTabletState.lastChangeTime)
	if !powerTabletState.lastChangeTime.IsZero() && timeSinceLastChange < powerTabletState.minDebounceTime {
		remaining := powerTabletState.minDebounceTime - timeSinceLastChange
		err := fmt.Errorf("power tablet debounce active, wait %v", remaining.Round(time.Second))
		log.Printf("warn: POWER TABLET DEBOUNCED - %v (action: %s, source: %s)", err, action, source)
		appendHistory(false, fmt.Sprintf("debounce %v remaining", remaining.Round(time.Second)))
		return err
	}

	// 2. Check redundant state
	if (action == "on" && powerTabletState.isOn) || (action == "off" && !powerTabletState.isOn) {
		log.Printf("info: POWER TABLET already in state %s, skipping redundant command (source: %s)", action, source)
		appendHistory(false, fmt.Sprintf("already %s", action))
		return nil
	}

	// 3. Execute command
	log.Printf("info: POWER TABLET %s (source: %s)", strings.ToUpper(action), source)

	if action == "on" {
		GPIOOutPin("power_tablet", "off") // Inverted logic
		powerTabletState.isOn = true
	} else {
		GPIOOutPin("power_tablet", "on") // Inverted logic
		powerTabletState.isOn = false
	}

	powerTabletState.lastChangeTime = time.Now()
	appendHistory(true, "")

	// Optional notification
	// PushoverSendPushNotification(fmt.Sprintf("📱 Tablet %s (%s)", action, source))

	return nil
}

// GetPowerTabletStatus returns the current power tablet state (thread-safe)
func GetPowerTabletStatus() map[string]interface{} {
	powerTabletState.mu.RLock()
	defer powerTabletState.mu.RUnlock()

	var timeSinceChange string
	if !powerTabletState.lastChangeTime.IsZero() {
		timeSinceChange = time.Since(powerTabletState.lastChangeTime).String()
	} else {
		timeSinceChange = "never"
	}

	// Copy history
	history := make([]PowerTabletChange, len(powerTabletState.changeHistory))
	copy(history, powerTabletState.changeHistory)

	return map[string]interface{}{
		"is_on":             powerTabletState.isOn,
		"last_change_time":  powerTabletState.lastChangeTime,
		"time_since_change": timeSinceChange,
		"debounce_seconds":  powerTabletState.minDebounceTime.Seconds(),
		"recent_history":    history,
	}
}

// cmdSendPushowerMessage invia una notifica push di test tramite Pushover con allegato immagine.
func (b *DoorPhoneServer) cmdSendPushowerMessage() {
	log.Println("info: PushoverSendPushNotificationWithattach")
	PushoverSendPushNotificationWithAttach("Test Immagine")
}

// cmdRebootServer riavvia il sistema tramite systemctl dopo aver inviato una notifica push.
func (b *DoorPhoneServer) cmdRebootServer() {
	log.Println("info: cmdRebootServer")
	PushoverSendPushNotificationWithAttach("Reboot Server")
	cmd := exec.Command("sudo", "systemctl", "reboot")
	err := cmd.Run()
	if err != nil {
		log.Printf("error: cmdRebootServer failed: %v", err)
		return
	}
}

// cmdGetRspiTemp legge la temperatura del processore Raspberry Pi tramite vcgencmd.
// @return stringa JSON con il valore di temperatura in gradi Celsius, o errore
func (b *DoorPhoneServer) cmdGetRspiTemp() (string, error) {
	cmd := exec.Command("vcgencmd", "measure_temp")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("errore nell'esecuzione del comando: %v", err)
	}

	tempStr := strings.TrimPrefix(string(output), "temp=")
	tempStr = strings.TrimSuffix(tempStr, "'C\n")
	temp, err := strconv.ParseFloat(tempStr, 64)
	if err != nil {
		return "", fmt.Errorf("errore nella conversione della temperatura: %v", err)
	}

	// Creare il JSON con la temperatura
	jsonResult := fmt.Sprintf(`{"temp": %.2f}`, temp)

	return jsonResult, nil
}

// execute_command interpreta e esegue i comandi ricevuti tramite messaggi di testo Mumble.
// I messaggi devono avere il prefisso "cmd-" seguito dal nome del comando.
// @param b istanza del client doorphoneserver
// @param message messaggio di testo ricevuto da Mumble
func execute_command(b *DoorPhoneServer, sender string, senderSession uint32, message string) {
	var cmd string
	message = strings.TrimSpace(message)

	if strings.HasPrefix(message, "cmd-") {

		cmd = message[4:]
		log.Printf("info: CMD RECEIVED:%s\n", cmd)

		if cmd == "accept-call" || cmd == "close-call" {
			senderLower := strings.ToLower(sender)
			activeCallMu.Lock()
			activeUserLower := strings.ToLower(activeCallUser)
			emptySession := activeCallUser == ""
			callUserForLog := activeCallUser
			activeCallMu.Unlock()
			if senderLower != activeUserLower {
				log.Printf("warn: [CALL] Comando '%s' da '%s' ignorato\n", cmd, sender)
				if emptySession {
					log.Printf("warn: [CALL]   Motivo: nessuna sessione attiva\n")
				} else {
					log.Printf("warn: [CALL]   Motivo: mittente non autorizzato (sessione aperta con '%s')\n", callUserForLog)
				}
				return
			}
		}

		if cmd == "accept-call" {
			log.Println("info: [CALL] Android client accepted call - starting TX")
			b.cmdStartTransmitting()
			b.cmdMuteUnmute("unmute")
			b.SendMessageToSession(senderSession, "ack-accept-call")
			log.Printf("info: [CALL] ACK sent to %s (cmd-ack-accept-call)\n", sender)
		}

		if cmd == "close-call" {
			log.Println("info: [CALL] Android client closed call - stopping TX")
			b.SendMessageToSession(senderSession, "ack-close-call")
			log.Printf("info: [CALL] ACK sent to %s (cmd-ack-close-call)\n", sender)
			if b.IsTransmitting.Load() {
				b.TransmitStop(false)
				log.Println("info: [CALL] TX stopped")
			} else {
				log.Println("info: [CALL] TX was not active")
			}
			GlobalSpeakingLog.Close()
			AudioChannelMonitor.Reset()
			activeCallMu.Lock()
			activeCallUser = ""
			activeCallMu.Unlock()
			log.Println("info: [CALL] Call session closed")
		}

		if cmd == "unlock" {
			_, err := b.cmdUnlockDoor("P")
			if err != nil {
				log.Printf("Errore durante lo sblocco della porta: %v", err)
				return // Esci se c'è un errore durante lo sblocco
			}
			b.cmdMuteUnmute("mute")
			time.Sleep(200 * time.Millisecond)
			b.cmdMuteUnmute("unmute")
		}

		if cmd == "temp" || cmd == "temperature" {
			result, err := b.cmdGetRspiTemp()
			if err != nil {
				log.Printf("Errore durante la lettura della temperatura: %v", err)
				return
			}
			log.Printf("Temperatura CPU: %s", result)
		}
	}

}

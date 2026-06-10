// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gregdel/pushover"
)

// PushoverUsageStats contiene l'ultimo stato noto dei messaggi Pushover
type PushoverUsageStats struct {
	mu        sync.RWMutex
	Remaining int
	Total     int
	NextReset time.Time
	LastUpdate time.Time
	Available bool // false se non è ancora stato inviato nessun messaggio
}

var pushoverUsage = &PushoverUsageStats{}

// GetPushoverUsage restituisce l'ultimo stato noto dei messaggi Pushover (thread-safe)
func GetPushoverUsage() (remaining, total int, nextReset time.Time, lastUpdate time.Time, available bool) {
	pushoverUsage.mu.RLock()
	defer pushoverUsage.mu.RUnlock()
	return pushoverUsage.Remaining, pushoverUsage.Total, pushoverUsage.NextReset, pushoverUsage.LastUpdate, pushoverUsage.Available
}

// FetchPushoverLimits interroga l'API Pushover per ottenere i limiti correnti
// senza inviare alcun messaggio. Viene chiamata al primo accesso al widget.
func FetchPushoverLimits() {
	if !Config.Global.Software.PUSHOVER.Enabled {
		return
	}
	token := Config.Global.Software.PUSHOVER.APIToken
	if token == "" {
		return
	}
	url := "https://api.pushover.net/1/apps/limits.json?token=" + token
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("warn: FetchPushoverLimits: %v", err)
		return
	}
	defer resp.Body.Close()
	var body struct {
		Status    int    `json:"status"`
		Limit     string `json:"limit"`
		Remaining string `json:"remaining"`
		Reset     int64  `json:"reset"`
	}
	// Prova prima header X-Ratelimit-* (standard Pushover)
	hLimit := resp.Header.Get("X-Limit-App-Limit")
	hRemaining := resp.Header.Get("X-Limit-App-Remaining")
	hReset := resp.Header.Get("X-Limit-App-Reset")
	if hLimit == "" {
		// Fallback: leggi il body JSON
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			log.Printf("warn: FetchPushoverLimits decode: %v", err)
			return
		}
		hLimit = body.Limit
		hRemaining = body.Remaining
		hReset = fmt.Sprintf("%d", body.Reset)
	}
	limit, _ := strconv.Atoi(hLimit)
	remaining, _ := strconv.Atoi(hRemaining)
	resetUnix, _ := strconv.ParseInt(hReset, 10, 64)
	if limit == 0 && remaining == 0 {
		return
	}
	var nextReset time.Time
	if resetUnix > 0 {
		nextReset = time.Unix(resetUnix, 0)
	}
	pushoverUsage.mu.Lock()
	defer pushoverUsage.mu.Unlock()
	pushoverUsage.Total = limit
	pushoverUsage.Remaining = remaining
	pushoverUsage.NextReset = nextReset
	pushoverUsage.LastUpdate = time.Now()
	pushoverUsage.Available = true
}

func updatePushoverUsage(resp *pushover.Response) {
	if resp == nil || resp.Limit == nil {
		return
	}
	pushoverUsage.mu.Lock()
	defer pushoverUsage.mu.Unlock()
	pushoverUsage.Remaining = resp.Limit.Remaining
	pushoverUsage.Total = resp.Limit.Total
	pushoverUsage.NextReset = resp.Limit.NextReset
	pushoverUsage.LastUpdate = time.Now()
	pushoverUsage.Available = true
}

// PushoverSendPushNotification invia una notifica push semplice tramite il servizio Pushover.
// @param message testo della notifica da inviare
func PushoverSendPushNotification(message string) {
	if !Config.Global.Software.PUSHOVER.Enabled {
		log.Println("debug: Pushnotification Pushover Disabled in Config")
		return
	}

	log.Println("debug: Pushnotification Pushover Enabled in Config")

	APIToken := Config.Global.Software.PUSHOVER.APIToken
	UserKey := Config.Global.Software.PUSHOVER.UserKey

	// Initialize Pushover app and recipient
	app := pushover.New(APIToken)
	recipient := pushover.NewRecipient(UserKey)

	// Create and send the message
	msg := pushover.NewMessage(message)
	response, err := app.SendMessage(msg, recipient)
	if err != nil {
		log.Printf("error sending pushover notification: %v\n", err)
		return
	}

	updatePushoverUsage(response)
	log.Printf("debug: Pushover Response: %+v\n", response)
}

// PushoverSendPushNotificationWithAttach cattura uno snapshot dalla telecamera e lo invia
// come allegato tramite notifica push Pushover con priorità alta.
// @param piano identificatore del piano/campanello che ha attivato la notifica (es. "P1", "P2")
func PushoverSendPushNotificationWithAttach(piano string) {
	if !Config.Global.Software.PUSHOVER.Enabled {
		log.Println("debug: Pushnotification Pushover Disabled in Config")
		return
	}

	log.Println("debug: Pushnotification Pushover Enabled in Config")

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
	snapshotDirPath := Config.Global.Software.Camera.Snapshot.Dir
	if snapshotDirPath == "" {
		log.Println("error: snapshot dir not configured in XML")
		return
	}

	// Nome file parlante: P1_snapshot_20060102_150405.jpeg
	timestamp := time.Now().Format("20060102_150405")
	fileName := filepath.Join(snapshotDirPath, piano+"_snapshot_"+timestamp+".jpeg")

	if err := os.MkdirAll(snapshotDirPath, 0750); err != nil {
		log.Printf("error creating snapshot directory: %v\n", err)
		return
	}

	// Capture snapshot using configured method; fallback to the other method on failure.
	if err := captureSnapshotForNotification(snapshotURL, fileName); err != nil {
		log.Printf("error capturing snapshot for notification: %v\n", err)
		return
	}

	// Rotazione snapshot
	maxSnapshots := Config.Global.Software.Camera.Snapshot.MaxSnapshots
	if maxSnapshots <= 0 {
		maxSnapshots = 20
	}
	retentionDays := Config.Global.Software.Camera.Snapshot.RetentionDays
	rotateSnapshots(snapshotDirPath, maxSnapshots, retentionDays)

	// Apri il file per allegare alla notifica
	file, err := os.Open(fileName)
	if err != nil {
		log.Printf("error opening snapshot for notification: %v\n", err)
		return
	}
	defer file.Close()

	APIToken := Config.Global.Software.PUSHOVER.APIToken
	UserKey := Config.Global.Software.PUSHOVER.UserKey

	// Initialize Pushover app and recipient
	app := pushover.New(APIToken)
	recipient := pushover.NewRecipient(UserKey)

	// Create the message with an attachment
	msg := &pushover.Message{
		Message:  piano,
		Title:    "Someone at Door",
		Priority: pushover.PriorityHigh,
		URL:      snapshotURL,
		URLTitle: "DoorStream",
		Sound:    pushover.SoundPushover,
	}
	if err := msg.AddAttachment(file); err != nil {
		log.Printf("error adding attachment: %v\n", err)
		return
	}

	// Send the message
	response, err := app.SendMessage(msg, recipient)
	if err != nil {
		log.Printf("error sending pushover notification with attachment: %v\n", err)
		return
	}

	updatePushoverUsage(response)
	log.Printf("debug: Pushover Response: %+v\n", response)
}

// captureSnapshotForNotification cattura uno snapshot usando il metodo configurato
// con fallback automatico all'altro metodo in caso di errore.
// @param snapshotURL URL HTTP per la cattura snapshot via HTTP
// @param fileName percorso del file dove salvare lo snapshot
// @return errore se entrambi i metodi di cattura falliscono
func captureSnapshotForNotification(snapshotURL, fileName string) error {
	method := configuredSnapshotMethod()

	if method == "ffmpeg" {
		if err := captureSnapshotViaFFmpeg(fileName); err == nil {
			return nil
		}
		log.Println("warn: ffmpeg snapshot failed for push, fallback to HTTP")
		return downloadFile(snapshotURL, fileName)
	}

	if err := downloadFile(snapshotURL, fileName); err == nil {
		return nil
	}
	log.Println("warn: HTTP snapshot failed for push, fallback to ffmpeg")
	return captureSnapshotViaFFmpeg(fileName)
}

// captureSnapshotViaFFmpeg cattura uno snapshot dalla telecamera tramite flusso RTSP con timeout di 15 secondi.
// @param fileName percorso del file dove salvare lo snapshot
// @return errore se la cattura non può essere completata entro il timeout
func captureSnapshotViaFFmpeg(fileName string) error {
	rtspURL := Config.Global.Software.Camera.Video.Endpoint
	if rtspURL == "" {
		return fmt.Errorf("no RTSP video endpoint configured")
	}
	if u, p := Config.Global.Software.Camera.Username, Config.Global.Software.Camera.Password; u != "" {
		rtspURL = strings.Replace(rtspURL, "rtsp://", "rtsp://"+u+":"+p+"@", 1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-rtsp_transport", "tcp", "-i", rtspURL, "-frames:v", "1", "-update", "1", fileName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(fileName)
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg snapshot timeout")
		}
		return fmt.Errorf("ffmpeg snapshot error: %v - %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(fileName); err != nil {
		os.Remove(fileName)
		return fmt.Errorf("ffmpeg snapshot file missing: %w", err)
	}
	return nil
}

// downloadFile scarica un file da un URL e lo salva nel percorso specificato.
// @param url URL da cui scaricare il file
// @param filePath percorso locale dove salvare il file scaricato
// @return errore se il download o il salvataggio falliscono
func downloadFile(url, filePath string) error {
	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	client := http.Client{Timeout: httpClientTimeout}

	// Download the content
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		os.Remove(filePath)
		return fmt.Errorf("snapshot HTTP status %d", resp.StatusCode)
	}

	// Copy the response body to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		os.Remove(filePath)
		return fmt.Errorf("failed to save file: %w", err)
	}

	log.Printf("File downloaded successfully: %s\n", filePath)
	return nil
}

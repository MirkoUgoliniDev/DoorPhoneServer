// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed webpanel_static
var staticFS embed.FS

// --- Log2Ram metrics history ---

// Log2RamMetric contiene un campione di metriche SD e log in RAM.
type Log2RamMetric struct {
	Timestamp      int64   `json:"timestamp"`
	SDWriteSectors uint64  `json:"sd_write_sectors"`
	SDWriteMB      float64 `json:"sd_write_mb"`
	SDIOTimeMs     uint64  `json:"sd_io_time_ms"`
	SDIOPct        float64 `json:"sd_io_pct"`
	LogRAMMB       float64 `json:"log_ram_mb"`
	UptimeSec      float64 `json:"uptime_sec"`
}

var (
	log2ramMetricsMu   sync.RWMutex
	log2ramMetricsHist []Log2RamMetric
)

const log2ramHistMax = 60

// --- Log2Ram install job ---
type log2ramInstallJobState struct {
	mu      sync.Mutex
	Running bool
	Done    bool
	Success bool
	Output  string
}

var l2rInstallJob log2ramInstallJobState

// RegisterWebPanelRoutes registers all web panel routes on the given mux
func (b *DoorPhoneServer) RegisterWebPanelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/panel", b.handlePanel)
	mux.HandleFunc("/panel/api/config", b.handleConfig)
	mux.HandleFunc("/panel/api/upload", b.handleUpload)
	mux.HandleFunc("/panel/api/sounds", b.handleSounds)
	mux.HandleFunc("/panel/api/sounds/delete", b.handleSoundDelete)
	mux.HandleFunc("/panel/api/sounds/play/", b.handleSoundPlay)
	mux.HandleFunc("/panel/api/sounds/playpi/", b.handleSoundPlayPi)
	mux.HandleFunc("/panel/api/sounds/stoppi", b.handleSoundStopPi)
	mux.HandleFunc("/panel/api/service", b.handleService)
	mux.HandleFunc("/panel/api/streamer", b.handleStreamer)
	mux.HandleFunc("/panel/api/stream", b.handleRTSPStream)
	mux.HandleFunc("/panel/api/stream/probe", b.handleRTSPProbe)
	mux.HandleFunc("/panel/api/webrtc/offer", b.handleWebRTCOffer)
	mux.HandleFunc("/panel/api/spotlight", b.handleSpotlight)
	mux.HandleFunc("/panel/api/cameratime", b.handleCameraTime)
	mux.HandleFunc("/panel/api/cameraosd", b.handleCameraOSD)
	mux.HandleFunc("/panel/api/mumble", b.handleMumble)
	mux.HandleFunc("/panel/api/tablet", b.handleTablet)
	mux.HandleFunc("/panel/api/tablet-status", b.handleTabletStatus)
	mux.HandleFunc("/panel/api/log", b.handleLog)
	mux.HandleFunc("/panel/api/stats", b.handleStats)
	mux.HandleFunc("/panel/api/volume", b.handleVolume)
	mux.HandleFunc("/panel/api/mute", b.handleMuteToggle)
	mux.HandleFunc("/panel/api/snapshots", b.handleSnapshots)
	mux.HandleFunc("/panel/api/snapshots/view/", b.handleSnapshotView)
	mux.HandleFunc("/panel/api/snapshots/delete", b.handleSnapshotDelete)
	mux.HandleFunc("/panel/api/snapshots/deleteall", b.handleSnapshotDeleteAll)
	mux.HandleFunc("/panel/api/snapshots/take", b.handleSnapshotTake)
	mux.HandleFunc("/panel/api/cleanup", b.handleDiskCleanup)
	mux.HandleFunc("/panel/api/pushover-usage", b.handlePushoverUsage)
	mux.HandleFunc("/panel/api/alarms", b.handleAlarms)
	b.startAlarmMonitor()
	mux.HandleFunc("/panel/api/mumbleusers", b.handleMumbleUsers)
	// APK Updater endpoints (Android app)
	mux.HandleFunc("/apk/list", b.handleApkList)
	mux.HandleFunc("/config", b.handleAppConfig)
	mux.HandleFunc("/config/", b.handleAppConfig)
	// APK Manager panel endpoints
	mux.HandleFunc("/panel/api/apk/list", b.handlePanelApkList)
	mux.HandleFunc("/panel/api/apk/upload", b.handleApkUpload)
	mux.HandleFunc("/panel/api/apk/delete", b.handleApkDelete)
	// Audio Test endpoints
	mux.HandleFunc("/panel/api/audiotest", b.handleAudioTestList)
	mux.HandleFunc("/panel/api/audiotest/upload", b.handleAudioTestUpload)
	mux.HandleFunc("/panel/api/audiotest/delete", b.handleAudioTestDelete)
	mux.HandleFunc("/panel/api/audiotest/play/", b.handleAudioTestPlay)
	mux.HandleFunc("/panel/api/audiotest/run", b.handleAudioTestRun)
	mux.HandleFunc("/panel/api/audiotest/rxstatus", b.handleAudioTestRxStatus)
	mux.HandleFunc("/panel/api/audiotest/txstatus", b.handleAudioTestTxStatus)
	// Chima AI — OpenRouter model catalogue
	mux.HandleFunc("/panel/api/openrouter/models", b.handleOpenRouterModels)
	mux.HandleFunc("/panel/api/openrouter/selected", b.handleOpenRouterSelected)
	mux.HandleFunc("/panel/api/ai/analyze", b.handleAIAnalyze)
	// Connection metrics endpoint
	mux.HandleFunc("/panel/api/connection-metrics", b.handleConnectionMetrics)
	// System monitoring endpoints
	mux.HandleFunc("/panel/api/system-metrics", b.handleSystemMetrics)
	mux.HandleFunc("/panel/api/system-metrics-history", b.handleSystemMetricsHistory)
	mux.HandleFunc("/panel/api/speaking-log", b.handleSpeakingLog)
	mux.HandleFunc("/panel/api/speaking-log/clear", b.handleSpeakingLogClear)
	// Crontab manager
	mux.HandleFunc("/panel/api/cron", b.handleCron)
	// Log2Ram manager
	mux.HandleFunc("/panel/api/log2ram/status", b.handleLog2RamStatus)
	mux.HandleFunc("/panel/api/log2ram/metrics", b.handleLog2RamMetrics)
	mux.HandleFunc("/panel/api/log2ram/metrics-history", b.handleLog2RamMetricsHistory)
	mux.HandleFunc("/panel/api/log2ram/sync", b.handleLog2RamSync)
	mux.HandleFunc("/panel/api/log2ram/restart", b.handleLog2RamRestart)
	mux.HandleFunc("/panel/api/log2ram/files", b.handleLog2RamFiles)
	mux.HandleFunc("/panel/api/log2ram/install", b.handleLog2RamInstall)
	mux.HandleFunc("/panel/api/log2ram/install/status", b.handleLog2RamInstallStatus)
	mux.HandleFunc("/panel/api/log2ram/config", b.handleLog2RamConfig)
	startLog2RamMetricsSampler()

	// System info
	mux.HandleFunc("/panel/api/sysinfo", b.handleSysInfo)
	mux.HandleFunc("/panel/api/features", b.handlePanelFeatures)
	// ESP32-S3 control endpoints
	mux.HandleFunc("/panel/api/esp32/status", b.handleESP32Status)
	mux.HandleFunc("/panel/api/esp32/fan", b.handleESP32Fan)
	mux.HandleFunc("/panel/api/esp32/door", b.handleESP32Door)
	mux.HandleFunc("/panel/api/esp32/cardlog/clear", b.handleESP32CardLogClear)
	mux.HandleFunc("/panel/api/esp32/usblog", b.handleESP32USBLog)
	mux.HandleFunc("/panel/api/esp32/tablet", b.handleESP32Tablet)
	mux.HandleFunc("/panel/api/esp32/floors", b.handleESP32Floors)
	// NFC Whitelist — gestione via protocol coordinato con ESP32
	mux.HandleFunc("/whitelist", b.handleWhitelistPage)
	mux.HandleFunc("/api/whitelist", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			b.handleWhitelistGet(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/whitelist/enroll", b.handleWhitelistEnrollStart)
	mux.HandleFunc("/api/whitelist/enroll/events", b.handleWhitelistEnrollEvents)
	mux.HandleFunc("/api/whitelist/enroll/cancel", b.handleWhitelistEnrollCancel)
	mux.HandleFunc("/api/whitelist/sync", b.handleWhitelistSync)
	mux.HandleFunc("/api/whitelist/clearall", b.handleWhitelistClearAll)
	mux.HandleFunc("/api/whitelist/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b.handleWhitelistUpdate(w, r)
		} else if r.Method == http.MethodDelete {
			b.handleWhitelistDelete(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/toggle") {
			b.handleWhitelistToggle(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	apkPath := filepath.Join(filepath.Dir(ConfigXMLFile), "apk")
	mux.Handle("/apk/", http.StripPrefix("/apk/", http.FileServer(http.Dir(apkPath))))
	staticContent, _ := fs.Sub(staticFS, "webpanel_static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))
}

// handleSpeakingLogClear svuota il log del parlato.
func (b *DoorPhoneServer) handleSpeakingLogClear(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	GlobalSpeakingLog.Clear()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

// handleSpeakingLog ritorna la sequenza cronologica di chi ha parlato nel canale Mumble.
func (b *DoorPhoneServer) handleSpeakingLog(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries := GlobalSpeakingLog.GetRecent(100)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		log.Printf("error: Failed to encode speaking log: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// --- Alarm Monitor ---

// AlarmConfig contiene le soglie di allarme persistenti per Load Average, Disk e Throttle.
type AlarmConfig struct {
	LoadAvg  AlarmThreshold `json:"load_avg"`
	DiskPct  AlarmThreshold `json:"disk_pct"`
	Throttle AlarmThreshold `json:"throttle"`
	CpuTemp  AlarmThreshold `json:"cpu_temp"`
	RamPct   AlarmThreshold `json:"ram_pct"`
}

type AlarmThreshold struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold,omitempty"` // non usato per Throttle
}

type AIConfig struct {
	SelectedModelID   string `json:"selected_model_id,omitempty"`
	SelectedModelName string `json:"selected_model_name,omitempty"`
}

var (
	alarmCooldown = map[string]time.Time{}
)

// jsonStr serializza una stringa in JSON-safe (gestisce newline, caratteri di controllo, ecc.).
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func alarmFilePath() string {
	return "/home/doorphoneserver/preferences/alarms.json"
}

func aiConfigFilePath() string {
	return "/home/doorphoneserver/preferences/ai.json"
}

func loadAIConfig() AIConfig {
	data, err := os.ReadFile(aiConfigFilePath())
	if err != nil {
		return AIConfig{}
	}
	var cfg AIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AIConfig{}
	}
	return cfg
}

func saveAIConfig(cfg AIConfig) error {
	if err := os.MkdirAll(filepath.Dir(aiConfigFilePath()), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(aiConfigFilePath(), data, 0644)
}

func loadAlarms() AlarmConfig {
	data, err := os.ReadFile(alarmFilePath())
	if err != nil {
		return AlarmConfig{}
	}
	var cfg AlarmConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("error: Failed to unmarshal alarm config: %v", err)
		return AlarmConfig{}
	}
	return cfg
}

func saveAlarms(cfg AlarmConfig) error {
	if err := os.MkdirAll(filepath.Dir(alarmFilePath()), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(alarmFilePath(), data, 0644)
}

func (b *DoorPhoneServer) handleAlarms(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		cfg := loadAlarms()
		if err := json.NewEncoder(w).Encode(cfg); err != nil {
			log.Printf("error: Failed to encode alarm config: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}
	if r.Method == http.MethodPost {
		var cfg AlarmConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			fmt.Fprintf(w, `{"ok":false,"error":"invalid json"}`)
			return
		}
		if err := saveAlarms(cfg); err != nil {
			fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(err.Error()))
			return
		}
		fmt.Fprintf(w, `{"ok":true}`)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// startAlarmMonitor avvia il loop di monitoraggio allarmi in background.
func (b *DoorPhoneServer) startAlarmMonitor() {
	go func() {
		for {
			time.Sleep(60 * time.Second)
			cfg := loadAlarms()

			// Load Average (primo valore, 1-min)
			if cfg.LoadAvg.Enabled {
				load := getLoadAvg()
				fields := strings.Fields(load)
				if len(fields) > 0 {
					val, err := strconv.ParseFloat(fields[0], 64)
					if err == nil && val > cfg.LoadAvg.Threshold {
						if time.Now().After(alarmCooldown["load_avg"]) {
							alarmCooldown["load_avg"] = time.Now().Add(30 * time.Minute)
							go PushoverSendPushNotification(fmt.Sprintf("⚠ Load Average alto: %.2f (soglia: %.2f)", val, cfg.LoadAvg.Threshold))
						}
					}
				}
			}

			// Disk Used %
			if cfg.DiskPct.Enabled {
				disk := getDiskUsage("/")
				if disk != nil && disk["total"] > 0 {
					pct := float64(disk["used"]) / float64(disk["total"]) * 100
					if pct > cfg.DiskPct.Threshold {
						if time.Now().After(alarmCooldown["disk_pct"]) {
							alarmCooldown["disk_pct"] = time.Now().Add(30 * time.Minute)
							go PushoverSendPushNotification(fmt.Sprintf("⚠ Disco pieno: %.0f%% (soglia: %.0f%%)", pct, cfg.DiskPct.Threshold))
						}
					}
				}
			}

			// CPU Temperature
			if cfg.CpuTemp.Enabled {
				temp := getCPUTemp()
				if temp > cfg.CpuTemp.Threshold {
					if time.Now().After(alarmCooldown["cpu_temp"]) {
						alarmCooldown["cpu_temp"] = time.Now().Add(30 * time.Minute)
						go PushoverSendPushNotification(fmt.Sprintf("⚠ Temperatura CPU alta: %.1f°C (soglia: %.1f°C)", temp, cfg.CpuTemp.Threshold))
					}
				}
			}

			// Throttle
			if cfg.Throttle.Enabled {
				thr := getThrottled()
				if thr != 0 {
					if time.Now().After(alarmCooldown["throttle"]) {
						alarmCooldown["throttle"] = time.Now().Add(30 * time.Minute)
						go PushoverSendPushNotification(fmt.Sprintf("⚠ Raspberry Pi in throttling! (0x%x)", thr))
					}
				}
			}

			// RAM Used %
			if cfg.RamPct.Enabled {
				memTotal := getMemInfo("MemTotal")
				memFree := getMemInfo("MemFree")
				if memTotal > 0 {
					pct := float64(memTotal-memFree) / float64(memTotal) * 100
					if pct > cfg.RamPct.Threshold {
						if time.Now().After(alarmCooldown["ram_pct"]) {
							alarmCooldown["ram_pct"] = time.Now().Add(30 * time.Minute)
							go PushoverSendPushNotification(fmt.Sprintf("⚠ RAM alta: %.0f%% (soglia: %.0f%%)", pct, cfg.RamPct.Threshold))
						}
					}
				}
			}
		}
	}()
}

// panelSecurityHeaders aggiunge gli header di sicurezza HTTP standard alle risposte del pannello web.
// @param w writer HTTP a cui aggiungere gli header di sicurezza
func panelSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
}

// --- Config Editor ---
func (b *DoorPhoneServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(ConfigXMLFile)
		if err != nil {
			http.Error(w, "Cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		if _, err := w.Write(data); err != nil {
			log.Printf("error: Failed to write config data: %v", err)
		}

	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1MB max
		if err != nil {
			http.Error(w, "Request too large or read error", http.StatusBadRequest)
			return
		}

		// validate XML is well-formed before saving
		decoder := xml.NewDecoder(strings.NewReader(string(body)))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"ok":false,"error":"XML non valido: %s"}`, strings.ReplaceAll(err.Error(), `"`, `\"`))
				return
			}
		}

		// backup before saving
		backupPath := ConfigXMLFile + ".bak." + time.Now().Format("20060102-150405")
		if orig, err := os.ReadFile(ConfigXMLFile); err == nil {
			if err := os.WriteFile(backupPath, orig, 0600); err != nil {
				log.Printf("error: Failed to create backup: %v", err)
			}
		}

		// Scrittura atomica (temp + rename): handleAppConfig rilegge l'XML dal disco
		// ad ogni richiesta, quindi un WriteFile non atomico potrebbe esporre un file
		// troncato a una lettura concorrente.
		tmpPath := ConfigXMLFile + ".tmp"
		if err := os.WriteFile(tmpPath, body, 0644); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"ok":false,"error":"Cannot write config: %s"}`, strings.ReplaceAll(err.Error(), `"`, `\"`))
			return
		}
		if err := os.Rename(tmpPath, ConfigXMLFile); err != nil {
			os.Remove(tmpPath)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"ok":false,"error":"Cannot write config: %s"}`, strings.ReplaceAll(err.Error(), `"`, `\"`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"backup":"%s"}`, filepath.Base(backupPath))

		// cleanup old config backups after creating a new one
		if configDir := filepath.Dir(ConfigXMLFile); configDir != "" {
			if removed, err := cleanupOldConfigBackups(configDir, "doorphoneserver.xml.bak.", 5*24*time.Hour); err == nil && removed > 0 {
				log.Printf("Removed %d old config backup files older than 5 days", removed)
			}
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// cleanupOldConfigBackups mantiene solo i keepN backup più recenti, rimuovendo i più vecchi.
// @param dir directory dove cercare i file di backup
// @param prefix prefisso del nome dei file di backup da pulire
// @param maxAge non utilizzato, mantenuto per compatibilità
// @param keepN numero massimo di backup da conservare
// @return numero di file rimossi e un eventuale errore
func cleanupOldConfigBackups(dir, prefix string, maxAge time.Duration, keepN ...int) (int, error) {
	max := 5
	if len(keepN) > 0 && keepN[0] > 0 {
		max = keepN[0]
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}

	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			backups = append(backups, e.Name())
		}
	}

	// os.ReadDir restituisce i file in ordine alfabetico = ordine cronologico per i nostri timestamp
	removed := 0
	if len(backups) > max {
		for _, name := range backups[:len(backups)-max] {
			_ = os.Remove(filepath.Join(dir, name))
			removed++
		}
	}
	return removed, nil
}

// --- Sound Upload ---
func (b *DoorPhoneServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
		http.Error(w, "File too large (max 10MB)", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file in request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// validate extension
	ext := strings.ToLower(filepath.Ext(handler.Filename))
	if ext != ".mp3" && ext != ".wav" {
		http.Error(w, "Only .mp3 and .wav files allowed", http.StatusBadRequest)
		return
	}

	// sanitize filename - only allow alphanumerics, hyphens, underscores, dots
	baseName := filepath.Base(handler.Filename)
	for _, c := range baseName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			http.Error(w, "Invalid filename characters", http.StatusBadRequest)
			return
		}
	}

	eventsDir := filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "events")
	destPath := filepath.Join(eventsDir, baseName)

	dst, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "Cannot create file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Write error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"file":"%s","size":%d}`, baseName, written)
}

// --- Sound List ---
func (b *DoorPhoneServer) handleSounds(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	eventsDir := filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "events")
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		http.Error(w, "Cannot read events dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type SoundFile struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}

	var files []SoundFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".mp3" || ext == ".wav" {
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			files = append(files, SoundFile{Name: e.Name(), Size: size})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(files); err != nil {
		log.Printf("error: Failed to encode sound files: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// --- Sound Delete ---
func (b *DoorPhoneServer) handleSoundDelete(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	eventsDir := filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "events")
	target := filepath.Join(eventsDir, name)

	// ensure file is within events dir
	absTarget, _ := filepath.Abs(target)
	absEvents, _ := filepath.Abs(eventsDir)
	if !strings.HasPrefix(absTarget, absEvents+string(os.PathSeparator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	if err := os.Remove(target); err != nil {
		http.Error(w, "Cannot delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"deleted":"%s"}`, name)
}

// --- Sound Play (serve audio file) ---
func (b *DoorPhoneServer) handleSoundPlay(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	name := strings.TrimPrefix(r.URL.Path, "/panel/api/sounds/play/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".mp3" && ext != ".wav" {
		http.Error(w, "Only .mp3 and .wav allowed", http.StatusBadRequest)
		return
	}

	eventsDir := filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "events")
	filePath := filepath.Join(eventsDir, name)

	absFile, _ := filepath.Abs(filePath)
	absEvents, _ := filepath.Abs(eventsDir)
	if !strings.HasPrefix(absFile, absEvents+string(os.PathSeparator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	if ext == ".mp3" {
		w.Header().Set("Content-Type", "audio/mpeg")
	} else {
		w.Header().Set("Content-Type", "audio/wav")
	}
	http.ServeFile(w, r, filePath)
}

// --- Sound Play on Pi (hardware audio output) ---
func (b *DoorPhoneServer) handleSoundPlayPi(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/panel/api/sounds/playpi/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".mp3" && ext != ".wav" {
		http.Error(w, "Only .mp3 and .wav allowed", http.StatusBadRequest)
		return
	}

	eventsDir := filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "events")
	filePath := filepath.Join(eventsDir, name)
	absFile, _ := filepath.Abs(filePath)
	absEvents, _ := filepath.Abs(eventsDir)
	if !strings.HasPrefix(absFile, absEvents+string(os.PathSeparator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	// unmute uscita audio (controllo/scheda risolti dall'XML, non più hardcoded)
	if !IsAudioCardPresent() {
		log.Printf("warn: No audio card detected — skipping unmute for audio test")
	} else if _, err := runAmixer("Headphone", "sset", "unmute"); err != nil {
		log.Printf("error: Failed to unmute speaker: %v", err)
	}

	// kill any existing playback
	if err := exec.Command("pkill", "-f", "ffplay").Run(); err != nil {
		log.Printf("error: Failed to kill ffplay: %v", err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/bin/ffplay", filePath, "-autoexit", "-nodisp", "-volume", "100", "-loglevel", "warning")
	var combinedBuf bytes.Buffer
	cmd.Stdout = &combinedBuf
	cmd.Stderr = &combinedBuf

	startTime := time.Now()
	err := cmd.Run()
	elapsed := time.Since(startTime)

	rawOutput := strings.TrimSpace(combinedBuf.String())
	ok := err == nil
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "timeout (15s)"
		}
	}

	// Build clean summary
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("File: %s\n", name))
	summary.WriteString(fmt.Sprintf("Durata riproduzione: %.1fs\n", elapsed.Seconds()))
	if rawOutput != "" {
		summary.WriteString(fmt.Sprintf("\n%s\n", rawOutput))
	}
	output := summary.String()

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"ok":     ok,
		"output": output,
		"error":  errMsg,
		"cmd":    "/usr/bin/ffplay " + name + " -autoexit -nodisp -volume 100",
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("error: Failed to encode play response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// --- Sound Stop on Pi ---
// handleSoundStopPi interrompe la riproduzione audio sul Raspberry Pi tramite pkill ffplay.
func (b *DoorPhoneServer) handleSoundStopPi(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := exec.Command("pkill", "-f", "ffplay").Run(); err != nil {
		log.Printf("error: Failed to stop ffplay: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

// --- Service Control ---
func (b *DoorPhoneServer) handleService(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method == http.MethodGet {
		out, err := exec.Command("systemctl", "is-active", "doorphoneserver").Output()
		status := strings.TrimSpace(string(out))
		if err != nil && status == "" {
			status = "unknown"
		}

		// uptime
		uptime := time.Since(StartTime).Round(time.Second).String()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"%s","uptime":"%s","version":"%s","build_time":"%s","connected":%t}`,
			status, uptime, doorphoneserverVersion, BuildTime, IsConnected.Load())
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		switch action {
		case "start", "stop", "restart":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true,"action":"%s"}`, action)
			go func() {
				time.Sleep(500 * time.Millisecond)
				if err := exec.Command("systemctl", action, "doorphoneserver").Run(); err != nil {
					log.Printf("error: systemctl %s doorphoneserver: %v", action, err)
				}
			}()
		default:
			http.Error(w, "Invalid action. Use: start, stop, restart", http.StatusBadRequest)
		}
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// --- Streamer Service Control ---
func (b *DoorPhoneServer) handleStreamer(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	const svc = "mjpeg_streamer"

	if r.Method == http.MethodGet {
		out, err := exec.Command("systemctl", "is-active", svc).Output()
		status := strings.TrimSpace(string(out))
		if err != nil && status == "" {
			status = "unknown"
		}

		// get enabled state
		outE, _ := exec.Command("systemctl", "is-enabled", svc).Output()
		enabled := strings.TrimSpace(string(outE))

		// get uptime if active
		uptime := ""
		if status == "active" {
			outT, _ := exec.Command("systemctl", "show", svc, "--property=ActiveEnterTimestamp").Output()
			ts := strings.TrimPrefix(strings.TrimSpace(string(outT)), "ActiveEnterTimestamp=")
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", ts); err == nil {
				uptime = time.Since(t).Round(time.Second).String()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"service":"%s","status":"%s","enabled":"%s","uptime":"%s"}`, svc, status, enabled, uptime)
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		switch action {
		case "start", "stop", "restart":
			w.Header().Set("Content-Type", "application/json")
			out, err := exec.Command("sudo", "-n", "systemctl", action, svc).CombinedOutput()
			outStr, _ := json.Marshal(strings.TrimSpace(string(out)))
			if err != nil {
				fmt.Fprintf(w, `{"ok":false,"action":"%s","output":%s}`, action, outStr)
			} else {
				fmt.Fprintf(w, `{"ok":true,"action":"%s","output":%s}`, action, outStr)
			}
		default:
			http.Error(w, "Invalid action. Use: start, stop, restart", http.StatusBadRequest)
		}
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// --- Mumble Server Control ---
func (b *DoorPhoneServer) handleMumble(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	const svc = "mumble-server"

	if r.Method == http.MethodGet {
		out, err := exec.Command("systemctl", "is-active", svc).Output()
		status := strings.TrimSpace(string(out))
		if err != nil && status == "" {
			status = "unknown"
		}
		outE, _ := exec.Command("systemctl", "is-enabled", svc).Output()
		enabled := strings.TrimSpace(string(outE))
		uptime := ""
		if status == "active" {
			outT, _ := exec.Command("systemctl", "show", svc, "--property=ActiveEnterTimestamp").Output()
			ts := strings.TrimPrefix(strings.TrimSpace(string(outT)), "ActiveEnterTimestamp=")
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", ts); err == nil {
				uptime = time.Since(t).Round(time.Second).String()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"service":"%s","status":"%s","enabled":"%s","uptime":"%s"}`, svc, status, enabled, uptime)
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		switch action {
		case "start", "stop", "restart":
			w.Header().Set("Content-Type", "application/json")
			if action == "stop" {
				MumbleServiceStopped.Store(true)
				ConnectAttempts = 0
			}
			out, err := exec.Command("sudo", "-n", "systemctl", action, svc).CombinedOutput()
			outStr, _ := json.Marshal(strings.TrimSpace(string(out)))
			if err != nil {
				fmt.Fprintf(w, `{"ok":false,"action":"%s","output":%s}`, action, outStr)
			} else {
				if action == "start" || action == "restart" {
					MumbleServiceStopped.Store(false)
					ConnectAttempts = 0
					go func() {
						time.Sleep(3 * time.Second)
						b.Connect()
					}()
				}
				fmt.Fprintf(w, `{"ok":true,"action":"%s","output":%s}`, action, outStr)
			}
		default:
			http.Error(w, "Invalid action", http.StatusBadRequest)
		}
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// --- Tablet Control ---
func (b *DoorPhoneServer) handleTablet(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		gpioNumber := GetGPIO("power_tablet")
		if gpioNumber == -1 {
			http.Error(w, "Tablet GPIO device not configured", http.StatusInternalServerError)
			return
		}

		status, err := GetGPIOState(gpioNumber)
		if err != nil {
			log.Printf("Error reading tablet GPIO state: %v", err)
			http.Error(w, "Unable to read tablet status", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, `{"ok":true,"data":{"power_tablet":%d}}`, status)
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		switch action {
		case "on":
			err := b.cmdPowertabletWithSource("on", "webpanel")
			if err != nil {
				fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(err.Error()))
			} else {
				fmt.Fprintf(w, `{"ok":true,"action":"on","output":"gpio write power_tablet LOW\nRelay attivo -> tablet ACCESO"}`)
			}
		case "off":
			err := b.cmdPowertabletWithSource("off", "webpanel")
			if err != nil {
				fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(err.Error()))
			} else {
				fmt.Fprintf(w, `{"ok":true,"action":"off","output":"gpio write power_tablet HIGH\nRelay disattivo -> tablet SPENTO"}`)
			}
		default:
			http.Error(w, "Invalid action", http.StatusBadRequest)
		}
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handlePushoverUsage restituisce l'ultimo stato noto dei messaggi Pushover in formato JSON.
func (b *DoorPhoneServer) handlePushoverUsage(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	remaining, total, nextReset, lastUpdate, available := GetPushoverUsage()
	if !available {
		FetchPushoverLimits()
		remaining, total, nextReset, lastUpdate, available = GetPushoverUsage()
	}

	used := 0
	percentUsed := 0.0
	if total > 0 {
		used = total - remaining
		percentUsed = float64(used) / float64(total) * 100
	}

	resp := map[string]interface{}{
		"available":    available,
		"remaining":    remaining,
		"total":        total,
		"used":         used,
		"percent_used": percentUsed,
		"next_reset":   nextReset.Format("02/01/2006"),
		"last_update":  lastUpdate.Format("15:04:05"),
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("error: failed to encode pushover usage: %v", err)
	}
}

// handleTabletStatus restituisce lo stato corrente del power tablet in formato JSON.
// Include stato on/off, ultimo cambio, debounce time e cronologia recente.
func (b *DoorPhoneServer) handleTabletStatus(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	status := GetPowerTabletStatus()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("error: failed to encode power tablet status: %v", err)
	}
}

// --- Log Viewer ---
func (b *DoorPhoneServer) handleLog(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	logPath := Config.Global.Software.Settings.LogFilenameAndPath

	lines := 100
	if n := r.URL.Query().Get("lines"); n != "" {
		if v, err := strconv.Atoi(n); err == nil && v > 0 && v <= 1000 {
			lines = v
		}
	}

	if r.Method == http.MethodPost {
		if r.FormValue("action") == "clear" {
			if err := os.Truncate(logPath, 0); err != nil {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(err.Error()))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)
			return
		}
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	out, err := exec.Command("tail", "-n", strconv.Itoa(lines), logPath).Output()
	if err != nil {
		http.Error(w, "Cannot read log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write(out); err != nil {
		log.Printf("error: Failed to write log output: %v", err)
	}
}

// --- Volume Control ---
func (b *DoorPhoneServer) handleVolume(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if !IsAudioCardPresent() {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, `{"headphone":0,"mic":0,"headphoneMute":false,"micMute":false,"noCard":true}`)
		} else {
			fmt.Fprintf(w, `{"ok":false,"output":"Nessuna scheda audio rilevata"}`)
		}
		return
	}

	if r.Method == http.MethodGet {
		hpPct := getAlsaVolume("Headphone")
		micPct := getAlsaVolume("Mic")
		hpMute := getAlsaMute("Headphone")
		micMute := getAlsaMute("Mic")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"headphone":%d,"mic":%d,"headphoneMute":%v,"micMute":%v}`, hpPct, micPct, hpMute, micMute)
		return
	}

	if r.Method == http.MethodPost {
		control := r.FormValue("control")
		volStr := r.FormValue("volume")

		// validate control name
		if control != "Headphone" && control != "Mic" {
			http.Error(w, "Invalid control. Use: Headphone, Mic", http.StatusBadRequest)
			return
		}

		vol, err := strconv.Atoi(volStr)
		if err != nil || vol < 0 || vol > 100 {
			http.Error(w, "Volume must be 0-100", http.StatusBadRequest)
			return
		}

		out, amixErr := runAmixer(control, "sset", fmt.Sprintf("%d%%", vol))
		okVal := amixErr == nil
		outStr := strings.TrimSpace(string(out))
		if outStr == "" {
			if okVal {
				outStr = "Volume impostato con successo"
			} else {
				outStr = amixErr.Error()
			}
		}
		outJSON, _ := json.Marshal(outStr)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":%v,"control":"%s","volume":%d,"output":%s}`, okVal, control, vol, outJSON)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// resolveMixerControl traduce il nome logico usato dal pannello ("Headphone"
// per l'uscita, "Mic" per l'ingresso) nel controllo mixer reale. L'uscita usa il
// controllo configurato nell'XML (outputvolcontroldevice), scritto dal setup in
// base alla scheda scelta dall'operatore (es. "PCM"/"Speaker"). L'ingresso non ha
// un campo dedicato: prova i nomi di cattura più comuni.
func resolveMixerControl(logical string) string {
	switch logical {
	case "Headphone":
		if c := Config.Global.Software.Settings.OutputVolControlDevice; c != "" {
			return c
		}
	case "Mic":
		for _, name := range []string{"Mic", "Capture", "Mic Capture"} {
			if alsaCardForControl(name) >= 0 {
				return name
			}
		}
	}
	return logical
}

// alsaCardForControl ritorna l'indice della prima scheda ALSA che possiede il
// controllo mixer indicato, oppure -1. Rende i comandi amixer indipendenti dal
// numero di scheda (che cambia tra installazioni diverse o dopo il reboot).
func alsaCardForControl(control string) int {
	for c := 0; c < 8; c++ {
		out, err := exec.Command("amixer", "-c", strconv.Itoa(c), "scontrols").Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(out), "'"+control+"'") {
			return c
		}
	}
	return -1
}

// firstMixerControl ritorna (controllo, scheda) del primo controllo mixer con la
// capacità richiesta ("pvolume" = playback, "cvolume" = capture) tra le schede.
// Serve da fallback quando il controllo configurato nell'XML non esiste su questo
// hardware, così il pannello resta funzionante senza regressioni.
func firstMixerControl(capToken string) (string, int) {
	for c := 0; c < 8; c++ {
		out, err := exec.Command("amixer", "-c", strconv.Itoa(c), "scontents").Output()
		if err != nil {
			continue
		}
		var cur string
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Simple mixer control '") {
				s := strings.TrimPrefix(line, "Simple mixer control '")
				if i := strings.Index(s, "'"); i >= 0 {
					cur = s[:i]
				}
			} else if cur != "" && strings.HasPrefix(line, "Capabilities:") && strings.Contains(line, capToken) {
				return cur, c
			}
		}
	}
	return "", -1
}

// runAmixer esegue amixer sul controllo logico, risolvendolo nel controllo reale
// (dall'XML) e agganciando con -c la scheda che effettivamente lo possiede. Se il
// controllo configurato non esiste su nessuna scheda, ripiega sul primo controllo
// reale con la capacità adeguata: il pannello funziona anche con XML non combaciante.
func runAmixer(logical, verb string, rest ...string) ([]byte, error) {
	ctrl := resolveMixerControl(logical)
	card := alsaCardForControl(ctrl)
	if card < 0 {
		capToken := "pvolume"
		if logical == "Mic" {
			capToken = "cvolume"
		}
		if fc, fcard := firstMixerControl(capToken); fcard >= 0 {
			ctrl, card = fc, fcard
		}
	}
	args := []string{}
	if card >= 0 {
		args = append(args, "-c", strconv.Itoa(card))
	}
	args = append(args, verb, ctrl)
	args = append(args, rest...)
	return exec.Command("amixer", args...).CombinedOutput()
}

// getAlsaMute verifica se il controllo audio ALSA specificato è in stato muto.
// @param control nome del controllo ALSA (es. "Headphone", "Mic")
// @return true se il controllo è mutato, false altrimenti
func getAlsaMute(control string) bool {
	out, err := runAmixer(control, "sget")
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "[off]")
}

// handleMuteToggle commuta lo stato mute di un controllo audio ALSA.
func (b *DoorPhoneServer) handleMuteToggle(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	control := r.FormValue("control")
	if control != "Headphone" && control != "Mic" {
		http.Error(w, "Invalid control", http.StatusBadRequest)
		return
	}
	isMuted := getAlsaMute(control)
	action := "mute"
	if isMuted {
		action = "unmute"
	}
	_, err := runAmixer(control, "sset", action)
	okVal := err == nil
	newMute := getAlsaMute(control)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":%v,"control":"%s","muted":%v}`, okVal, control, newMute)
}

// getAlsaVolume legge il livello di volume corrente di un controllo audio ALSA.
// @param control nome del controllo ALSA (es. "Headphone", "Mic")
// @return livello di volume in percentuale (0-100), o 0 in caso di errore
func getAlsaVolume(control string) int {
	out, err := runAmixer(control, "sget")
	if err != nil {
		return 0
	}
	// parse percentage from output like "[72%]"
	s := string(out)
	idx := strings.Index(s, "[")
	for idx >= 0 {
		end := strings.Index(s[idx:], "%]")
		if end > 0 {
			pctStr := s[idx+1 : idx+end]
			if v, err := strconv.Atoi(pctStr); err == nil {
				return v
			}
		}
		next := strings.Index(s[idx+1:], "[")
		if next < 0 {
			break
		}
		idx = idx + 1 + next
	}
	return 0
}

// --- Snapshots ---
// snapshotDir restituisce la directory degli snapshot dalla configurazione, con fallback predefinito.
// @return percorso della directory degli snapshot
func snapshotDir() string {
	dir := Config.Global.Software.Camera.Snapshot.Dir
	if dir == "" {
		dir = "/home/doorphoneserver/snapshots"
	}
	return dir
}

// extractFloor estrae il piano di provenienza dal nome di un file snapshot.
// @param name nome del file snapshot (es. "P1_snapshot_20250101_120000.jpg")
// @return identificatore del piano ("P1"-"P4"), o stringa vuota se non trovato
func extractFloor(name string) string {
	for _, p := range []string{"P1", "P2", "P3", "P4"} {
		if strings.HasPrefix(name, p+"_") {
			return p
		}
	}
	return ""
}

// handleSnapshots restituisce la lista degli snapshot disponibili, opzionalmente filtrati per piano.
func (b *DoorPhoneServer) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	dir := snapshotDir()
	floorFilter := r.URL.Query().Get("floor")
	entries, err := os.ReadDir(dir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("[]")); err != nil {
			log.Printf("error: Failed to write empty array: %v", err)
		}
		return
	}
	type snapInfo struct {
		Name    string `json:"name"`
		Floor   string `json:"floor"`
		Size    int64  `json:"size"`
		ModTime string `json:"mod_time"`
	}
	var result []snapInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !isSnapshotFile(e.Name()) {
			continue
		}
		floor := extractFloor(e.Name())
		if floorFilter != "" && floor != floorFilter {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, snapInfo{
			Name:    e.Name(),
			Floor:   floor,
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ModTime > result[j].ModTime
	})
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("error: failed to encode log list: %v", err)
	}
}

// handleSnapshotView serve un singolo file snapshot come immagine JPEG.
func (b *DoorPhoneServer) handleSnapshotView(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	name := strings.TrimPrefix(r.URL.Path, "/panel/api/snapshots/view/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") || !isSnapshotFile(name) {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	fpath := filepath.Join(snapshotDir(), name)
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, fpath)
}

// handleSnapshotDelete elimina uno snapshot specifico dal filesystem.
func (b *DoorPhoneServer) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.FormValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	fpath := filepath.Join(snapshotDir(), name)
	if err := os.Remove(fpath); err != nil {
		http.Error(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"deleted":"%s"}`, name)
}

// handleSnapshotDeleteAll elimina tutti gli snapshot, opzionalmente filtrati per piano.
func (b *DoorPhoneServer) handleSnapshotDeleteAll(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	floorFilter := r.FormValue("floor")
	dir := snapshotDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "Read dir failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	deleted := 0
	for _, e := range entries {
		if e.IsDir() || !isSnapshotFile(e.Name()) {
			continue
		}
		if floorFilter != "" && extractFloor(e.Name()) != floorFilter {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			deleted++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"deleted":%d}`, deleted)
}

// handleSnapshotTake cattura immediatamente uno snapshot dalla telecamera.
func (b *DoorPhoneServer) handleSnapshotTake(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b.cmdTakeSnapshot()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
		log.Printf("error: write snapshot response: %v", err)
	}
}

// --- Disk Cleanup ---
// handleDiskCleanup esegue operazioni di pulizia del disco: cache Go, journal systemd, cache apt, backup config e /tmp.
func (b *DoorPhoneServer) handleDiskCleanup(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type cleanResult struct {
		Action string `json:"action"`
		Freed  string `json:"freed"`
		Err    string `json:"error,omitempty"`
	}
	var results []cleanResult

	// Get disk usage before
	beforeBytes := getDiskUsedBytes("/")

	// 1. Clean Go build cache (both users)
	for _, user := range []string{"doorphoneserver", "pi"} {
		home := "/home/" + user
		cacheDir := home + "/.cache/go-build"
		if info, err := os.Stat(cacheDir); err == nil && info.IsDir() {
			if err := exec.Command("sudo", "-u", user, "go", "clean", "-cache").Run(); err != nil {
				log.Printf("warn: go clean -cache for %s: %v", user, err)
			}
			results = append(results, cleanResult{Action: "Go build cache (" + user + ")", Freed: "cleaned"})
		}
	}

	// 2. Clean Go module cache (both users) — use sudo rm -rf to bypass read-only module files
	for _, user := range []string{"doorphoneserver", "pi"} {
		modDir := "/home/" + user + "/gocode/pkg/mod"
		if info, err := os.Stat(modDir); err == nil && info.IsDir() {
			sizeMB := dirSizeBytes(modDir) / (1024 * 1024)
			if err := exec.Command("sudo", "chmod", "-R", "u+w", modDir).Run(); err != nil {
				log.Printf("warn: chmod module cache %s: %v", modDir, err)
			}
			if err := exec.Command("sudo", "rm", "-rf", modDir).Run(); err != nil {
				log.Printf("warn: rm module cache %s: %v", modDir, err)
			}
			results = append(results, cleanResult{Action: "Go module cache (" + user + ")", Freed: fmt.Sprintf("~%d MB freed", sizeMB)})
		}
	}

	// 3. Clean gopls and other tool caches (both users)
	for _, user := range []string{"doorphoneserver", "pi"} {
		home := "/home/" + user
		for _, cacheSubdir := range []string{"gopls", "cloud-code", "golangci-lint", "staticcheck"} {
			dir := home + "/.cache/" + cacheSubdir
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				sizeMB := dirSizeBytes(dir) / (1024 * 1024)
				if err := exec.Command("sudo", "rm", "-rf", dir).Run(); err != nil {
					log.Printf("warn: rm cache %s: %v", dir, err)
				}
				results = append(results, cleanResult{Action: "Cache " + cacheSubdir + " (" + user + ")", Freed: fmt.Sprintf("~%d MB freed", sizeMB)})
			}
		}
	}

	// 4. Remove old VS Code Server versions (keep only the latest)
	for _, user := range []string{"doorphoneserver", "pi"} {
		serversDir := "/home/" + user + "/.vscode-server/cli/servers"
		entries, err := os.ReadDir(serversDir)
		if err == nil && len(entries) > 1 {
			type dirEntry struct {
				name    string
				modTime time.Time
			}
			var dirs []dirEntry
			for _, e := range entries {
				if e.IsDir() {
					if info, err2 := e.Info(); err2 == nil {
						dirs = append(dirs, dirEntry{e.Name(), info.ModTime()})
					}
				}
			}
			// Sort descending by modtime (newest first)
			for i := 0; i < len(dirs)-1; i++ {
				for j := i + 1; j < len(dirs); j++ {
					if dirs[j].modTime.After(dirs[i].modTime) {
						dirs[i], dirs[j] = dirs[j], dirs[i]
					}
				}
			}
			removed := 0
			var freedBytes uint64
			for _, d := range dirs[1:] { // skip the newest
				fullPath := serversDir + "/" + d.name
				freedBytes += dirSizeBytes(fullPath)
				if err := exec.Command("sudo", "rm", "-rf", fullPath).Run(); err != nil {
					log.Printf("warn: rm server cache %s: %v", fullPath, err)
				}
				removed++
			}
			if removed > 0 {
				results = append(results, cleanResult{Action: "VS Code Server old versions (" + user + ")", Freed: fmt.Sprintf("%d removed, ~%d MB freed", removed, freedBytes/(1024*1024))})
			}
		}
	}

	// 5. Truncate systemd journal to 50MB
	if out, err := exec.Command("sudo", "journalctl", "--vacuum-size=50M").CombinedOutput(); err == nil {
		results = append(results, cleanResult{Action: "Journal logs vacuum", Freed: strings.TrimSpace(string(out))})
	}

	// 6. Clean apt cache
	if out, err := exec.Command("sudo", "apt-get", "clean").CombinedOutput(); err == nil {
		_ = out
		results = append(results, cleanResult{Action: "APT cache", Freed: "cleaned"})
	}

	// 7. Remove old doorphoneserver.xml backup files older than 5 days
	configDir := filepath.Dir(ConfigXMLFile)
	if removed, err := cleanupOldConfigBackups(configDir, "doorphoneserver.xml.bak.", 5*24*time.Hour); err == nil {
		results = append(results, cleanResult{Action: "Old config backups", Freed: fmt.Sprintf("removed %d files", removed)})
	}

	// 8. Clean /tmp
	if err := exec.Command("sudo", "find", "/tmp", "-type", "f", "-atime", "+2", "-delete").Run(); err != nil {
		log.Printf("warn: clean /tmp: %v", err)
	}
	results = append(results, cleanResult{Action: "Old /tmp files", Freed: "cleaned"})

	// Get disk usage after
	afterBytes := getDiskUsedBytes("/")
	freedMB := float64(beforeBytes-afterBytes) / (1024 * 1024)
	if freedMB < 0 {
		freedMB = 0
	}

	var sb strings.Builder
	for _, res := range results {
		if res.Err != "" {
			sb.WriteString("[ERR] " + res.Action + "  ->  " + res.Err + "\n")
		} else {
			sb.WriteString("[ OK] " + res.Action + "  ->  " + res.Freed + "\n")
		}
	}
	sb.WriteString(fmt.Sprintf("\nSpazio liberato: ~%d MB", int(freedMB)))
	if freedMB == 0 {
		sb.WriteString(" (disco gia pulito)")
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"freed_mb": int(freedMB),
		"output":   sb.String(),
		"details":  results,
	}); err != nil {
		log.Printf("error: encode clean response: %v", err)
	}
}

// dirSizeBytes calcola ricorsivamente la dimensione in byte di una directory.
func dirSizeBytes(path string) uint64 {
	var size uint64
	if err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += uint64(info.Size())
		}
		return nil
	}); err != nil {
		log.Printf("error: Failed to walk path %s: %v", path, err)
	}
	return size
}

// getDiskUsedBytes restituisce il numero di byte utilizzati nel filesystem del percorso specificato.
// @param path percorso del filesystem da misurare
// @return byte utilizzati, o 0 in caso di errore
func getDiskUsedBytes(path string) uint64 {
	out, err := exec.Command("df", "-B1", path).Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return 0
	}
	used, _ := strconv.ParseUint(fields[2], 10, 64)
	return used
}

// --- System Stats ---
// handleMumbleUsers restituisce la lista degli utenti Mumble connessi con le loro informazioni.
// Include nome, sessione, canale, stato mute/deaf, e timestamp di connessione.
func (b *DoorPhoneServer) handleMumbleUsers(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	type UserInfo struct {
		Name            string `json:"name"`
		Session         uint32 `json:"session"`
		Channel         string `json:"channel"`
		Muted           bool   `json:"muted"`
		Deafened        bool   `json:"deafened"`
		SelfMuted       bool   `json:"self_muted"`
		SelfDeafened    bool   `json:"self_deafened"`
		Suppressed      bool   `json:"suppressed"`
		PrioritySpeaker bool   `json:"priority_speaker"`
		Recording       bool   `json:"recording"`
		Comment         string `json:"comment"`
		Hash            string `json:"hash"`
		Registered      bool   `json:"registered"`
		IsSelf          bool   `json:"is_self"`
		ConnectedAt     string `json:"connected_at"`
	}

	if !IsConnected.Load() || b.Client == nil {
		if err := json.NewEncoder(w).Encode([]UserInfo{}); err != nil {
			log.Printf("error: encode empty users: %v", err)
		}
		return
	}

	users := []UserInfo{}
	for _, usr := range b.Client.Users {
		chName := ""
		if usr.Channel != nil {
			chName = usr.Channel.Name
		}
		isSelf := b.Client.Self != nil && usr.Session == b.Client.Self.Session
		connAt := GetUserConnectedAt(usr.Session)
		connAtStr := ""
		if !connAt.IsZero() {
			connAtStr = connAt.Format("15:04:05")
		}
		// Mumble protocol invariant: SelfDeafened implies SelfMuted.
		// Murmur sometimes omits SelfMute in the initial UserState when only SelfDeaf is set.
		selfMuted := usr.SelfMuted || usr.SelfDeafened
		users = append(users, UserInfo{
			Name:            usr.Name,
			Session:         usr.Session,
			Channel:         chName,
			Muted:           usr.Muted,
			Deafened:        usr.Deafened,
			SelfMuted:       selfMuted,
			SelfDeafened:    usr.SelfDeafened,
			Suppressed:      usr.Suppressed,
			PrioritySpeaker: usr.PrioritySpeaker,
			Recording:       usr.Recording,
			Comment:         usr.Comment,
			Hash:            usr.Hash,
			Registered:      usr.UserID != 0,
			IsSelf:          isSelf,
			ConnectedAt:     connAtStr,
		})
	}
	if err := json.NewEncoder(w).Encode(users); err != nil {
		log.Printf("error: encode users: %v", err)
	}
}

// handleStats restituisce le statistiche di sistema del Raspberry Pi in formato JSON.
// Include goroutine attive, memoria, CPU, disco, temperatura, frequenza e tensione del core.
func (b *DoorPhoneServer) handleStats(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := map[string]interface{}{
		"goroutines":  runtime.NumGoroutine(),
		"mem_alloc":   m.Alloc,
		"mem_sys":     m.Sys,
		"mem_gc":      m.NumGC,
		"cpu_percent": getCPUPercent(),
		"disk":        getDiskUsage("/"),
		"temperature": getCPUTemp(),
		"load":        getLoadAvg(),
		"mem_total":   getMemInfo("MemTotal"),
		"mem_free":    getMemInfo("MemFree") + getMemInfo("Buffers") + getMemInfo("Cached"),
		"cpu_freq":    getCPUFreq(),
		"core_volt":   getCoreVoltage(),
		"throttled":   getThrottled(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		log.Printf("error: encode stats: %v", err)
	}
}

// handleConnectionMetrics restituisce le metriche di connessione/disconnessione in formato JSON.
// Include statistiche su connessioni, disconnessioni, uptime e cronologia eventi.
func (b *DoorPhoneServer) handleConnectionMetrics(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	metrics := GetConnectionMetrics()

	// Calculate current connection status and uptime
	var currentUptime time.Duration
	if IsConnected.Load() && !metrics.LastConnectTime.IsZero() {
		currentUptime = time.Since(metrics.LastConnectTime)
	}

	response := map[string]interface{}{
		"is_connected":            IsConnected.Load(),
		"total_connects":          metrics.TotalConnects,
		"total_disconnects":       metrics.TotalDisconnects,
		"last_connect_time":       metrics.LastConnectTime,
		"last_disconnect_time":    metrics.LastDisconnectTime,
		"last_disconnect_reason":  metrics.LastDisconnectReason,
		"current_uptime_seconds":  int64(currentUptime.Seconds()),
		"current_uptime":          currentUptime.String(),
		"last_uptime_seconds":     int64(metrics.ConnectionUptime.Seconds()),
		"last_uptime":             metrics.ConnectionUptime.String(),
		"consecutive_disconnects": metrics.ConsecutiveDisconnects,
		"disconnect_history":      metrics.DisconnectHistory,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("error: encode connection metrics: %v", err)
	}
}

// handleSystemMetrics restituisce le metriche di sistema correnti in formato JSON.
// Include goroutine, memoria, temperatura, stream attivi e stato connessione.
func (b *DoorPhoneServer) handleSystemMetrics(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	metrics := GetCurrentMetrics()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		log.Printf("error: encode metrics: %v", err)
	}
}

// handleSystemMetricsHistory restituisce la cronologia delle metriche di sistema in formato JSON.
// Include fino a 100 campioni storici delle metriche raccolte.
func (b *DoorPhoneServer) handleSystemMetricsHistory(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	history := GetMetricsHistory()

	response := map[string]interface{}{
		"history": history,
		"count":   len(history),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("error: encode metrics history: %v", err)
	}
}

// getCPUPercent calcola la percentuale di utilizzo CPU leggendo /proc/stat.
// @return percentuale di utilizzo CPU (0.0-100.0)
func getCPUPercent() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 {
		return 0
	}
	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		total += v
		if i == 4 {
			idle = v
		}
	}
	if total == 0 {
		return 0
	}
	return float64(total-idle) / float64(total) * 100
}

// getDiskUsage restituisce le informazioni sull'utilizzo del disco per il percorso specificato.
// @param path percorso del filesystem da monitorare
// @return mappa con "total", "used" e "free" in byte, o nil in caso di errore
func getDiskUsage(path string) map[string]uint64 {
	out, err := exec.Command("df", "-B1", path).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return nil
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return nil
	}
	total, _ := strconv.ParseUint(fields[1], 10, 64)
	used, _ := strconv.ParseUint(fields[2], 10, 64)
	free, _ := strconv.ParseUint(fields[3], 10, 64)
	return map[string]uint64{"total": total, "used": used, "free": free}
}

// getCPUTemp legge la temperatura del processore dal file sysfs.
// @return temperatura in gradi Celsius, o 0 in caso di errore
func getCPUTemp() float64 {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	val, _ := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	return val / 1000.0
}

// getLoadAvg legge il carico medio del sistema da /proc/loadavg.
// @return stringa con i valori di carico a 1, 5 e 15 minuti, o "N/A" in caso di errore
func getLoadAvg() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "N/A"
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[0] + " " + fields[1] + " " + fields[2]
	}
	return "N/A"
}

// getCPUFreq legge la frequenza corrente del primo core CPU dal sysfs.
// @return frequenza in MHz, o 0 in caso di errore
func getCPUFreq() int {
	data, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq")
	if err != nil {
		return 0
	}
	v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return v / 1000 // kHz → MHz
}

// getCoreVoltage legge la tensione del core del Raspberry Pi tramite vcgencmd.
// @return stringa con il valore in volt (es. "1.2250V"), o "N/A" in caso di errore
func getCoreVoltage() string {
	out, err := exec.Command("vcgencmd", "measure_volts", "core").Output()
	if err != nil {
		return "N/A"
	}
	s := strings.TrimSpace(string(out))
	s = strings.TrimPrefix(s, "volt=")
	return s
}

// getThrottled legge lo stato di throttling del Raspberry Pi tramite vcgencmd.
// @return valore bitmask dello stato throttling, o 0 in caso di errore
func getThrottled() uint64 {
	out, err := exec.Command("vcgencmd", "get_throttled").Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	s = strings.TrimPrefix(s, "throttled=")
	v, _ := strconv.ParseUint(s, 0, 64)
	return v
}

// getMemInfo legge un valore specifico da /proc/meminfo.
// @param key chiave da cercare in meminfo (es. "MemTotal", "MemFree", "Cached")
// @return valore in byte, o 0 in caso di errore o chiave non trovata
func getMemInfo(key string) uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, key+":") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, _ := strconv.ParseUint(fields[1], 10, 64)
				return v * 1024 // kB → bytes
			}
		}
	}
	return 0
}

// --- Main Panel HTML ---
// handlePanel serve la pagina HTML principale del pannello web amministrativo.
func (b *DoorPhoneServer) handlePanel(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := staticFS.ReadFile("webpanel_static/panel.html")
	if err != nil {
		http.Error(w, "Panel template not found: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(data); err != nil {
		log.Printf("error: write panel response: %v", err)
	}
}

// --- APK Updater Handlers ---

// handleApkList returns a JSON array of .apk filenames found in the APK directory.
// Android app calls: GET /apk/list
func (b *DoorPhoneServer) handleApkList(w http.ResponseWriter, r *http.Request) {
	apkPath := filepath.Join(filepath.Dir(ConfigXMLFile), "apk")
	files, _ := filepath.Glob(filepath.Join(apkPath, "*.apk"))
	names := []string{}
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(names); err != nil {
		log.Printf("error: encode names: %v", err)
	}
}

// handleAppConfig returns a JSON config for the Android app (called at boot).
// GET /config
func (b *DoorPhoneServer) handleAppConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rileggi la configurazione XML dal disco a ogni richiesta, così che le
	// modifiche al file (salvataggio dal pannello o edit manuale) si riflettano
	// subito nel JSON servito ai tablet senza riavviare il binario. Le credenziali
	// (username/password Mumble e camera) hanno tag xml:"-": non stanno nell'XML
	// ma derivano dal .env risolto all'avvio, quindi le riprendiamo dalla Config
	// globale. In caso di errore di lettura/parsing si usa la Config globale.
	src := Config
	if data, err := os.ReadFile(ConfigXMLFile); err != nil {
		log.Printf("warn: handleAppConfig: rilettura %s fallita, uso config in memoria: %v", filepath.Base(ConfigXMLFile), err)
	} else {
		var parsed ConfigStruct
		if err := xml.Unmarshal(data, &parsed); err != nil {
			log.Printf("warn: handleAppConfig: parsing %s fallito, uso config in memoria: %v", filepath.Base(ConfigXMLFile), err)
		} else {
			// Reinserisci le credenziali (non presenti nell'XML) dalla Config globale.
			parsed.Global.Software.Camera.Username = Config.Global.Software.Camera.Username
			parsed.Global.Software.Camera.Password = Config.Global.Software.Camera.Password
			for i := range parsed.Accounts.Account {
				for _, g := range Config.Accounts.Account {
					if g.Name == parsed.Accounts.Account[i].Name {
						parsed.Accounts.Account[i].UserName = g.UserName
						parsed.Accounts.Account[i].Password = g.Password
						break
					}
				}
			}
			src = parsed
		}
	}

	apkPath := filepath.Join(filepath.Dir(ConfigXMLFile), "apk")
	files, _ := filepath.Glob(filepath.Join(apkPath, "*.apk"))
	apkNames := []string{}
	for _, f := range files {
		apkNames = append(apkNames, filepath.Base(f))
	}

	type CameraVideoConfig struct {
		Enabled  bool   `json:"enabled"`
		Endpoint string `json:"endpoint"`
	}
	type CameraSnapshotConfig struct {
		Enabled  bool   `json:"enabled"`
		Endpoint string `json:"endpoint"`
	}
	type CameraConfig struct {
		Video    CameraVideoConfig    `json:"video"`
		Snapshot CameraSnapshotConfig `json:"snapshot"`
		Username string               `json:"username"`
		Password string               `json:"password"`
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	apkDownloadURL := scheme + "://" + r.Host + "/apk/"

	type ApkConfig struct {
		Files       []string `json:"files"`
		DownloadURL string   `json:"download_url"`
	}

	kioskMode := false
	hideStatusBar := false
	micLevel := 100
	speakerLevel := 100
	screenBrightnessLevel := 100
	if src.Global.Software.Tablet.Enabled {
		switch strings.ToLower(r.URL.Query().Get("p")) {
		case "p2":
			kioskMode = src.Global.Software.Tablet.P2.KioskMode
			hideStatusBar = src.Global.Software.Tablet.P2.HideStatusBar
			if v := src.Global.Software.Tablet.P2.MicLevel; v > 0 { micLevel = v }
			if v := src.Global.Software.Tablet.P2.SpeakerLevel; v > 0 { speakerLevel = v }
			if v := src.Global.Software.Tablet.P2.ScreenBrightnessLevel; v > 0 { screenBrightnessLevel = v }
		case "p3":
			kioskMode = src.Global.Software.Tablet.P3.KioskMode
			hideStatusBar = src.Global.Software.Tablet.P3.HideStatusBar
			if v := src.Global.Software.Tablet.P3.MicLevel; v > 0 { micLevel = v }
			if v := src.Global.Software.Tablet.P3.SpeakerLevel; v > 0 { speakerLevel = v }
			if v := src.Global.Software.Tablet.P3.ScreenBrightnessLevel; v > 0 { screenBrightnessLevel = v }
		case "p4":
			kioskMode = src.Global.Software.Tablet.P4.KioskMode
			hideStatusBar = src.Global.Software.Tablet.P4.HideStatusBar
			if v := src.Global.Software.Tablet.P4.MicLevel; v > 0 { micLevel = v }
			if v := src.Global.Software.Tablet.P4.SpeakerLevel; v > 0 { speakerLevel = v }
			if v := src.Global.Software.Tablet.P4.ScreenBrightnessLevel; v > 0 { screenBrightnessLevel = v }
		default: // p1 o parametro assente
			kioskMode = src.Global.Software.Tablet.P1.KioskMode
			hideStatusBar = src.Global.Software.Tablet.P1.HideStatusBar
			if v := src.Global.Software.Tablet.P1.MicLevel; v > 0 { micLevel = v }
			if v := src.Global.Software.Tablet.P1.SpeakerLevel; v > 0 { speakerLevel = v }
			if v := src.Global.Software.Tablet.P1.ScreenBrightnessLevel; v > 0 { screenBrightnessLevel = v }
		}
	}

	type MumbleConfig struct {
		Server   string `json:"server"`
		Port     string `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	var mumbleCfg MumbleConfig
	for _, acc := range src.Accounts.Account {
		if acc.Default {
			host, port, err := net.SplitHostPort(acc.ServerAndPort)
			if err != nil {
				host = acc.ServerAndPort
				port = ""
			}
			mumbleCfg = MumbleConfig{
				Server:   host,
				Port:     port,
				Username: acc.UserName,
				Password: acc.Password,
			}
			break
		}
	}

	type DoorpiConfig struct {
		Host          string `json:"host"`
		ApiPort       string `json:"api_port"`
		UnlockCommand string `json:"unlock_command"`
	}

	reqHost, reqPort, err := net.SplitHostPort(r.Host)
	if err != nil {
		reqHost = r.Host
		reqPort = "8080"
	}

	// Il server Mumble è configurato come loopback (127.0.0.1) perché
	// DoorPhoneServer gira sulla stessa macchina di Murmur. I TABLET però non
	// possono usare 127.0.0.1 (sarebbero loro stessi): devono raggiungere il Pi
	// all'IP con cui hanno contattato il pannello (reqHost). Sostituiamo solo gli
	// indirizzi di loopback, lasciando intatto un eventuale Mumble remoto.
	if mumbleCfg.Server == "127.0.0.1" || mumbleCfg.Server == "localhost" || mumbleCfg.Server == "::1" {
		mumbleCfg.Server = reqHost
	}

	type AppConfig struct {
		Kiosk         bool         `json:"kiosk"`
		HideStatusBar bool         `json:"hide_status_bar"`
		MicLevel              int          `json:"miclevel"`
		SpeakerLevel          int          `json:"speakerlevel"`
		ScreenBrightnessLevel int          `json:"screenbrightnesslevel"`
		Timezone      string       `json:"timezone"`
		ServerTime    int64        `json:"server_time"`
		Mumble        MumbleConfig `json:"mumbleserver"`
		Doorpi        DoorpiConfig `json:"doorpi"`
		Apk           ApkConfig    `json:"apk"`
		Camera        CameraConfig `json:"camera"`
	}

	now := time.Now()
	cfg := AppConfig{
		Kiosk:         kioskMode,
		HideStatusBar: hideStatusBar,
		MicLevel:              micLevel,
		SpeakerLevel:          speakerLevel,
		ScreenBrightnessLevel: screenBrightnessLevel,
		Timezone:      func() string {
			tz := now.Location().String()
			if tz == "Local" {
				if b, err := os.ReadFile("/etc/timezone"); err == nil {
					tz = strings.TrimSpace(string(b))
				}
			}
			return tz
		}(),
		ServerTime:    now.Unix(),
		Mumble:        mumbleCfg,
		Doorpi: DoorpiConfig{
			Host:          reqHost,
			ApiPort:       reqPort,
			UnlockCommand: "unlockdoor",
		},
		Apk: ApkConfig{
			Files:       apkNames,
			DownloadURL: apkDownloadURL,
		},
		Camera: CameraConfig{
			Video: CameraVideoConfig{
				Enabled:  src.Global.Software.Camera.Video.Enabled,
				Endpoint: src.Global.Software.Camera.Video.Endpoint,
			},
			Snapshot: CameraSnapshotConfig{
				Enabled:  src.Global.Software.Camera.Snapshot.Enabled,
				Endpoint: src.Global.Software.Camera.Snapshot.Endpoint,
			},
			Username: src.Global.Software.Camera.Username,
			Password: src.Global.Software.Camera.Password,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		log.Printf("error: encode cfg: %v", err)
	}
}

// handlePanelApkList returns a JSON array of APK objects {name, size} for the panel UI.
func (b *DoorPhoneServer) handlePanelApkList(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	type ApkFile struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	apkPath := filepath.Join(filepath.Dir(ConfigXMLFile), "apk")
	entries, err := os.ReadDir(apkPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode([]ApkFile{}); err != nil {
			log.Printf("error: encode empty apk list: %v", err)
		}
		return
	}
	files := []ApkFile{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(e.Name())) != ".apk" {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		files = append(files, ApkFile{Name: e.Name(), Size: size})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(files); err != nil {
		log.Printf("error: encode apk files: %v", err)
	}
}

// handleApkUpload handles APK file upload from the panel.
func (b *DoorPhoneServer) handleApkUpload(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(200 << 20); err != nil { // 200MB max
		http.Error(w, "File too large (max 200MB)", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file in request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(handler.Filename))
	if ext != ".apk" {
		http.Error(w, "Only .apk files allowed", http.StatusBadRequest)
		return
	}

	baseName := filepath.Base(handler.Filename)
	for _, c := range baseName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			http.Error(w, "Invalid filename characters", http.StatusBadRequest)
			return
		}
	}

	apkPath := filepath.Join(filepath.Dir(ConfigXMLFile), "apk")
	if err := os.MkdirAll(apkPath, 0755); err != nil {
		http.Error(w, "Cannot create apk directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	destPath := filepath.Join(apkPath, baseName)
	dst, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "Cannot create file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Write error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"file":"%s","size":%d}`, baseName, written)
}

// handleApkDelete deletes an APK file from the apk directory.
func (b *DoorPhoneServer) handleApkDelete(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.FormValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	apkPath := filepath.Join(filepath.Dir(ConfigXMLFile), "apk")
	target := filepath.Join(apkPath, name)

	absTarget, _ := filepath.Abs(target)
	absApk, _ := filepath.Abs(apkPath)
	if !strings.HasPrefix(absTarget, absApk+string(os.PathSeparator)) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	if err := os.Remove(target); err != nil {
		http.Error(w, "Cannot delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"deleted":"%s"}`, name)
}

// --- RTSP helpers ---
// rtspURLWithCreds inserisce le credenziali nell'URL RTSP se username è specificato.
// @param endpoint URL RTSP originale senza credenziali
// @param username nome utente per l'autenticazione
// @param password password per l'autenticazione
// @return URL RTSP con credenziali incorporate (rtsp://user:pass@host/path)
func rtspURLWithCreds(endpoint, username, password string) string {
	if username == "" {
		return endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	u.User = url.UserPassword(username, password)
	return u.String()
}

// --- RTSP Stream via FFmpeg proxy (image2pipe + manual multipart) ---
func (b *DoorPhoneServer) handleRTSPStream(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	vs := Config.Global.Software.Camera
	if !vs.Video.Enabled || vs.Video.Endpoint == "" {
		http.Error(w, "RTSP stream not configured or disabled", http.StatusServiceUnavailable)
		return
	}

	rtspURL := rtspURLWithCreds(vs.Video.Endpoint, vs.Username, vs.Password)

	const boundary = "tkframe"
	w.Header().Set("Content-Type", "multipart/x-mixed-replace;boundary="+boundary)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fl, canFlush := w.(http.Flusher)
	ctx := r.Context()

	soi := []byte{0xFF, 0xD8}
	eoi := []byte{0xFF, 0xD9}

	pr, pw := io.Pipe()
	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-vf", "fps=10",
		"-vcodec", "mjpeg",
		"-f", "image2pipe",
		"-q:v", "5",
		"pipe:1",
	)
	cmd.Stdout = pw
	cmd.Stderr = &stderrBuf
	go func() {
		if err := cmd.Run(); err != nil && ctx.Err() == nil {
			log.Printf("[rtsp-stream] ffmpeg: %v\n%s", err, stderrBuf.String())
		}
		pw.Close()
	}()

	var frameBuf []byte
	chunk := make([]byte, 32*1024)
	for {
		n, err := pr.Read(chunk)
		if n > 0 {
			frameBuf = append(frameBuf, chunk[:n]...)
			for {
				start := bytes.Index(frameBuf, soi)
				if start < 0 {
					frameBuf = nil
					break
				}
				end := bytes.Index(frameBuf[start+2:], eoi)
				if end < 0 {
					break
				}
				end = start + 2 + end + 2
				frame := frameBuf[start:end]
				hdr := fmt.Sprintf("--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
				if _, err := w.Write([]byte(hdr)); err != nil {
					return
				}
				if _, err := w.Write(frame); err != nil {
					return
				}
				if _, err := w.Write([]byte("\r\n")); err != nil {
					return
				}
				if canFlush {
					fl.Flush()
				}
				frameBuf = frameBuf[end:]
			}
		}
		if err != nil {
			break
		}
	}
}

// --- RTSP Probe (connectivity test) ---
func (b *DoorPhoneServer) handleRTSPProbe(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	vs := Config.Global.Software.Camera
	if !vs.Video.Enabled || vs.Video.Endpoint == "" {
		fmt.Fprintf(w, `{"ok":false,"error":"RTSP stream not configured or disabled"}`)
		return
	}

	rtspURL := rtspURLWithCreds(vs.Video.Endpoint, vs.Username, vs.Password)

	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(r.Context(), "ffmpeg",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-t", "3",
		"-f", "null",
		"-",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	output := stderrBuf.String()

	if err != nil {
		lines := strings.Split(output, "\n")
		var relevant []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" && (strings.Contains(l, "Error") || strings.Contains(l, "error") ||
				strings.Contains(l, "refused") || strings.Contains(l, "timeout") ||
				strings.Contains(l, "Stream") || strings.Contains(l, "Video") ||
				strings.Contains(l, "Could not") || strings.Contains(l, "Invalid")) {
				relevant = append(relevant, l)
			}
		}
		summary := strings.Join(relevant, " | ")
		if summary == "" {
			summary = strings.TrimSpace(output)
			if len(summary) > 500 {
				summary = summary[len(summary)-500:]
			}
		}
		outJ, _ := json.Marshal(summary)
		fmt.Fprintf(w, `{"ok":false,"error":%s}`, outJ)
		return
	}

	endpointJ, _ := json.Marshal(vs.Video.Endpoint)
	fmt.Fprintf(w, `{"ok":true,"endpoint":%s}`, endpointJ)
}

// --- Camera Spotlight Control (Reolink API) ---
func (b *DoorPhoneServer) handleSpotlight(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vs := Config.Global.Software.Camera
	if vs.Video.Endpoint == "" {
		fmt.Fprintf(w, `{"ok":false,"error":"Camera endpoint not configured"}`)
		return
	}

	// Parse camera IP from RTSP endpoint
	u, err := url.Parse(vs.Video.Endpoint)
	if err != nil {
		fmt.Fprintf(w, `{"ok":false,"error":"invalid endpoint URL"}`)
		return
	}

	action := r.FormValue("action") // "on" or "off"
	state := 0
	if action == "on" {
		state = 1
	}

	cameraIP := u.Hostname()
	apiURL := fmt.Sprintf("http://%s/cgi-bin/api.cgi?user=%s&password=%s",
		cameraIP, vs.Username, vs.Password)

	type whiteledParam struct {
		Channel int `json:"channel"`
		State   int `json:"state"`
		Bright  int `json:"bright"`
		Mode    int `json:"mode"`
	}
	type setWhiteLed struct {
		Cmd   string `json:"cmd"`
		Param struct {
			WhiteLed whiteledParam `json:"WhiteLed"`
		} `json:"param"`
	}

	payload := []setWhiteLed{{
		Cmd: "SetWhiteLed",
		Param: struct {
			WhiteLed whiteledParam `json:"WhiteLed"`
		}{
			WhiteLed: whiteledParam{Channel: 0, State: state, Bright: 100, Mode: 0},
		},
	}}

	body, _ := json.Marshal(payload)
	//log.Printf("info: Spotlight action=%s state=%d cameraIP=%s\n", action, state, cameraIP)

	// --- Method 1: Reolink HTTP SetWhiteLed ---
	//log.Printf("info: Trying Reolink HTTP SetWhiteLed -> %s\n", apiURL)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(apiURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		//log.Printf("warn: Reolink SetWhiteLed HTTP error: %v\n", err)
	} else {
		defer resp.Body.Close()
		var result []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result) == 0 {
			//log.Printf("warn: Reolink SetWhiteLed invalid response (decode err=%v, results=%d)\n", err, len(result))
		} else {
			code, _ := result[0]["code"].(float64)
			//log.Printf("info: Reolink SetWhiteLed response code=%.0f\n", code)
			if code == 0 {
				//log.Printf("info: SetWhiteLed succeeded\n")
				fmt.Fprintf(w, `{"ok":true,"cmd":"SetWhiteLed","action":"%s"}`, action)
				return
			}
		}
	}

	// --- Method 2: Reolink HTTP SetLighting ---
		//log.Printf("info: Trying Reolink HTTP SetLighting fallback\n")
		type lightingParam struct {
			Channel    int `json:"channel"`
			Type       int `json:"type"`
			State      int `json:"state"`
			Brightness int `json:"brightness"`
		}
		type setLighting struct {
			Cmd   string `json:"cmd"`
			Param struct {
				Lighting lightingParam `json:"Lighting"`
			} `json:"param"`
		}
		payload2 := []setLighting{{
			Cmd: "SetLighting",
			Param: struct {
				Lighting lightingParam `json:"Lighting"`
			}{
				Lighting: lightingParam{Channel: 0, Type: 1, State: state, Brightness: 100},
			},
		}}
		body2, _ := json.Marshal(payload2)
		resp2, err2 := (&http.Client{Timeout: 5 * time.Second}).Post(apiURL, "application/json", strings.NewReader(string(body2)))
		if err2 != nil {
			//log.Printf("warn: Reolink SetLighting HTTP error: %v\n", err2)
		} else {
			defer resp2.Body.Close()
			var result2 []map[string]interface{}
			if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
				log.Printf("error: Failed to decode Reolink response: %v", err)
			} else if len(result2) > 0 {
				code2, _ := result2[0]["code"].(float64)
				//log.Printf("info: Reolink SetLighting response code=%.0f\n", code2)
				if code2 == 0 {
					//log.Printf("info: SetLighting succeeded\n")
					fmt.Fprintf(w, `{"ok":true,"cmd":"SetLighting","action":"%s"}`, action)
					return
				}
			}
		}

	// --- Method 3: ONVIF fallback ---
	onvifPort := vs.OnvifPort
	if onvifPort == 0 {
		onvifPort = 8000
	}
	//log.Printf("info: Reolink HTTP methods failed, trying ONVIF on %s:%d user=%s\n", cameraIP, onvifPort, vs.Username)
	onvifOK, _ := onvifSetSpotlight(cameraIP, onvifPort, vs.Username, vs.Password, state)
	if onvifOK {
		//log.Printf("info: ONVIF spotlight succeeded action=%s\n", action)
		fmt.Fprintf(w, `{"ok":true,"cmd":"ONVIF","action":"%s"}`, action)
		return
	}
	//log.Printf("warn: ONVIF failed: %v, trying Baichuan protocol\n", onvifErr)

	// --- Method 4: Baichuan protocol (port 9000) ---
	duration := 60
	if state == 0 {
		duration = 0
	}
	bcErr := BaichuanSetFloodlight(cameraIP, vs.Username, vs.Password, state, duration)
	if bcErr == nil {
		log.Printf("info: Baichuan floodlight succeeded action=%s\n", action)
		fmt.Fprintf(w, `{"ok":true,"cmd":"Baichuan","action":"%s"}`, action)
		return
	}
	log.Printf("error: Baichuan floodlight failed: %v\n", bcErr)

	errMsg := "camera rejected SetWhiteLed, SetLighting, ONVIF and Baichuan"
	errMsg += ": " + bcErr.Error()
	//log.Printf("error: Spotlight all methods failed: %s\n", errMsg)
	outJ3, _ := json.Marshal(errMsg)
	fmt.Fprintf(w, `{"ok":false,"error":%s}`, outJ3)
}

// spotlightSet accende (state=1) o spegne (state=0) il faretto della camera
// usando i metodi disponibili: Reolink HTTP, ONVIF, Baichuan.
func (b *DoorPhoneServer) spotlightSet(state int) {
	vs := Config.Global.Software.Camera
	if vs.Video.Endpoint == "" {
		return
	}
	u, err := url.Parse(vs.Video.Endpoint)
	if err != nil {
		return
	}
	cameraIP := u.Hostname()
	apiURL := fmt.Sprintf("http://%s/cgi-bin/api.cgi?user=%s&password=%s",
		cameraIP, vs.Username, vs.Password)

	type whiteledParam struct {
		Channel int `json:"channel"`
		State   int `json:"state"`
		Bright  int `json:"bright"`
		Mode    int `json:"mode"`
	}
	type setWhiteLed struct {
		Cmd   string `json:"cmd"`
		Param struct {
			WhiteLed whiteledParam `json:"WhiteLed"`
		} `json:"param"`
	}
	payload := []setWhiteLed{{
		Cmd: "SetWhiteLed",
		Param: struct {
			WhiteLed whiteledParam `json:"WhiteLed"`
		}{WhiteLed: whiteledParam{Channel: 0, State: state, Bright: 100, Mode: 0}},
	}}
	body, _ := json.Marshal(payload)

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(apiURL, "application/json", strings.NewReader(string(body)))
	if err == nil {
		defer resp.Body.Close()
		var result []map[string]interface{}
		if err2 := json.NewDecoder(resp.Body).Decode(&result); err2 == nil && len(result) > 0 {
			if code, _ := result[0]["code"].(float64); code == 0 {
				if BaichuanDebug {
					log.Printf("info: SpotlightTrigger SetWhiteLed state=%d OK\n", state)
				}
				return
			}
		}
	}

	type lightingParam struct {
		Channel    int `json:"channel"`
		Type       int `json:"type"`
		State      int `json:"state"`
		Brightness int `json:"brightness"`
	}
	type setLighting struct {
		Cmd   string `json:"cmd"`
		Param struct {
			Lighting lightingParam `json:"Lighting"`
		} `json:"param"`
	}
	payload2 := []setLighting{{
		Cmd: "SetLighting",
		Param: struct {
			Lighting lightingParam `json:"Lighting"`
		}{Lighting: lightingParam{Channel: 0, Type: 1, State: state, Brightness: 100}},
	}}
	body2, _ := json.Marshal(payload2)
	resp2, err2 := (&http.Client{Timeout: 5 * time.Second}).Post(apiURL, "application/json", strings.NewReader(string(body2)))
	if err2 == nil {
		defer resp2.Body.Close()
		var result2 []map[string]interface{}
		if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
			log.Printf("error: Failed to decode Reolink response: %v", err)
		} else if len(result2) > 0 {
			if code2, _ := result2[0]["code"].(float64); code2 == 0 {
				if BaichuanDebug {
					log.Printf("info: SpotlightTrigger SetLighting state=%d OK\n", state)
				}
				return
			}
		}
	}

	onvifPort := vs.OnvifPort
	if onvifPort == 0 {
		onvifPort = 8000
	}
	if ok, _ := onvifSetSpotlight(cameraIP, onvifPort, vs.Username, vs.Password, state); ok {
		if BaichuanDebug {
			log.Printf("info: SpotlightTrigger ONVIF state=%d OK\n", state)
		}
		return
	}

	duration := 60
	if state == 0 {
		duration = 0
	}
	if err := BaichuanSetFloodlight(cameraIP, vs.Username, vs.Password, state, duration); err == nil {
		if BaichuanDebug {
			log.Printf("info: SpotlightTrigger Baichuan state=%d OK\n", state)
		}
		return
	}
	log.Printf("warn: SpotlightTrigger tutti i metodi falliti state=%d\n", state)
}

// SpotlightTrigger accende il faretto e lo spegne automaticamente dopo durationSec secondi.
// Viene chiamata dai pulsanti del citofono (P1/P2/P3).
func (b *DoorPhoneServer) SpotlightTrigger(durationSec int) {
	b.spotlightSet(1)
	go func() {
		time.Sleep(time.Duration(durationSec) * time.Second)
		b.spotlightSet(0)
	}()
}

// handleCameraTime syncs the camera clock to the current system time via Baichuan protocol
func (b *DoorPhoneServer) handleCameraTime(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vs := Config.Global.Software.Camera
	if vs.Video.Endpoint == "" {
		fmt.Fprintf(w, `{"ok":false,"error":"Camera endpoint not configured"}`)
		return
	}

	u, err := url.Parse(vs.Video.Endpoint)
	if err != nil {
		fmt.Fprintf(w, `{"ok":false,"error":"invalid endpoint URL"}`)
		return
	}

	cameraIP := u.Hostname()
	err = BaichuanSetTime(cameraIP, vs.Username, vs.Password)
	if err != nil {
		log.Printf("error: BaichuanSetTime failed: %v\n", err)
		errJSON, _ := json.Marshal(err.Error())
		fmt.Fprintf(w, `{"ok":false,"error":%s}`, errJSON)
		return
	}

	now := time.Now()
	fmt.Fprintf(w, `{"ok":true,"time":"%04d-%02d-%02d %02d:%02d:%02d"}`,
		now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())
}

// handleCameraOSD enables/disables camera OSD title and sets its text via Baichuan protocol.
func (b *DoorPhoneServer) handleCameraOSD(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		fmt.Fprintf(w, `{"ok":false,"error":"invalid form"}`)
		return
	}

	vs := Config.Global.Software.Camera
	if vs.Video.Endpoint == "" {
		fmt.Fprintf(w, `{"ok":false,"error":"Camera endpoint not configured"}`)
		return
	}

	u, err := url.Parse(vs.Video.Endpoint)
	if err != nil {
		fmt.Fprintf(w, `{"ok":false,"error":"invalid endpoint URL"}`)
		return
	}

	enabled := true
	sw := strings.ToLower(strings.TrimSpace(r.FormValue("enabled")))
	if sw == "0" || sw == "false" || sw == "off" {
		enabled = false
	}

	text := strings.TrimSpace(r.FormValue("text"))
	pos := strings.TrimSpace(r.FormValue("pos"))
	err = BaichuanSetOSDText(u.Hostname(), vs.Username, vs.Password, enabled, text, pos)
	if err != nil {
		log.Printf("error: BaichuanSetOSDText failed: %v\n", err)
		errJSON, _ := json.Marshal(err.Error())
		fmt.Fprintf(w, `{"ok":false,"error":%s}`, errJSON)
		return
	}

	readback, rbErr := BaichuanGetOSDText(u.Hostname(), vs.Username, vs.Password)
	if rbErr != nil {
		textJSON, _ := json.Marshal(text)
		warnJSON, _ := json.Marshal(rbErr.Error())
		fmt.Fprintf(w, `{"ok":true,"enabled":%t,"text":%s,"readback_ok":false,"warning":%s}`,
			enabled, textJSON, warnJSON)
		return
	}

	textJSON, _ := json.Marshal(text)
	rbTextJSON, _ := json.Marshal(readback.Text)
	fmt.Fprintf(w, `{"ok":true,"enabled":%t,"text":%s,"readback_ok":true,"readback_enabled":%t,"readback_text":%s}`,
		enabled, textJSON, readback.Enabled, rbTextJSON)
}

// onvifSetSpotlight controls a camera spotlight via ONVIF auxiliary commands.
// It tries multiple ONVIF methods: PTZ AuxiliaryCommand, then Imaging supplemental light.
func onvifSetSpotlight(cameraIP string, port int, username, password string, state int) (bool, error) {
	//log.Printf("info: ONVIF onvifSetSpotlight called: cameraIP=%s port=%d state=%d\n", cameraIP, port, state)
	serviceURL := fmt.Sprintf("http://%s:%d/onvif/ptz_service", cameraIP, port)

	// Try PTZ AuxiliaryCommand (most common for spotlight)
	auxData := "tt:WhiteLight|On"
	if state == 0 {
		auxData = "tt:WhiteLight|Off"
	}

	// First get a media profile token
	//log.Printf("info: ONVIF getting media profile token...\n")
	profileToken, _ := onvifGetProfileToken(cameraIP, port, username, password)
	if profileToken == "" {
		//log.Printf("warn: ONVIF GetProfiles failed (%v), using default Profile_1\n", ptErr)
		profileToken = "Profile_1" // common default
	}
	//log.Printf("info: ONVIF got profile token: %s\n", profileToken)

	// Method 1: PTZ SetAuxiliaryCommand
	ptzBody := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Header>%s</s:Header>
  <s:Body>
    <tptz:SendAuxiliaryCommand>
      <tptz:ProfileToken>%s</tptz:ProfileToken>
      <tptz:AuxiliaryData>%s</tptz:AuxiliaryData>
    </tptz:SendAuxiliaryCommand>
  </s:Body>
</s:Envelope>`, onvifSecurityHeader(username, password), profileToken, auxData)

	//log.Printf("info: ONVIF Method 1: PTZ SendAuxiliaryCommand -> %s auxData=%s\n", serviceURL, auxData)
	ok, err := onvifSoapRequest(serviceURL, "http://www.onvif.org/ver20/ptz/wsdl/SendAuxiliaryCommand", ptzBody)
	if ok {
		//log.Printf("info: ONVIF PTZ SendAuxiliaryCommand succeeded\n")
		return true, nil
	}
	//log.Printf("warn: ONVIF Method 1 PTZ SendAuxiliaryCommand failed: %v\n", err)

	// Method 2: Imaging Service SetImagingSettings (supplemental light / IR)
	imgServiceURL := fmt.Sprintf("http://%s:%d/onvif/imaging_service", cameraIP, port)
	modeVal := "OFF"
	if state == 1 {
		modeVal = "ON"
	}
	imgBody := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:timg="http://www.onvif.org/ver20/imaging/wsdl"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Header>%s</s:Header>
  <s:Body>
    <timg:SetImagingSettings>
      <timg:VideoSourceToken>VideoSource_1</timg:VideoSourceToken>
      <timg:ImagingSettings>
        <tt:IrCutFilter>%s</tt:IrCutFilter>
      </timg:ImagingSettings>
    </timg:SetImagingSettings>
  </s:Body>
</s:Envelope>`, onvifSecurityHeader(username, password), modeVal)

	//log.Printf("info: ONVIF Method 2: Imaging IrCutFilter -> %s mode=%s\n", imgServiceURL, modeVal)
	ok2, err2 := onvifSoapRequest(imgServiceURL, "http://www.onvif.org/ver20/imaging/wsdl/SetImagingSettings", imgBody)
	if ok2 {
		//log.Printf("info: ONVIF Imaging IrCutFilter succeeded\n")
		return true, nil
	}
	//log.Printf("warn: ONVIF Method 2 Imaging IrCutFilter failed: %v\n", err2)

	// Method 3: try supplemental light via imaging extension
	imgBody2 := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:timg="http://www.onvif.org/ver20/imaging/wsdl"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Header>%s</s:Header>
  <s:Body>
    <timg:SetImagingSettings>
      <timg:VideoSourceToken>VideoSource_1</timg:VideoSourceToken>
      <timg:ImagingSettings>
        <tt:Extension>
          <tt:Extension>
            <tt:SupplementaryLight>
              <tt:Mode>%s</tt:Mode>
            </tt:SupplementaryLight>
          </tt:Extension>
        </tt:Extension>
      </timg:ImagingSettings>
    </timg:SetImagingSettings>
  </s:Body>
</s:Envelope>`, onvifSecurityHeader(username, password), modeVal)

	//log.Printf("info: ONVIF Method 3: Imaging SupplementaryLight -> %s mode=%s\n", imgServiceURL, modeVal)
	ok3, err3 := onvifSoapRequest(imgServiceURL, "http://www.onvif.org/ver20/imaging/wsdl/SetImagingSettings", imgBody2)
	if ok3 {
		//log.Printf("info: ONVIF Imaging SupplementaryLight succeeded\n")
		return true, nil
	}
	//log.Printf("warn: ONVIF Method 3 Imaging SupplementaryLight failed: %v\n", err3)

	if err3 != nil {
		return false, err3
	}
	if err2 != nil {
		return false, err2
	}
	return false, err
}

// onvifGetProfileToken retrieves the first media profile token via ONVIF.
func onvifGetProfileToken(cameraIP string, port int, username, password string) (string, error) {
	serviceURL := fmt.Sprintf("http://%s:%d/onvif/media_service", cameraIP, port)
	//log.Printf("info: ONVIF GetProfiles -> %s\n", serviceURL)
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
  <s:Header>%s</s:Header>
  <s:Body>
    <trt:GetProfiles/>
  </s:Body>
</s:Envelope>`, onvifSecurityHeader(username, password))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", serviceURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Extract first profile token from response
	type profilesEnvelope struct {
		Body struct {
			GetProfilesResponse struct {
				Profiles []struct {
					Token string `xml:"token,attr"`
				} `xml:"Profiles"`
			} `xml:"GetProfilesResponse"`
		} `xml:"Body"`
	}
	var env profilesEnvelope
	if err := xml.Unmarshal(respBody, &env); err != nil {
		return "", err
	}
	if len(env.Body.GetProfilesResponse.Profiles) > 0 {
		return env.Body.GetProfilesResponse.Profiles[0].Token, nil
	}
	return "", fmt.Errorf("no profiles found")
}

// onvifSoapRequest sends a SOAP request and returns true if the response indicates success.
func onvifSoapRequest(serviceURL, soapAction, body string) (bool, error) {
	//log.Printf("info: ONVIF SOAP request -> %s action=%s\n", serviceURL, soapAction)
	//log.Printf("debug: ONVIF SOAP body:\n%s\n", body)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", serviceURL, strings.NewReader(body))
	if err != nil {
		//log.Printf("error: ONVIF failed to create request: %v\n", err)
		return false, err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	resp, err := client.Do(req)
	if err != nil {
		//log.Printf("error: ONVIF HTTP request failed: %v\n", err)
		return false, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	//log.Printf("info: ONVIF SOAP response HTTP %d, body length=%d\n", resp.StatusCode, len(respBody))
	//log.Printf("debug: ONVIF SOAP response body:\n%s\n", string(respBody))

	// Check for SOAP Fault in response body (can come with 200 or 400)
	respStr := string(respBody)
	if strings.Contains(respStr, ":Fault>") || strings.Contains(respStr, "<Fault>") {
		// Extract fault reason for cleaner error message
		faultReason := "unknown fault"
		if idx := strings.Index(respStr, "<SOAP-ENV:Text"); idx >= 0 {
			if end := strings.Index(respStr[idx:], "</SOAP-ENV:Text>"); end >= 0 {
				inner := respStr[idx : idx+end]
				if gt := strings.Index(inner, ">"); gt >= 0 {
					faultReason = inner[gt+1:]
				}
			}
		}
		//log.Printf("warn: ONVIF SOAP fault: %s\n", faultReason)
		return false, fmt.Errorf("ONVIF SOAP fault: %s", faultReason)
	}

	if resp.StatusCode != 200 {
		return false, fmt.Errorf("ONVIF HTTP %d: %s", resp.StatusCode, respStr)
	}

	//log.Printf("info: ONVIF SOAP request succeeded\n")
	return true, nil
}

// onvifSecurityHeader generates a WS-Security UsernameToken header for ONVIF authentication.
func onvifSecurityHeader(username, password string) string {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		log.Printf("error: Failed to read random nonce: %v", err)
		// Continue with zero nonce, though insecure
	}
	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Password Digest = Base64(SHA1(nonce + created + password))
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	nonceB64 := base64.StdEncoding.EncodeToString(nonce)

	//log.Printf("debug: ONVIF auth: user=%s created=%s nonceB64=%s digestB64=%s\n", username, created, nonceB64, digest)

	return fmt.Sprintf(`<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd" s:mustUnderstand="true">
      <wsse:UsernameToken>
        <wsse:Username>%s</wsse:Username>
        <wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password>
        <wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</wsse:Nonce>
        <wsu:Created>%s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>`, username, digest, nonceB64, created)
}

// --- Audio Test ---

func audioTestDir() string {
	return filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "audiotest")
}

func audioTestSafePath(name string) (string, error) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		return "", fmt.Errorf("invalid filename")
	}
	dir := audioTestDir()
	abs, _ := filepath.Abs(filepath.Join(dir, name))
	absDir, _ := filepath.Abs(dir)
	if !strings.HasPrefix(abs, absDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid path")
	}
	return abs, nil
}

func (b *DoorPhoneServer) handleAudioTestList(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	dir := audioTestDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		http.Error(w, "Cannot create audiotest dir", http.StatusInternalServerError)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "Cannot read audiotest dir", http.StatusInternalServerError)
		return
	}
	type F struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var files []F
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".wav" || ext == ".mp3" {
			info, _ := e.Info()
			sz := int64(0)
			if info != nil {
				sz = info.Size()
			}
			files = append(files, F{Name: e.Name(), Size: sz})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(files); err != nil {
		log.Printf("error: encode files: %v", err)
	}
}

func (b *DoorPhoneServer) handleAudioTestUpload(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "File too large (max 10MB)", http.StatusBadRequest)
		return
	}
	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if ext := strings.ToLower(filepath.Ext(handler.Filename)); ext != ".wav" && ext != ".mp3" {
		http.Error(w, "Only .wav and .mp3 files allowed", http.StatusBadRequest)
		return
	}
	baseName := filepath.Base(handler.Filename)
	for _, c := range baseName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			http.Error(w, "Invalid filename characters", http.StatusBadRequest)
			return
		}
	}
	dir := audioTestDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		http.Error(w, "Cannot create audiotest dir", http.StatusInternalServerError)
		return
	}
	dst, err := os.Create(filepath.Join(dir, baseName))
	if err != nil {
		http.Error(w, "Cannot create file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	written, err := io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Write error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"file":"%s","size":%d}`, baseName, written)
}

func (b *DoorPhoneServer) handleAudioTestDelete(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	abs, err := audioTestSafePath(r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Remove(abs); err != nil {
		http.Error(w, "Cannot delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

func (b *DoorPhoneServer) handleAudioTestPlay(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	name := strings.TrimPrefix(r.URL.Path, "/panel/api/audiotest/play/")
	abs, err := audioTestSafePath(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.ToLower(filepath.Ext(name)) == ".mp3" {
		w.Header().Set("Content-Type", "audio/mpeg")
	} else {
		w.Header().Set("Content-Type", "audio/wav")
	}
	http.ServeFile(w, r, abs)
}

func (b *DoorPhoneServer) handleAudioTestRun(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	abs, err := audioTestSafePath(r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(abs); err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	go b.playIntoStreamDirect(abs, 100)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

func (b *DoorPhoneServer) handleAudioTestRxStatus(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	RxStatus.mu.RLock()
	from := RxStatus.From
	level := RxStatus.Level
	when := RxStatus.When
	RxStatus.mu.RUnlock()
	receiving := !when.IsZero() && time.Since(when) < 600*time.Millisecond
	w.Header().Set("Content-Type", "application/json")
	fromJSON, _ := json.Marshal(from)
	fmt.Fprintf(w, `{"receiving":%v,"from":%s,"level":%d}`, receiving, fromJSON, level)
}

func (b *DoorPhoneServer) handleAudioTestTxStatus(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	AudioChannelMonitor.mu.RLock()
	active := AudioChannelMonitor.TxActive
	level := int(AudioChannelMonitor.TxAvgLevel)
	AudioChannelMonitor.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"transmitting":%v,"level":%d}`, active, level)
}

// handleAIAnalyze legge il log di doorphoneserver e lo invia al modello OpenRouter selezionato
// per un'analisi intelligente di crash, errori e anomalie.
func (b *DoorPhoneServer) handleAIAnalyze(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	apiKey := readDotEnvKey("OPENROUTER_API_KEY")
	if apiKey == "" {
		http.Error(w, `{"error":"OPENROUTER_API_KEY not set"}`, http.StatusServiceUnavailable)
		return
	}
	aiCfg := loadAIConfig()
	modelID := aiCfg.SelectedModelID
	if modelID == "" {
		http.Error(w, `{"error":"Nessun modello AI selezionato nel tab AI"}`, http.StatusBadRequest)
		return
	}

	// Legge le ultime 300 righe del log
	logPath := Config.Global.Software.Settings.LogFilenameAndPath
	logOut, err := exec.Command("tail", "-n", "300", logPath).Output()
	if err != nil {
		http.Error(w, `{"error":"impossibile leggere il log"}`, http.StatusInternalServerError)
		return
	}

	systemPrompt := `Sei un esperto di sistemi embedded Linux e del software DoorPhoneServer (client PTT Mumble su Raspberry Pi).
Analizza il seguente log di sistema e rispondi in italiano con:
1. **Stato generale**: breve valutazione (OK / ATTENZIONE / CRITICO)
2. **Arresti anomali**: elenca eventuali crash, panic, SIGKILL, SIGTERM inattesi
3. **Errori ricorrenti**: pattern di errori che si ripetono
4. **Connessioni Mumble**: stabilità delle connessioni (disconnessioni frequenti?)
5. **Consigli**: azioni correttive se necessario
Sii conciso e diretto. Se il log è pulito, dillo chiaramente.`

	type chatMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type chatReq struct {
		Model    string    `json:"model"`
		Messages []chatMsg `json:"messages"`
	}
	payload := chatReq{
		Model: modelID,
		Messages: []chatMsg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: "LOG DOORPHONESERVER:\n```\n" + string(logOut) + "\n```"},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"errore costruzione richiesta"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://doorphoneserver")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"openrouter non raggiungibile"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		fmt.Fprintf(w, `{"error":"API key OpenRouter non valida o scaduta. Aggiornala nel file .env"}`)
		return
	}

	// Estrae il testo dalla risposta OpenRouter
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || len(result.Choices) == 0 {
		errMsg := "risposta non valida da OpenRouter"
		if result.Error != nil {
			errMsg = result.Error.Message
		}
		w.Header().Set("Content-Type", "application/json")
		errJSON, _ := json.Marshal(errMsg)
		fmt.Fprintf(w, `{"error":%s}`, errJSON)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	out, err := json.Marshal(map[string]string{
		"model":  aiCfg.SelectedModelName,
		"result": result.Choices[0].Message.Content,
	})
	if err != nil {
		http.Error(w, `{"error":"errore serializzazione risposta"}`, http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(out); err != nil {
		log.Println("Error writing AI analysis response:", err)
	}
}

// handleOpenRouterSelected legge/scrive il modello AI selezionato in preferences/ai.json.
func (b *DoorPhoneServer) handleOpenRouterSelected(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		cfg := loadAIConfig()
		if err := json.NewEncoder(w).Encode(map[string]string{
			"id":   cfg.SelectedModelID,
			"name": cfg.SelectedModelName,
		}); err != nil {
			log.Printf("error: encode selected model: %v", err)
		}
		return
	}
	if r.Method == http.MethodPost {
		var sel struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&sel); err != nil {
			fmt.Fprintf(w, `{"ok":false,"error":"invalid json"}`)
			return
		}
		cfg := AIConfig{SelectedModelID: sel.ID, SelectedModelName: sel.Name}
		if err := saveAIConfig(cfg); err != nil {
			fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(err.Error()))
			return
		}
		fmt.Fprintf(w, `{"ok":true}`)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleOpenRouterModels proxies the OpenRouter model catalogue so the API key
// stays server-side. Returns the raw JSON from https://openrouter.ai/api/v1/models.
func (b *DoorPhoneServer) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	apiKey := readDotEnvKey("OPENROUTER_API_KEY")
	if apiKey == "" {
		http.Error(w, `{"error":"OPENROUTER_API_KEY not set"}`, http.StatusServiceUnavailable)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		http.Error(w, `{"error":"failed to build request"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://doorphoneserver")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"openrouter unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
		return
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		fmt.Fprintf(w, `{"error":"API key OpenRouter non valida o scaduta. Aggiornala nel file .env"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil {
		log.Printf("error: write proxy response: %v", err)
	}
}

// --- Crontab Manager ---

// cronIsJobLine returns true if a line (after stripping a leading #) looks like a cron job entry.
func cronIsJobLine(line string) bool {
	s := strings.TrimSpace(line)
	if strings.HasPrefix(s, "#") {
		rest := s[1:]
		// disabled jobs have '#' immediately followed by digit or '*' (no space)
		// lines like '# comment' or '# 0 5 * * 1 example' are pure comments
		if len(rest) == 0 || rest[0] == ' ' || rest[0] == '\t' {
			return false
		}
		s = rest
	}
	fields := strings.Fields(s)
	if len(fields) < 6 {
		return false
	}
	for _, f := range fields[:5] {
		for _, part := range strings.Split(f, ",") {
			part = strings.Split(part, "/")[0]
			part = strings.Split(part, "-")[0]
			if part != "*" {
				if _, err := strconv.Atoi(part); err != nil {
					return false
				}
			}
		}
	}
	return true
}

func handleCronRead() ([]string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		// "no crontab for user" è normale: restituiamo lista vuota
		return []string{}, nil
	}
	return strings.Split(string(out), "\n"), nil
}

func handleCronWrite(lines []string) error {
	content := strings.Join(lines, "\n")
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func (b *DoorPhoneServer) handleCron(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)

	if r.Method == http.MethodGet {
		lines, err := handleCronRead()
		if err != nil {
			http.Error(w, `{"error":"cannot read crontab"}`, http.StatusInternalServerError)
			return
		}
		type CronJob struct {
			Index   int    `json:"index"`
			Enabled bool   `json:"enabled"`
			Raw     string `json:"raw"`
			Schedule string `json:"schedule"`
			Command  string `json:"command"`
		}
		var jobs []CronJob
		for i, line := range lines {
			if cronIsJobLine(line) {
				enabled := !strings.HasPrefix(strings.TrimSpace(line), "#")
				s := strings.TrimSpace(line)
				if !enabled {
					s = strings.TrimSpace(s[1:])
				}
				fields := strings.Fields(s)
				schedule := strings.Join(fields[:5], " ")
				command := strings.Join(fields[5:], " ")
				jobs = append(jobs, CronJob{
					Index:    i,
					Enabled:  enabled,
					Raw:      line,
					Schedule: schedule,
					Command:  command,
				})
			}
		}
		if jobs == nil {
			jobs = []CronJob{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jobs); err != nil {
			log.Printf("error: encode cron jobs: %v", err)
		}
		return
	}

	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		lines, err := handleCronRead()
		if err != nil {
			http.Error(w, `{"error":"cannot read crontab"}`, http.StatusInternalServerError)
			return
		}

		switch action {
		case "toggle":
			idxStr := r.FormValue("index")
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 || idx >= len(lines) {
				http.Error(w, `{"error":"invalid index"}`, http.StatusBadRequest)
				return
			}
			line := lines[idx]
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				lines[idx] = strings.TrimSpace(trimmed[1:])
			} else {
				lines[idx] = "#" + trimmed
			}
			if err := handleCronWrite(lines); err != nil {
				http.Error(w, `{"error":"cannot write crontab"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)

		case "delete":
			idxStr := r.FormValue("index")
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 || idx >= len(lines) {
				http.Error(w, `{"error":"invalid index"}`, http.StatusBadRequest)
				return
			}
			lines = append(lines[:idx], lines[idx+1:]...)
			if err := handleCronWrite(lines); err != nil {
				http.Error(w, `{"error":"cannot write crontab"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)

		case "add":
			schedule := strings.TrimSpace(r.FormValue("schedule"))
			command := strings.TrimSpace(r.FormValue("command"))
			if schedule == "" || command == "" {
				http.Error(w, `{"error":"schedule and command required"}`, http.StatusBadRequest)
				return
			}
			newLine := schedule + " " + command
			if !cronIsJobLine(newLine) {
				http.Error(w, `{"error":"invalid cron schedule format"}`, http.StatusBadRequest)
				return
			}
			lines = append(lines, newLine)
			if err := handleCronWrite(lines); err != nil {
				http.Error(w, `{"error":"cannot write crontab"}`, http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok":true}`)

		default:
			http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		}
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// =============================================================================
// Log2Ram handlers
// =============================================================================

// sampleLog2RamMetrics legge le metriche correnti di SD e /var/log.
func sampleLog2RamMetrics() Log2RamMetric {
	m := Log2RamMetric{Timestamp: time.Now().Unix()}

	// Uptime da /proc/uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			m.UptimeSec, _ = strconv.ParseFloat(fields[0], 64)
		}
	}

	// Statistiche SD da /sys/block/mmcblk0/stat
	// Campi (0-based): 0=reads_completed 1=reads_merged 2=sectors_read 3=time_reading
	//                  4=writes_completed 5=writes_merged 6=sectors_written 7=time_writing
	//                  8=ios_in_progress 9=time_doing_io ...
	if data, err := os.ReadFile("/sys/block/mmcblk0/stat"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) >= 10 {
			m.SDWriteSectors, _ = strconv.ParseUint(fields[6], 10, 64)
			m.SDWriteMB = float64(m.SDWriteSectors) * 512 / 1048576
			ioTimeMs, _ := strconv.ParseUint(fields[9], 10, 64)
			m.SDIOTimeMs = ioTimeMs
			if m.UptimeSec > 0 {
				m.SDIOPct = float64(ioTimeMs) / (m.UptimeSec * 1000) * 100
				if m.SDIOPct > 100 {
					m.SDIOPct = 100
				}
			}
		}
	}

	// Dimensione /var/log tramite df
	if out, err := exec.Command("df", "--output=used", "/var/log").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			kb, _ := strconv.ParseFloat(strings.TrimSpace(lines[1]), 64)
			m.LogRAMMB = kb / 1024
		}
	}

	return m
}

// startLog2RamMetricsSampler avvia la goroutine che campiona le metriche ogni 10s.
func startLog2RamMetricsSampler() {
	go func() {
		for {
			m := sampleLog2RamMetrics()
			log2ramMetricsMu.Lock()
			log2ramMetricsHist = append(log2ramMetricsHist, m)
			if len(log2ramMetricsHist) > log2ramHistMax {
				log2ramMetricsHist = log2ramMetricsHist[1:]
			}
			log2ramMetricsMu.Unlock()
			time.Sleep(10 * time.Second)
		}
	}()
}

// handleLog2RamStatus restituisce lo stato di log2ram: installato, attivo, mount, RAM usata.
func (b *DoorPhoneServer) handleLog2RamStatus(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type Status struct {
		Installed       bool    `json:"installed"`
		Active          bool    `json:"active"`
		Log2RamMount    bool    `json:"log2ram_mount"`
		RAMUsedBytes    int64   `json:"ram_used_bytes"`
		RAMTotalBytes   int64   `json:"ram_total_bytes"`
		RAMUsedPct      float64 `json:"ram_used_pct"`
		BackupSizeBytes int64   `json:"backup_size_bytes"`
		BackupPath      string  `json:"backup_path"`
		LastSync        string  `json:"last_sync"`
		JournalVolatile bool    `json:"journal_volatile"`
	}

	s := Status{BackupPath: "/var/hdd.log"}

	// Installato?
	for _, p := range []string{"/usr/sbin/log2ram", "/usr/bin/log2ram", "/usr/local/bin/log2ram"} {
		if _, err := os.Stat(p); err == nil {
			s.Installed = true
			break
		}
	}

	// Attivo?
	if out, err := exec.Command("systemctl", "is-active", "log2ram").Output(); err == nil {
		s.Active = strings.TrimSpace(string(out)) == "active"
	}

	// /var/log montato su tmpfs?
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[1] == "/var/log" && fields[2] == "tmpfs" {
				s.Log2RamMount = true
				break
			}
		}
	}

	// RAM usata da df — solo se /var/log è effettivamente su tmpfs (log2ram attivo)
	if s.Log2RamMount {
		if out, err := exec.Command("df", "--output=used,size", "/var/log").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 2 {
				parts := strings.Fields(lines[1])
				if len(parts) >= 2 {
					if used, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
						s.RAMUsedBytes = used * 1024
					}
					if total, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						s.RAMTotalBytes = total * 1024
						if s.RAMTotalBytes > 0 {
							s.RAMUsedPct = float64(s.RAMUsedBytes) / float64(s.RAMTotalBytes) * 100
						}
					}
				}
			}
		}
	}

	// Dimensione backup su SD
	if out, err := exec.Command("du", "-sb", "/var/hdd.log").Output(); err == nil {
		if parts := strings.Fields(string(out)); len(parts) > 0 {
			s.BackupSizeBytes, _ = strconv.ParseInt(parts[0], 10, 64)
		}
	}

	// Ultima sync = mod time di /var/hdd.log
	if fi, err := os.Stat("/var/hdd.log"); err == nil {
		s.LastSync = fi.ModTime().UTC().Format(time.RFC3339)
	}

	// Journal volatile = /var/log/journal NON esiste
	_, statErr := os.Stat("/var/log/journal")
	s.JournalVolatile = os.IsNotExist(statErr)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// handleLog2RamMetrics restituisce il campione corrente di metriche.
func (b *DoorPhoneServer) handleLog2RamMetrics(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m := sampleLog2RamMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

// handleLog2RamMetricsHistory restituisce lo storico degli ultimi 60 campioni.
func (b *DoorPhoneServer) handleLog2RamMetricsHistory(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log2ramMetricsMu.RLock()
	out := make([]Log2RamMetric, len(log2ramMetricsHist))
	copy(out, log2ramMetricsHist)
	log2ramMetricsMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	if len(out) == 0 {
		fmt.Fprintf(w, `[]`)
		return
	}
	json.NewEncoder(w).Encode(out)
}

// handleLog2RamSync forza la sincronizzazione RAM→SD tramite `log2ram write`.
func (b *DoorPhoneServer) handleLog2RamSync(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if out, err := exec.Command("sudo", "log2ram", "write").CombinedOutput(); err != nil {
		log.Printf("error: log2ram sync: %v: %s", err, out)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(strings.TrimSpace(string(out))))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

// handleLog2RamRestart riavvia il servizio log2ram.
func (b *DoorPhoneServer) handleLog2RamRestart(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if out, err := exec.Command("sudo", "systemctl", "restart", "log2ram").CombinedOutput(); err != nil {
		log.Printf("error: log2ram restart: %v: %s", err, out)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":false,"error":%s}`, jsonStr(strings.TrimSpace(string(out))))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

// handleLog2RamFiles elenca i file in /var/log con dimensione e data, ordinati per dimensione.
func (b *DoorPhoneServer) handleLog2RamFiles(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type FileEntry struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified string `json:"modified"`
	}

	entries, err := os.ReadDir("/var/log")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[]`)
		return
	}

	var files []FileEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, FileEntry{
			Name:     entry.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().Format("02/01 15:04"),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Size > files[j].Size
	})

	w.Header().Set("Content-Type", "application/json")
	if len(files) == 0 {
		fmt.Fprintf(w, `[]`)
		return
	}
	json.NewEncoder(w).Encode(files)
}

// handleLog2RamInstall avvia l'installazione di log2ram in background e risponde subito.
func (b *DoorPhoneServer) handleLog2RamInstall(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	l2rInstallJob.mu.Lock()
	if l2rInstallJob.Running {
		l2rInstallJob.mu.Unlock()
		fmt.Fprintf(w, `{"ok":false,"error":"Installazione già in corso"}`)
		return
	}
	l2rInstallJob.Running = true
	l2rInstallJob.Done = false
	l2rInstallJob.Success = false
	l2rInstallJob.Output = ""
	l2rInstallJob.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sudo", "/usr/local/sbin/doorphoneserver-log2ram-install.sh").CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		l2rInstallJob.mu.Lock()
		l2rInstallJob.Running = false
		l2rInstallJob.Done = true
		l2rInstallJob.Output = outStr
		if err != nil {
			log.Printf("error: log2ram install: %v: %s", err, out)
			l2rInstallJob.Success = false
		} else {
			l2rInstallJob.Success = true
		}
		l2rInstallJob.mu.Unlock()
	}()

	fmt.Fprintf(w, `{"ok":true,"started":true}`)
}

// handleLog2RamInstallStatus restituisce lo stato del job di installazione in corso.
func (b *DoorPhoneServer) handleLog2RamInstallStatus(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	l2rInstallJob.mu.Lock()
	running := l2rInstallJob.Running
	done := l2rInstallJob.Done
	success := l2rInstallJob.Success
	output := l2rInstallJob.Output
	l2rInstallJob.mu.Unlock()
	outEsc := strings.ReplaceAll(output, "\n", "\\n")
	fmt.Fprintf(w, `{"running":%v,"done":%v,"success":%v,"output":"%s"}`,
		running, done, success, outEsc)
}

// handleLog2RamConfig legge (GET) o aggiorna (POST) il parametro SIZE in /etc/log2ram.conf.
func (b *DoorPhoneServer) handleLog2RamConfig(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		sizeMB := 0
		if data, err := os.ReadFile("/etc/log2ram.conf"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "SIZE=") {
					val := strings.TrimPrefix(line, "SIZE=")
					val = strings.TrimSpace(val)
					if strings.HasSuffix(val, "M") {
						sizeMB, _ = strconv.Atoi(strings.TrimSuffix(val, "M"))
					} else if strings.HasSuffix(val, "G") {
						gb, _ := strconv.Atoi(strings.TrimSuffix(val, "G"))
						sizeMB = gb * 1024
					}
					break
				}
			}
		}
		fmt.Fprintf(w, `{"size_mb":%d}`, sizeMB)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			SizeMB int `json:"size_mb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"ok":false,"error":"json non valido"}`)
			return
		}
		if req.SizeMB < 64 || req.SizeMB > 512 {
			fmt.Fprintf(w, `{"ok":false,"error":"Valore fuori range (64–512 MB)"}`)
			return
		}
		out, err := exec.Command("sudo", "/usr/local/sbin/doorphoneserver-log2ram-setsize.sh",
			strconv.Itoa(req.SizeMB)).CombinedOutput()
		if err != nil {
			log.Printf("error: log2ram setsize: %v: %s", err, out)
			errJSON, _ := json.Marshal(strings.TrimSpace(string(out)))
			fmt.Fprintf(w, `{"ok":false,"error":%s}`, errJSON)
			return
		}
		fmt.Fprintf(w, `{"ok":true}`)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleSysInfo restituisce informazioni statiche sul sistema operativo e sull'ambiente.
func (b *DoorPhoneServer) handleSysInfo(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	type SysInfo struct {
		OS         string `json:"os"`
		KernelVer  string `json:"kernel"`
		Arch       string `json:"arch"`
		Hostname   string `json:"hostname"`
		GoVersion  string `json:"go_version"`
		AppVersion string `json:"app_version"`
		UptimeSys  string `json:"uptime_sys"`
		BootTime   string `json:"boot_time"`
		CPUModel   string `json:"cpu_model"`
		CPUCores   int    `json:"cpu_cores"`
	}

	info := SysInfo{
		GoVersion:  runtime.Version(),
		AppVersion: doorphoneserverVersion,
		CPUCores:   runtime.NumCPU(),
	}

	// OS pretty name da /etc/os-release
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				break
			}
		}
	}
	if info.OS == "" {
		info.OS = "Linux"
	}

	// Kernel e architettura
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		info.KernelVer = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		info.Arch = strings.TrimSpace(string(out))
	}

	// Hostname
	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	// Uptime di sistema da /proc/uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		parts := strings.Fields(string(data))
		if len(parts) > 0 {
			if secs, err := strconv.ParseFloat(parts[0], 64); err == nil {
				d := int(secs)
				days := d / 86400
				hours := (d % 86400) / 3600
				mins := (d % 3600) / 60
				secs2 := d % 60
				if days > 0 {
					info.UptimeSys = fmt.Sprintf("%dd %dh %dm %ds", days, hours, mins, secs2)
				} else if hours > 0 {
					info.UptimeSys = fmt.Sprintf("%dh %dm %ds", hours, mins, secs2)
				} else {
					info.UptimeSys = fmt.Sprintf("%dm %ds", mins, secs2)
				}
				bootT := time.Now().Add(-time.Duration(secs) * time.Second)
				info.BootTime = bootT.Format("02/01/2006 15:04:05")
			}
		}
	}

	// Modello CPU da /proc/cpuinfo (Raspberry Pi espone "Model" o "Hardware")
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Model\t") {
				info.CPUModel = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				break
			}
		}
		if info.CPUModel == "" {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "Hardware\t") {
					info.CPUModel = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					break
				}
			}
		}
		if info.CPUModel == "" {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") {
					info.CPUModel = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					break
				}
			}
		}
	}
	if info.CPUModel == "" {
		info.CPUModel = info.Arch
	}

	json.NewEncoder(w).Encode(info)
}

// handlePanelFeatures restituisce le feature abilitate in base alla configurazione.
// Il frontend lo usa all'avvio per mostrare/nascondere tab (es. ESP32/NFC con backend=rpi).
func (b *DoorPhoneServer) handlePanelFeatures(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"esp32":%v}`, ioUseESP32())
}

// handleESP32Status restituisce lo stato corrente del bridge ESP32-S3:
// connessione, valori pin GPIO, log tessere.
func (b *DoorPhoneServer) handleESP32Status(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	type statusResp struct {
		Connected bool              `json:"connected"`
		Pins      map[string]int    `json:"pins"`
		CardLog   []ESP32CardLog    `json:"card_log"`
		USBLog    []string          `json:"usb_log"`
		RingFlash map[string]int64  `json:"ring_flash"`
		TabletOn  bool              `json:"tablet_on"`
		FanPct    int               `json:"fan_pct"`
		Floors    floorsJSON        `json:"floors"`
	}

	resp := statusResp{
		Pins:      make(map[string]int),
		CardLog:   []ESP32CardLog{},
		USBLog:    []string{},
		RingFlash: make(map[string]int64),
	}
	if b.USBBridge != nil {
		resp.Connected, resp.Pins, resp.CardLog = b.USBBridge.State.snapshot()
		resp.USBLog = USBLogSnapshot()
		resp.RingFlash = b.USBBridge.State.getRingFlash()
		resp.TabletOn = b.USBBridge.State.getTablet()
		resp.FanPct = b.USBBridge.State.getFanPct()
		f := b.USBBridge.State.getFloors()
		resp.Floors = floorsJSON{P1: f[0], P2: f[1], P3: f[2]}
	}
	json.NewEncoder(w).Encode(resp)
}

// handleESP32Fan imposta la velocità ventola via PWM sull'ESP32-S3.
// POST con campo "duty" (0-100). Restituisce errore se ESP32-S3 non connesso.
func (b *DoorPhoneServer) handleESP32Fan(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dutyStr := r.FormValue("duty")
	duty, err := strconv.Atoi(dutyStr)
	if err != nil || duty < 0 || duty > 100 {
		http.Error(w, "duty deve essere 0-100", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if b.USBBridge == nil || !b.USBBridge.State.isConnected() {
		fmt.Fprintf(w, `{"ok":false,"error":"ESP32-S3 non connesso"}`)
		return
	}
	b.USBBridge.Send("FAN-" + strconv.Itoa(duty) + "\n")
	log.Printf("[PANEL] ESP32 FAN-%d", duty)
	fmt.Fprintf(w, `{"ok":true}`)
}

// handleESP32USBLog restituisce le ultime righe del log seriale USB (← ricevute, → inviate).
// GET → JSON array di stringhe. POST → svuota il buffer.
func (b *DoorPhoneServer) handleESP32USBLog(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		USBLogClear()
		fmt.Fprintf(w, `{"ok":true}`)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	lines := USBLogSnapshot()
	if err := json.NewEncoder(w).Encode(lines); err != nil {
		log.Printf("error: encode usblog: %v", err)
	}
}

// handleESP32Tablet invia TABLET-ON o TABLET-OFF all'ESP32-S3.
// POST con campo "state=on|off".
func (b *DoorPhoneServer) handleESP32Tablet(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if b.USBBridge == nil || !b.USBBridge.State.isConnected() {
		fmt.Fprintf(w, `{"ok":false,"error":"ESP32-S3 non connesso"}`)
		return
	}
	state := r.FormValue("state")
	switch state {
	case "on":
		b.USBBridge.Send("TABLET-ON\n")
		b.USBBridge.State.setTablet(true)
		log.Printf("[PANEL] tablet ON")
		fmt.Fprintf(w, `{"ok":true,"tablet_on":true}`)
	case "off":
		b.USBBridge.Send("TABLET-OFF\n")
		b.USBBridge.State.setTablet(false)
		log.Printf("[PANEL] tablet OFF")
		fmt.Fprintf(w, `{"ok":true,"tablet_on":false}`)
	default:
		http.Error(w, "state deve essere on o off", http.StatusBadRequest)
	}
}

// handleESP32Floors gestisce i testi occupanti (4 slot per piano) per P1/P2/P3.
// GET → {"floors":{"p1":[s1,s2,s3,s4],"p2":[...],"p3":[...]}}
// POST floor=P1|P2|P3 + t1..t4 → invia FLOOR-SET P1 s1|s2|s3|s4 e attende ACK.
func (b *DoorPhoneServer) handleESP32Floors(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		var fj floorsJSON
		if b.USBBridge != nil {
			f := b.USBBridge.State.getFloors()
			fj = floorsJSON{P1: f[0], P2: f[1], P3: f[2]}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"floors": fj})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if b.USBBridge == nil || !b.USBBridge.State.isConnected() {
		fmt.Fprintf(w, `{"ok":false,"error":"ESP32-S3 non connesso"}`)
		return
	}

	floor := strings.ToUpper(strings.TrimSpace(r.FormValue("floor")))
	idxMap := map[string]int{"P1": 0, "P2": 1, "P3": 2}
	idx, ok := idxMap[floor]
	if !ok {
		http.Error(w, "floor deve essere P1, P2 o P3", http.StatusBadRequest)
		return
	}

	clean := func(s string) string {
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
		if len(s) > 20 {
			s = s[:20]
		}
		return s
	}
	var slots [4]string
	for i, key := range []string{"t1", "t2", "t3", "t4"} {
		slots[i] = clean(r.FormValue(key))
	}

	// Protocollo: "FLOOR-SET P1 slot1|slot2|slot3|slot4"
	payload := strings.Join(slots[:], "|")
	_, ackOK := b.USBBridge.SendAndWait("FLOOR-SET "+floor+" "+payload+"\n", "ACK-FLOOR-"+floor, 3*time.Second)
	b.USBBridge.State.setFloorSlots(idx, slots)

	if ackOK {
		log.Printf("[PANEL] FLOOR-SET %s → %v", floor, slots)
		fmt.Fprintf(w, `{"ok":true}`)
	} else {
		log.Printf("[PANEL] FLOOR-SET %s timeout", floor)
		fmt.Fprintf(w, `{"ok":false,"error":"timeout: ESP32 non ha risposto"}`)
	}
}

// handleESP32CardLogClear svuota il log tessere in memoria.
func (b *DoorPhoneServer) handleESP32CardLogClear(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if b.USBBridge != nil {
		b.USBBridge.State.clearCards()
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true}`)
}

// handleESP32Door invia un impulso di apertura portone all'ESP32-S3.
// POST senza parametri richiesti. Restituisce errore se ESP32-S3 non connesso.
func (b *DoorPhoneServer) handleESP32Door(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if b.USBBridge == nil || !b.USBBridge.State.isConnected() {
		fmt.Fprintf(w, `{"ok":false,"error":"ESP32-S3 non connesso"}`)
		return
	}
	b.USBBridge.Send("UNLOCK-DOOR\n")
	log.Printf("[PANEL] ESP32 UNLOCK_DOOR inviato")
	fmt.Fprintf(w, `{"ok":true}`)
}

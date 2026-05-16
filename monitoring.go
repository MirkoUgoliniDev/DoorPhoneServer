// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"
)

// SystemMetrics contiene le metriche di sistema raccolte periodicamente
type SystemMetrics struct {
	Timestamp        time.Time
	Goroutines       int
	MemAllocMB       float64
	MemTotalAllocMB  float64
	MemSysMB         float64
	NumGC            uint32
	CPUUsagePercent  float64
	DiskUsagePercent float64
	Temperature      float64
	UptimeSeconds    int64

	// Metriche DoorPhoneServer specifiche
	ActiveStreams    int
	StaleStreams     int
	TotalConnects    int
	TotalDisconnects int
	IsConnected      bool
}

var (
	metricsMu      sync.RWMutex
	currentMetrics SystemMetrics
	metricsHistory []SystemMetrics
	maxHistorySize = 100
	startTime      = time.Now()
)

// StartSystemMonitoring avvia il monitoraggio periodico delle risorse di sistema
func StartSystemMonitoring(interval time.Duration) {
	log.Printf("info: Starting system monitoring (interval: %v)", interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-GetGlobalContext().Done():
				log.Println("info: System monitoring stopped")
				return
			case <-ticker.C:
				collectMetrics()
				checkThresholds()
			}
		}
	}()
}

// collectMetrics raccoglie le metriche correnti del sistema
func collectMetrics() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metricsMu.Lock()
	defer metricsMu.Unlock()

	currentMetrics.Timestamp = time.Now()
	currentMetrics.Goroutines = runtime.NumGoroutine()
	currentMetrics.MemAllocMB = float64(m.Alloc) / 1024 / 1024
	currentMetrics.MemTotalAllocMB = float64(m.TotalAlloc) / 1024 / 1024
	currentMetrics.MemSysMB = float64(m.Sys) / 1024 / 1024
	currentMetrics.NumGC = m.NumGC
	currentMetrics.UptimeSeconds = int64(time.Since(startTime).Seconds())

	// Metriche DoorPhoneServer
	StreamTrackerMu.RLock()
	currentMetrics.ActiveStreams = len(StreamTracker)
	StreamTrackerMu.RUnlock()
	currentMetrics.StaleStreams = NeedToKill
	currentMetrics.IsConnected = IsConnected.Load()

	// Metriche connessione
	connMetrics := GetConnectionMetrics()
	currentMetrics.TotalConnects = connMetrics.TotalConnects
	currentMetrics.TotalDisconnects = connMetrics.TotalDisconnects

	// Temperatura Raspberry Pi
	temp, err := readRaspberryPiTemperature()
	if err == nil {
		currentMetrics.Temperature = temp
	}

	// Aggiungi alla history (copia esplicita per evitare copylock)
	metricsCopy := SystemMetrics{
		Timestamp:         currentMetrics.Timestamp,
		Goroutines:        currentMetrics.Goroutines,
		MemAllocMB:        currentMetrics.MemAllocMB,
		MemTotalAllocMB:   currentMetrics.MemTotalAllocMB,
		MemSysMB:          currentMetrics.MemSysMB,
		NumGC:             currentMetrics.NumGC,
		CPUUsagePercent:   currentMetrics.CPUUsagePercent,
		DiskUsagePercent:  currentMetrics.DiskUsagePercent,
		Temperature:       currentMetrics.Temperature,
		UptimeSeconds:     currentMetrics.UptimeSeconds,
		ActiveStreams:     currentMetrics.ActiveStreams,
		StaleStreams:      currentMetrics.StaleStreams,
		TotalConnects:     currentMetrics.TotalConnects,
		TotalDisconnects:  currentMetrics.TotalDisconnects,
		IsConnected:       currentMetrics.IsConnected,
	}
	metricsHistory = append(metricsHistory, metricsCopy)
	if len(metricsHistory) > maxHistorySize {
		metricsHistory = metricsHistory[1:]
	}
}

// readRaspberryPiTemperature legge la temperatura del SoC Raspberry Pi
func readRaspberryPiTemperature() (float64, error) {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}

	var temp int
	_, err = fmt.Sscanf(string(data), "%d", &temp)
	if err != nil {
		return 0, err
	}

	return float64(temp) / 1000.0, nil
}

// checkThresholds verifica se le metriche superano soglie critiche
func checkThresholds() {
	metricsMu.RLock()
	defer metricsMu.RUnlock()

	// Soglia goroutine
	if currentMetrics.Goroutines > 100 {
		log.Printf("warn: High goroutine count: %d (stale: %d)",
			currentMetrics.Goroutines, currentMetrics.StaleStreams)
	}

	// Soglia memoria
	if currentMetrics.MemAllocMB > 200 {
		log.Printf("warn: High memory usage: %.2f MB", currentMetrics.MemAllocMB)
	}

	// Soglia temperatura
	if currentMetrics.Temperature > 70.0 {
		log.Printf("warn: High CPU temperature: %.1f°C", currentMetrics.Temperature)
		PushoverSendPushNotification(fmt.Sprintf("⚠️ Temperatura alta: %.1f°C", currentMetrics.Temperature))
	}

	// Soglia stream stale
	if currentMetrics.StaleStreams > 10 {
		log.Printf("warn: High stale stream count: %d - forcing cleanup", currentMetrics.StaleStreams)
		CleanupStaleStreams(5 * time.Minute)
	}
}

// GetCurrentMetrics ritorna le metriche correnti (thread-safe)
func GetCurrentMetrics() SystemMetrics {
	metricsMu.RLock()
	defer metricsMu.RUnlock()
	
	// Copia esplicita per evitare copylock
	return SystemMetrics{
		Timestamp:         currentMetrics.Timestamp,
		Goroutines:        currentMetrics.Goroutines,
		MemAllocMB:        currentMetrics.MemAllocMB,
		MemTotalAllocMB:   currentMetrics.MemTotalAllocMB,
		MemSysMB:          currentMetrics.MemSysMB,
		NumGC:             currentMetrics.NumGC,
		CPUUsagePercent:   currentMetrics.CPUUsagePercent,
		DiskUsagePercent:  currentMetrics.DiskUsagePercent,
		Temperature:       currentMetrics.Temperature,
		UptimeSeconds:     currentMetrics.UptimeSeconds,
		ActiveStreams:     currentMetrics.ActiveStreams,
		StaleStreams:      currentMetrics.StaleStreams,
		TotalConnects:     currentMetrics.TotalConnects,
		TotalDisconnects:  currentMetrics.TotalDisconnects,
		IsConnected:       currentMetrics.IsConnected,
	}
}

// GetMetricsHistory ritorna la cronologia delle metriche
func GetMetricsHistory() []SystemMetrics {
	metricsMu.RLock()
	defer metricsMu.RUnlock()

	history := make([]SystemMetrics, len(metricsHistory))
	copy(history, metricsHistory)
	return history
}

// LogMetricsSummary stampa un riepilogo delle metriche correnti
func LogMetricsSummary() {
	m := GetCurrentMetrics()

	log.Println("========== SYSTEM METRICS SUMMARY ==========")
	log.Printf("Uptime: %v", time.Duration(m.UptimeSeconds)*time.Second)
	log.Printf("Goroutines: %d (Stale Streams: %d)", m.Goroutines, m.StaleStreams)
	log.Printf("Memory: Alloc=%.2fMB TotalAlloc=%.2fMB Sys=%.2fMB",
		m.MemAllocMB, m.MemTotalAllocMB, m.MemSysMB)
	log.Printf("GC Runs: %d", m.NumGC)
	log.Printf("Temperature: %.1f°C", m.Temperature)
	log.Printf("Active Streams: %d", m.ActiveStreams)
	log.Printf("Connected: %v (Total Connects: %d, Disconnects: %d)",
		m.IsConnected, m.TotalConnects, m.TotalDisconnects)
	log.Println("============================================")
}

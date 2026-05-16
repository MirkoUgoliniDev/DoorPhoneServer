// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RotatingLogWriter implements io.Writer with automatic log rotation
// based on file size and cleanup of old rotated files based on retention days.
type RotatingLogWriter struct {
	mu            sync.Mutex
	file          *os.File
	filePath      string
	currentSize   int64
	maxSize       int64
	retentionDays int
}

// NewRotatingLogWriter creates a new rotating log writer.
// maxSizeMB: max file size in MB before rotation (0 = no rotation).
// retentionDays: days to keep old rotated files (0 = keep forever).
func NewRotatingLogWriter(filePath string, maxSizeMB int, retentionDays int) (*RotatingLogWriter, error) {
	w := &RotatingLogWriter{
		filePath:      filePath,
		maxSize:       int64(maxSizeMB) * 1024 * 1024,
		retentionDays: retentionDays,
	}

	if err := w.openFile(); err != nil {
		return nil, err
	}

	// Clean up old rotated files at startup
	w.cleanup()

	return w, nil
}

// openFile apre o crea il file di log e aggiorna la dimensione corrente.
// @return errore se il file non può essere aperto o analizzato
func (w *RotatingLogWriter) openFile() error {
	f, err := os.OpenFile(w.filePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("cannot open log file %s: %v", w.filePath, err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("cannot stat log file %s: %v", w.filePath, err)
	}

	w.file = f
	w.currentSize = info.Size()
	return nil
}

// Write implements io.Writer. Thread-safe.
func (w *RotatingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate if adding this write would exceed max size
	if w.maxSize > 0 && w.currentSize+int64(len(p)) > w.maxSize {
		if rotErr := w.rotate(); rotErr != nil {
			fmt.Fprintf(os.Stderr, "log rotation failed: %v\n", rotErr)
		}
	}

	n, err = w.file.Write(p)
	w.currentSize += int64(n)
	return n, err
}

// rotate ruota il file di log corrente rinominandolo con un suffisso timestamp
// e ne apre uno nuovo. Avvia il cleanup dei file vecchi in background.
// @return errore se la rotazione non può essere completata
func (w *RotatingLogWriter) rotate() error {
	if w.file != nil {
		w.file.Close()
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	rotatedPath := w.filePath + "." + timestamp

	if err := os.Rename(w.filePath, rotatedPath); err != nil {
		// If rename fails, try to reopen the original
		if reopenErr := w.openFile(); reopenErr != nil {
			log.Printf("warn: failed to reopen log file after rename error: %v\n", reopenErr)
		}
		return fmt.Errorf("cannot rename log file: %v", err)
	}

	if err := w.openFile(); err != nil {
		return err
	}

	// Cleanup old files in background
	go w.cleanup()

	return nil
}

// cleanup rimuove i file di log ruotati più vecchi del periodo di retention configurato.
func (w *RotatingLogWriter) cleanup() {
	if w.retentionDays <= 0 {
		return
	}

	dir := filepath.Dir(w.filePath)
	base := filepath.Base(w.filePath)
	pattern := filepath.Join(dir, base+".*")

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)

	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(match)
		}
	}
}

// Close closes the underlying log file.
func (w *RotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

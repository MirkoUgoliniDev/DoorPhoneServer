package doorphoneserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const keyStateFile = "preferences/key_state.json"

type keyStateData struct {
	FP             string `json:"fp"`
	ReEnrollNeeded bool   `json:"re_enroll_needed"`
}

// keyStateMu serializza ogni operazione read-modify-write: il lock viene tenuto
// per l'intera sequenza load→modify→persist, eliminando la finestra TOCTOU.
var (
	keyStateMu    sync.Mutex
	keyStateCache *keyStateData
)

// loadLocked legge lo stato dalla cache o dal disco. Richiede keyStateMu acquisito.
func loadLocked() keyStateData {
	if keyStateCache != nil {
		return *keyStateCache
	}
	var s keyStateData
	if data, err := os.ReadFile(keyStateFile); err == nil {
		json.Unmarshal(data, &s) //nolint:errcheck — zero value su JSON malformato è safe
	}
	keyStateCache = &s
	return s
}

// persistLocked scrive su disco e aggiorna la cache. Richiede keyStateMu acquisito.
func persistLocked(s keyStateData) error {
	if err := os.MkdirAll(filepath.Dir(keyStateFile), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyStateFile, data, 0644); err != nil {
		return err
	}
	keyStateCache = &s
	return nil
}

// GetKeyFP ritorna il fingerprint salvato e true, oppure ("", false) se assente.
func GetKeyFP() (fp string, ok bool) {
	keyStateMu.Lock()
	defer keyStateMu.Unlock()
	s := loadLocked()
	if s.FP == "" {
		return "", false
	}
	return s.FP, true
}

// SaveKeyFP salva il fingerprint su disco in modo atomico rispetto a SetReEnrollNeeded.
func SaveKeyFP(fp string) error {
	keyStateMu.Lock()
	defer keyStateMu.Unlock()
	s := loadLocked()
	s.FP = fp
	return persistLocked(s)
}

// IsReEnrollNeeded ritorna true se le tessere devono essere re-enrollate.
func IsReEnrollNeeded() bool {
	keyStateMu.Lock()
	defer keyStateMu.Unlock()
	return loadLocked().ReEnrollNeeded
}

// SetReEnrollNeeded aggiorna il flag re-enroll su disco in modo atomico rispetto a SaveKeyFP.
func SetReEnrollNeeded(v bool) error {
	keyStateMu.Lock()
	defer keyStateMu.Unlock()
	s := loadLocked()
	s.ReEnrollNeeded = v
	return persistLocked(s)
}

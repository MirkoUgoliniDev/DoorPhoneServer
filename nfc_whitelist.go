package doorphoneserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const nfcWhitelistFile = "preferences/nfc_whitelist.json"

// NFCEntry descrive un tag NFC autorizzato.
type NFCEntry struct {
	Name        string     `json:"name"`
	Type        string     `json:"type"` // "PLAIN" o "DESFIRE"
	AddedAt     time.Time  `json:"added_at"`
	LastAccess  *time.Time `json:"last_access"`
	AccessCount int        `json:"access_count"`
}

// EnrollSession traccia uno stato di enrollment in corso.
type EnrollSession struct {
	Name      string
	Mode      string // "plain", "desfire-new", "desfire-existing"
	Phase     int    // 1 o 2 (solo desfire-new)
	FormatUID string // UID dal TAG-FORMAT-OK (fase 1)
	SSEChan   chan SSEEvent
	Ctx       context.Context
	Cancel    context.CancelFunc
}

// SSEEvent inviato al client via Server-Sent Events.
type SSEEvent struct {
	Event      string `json:"event"`
	Code       string `json:"code,omitempty"`
	Msg        string `json:"msg,omitempty"`
	UID        string `json:"uid,omitempty"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	TagType    string `json:"tag_type,omitempty"` // per tag-detected: PLAIN, DESFIRE-CONFIGURED, DESFIRE-NEW
	Step       int    `json:"step,omitempty"`
	TotalSteps int    `json:"total_steps,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// NFCWhitelistManager gestisce la whitelist NFC con persistenza su file JSON.
// Thread-safe per accessi da goroutine seriale e HTTP.
type NFCWhitelistManager struct {
	mu       sync.RWMutex
	entries  map[string]NFCEntry
	filePath string

	enrollMu      sync.Mutex
	enrollSession *EnrollSession

	seenMu      sync.Mutex
	lastSeenUID string
}

// UID: esattamente 14 caratteri hex maiuscolo = 7 byte
var uidRegex = regexp.MustCompile(`^[A-F0-9]{14}$`)

// NewNFCWhitelistManager crea il gestore e carica la whitelist da file.
func NewNFCWhitelistManager() *NFCWhitelistManager {
	m := &NFCWhitelistManager{
		entries:  make(map[string]NFCEntry),
		filePath: nfcWhitelistFile,
	}
	if err := m.load(); err != nil {
		log.Printf("[NFC] whitelist non trovata, verrà creata al primo salvataggio: %v", err)
	}
	return m
}

func (m *NFCWhitelistManager) load() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Unmarshal(data, &m.entries)
}

func (m *NFCWhitelistManager) save() error {
	m.mu.RLock()
	data, err := json.MarshalIndent(m.entries, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.filePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0644)
}

// ── Enrollment session lifecycle ──────────────────────────────────────────────

// StartEnrollSession avvia una nuova sessione di enrollment.
func (m *NFCWhitelistManager) StartEnrollSession(name, mode string) (*EnrollSession, error) {
	if err := m.validateName(name); err != nil {
		return nil, fmt.Errorf("nome non valido: %v", err)
	}
	if mode != "plain" && mode != "desfire-new" && mode != "desfire-existing" && mode != "auto" {
		return nil, fmt.Errorf("mode non valido: %s", mode)
	}

	m.enrollMu.Lock()
	defer m.enrollMu.Unlock()

	if m.enrollSession != nil {
		return nil, fmt.Errorf("enrollment già in corso")
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &EnrollSession{
		Name:    name,
		Mode:    mode,
		Phase:   1,
		SSEChan: make(chan SSEEvent, 16),
		Ctx:     ctx,
		Cancel:  cancel,
	}
	m.enrollSession = session
	go m.enrollTimeout(session)
	return session, nil
}

// enrollTimeout invia un errore SSE dopo 35s se l'enrollment non si completa.
func (m *NFCWhitelistManager) enrollTimeout(session *EnrollSession) {
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	select {
	case <-session.Ctx.Done():
		return // completato o annullato esternamente
	case <-timer.C:
		m.enrollMu.Lock()
		if m.enrollSession == session {
			m.enrollSession = nil
		}
		m.enrollMu.Unlock()
		select {
		case session.SSEChan <- SSEEvent{
			Event: "error", Code: "timeout",
			Msg: "Timeout: nessun tag rilevato entro 35 secondi",
		}:
		default:
		}
		session.Cancel()
	}
}

// GetEnrollSession ritorna la sessione attiva o nil.
func (m *NFCWhitelistManager) GetEnrollSession() *EnrollSession {
	m.enrollMu.Lock()
	defer m.enrollMu.Unlock()
	return m.enrollSession
}

// AbortEnroll chiude la sessione di enrollment (es. annullamento utente).
func (m *NFCWhitelistManager) AbortEnroll() {
	m.enrollMu.Lock()
	session := m.enrollSession
	m.enrollSession = nil
	m.enrollMu.Unlock()
	if session != nil {
		select {
		case session.SSEChan <- SSEEvent{
			Event: "error", Code: "cancelled",
			Msg: "Enrollment annullato",
		}:
		default:
		}
		session.Cancel()
	}
}

// ── Gestori eventi seriale NFC ────────────────────────────────────────────────

// HandleNFCEvent gestisce "EVT nfc <uid>": salva lastSeenUID e aggiorna last_access.
func (m *NFCWhitelistManager) HandleNFCEvent(uid string) {
	uid = strings.ToUpper(uid)

	m.seenMu.Lock()
	m.lastSeenUID = uid
	m.seenMu.Unlock()

	// Aggiorna last_access se il tag è nella whitelist
	m.mu.Lock()
	entry, ok := m.entries[uid]
	if ok {
		now := time.Now().UTC()
		entry.LastAccess = &now
		m.entries[uid] = entry
	}
	m.mu.Unlock()

	if ok {
		if err := m.save(); err != nil {
			log.Printf("[NFC] errore aggiornamento last_access: %v", err)
		}
	}
}

// HandleTagInfo gestisce "TAG-INFO <uid> <type>" — inviato dall'ESP32 appena rileva il tag.
// Manda un SSE tag-detected al client con UID e tipo identificato.
func (m *NFCWhitelistManager) HandleTagInfo(uid, rawType string) {
	uid = strings.ToUpper(uid)
	rawType = strings.ToUpper(rawType)

	m.enrollMu.Lock()
	session := m.enrollSession
	m.enrollMu.Unlock()

	if session == nil {
		log.Printf("[NFC] TAG-INFO ricevuto senza enrollment attivo: uid=%s tipo=%s", uid, rawType)
		return
	}

	log.Printf("[NFC] tag rilevato: uid=%s tipo=%s", uid, rawType)
	select {
	case session.SSEChan <- SSEEvent{
		Event: "tag-detected", UID: uid, TagType: rawType,
	}:
	default:
		log.Printf("[NFC] warn: SSEChan pieno, tag-detected non inviato")
	}
}

// HandleUIDOK gestisce "UID-OK": incrementa access_count per lastSeenUID.
func (m *NFCWhitelistManager) HandleUIDOK() {
	m.seenMu.Lock()
	uid := m.lastSeenUID
	m.seenMu.Unlock()

	if uid == "" {
		return
	}

	m.mu.Lock()
	entry, ok := m.entries[uid]
	if ok {
		entry.AccessCount++
		m.entries[uid] = entry
	}
	m.mu.Unlock()

	if ok {
		log.Printf("[NFC] accesso OK: uid=%s nome=%q count=%d", uid, entry.Name, entry.AccessCount)
		if err := m.save(); err != nil {
			log.Printf("[NFC] errore salvataggio access_count: %v", err)
		}
	} else {
		log.Printf("[NFC] accesso OK: uid=%s (non nel JSON)", uid)
	}
}

// HandleUIDKO gestisce "UID-KO": logga il tentativo negato.
func (m *NFCWhitelistManager) HandleUIDKO() {
	m.seenMu.Lock()
	uid := m.lastSeenUID
	m.seenMu.Unlock()
	log.Printf("[NFC] accesso NEGATO: uid=%s", uid)
}

// HandleTagEnrolled gestisce "TAG-ENROLLED <uid> <PLAIN|DESFIRE>".
// Il nome viene preso da enrollSession.Name (non dall'ESP32).
func (m *NFCWhitelistManager) HandleTagEnrolled(uid, tagType string) {
	uid = strings.ToUpper(uid)
	tagType = strings.ToUpper(tagType)

	m.enrollMu.Lock()
	session := m.enrollSession
	if session == nil {
		m.enrollMu.Unlock()
		log.Printf("[NFC] TAG-ENROLLED ricevuto senza enrollment attivo: uid=%s", uid)
		return
	}
	name := session.Name
	m.enrollSession = nil
	m.enrollMu.Unlock()

	// Aggiungi alla whitelist JSON
	m.mu.Lock()
	m.entries[uid] = NFCEntry{
		Name:    name,
		Type:    tagType,
		AddedAt: time.Now().UTC(),
	}
	m.mu.Unlock()

	if err := m.save(); err != nil {
		log.Printf("[NFC] errore salvataggio enrolled: %v", err)
	}
	log.Printf("[NFC] tag enrollato: uid=%s nome=%q tipo=%s", uid, name, tagType)

	select {
	case session.SSEChan <- SSEEvent{
		Event: "enrolled", UID: uid, Name: name, Type: tagType,
	}:
	default:
		log.Printf("[NFC] warn: SSEChan pieno, enrolled event non inviato")
	}
	session.Cancel()
}

// HandleEnrollFail gestisce "TAG-ENROLL-FAIL".
func (m *NFCWhitelistManager) HandleEnrollFail(line string) {
	m.enrollMu.Lock()
	session := m.enrollSession
	m.enrollSession = nil
	m.enrollMu.Unlock()

	if session == nil {
		return
	}

	code := "enroll-fail"
	msg := "Enrollment fallito"
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, "ALREADY"):
		code = "already-enrolled"
		msg = "Tag già registrato nella NVS"
	case strings.Contains(upper, "FULL"):
		code = "whitelist-full"
		msg = "Whitelist piena (max 10 tag)"
	case strings.Contains(upper, "AUTH"):
		code = "auth-fail"
		msg = "Autenticazione fallita — chiave errata"
	}

	select {
	case session.SSEChan <- SSEEvent{Event: "error", Code: code, Msg: msg}:
	default:
	}
	session.Cancel()
}

// HandleTagFormatOK gestisce "TAG-FORMAT-OK <uid>".
// NOTA: il bridge deve aver già inviato TAG-ENROLL prima di chiamare questo metodo.
func (m *NFCWhitelistManager) HandleTagFormatOK(uid string) {
	uid = strings.ToUpper(uid)

	m.enrollMu.Lock()
	session := m.enrollSession
	if session == nil {
		m.enrollMu.Unlock()
		log.Printf("[NFC] TAG-FORMAT-OK ricevuto senza enrollment attivo: uid=%s", uid)
		return
	}
	session.FormatUID = uid
	session.Phase = 2
	m.enrollMu.Unlock()

	// Invia due eventi SSE in sequenza
	select {
	case session.SSEChan <- SSEEvent{
		Event: "format-ok", Step: 1, TotalSteps: 2, UID: uid,
		Msg: "Inizializzazione OK. Rimuovi e riavvicina il tag.",
	}:
	default:
	}
	select {
	case session.SSEChan <- SSEEvent{
		Event: "waiting-enroll", Step: 2, TotalSteps: 2, TimeoutSec: 30,
		Msg: "Riavvicina il tag al lettore",
	}:
	default:
	}
}

// HandleFormatFail gestisce "TAG-FORMAT-FAIL" o "TAG-FORMAT-FAIL NOT-DESFIRE".
func (m *NFCWhitelistManager) HandleFormatFail(code string) {
	m.enrollMu.Lock()
	session := m.enrollSession
	m.enrollSession = nil
	m.enrollMu.Unlock()

	if session == nil {
		return
	}

	msg := "Inizializzazione tag fallita"
	if code == "not-desfire" {
		msg = "Il tag non è DESFire"
	}

	select {
	case session.SSEChan <- SSEEvent{Event: "error", Code: code, Msg: msg}:
	default:
	}
	session.Cancel()
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

// Has ritorna true se il tag è nella whitelist JSON.
func (m *NFCWhitelistManager) Has(uid string) bool {
	m.mu.RLock()
	_, ok := m.entries[strings.ToUpper(uid)]
	m.mu.RUnlock()
	return ok
}

// List ritorna tutti gli entry come slice per API REST.
func (m *NFCWhitelistManager) List() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]map[string]interface{}, 0, len(m.entries))
	for uid, e := range m.entries {
		result = append(result, map[string]interface{}{
			"uid":          uid,
			"name":         e.Name,
			"type":         e.Type,
			"added_at":     e.AddedAt,
			"last_access":  e.LastAccess,
			"access_count": e.AccessCount,
		})
	}
	return result
}

// Update cambia il nome di un tag (solo JSON).
func (m *NFCWhitelistManager) Update(uid string, name *string) error {
	uid = strings.ToUpper(uid)
	if err := m.validateUID(uid); err != nil {
		return err
	}

	m.mu.Lock()
	entry, ok := m.entries[uid]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("tag non trovato")
	}
	if name != nil {
		if err := m.validateName(*name); err != nil {
			m.mu.Unlock()
			return err
		}
		entry.Name = *name
	}
	m.entries[uid] = entry
	m.mu.Unlock()
	return m.save()
}

// Delete rimuove un tag dalla whitelist JSON.
func (m *NFCWhitelistManager) Delete(uid string) error {
	uid = strings.ToUpper(uid)
	if err := m.validateUID(uid); err != nil {
		return err
	}
	return m.deleteEntry(uid)
}

// deleteEntry rimuove un tag per chiave senza validare il formato UID.
// Usato per entry legacy (es. UID 8-char dal vecchio firmware).
func (m *NFCWhitelistManager) deleteEntry(uid string) error {
	m.mu.Lock()
	if _, ok := m.entries[uid]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("tag non trovato")
	}
	delete(m.entries, uid)
	m.mu.Unlock()
	return m.save()
}

// SyncFromESP32 confronta i tag ESP32 NVS con il JSON locale.
func (m *NFCWhitelistManager) SyncFromESP32(esp32Tags map[string]string) map[string]interface{} {
	m.mu.RLock()
	jsonUIDs := make(map[string]bool, len(m.entries))
	for uid := range m.entries {
		jsonUIDs[uid] = true
	}
	m.mu.RUnlock()

	esp32Only := []string{}
	jsonOnly := []string{}

	for uid := range esp32Tags {
		if !jsonUIDs[uid] {
			esp32Only = append(esp32Only, uid)
		}
	}
	for uid := range jsonUIDs {
		if _, ok := esp32Tags[uid]; !ok {
			jsonOnly = append(jsonOnly, uid)
		}
	}

	inSync := len(esp32Only) == 0 && len(jsonOnly) == 0
	if !inSync {
		log.Printf("[NFC] differenze: esp32-only=%v json-only=%v", esp32Only, jsonOnly)
	}

	return map[string]interface{}{
		"esp32_count": len(esp32Tags),
		"json_count":  len(jsonUIDs),
		"in_sync":     inSync,
		"esp32_only":  esp32Only,
		"json_only":   jsonOnly,
	}
}

// ── Validazione ───────────────────────────────────────────────────────────────

func (m *NFCWhitelistManager) validateUID(uid string) error {
	if !uidRegex.MatchString(strings.ToUpper(uid)) {
		return fmt.Errorf("UID non valido (richiesti 14 hex maiuscolo): %s", uid)
	}
	return nil
}

func (m *NFCWhitelistManager) validateName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) == 0 {
		return fmt.Errorf("nome obbligatorio")
	}
	if len(name) > 64 {
		return fmt.Errorf("nome max 64 caratteri")
	}
	return nil
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

func (b *DoorPhoneServer) handleWhitelistPage(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	sub, err := fs.Sub(staticFS, "webpanel_static")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data, err := fs.ReadFile(sub, "whitelist.html")
	if err != nil {
		http.Error(w, "whitelist.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

// GET /api/whitelist
func (b *DoorPhoneServer) handleWhitelistGet(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(b.NFCWhitelist.List())
}

// POST /api/whitelist/enroll
func (b *DoorPhoneServer) handleWhitelistEnrollStart(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"json non valido"}`)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Mode = strings.TrimSpace(req.Mode)

	session, err := b.NFCWhitelist.StartEnrollSession(req.Name, req.Mode)
	if err != nil {
		if strings.Contains(err.Error(), "già in corso") {
			w.WriteHeader(http.StatusConflict)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}

	// Invia comando all'ESP32 e attendi ACK (timeout 3s)
	var cmd, ackKey string
	switch session.Mode {
	case "auto":
		cmd = "TAG-SCAN\n"
		ackKey = "ACK-SCAN"
	case "desfire-new":
		cmd = "TAG-FORMAT\n"
		ackKey = "ACK-FORMAT"
	default:
		cmd = "TAG-ENROLL\n"
		ackKey = "ACK-ENROLL"
	}

	_, ok := b.USBBridge.SendAndWait(cmd, ackKey, 3*time.Second)
	if !ok {
		b.NFCWhitelist.AbortEnroll()
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"ESP32 non risponde (timeout ACK)"}`)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"mode":%q}`, session.Mode)
}

// GET /api/whitelist/enroll/events — SSE stream enrollment progress
func (b *DoorPhoneServer) handleWhitelistEnrollEvents(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := b.NFCWhitelist.GetEnrollSession()
	if session == nil {
		http.Error(w, `{"error":"nessun enrollment in corso"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// Evento iniziale in base alla modalità
	switch session.Mode {
	case "desfire-new":
		b.sseWrite(w, flusher, SSEEvent{
			Event: "waiting-format", Step: 1, TotalSteps: 2,
			TimeoutSec: 30, Msg: "Avvicina il tag DESFire per inizializzarlo",
		})
	default: // "auto", "plain", "desfire-existing"
		b.sseWrite(w, flusher, SSEEvent{
			Event: "waiting", Step: 1, TotalSteps: 1,
			TimeoutSec: 30, Msg: "Avvicina il tag al lettore",
		})
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-session.SSEChan:
			b.sseWrite(w, flusher, evt)
			if evt.Event == "enrolled" || evt.Event == "error" {
				return
			}
		}
	}
}

// DELETE /api/whitelist/enroll/cancel
func (b *DoorPhoneServer) handleWhitelistEnrollCancel(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b.NFCWhitelist.AbortEnroll()
	fmt.Fprintf(w, `{"ok":true}`)
}

// GET /api/whitelist/sync
func (b *DoorPhoneServer) handleWhitelistSync(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tags, err := b.USBBridge.SendTagList(5 * time.Second)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}

	result := b.NFCWhitelist.SyncFromESP32(tags)
	json.NewEncoder(w).Encode(result)
}

// PUT /api/whitelist/{uid}
func (b *DoorPhoneServer) handleWhitelistUpdate(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uid := strings.TrimPrefix(r.URL.Path, "/api/whitelist/")
	uid = strings.TrimSpace(uid)

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"json non valido"}`)
		return
	}

	if err := b.NFCWhitelist.Update(uid, &req.Name); err != nil {
		if strings.Contains(err.Error(), "non trovato") {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}
	fmt.Fprintf(w, `{"ok":true}`)
}

// DELETE /api/whitelist/{uid}
func (b *DoorPhoneServer) handleWhitelistDelete(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uid := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/whitelist/")))
	if uid == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"UID mancante"}`)
		return
	}

	removedFromESP32 := false
	removedFromJSON := false

	// Tenta TAG-DEL su ESP32 solo per UID a 14 char (formato valido firmware)
	if uidRegex.MatchString(uid) {
		resp, ok := b.USBBridge.SendAndWait("TAG-DEL "+uid+"\n", "TAG-DEL", 3*time.Second)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"error":"ESP32 non risponde"}`)
			return
		}
		if resp == "OK" {
			removedFromESP32 = true
		} else if !strings.HasPrefix(resp, "FAIL NOT-FOUND") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":"rimozione ESP32 fallita: %s"}`, resp)
			return
		}
	}
	// Cancella dal JSON (funziona per qualsiasi formato UID, anche legacy 8-char)
	if err := b.NFCWhitelist.deleteEntry(uid); err == nil {
		removedFromJSON = true
	} else {
		log.Printf("[NFC] delete JSON %s: %v", uid, err)
	}

	if !removedFromESP32 && !removedFromJSON {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"tag non trovato"}`)
		return
	}

	fmt.Fprintf(w, `{"uid":%q,"removed_from_esp32":%v,"removed_from_json":%v}`,
		uid, removedFromESP32, removedFromJSON)
}

// ClearAll rimuove tutti i tag dalla whitelist JSON.
func (m *NFCWhitelistManager) ClearAll() error {
	m.mu.Lock()
	m.entries = make(map[string]NFCEntry)
	m.mu.Unlock()
	return m.save()
}

// POST /api/whitelist/clearall — rimuove TUTTI i tag da JSON e ESP32
func (b *DoorPhoneServer) handleWhitelistClearAll(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Invia TAG-CLEAR all'ESP32 e attendi ACK (timeout 5s)
	_, esp32ok := b.USBBridge.SendAndWait("TAG-CLEAR\n", "ACK-CLEAR", 5*time.Second)

	// Cancella JSON locale indipendentemente dall'ESP32
	if err := b.NFCWhitelist.ClearAll(); err != nil {
		log.Printf("[NFC] errore salvataggio dopo clearall: %v", err)
	}

	log.Printf("[NFC] clearAll: esp32_ok=%v", esp32ok)
	fmt.Fprintf(w, `{"ok":true,"esp32_cleared":%v}`, esp32ok)
}

// sseWrite serializza un SSEEvent e lo scrive nello stream.
func (b *DoorPhoneServer) sseWrite(w http.ResponseWriter, flusher http.Flusher, evt SSEEvent) {
	data, _ := json.Marshal(evt)
	fmt.Fprintf(w, "data: %s\n\n", string(data))
	if flusher != nil {
		flusher.Flush()
	}
}

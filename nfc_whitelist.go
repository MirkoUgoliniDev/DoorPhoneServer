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
	Disabled    bool       `json:"disabled,omitempty"` // false (omesso) = abilitato; true = disabilitato
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
	TagType    string `json:"tag_type,omitempty"` // per tag-detected/identified: PLAIN, DESFIRE-CONFIGURED, DESFIRE-NEW
	Step       int    `json:"step,omitempty"`
	TotalSteps int    `json:"total_steps,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`

	// Solo per l'evento "identified" (modalità lettura one-shot):
	Known       bool `json:"known,omitempty"`        // UID presente nella whitelist server
	Disabled    bool `json:"disabled,omitempty"`     // se in whitelist, è disabilitato?
	AccessCount int  `json:"access_count,omitempty"` // accessi registrati (se in whitelist)
}

// NFCWhitelistManager gestisce la whitelist NFC con persistenza su file JSON.
// Thread-safe per accessi da goroutine seriale e HTTP.
type NFCWhitelistManager struct {
	mu       sync.RWMutex
	entries  map[string]NFCEntry
	filePath string

	enrollMu      sync.Mutex
	enrollSession *EnrollSession
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
	if mode != "plain" && mode != "desfire-new" && mode != "desfire-existing" && mode != "auto" && mode != "identify" {
		return nil, fmt.Errorf("mode non valido: %s", mode)
	}
	// In modalità "identify" (sola lettura) non si registra nulla: il nome non serve.
	if mode != "identify" {
		if err := m.validateName(name); err != nil {
			return nil, fmt.Errorf("nome non valido: %v", err)
		}
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

// HandleNFCEvent gestisce "EVT nfc <uid>".
// Nel modello crypto-only l'ESP32 emette questo evento SOLO per tessere DESFire
// che hanno superato l'auth AES a bordo: l'UID è quindi già crittograficamente
// valido (cloni PLAIN e tessere di altri impianti non arrivano mai qui).
//
// Il server fa da "gate di autorizzazione": cerca l'UID nella whitelist e, se
// presente e abilitato, aggiorna i contatori e ritorna true → il portone va
// aperto. Ritorna false (e logga) se l'UID non è autorizzato o è disabilitato.
func (m *NFCWhitelistManager) HandleNFCEvent(uid string) bool {
	uid = strings.ToUpper(uid)

	m.mu.Lock()
	entry, ok := m.entries[uid]
	if ok && !entry.Disabled {
		now := time.Now().UTC()
		entry.LastAccess = &now
		entry.AccessCount++
		m.entries[uid] = entry
	}
	m.mu.Unlock()

	if !ok {
		log.Printf("[NFC] tessera valida ma NON autorizzata: uid=%s — portone non aperto", uid)
		return false
	}
	if entry.Disabled {
		log.Printf("[NFC] ACCESSO NEGATO: uid=%s nome=%q disabilitato — portone non aperto", uid, entry.Name)
		return false
	}

	log.Printf("[NFC] accesso autorizzato: uid=%s nome=%q count=%d", uid, entry.Name, entry.AccessCount)
	if err := m.save(); err != nil {
		log.Printf("[NFC] errore salvataggio access_count: %v", err)
	}
	return true
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
		log.Printf("[NFC] TAG-INFO ricevuto senza sessione attiva: uid=%s tipo=%s", uid, rawType)
		return
	}

	// Modalità lettura one-shot: costruisci l'esito completo e chiudi.
	if session.Mode == "identify" {
		m.handleIdentifyResult(session, uid, rawType)
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

// handleIdentifyResult costruisce l'esito della modalità lettura one-shot:
// combina lo stato crittografico riportato dall'ESP32 (rawType) con la presenza
// dell'UID nella whitelist server. Non modifica nulla e chiude la sessione.
func (m *NFCWhitelistManager) handleIdentifyResult(session *EnrollSession, uid, rawType string) {
	m.mu.RLock()
	entry, known := m.entries[uid]
	m.mu.RUnlock()

	evt := SSEEvent{Event: "identified", UID: uid, TagType: rawType, Known: known}
	if known {
		evt.Name = entry.Name
		evt.Type = entry.Type
		evt.Disabled = entry.Disabled
		evt.AccessCount = entry.AccessCount
	}

	m.enrollMu.Lock()
	if m.enrollSession == session {
		m.enrollSession = nil
	}
	m.enrollMu.Unlock()

	log.Printf("[NFC] lettura tessera: uid=%s crypto=%s in_whitelist=%v nome=%q disabled=%v",
		uid, rawType, known, evt.Name, evt.Disabled)

	select {
	case session.SSEChan <- evt:
	default:
		log.Printf("[NFC] warn: SSEChan pieno, identified non inviato")
	}
	session.Cancel()
}

// HandleUIDOK gestisce "UID-OK": è solo diagnostica dell'esito auth AES a bordo.
// La decisione di aprire il portone NON dipende più da qui ma da EVT nfc
// (HandleNFCEvent), che è l'unico punto in cui si applica la whitelist server.
func (m *NFCWhitelistManager) HandleUIDOK() {
	log.Printf("[NFC] UID-OK (auth AES a bordo riuscita) — apertura decisa su EVT nfc")
}

// HandleUIDKO gestisce "UID-KO": diagnostica di tessera non DESFire o auth fallita
// a bordo. Per queste tessere l'ESP32 non emette alcun EVT nfc.
func (m *NFCWhitelistManager) HandleUIDKO() {
	log.Printf("[NFC] UID-KO (tessera non DESFire o auth AES fallita a bordo)")
}

// HandleTagEnrolled gestisce "TAG-ENROLLED <uid> <PLAIN|DESFIRE>".
// Il nome viene preso da enrollSession.Name (non dall'ESP32).
func (m *NFCWhitelistManager) HandleTagEnrolled(uid, tagType string) {
	uid = strings.ToUpper(uid)
	tagType = strings.ToUpper(tagType)

	if !uidRegex.MatchString(uid) {
		log.Printf("[NFC] TAG-ENROLLED ignorato: UID non valido %q", uid)
		return
	}

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
	case strings.Contains(upper, "NOT-DESFIRE"):
		code = "not-desfire"
		msg = "Solo tessere DESFire sono supportate"
	case strings.Contains(upper, "NO-KEY"):
		code = "no-key"
		msg = "Genera prima la chiave (KEY-GEN)"
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
	switch code {
	case "not-desfire":
		msg = "Il tag non è DESFire"
	case "no-key":
		msg = "Genera prima la chiave (KEY-GEN)"
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
			"disabled":     e.Disabled,
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

// SetDisabled abilita o disabilita un tag senza rimuoverlo dalla whitelist.
func (m *NFCWhitelistManager) SetDisabled(uid string, disabled bool) error {
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
	entry.Disabled = disabled
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

	if b.USBBridge == nil {
		b.NFCWhitelist.AbortEnroll()
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"ESP32 non disponibile (backend=rpi)"}`)
		return
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

// POST /api/whitelist/identify — avvia la modalità lettura one-shot.
// Invia TAG-INFO all'ESP32; al primo tap il server risponde via SSE con l'evento
// "identified" (stato crypto + presenza in whitelist), poi chiude la sessione.
func (b *DoorPhoneServer) handleWhitelistIdentifyStart(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := b.NFCWhitelist.StartEnrollSession("", "identify")
	if err != nil {
		if strings.Contains(err.Error(), "già in corso") {
			w.WriteHeader(http.StatusConflict)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}

	if b.USBBridge == nil {
		b.NFCWhitelist.AbortEnroll()
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"ESP32 non disponibile (backend=rpi)"}`)
		return
	}
	if _, ok := b.USBBridge.SendAndWait("TAG-INFO\n", "ACK-INFO", 3*time.Second); !ok {
		b.NFCWhitelist.AbortEnroll()
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"ESP32 non risponde (timeout ACK)"}`)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ok":true,"mode":"identify"}`)
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
	case "identify":
		b.sseWrite(w, flusher, SSEEvent{
			Event: "waiting", Step: 1, TotalSteps: 1,
			TimeoutSec: 30, Msg: "Avvicina la tessera da leggere",
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
			if evt.Event == "enrolled" || evt.Event == "error" || evt.Event == "identified" {
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

	// Modello crypto-only: la whitelist vive solo sul server. L'ESP32 non ha più
	// una NVS locale da cui cancellare, quindi si rimuove solo dal JSON.
	// deleteEntry accetta qualsiasi formato UID, anche legacy 8-char.
	if err := b.NFCWhitelist.deleteEntry(uid); err != nil {
		log.Printf("[NFC] delete JSON %s: %v", uid, err)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"tag non trovato"}`)
		return
	}

	fmt.Fprintf(w, `{"uid":%q,"removed":true}`, uid)
}

// POST /api/whitelist/{uid}/toggle — abilita o disabilita un tag senza rimuoverlo.
func (b *DoorPhoneServer) handleWhitelistToggle(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	uid := strings.ToUpper(strings.TrimSpace(
		strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/whitelist/"), "/toggle"),
	))
	if uid == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"UID mancante"}`)
		return
	}

	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"json non valido"}`)
		return
	}

	if err := b.NFCWhitelist.SetDisabled(uid, req.Disabled); err != nil {
		if strings.Contains(err.Error(), "non trovato") {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
		return
	}

	action := "abilitato"
	if req.Disabled {
		action = "disabilitato"
	}
	log.Printf("[NFC] tag %s %s", uid, action)
	fmt.Fprintf(w, `{"ok":true,"uid":%q,"disabled":%v}`, uid, req.Disabled)
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

	// Modello crypto-only: la whitelist vive solo sul server, basta svuotare il JSON.
	if err := b.NFCWhitelist.ClearAll(); err != nil {
		log.Printf("[NFC] errore salvataggio dopo clearall: %v", err)
	}

	log.Printf("[NFC] clearAll: whitelist server svuotata")
	fmt.Fprintf(w, `{"ok":true}`)
}

// sseWrite serializza un SSEEvent e lo scrive nello stream.
func (b *DoorPhoneServer) sseWrite(w http.ResponseWriter, flusher http.Flusher, evt SSEEvent) {
	data, _ := json.Marshal(evt)
	fmt.Fprintf(w, "data: %s\n\n", string(data))
	if flusher != nil {
		flusher.Flush()
	}
}

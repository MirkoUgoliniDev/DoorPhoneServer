# Audio Test Tab — Documentazione Implementazione

**Data:** 2026-04-26  
**Branch:** `BUG-FIX`  
**File modificati:**
- `webpanel.go` — backend Go (route + handler)
- `webpanel_static/panel.html` — struttura HTML del tab
- `webpanel_static/panel.js` — logica JavaScript frontend

---

## 1. Obiettivo

Aggiungere al Web Panel di DoorPhoneServer un tab dedicato al test audio, che permetta all'operatore di:

1. **Caricare** file `.wav` sul server (upload drag & drop o click)
2. **Ascoltare** un file direttamente nel browser (player HTML5)
3. **Inviare** un file `.wav` nel canale Mumble attivo come test (tramite `playIntoStream`)
4. **Eliminare** file caricati non più necessari

---

## 2. Struttura delle directory

I file `.wav` di test vengono salvati in una directory dedicata, separata dagli altri soundfiles del sistema:

```
<dir di doorphoneserver.xml>/
└── soundfiles/
    ├── events/          ← suoni di sistema (preesistenti)
    └── audiotest/       ← directory creata dal feature Audio Test
```

La directory `audiotest/` viene creata automaticamente alla prima richiesta di lista o upload se non esiste, con permessi `0750`.

---

## 3. Backend Go — `webpanel.go`

### 3.1 Registrazione delle route

Aggiunto in `RegisterWebPanelRoutes()` (linea ~70):

```go
mux.HandleFunc("/panel/api/audiotest",          b.handleAudioTestList)
mux.HandleFunc("/panel/api/audiotest/upload",   b.handleAudioTestUpload)
mux.HandleFunc("/panel/api/audiotest/delete",   b.handleAudioTestDelete)
mux.HandleFunc("/panel/api/audiotest/play/",    b.handleAudioTestPlay)
mux.HandleFunc("/panel/api/audiotest/run",      b.handleAudioTestRun)
```

### 3.2 Funzioni helper

#### `audioTestDir() string`
Restituisce il percorso assoluto della directory `audiotest`, costruito relativo alla posizione del file di configurazione XML:
```go
func audioTestDir() string {
    return filepath.Join(filepath.Dir(ConfigXMLFile), "soundfiles", "audiotest")
}
```

#### `audioTestSafePath(name string) (string, error)`
Protegge contro path traversal attack. Validazioni applicate:
- Rifiuta stringhe vuote
- Rifiuta nomi contenenti `/` o `\`
- Rifiuta `.` e `..`
- Verifica che il percorso assoluto risultante sia figlio della directory `audiotest` (prefisso `absDir + os.PathSeparator`)

```go
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
```

### 3.3 Handler: `handleAudioTestList`

**Route:** `GET /panel/api/audiotest`

Legge la directory `audiotest/`, filtra solo i file con estensione `.wav` (case-insensitive), e restituisce un array JSON con nome e dimensione di ciascun file.

**Response:**
```json
[
  {"name": "sirena.wav", "size": 44100},
  {"name": "beep.wav", "size": 8820}
]
```

- Se la directory non esiste, viene creata automaticamente con `os.MkdirAll`
- Se non ci sono file `.wav`, restituisce `null` (array vuoto — gestito lato JS)

### 3.4 Handler: `handleAudioTestUpload`

**Route:** `POST /panel/api/audiotest/upload`  
**Content-Type:** `multipart/form-data`  
**Campo form:** `file`

Validazioni applicate prima di salvare:
1. Metodo deve essere `POST`
2. Dimensione massima: **10 MB** (`10 << 20` byte)
3. Estensione file: solo `.wav` (case-insensitive)
4. Caratteri consentiti nel nome: `a-z A-Z 0-9 - _ .` — rifiuta spazi, accenti, caratteri speciali

Il file viene salvato in `audiotest/<baseName>` usando `os.Create` + `io.Copy`.

**Response (successo):**
```json
{"ok": true, "file": "sirena.wav", "size": 44100}
```

### 3.5 Handler: `handleAudioTestDelete`

**Route:** `POST /panel/api/audiotest/delete`  
**Content-Type:** `application/x-www-form-urlencoded`  
**Parametro:** `name=<nomefile.wav>`

Usa `audioTestSafePath` per validare il percorso, poi chiama `os.Remove`.

**Response (successo):**
```json
{"ok": true}
```

### 3.6 Handler: `handleAudioTestPlay`

**Route:** `GET /panel/api/audiotest/play/<nomefile.wav>`

Serve il file `.wav` direttamente al browser come stream audio.

- Estrae il nome file dal path con `strings.TrimPrefix`
- Valida con `audioTestSafePath`
- Imposta header `Content-Type: audio/wav`
- Usa `http.ServeFile` (supporta range requests, utile per player HTML5)

Questo endpoint viene usato dal player HTML5 nel browser. **Non riproduce nulla sul dispositivo DoorPhoneServer**, serve solo per l'ascolto remoto dall'operatore.

### 3.7 Handler: `handleAudioTestRun`

**Route:** `POST /panel/api/audiotest/run`  
**Content-Type:** `application/x-www-form-urlencoded`  
**Parametro:** `name=<nomefile.wav>`

Invia il file `.wav` nel canale Mumble attivo tramite la pipeline audio esistente di DoorPhoneServer.

**Sequenza di esecuzione:**
```go
IsPlayStream.Store(true)
go b.playIntoStream(abs, 100)
```

- `IsPlayStream` è una `atomic.Bool` (dichiarata in `xmlparser.go`) che abilita la riproduzione
- `playIntoStream` (in `stream.go:275`) usa `gumbleffmpeg` per trasmettere il file nel canale Mumble
- Il volume è fissato a `100` (corrisponde a `vol/100 = 1.0` — volume massimo)
- Viene avviato in goroutine separata: la risposta HTTP è immediata, la riproduzione avviene in background

**Response (successo):**
```json
{"ok": true}
```

#### Dettaglio `playIntoStream` (stream.go:275)

```
playIntoStream(filepath string, vol float32)
├── Controlla IsPlayStream.Load() — se false, ferma immediatamente
├── Cerca eventSound "stream" nella configurazione
│   ├── Se enabled:
│   │   ├── Ferma eventuale stream in corso
│   │   ├── Crea nuovo gumbleffmpeg con SourceFile(filepath) e vol/100
│   │   ├── Chiama p.Play()
│   │   ├── Attende il completamento con p.Wait()
│   │   └── Chiama p.Stop()
│   └── Se disabled: log "Sound Disabled by Config"
```

**Nota:** `playIntoStream` richiede che l'evento `"stream"` sia abilitato nella configurazione XML. Se disabilitato, la riproduzione non avviene e viene loggato un warning.

---

## 4. Frontend HTML — `panel.html`

### 4.1 Tab navigation

Aggiunto il tab nella barra di navigazione:
```html
<div class="tab" data-page="audiotest">Audio Test</div>
```

### 4.2 Struttura della pagina

```html
<!-- AUDIO TEST -->
<div class="page" id="page-audiotest">

  <!-- Card 1: Upload -->
  <div class="card" style="margin-bottom:16px">
    <h2>Upload File Audio (.wav)</h2>
    <div class="drop-zone" id="atDropZone" onclick="...">
      Drop files here or click to browse
      <small>Max 10MB — solo .wav</small>
    </div>
    <input type="file" id="atFileInput" accept=".wav" style="display:none" onchange="uploadAudioTest(this.files[0])">
  </div>

  <!-- Card 2: Lista file + Player -->
  <div class="card">
    <h2>File Audio Test</h2>

    <!-- Player box (nascosto di default) -->
    <div id="atPlayerBox" style="display:none">
      <span id="atNowPlaying"></span>
      <button onclick="atStopAudio()">Stop</button>
      <audio id="atAudioPlayer" controls></audio>
    </div>

    <!-- Lista file .wav -->
    <ul class="file-list" id="atFileList"></ul>
  </div>

</div>
```

**Elementi chiave:**

| ID elemento    | Tipo         | Scopo                                      |
|----------------|--------------|--------------------------------------------|
| `atDropZone`   | `div`        | Area drag & drop / click per upload        |
| `atFileInput`  | `input[file]`| Input file nascosto, attivato da atDropZone|
| `atPlayerBox`  | `div`        | Contenitore player, visibile solo durante riproduzione |
| `atNowPlaying` | `span`       | Mostra il nome del file in riproduzione    |
| `atAudioPlayer`| `audio`      | Player HTML5 nativo con controlli          |
| `atFileList`   | `ul`         | Lista dinamica dei file caricati           |

---

## 5. Frontend JavaScript — `panel.js`

### 5.1 Trigger al cambio tab

```javascript
if(t.dataset.page === 'audiotest') loadAudioTestFiles();
```

Quando l'utente clicca sul tab "Audio Test", viene chiamata automaticamente `loadAudioTestFiles()` per caricare la lista aggiornata.

### 5.2 `loadAudioTestFiles()`

Chiama `GET /panel/api/audiotest`, poi costruisce dinamicamente la lista `<ul id="atFileList">`.

Per ogni file genera un `<li>` con:
- Icona speaker (`&#128266;`)
- Nome file
- Dimensione formattata con `fmtBytes()`
- 3 pulsanti:
  - **Play browser** (blu, `bi-play-fill`) → `atPlayBrowser(name)`
  - **Invia su Mumble** (viola `#7c3aed`, `bi-broadcast`) → `atRunTest(name)`
  - **Elimina** (rosso, `bi-trash3-fill`) → `atDeleteFile(name)`

Se la lista è vuota o `null`: mostra `"Nessun file .wav trovato"`.

### 5.3 `uploadAudioTest(file)`

Chiamata sia da click (tramite `atFileInput.onchange`) che da drag & drop.

**Validazioni client-side (pre-fetch):**
1. Estensione `.wav` — toast di errore se diversa
2. Dimensione ≤ 10 MB — toast di errore se superiore

**Flusso:**
1. Crea `FormData` con il file
2. Mostra `"Uploading..."` nella drop zone
3. `POST /panel/api/audiotest/upload`
4. Successo → toast verde + `loadAudioTestFiles()` + ripristino testo drop zone
5. Errore → toast rosso + ripristino testo drop zone

### 5.4 `atPlayBrowser(name)`

Riproduce il file direttamente nel browser dell'operatore:
1. Imposta `atNowPlaying` con il nome del file
2. Imposta `atAudioPlayer.src = '/panel/api/audiotest/play/' + encodeURIComponent(name)`
3. Rende visibile `atPlayerBox`
4. Chiama `player.play()`

### 5.5 `atStopAudio()`

Ferma la riproduzione browser:
1. `player.pause()`
2. `player.src = ''` (libera la risorsa)
3. Nasconde `atPlayerBox`

### 5.6 `atRunTest(name)`

Invia il file nel canale Mumble come test, con conferma modale:
1. Apre `confirmModal('Avvia Test Audio', ...)` con bottone "Avvia"
2. Se confermato: `POST /panel/api/audiotest/run` con `name=<filename>`
3. Successo → toast `"Test avviato: <name>"`
4. Errore → toast rosso

### 5.7 `atDeleteFile(name)`

Elimina un file, con conferma modale:
1. Apre `confirmModal('Elimina File', ...)` con bottone "Elimina" (danger)
2. Se confermato: `POST /panel/api/audiotest/delete` con `name=<filename>`
3. Successo → toast `"Eliminato: <name>"` + `loadAudioTestFiles()`
4. Errore → toast rosso

### 5.8 Setup drag & drop (IIFE)

```javascript
(function(){
  const dz = document.getElementById('atDropZone');
  if(!dz) return;
  dz.addEventListener('dragover',  e => { e.preventDefault(); dz.classList.add('dragover'); });
  dz.addEventListener('dragleave', () => dz.classList.remove('dragover'));
  dz.addEventListener('drop',      e => {
    e.preventDefault();
    dz.classList.remove('dragover');
    if(e.dataTransfer.files.length) uploadAudioTest(e.dataTransfer.files[0]);
  });
})();
```

- IIFE eseguita all'avvio dello script (il DOM è già caricato perché lo script è in fondo al body)
- `dragover` + `dragleave`: feedback visivo via classe CSS `dragover`
- `drop`: passa il primo file a `uploadAudioTest()`
- Il guard `if(!dz) return` previene errori se l'elemento non fosse trovato

---

## 6. Tabella riepilogativa delle API

| Metodo | Route                               | Descrizione                              | Parametri                  | Response                         |
|--------|-------------------------------------|------------------------------------------|----------------------------|----------------------------------|
| GET    | `/panel/api/audiotest`              | Lista file `.wav` presenti               | —                          | `[{name, size}]`                 |
| POST   | `/panel/api/audiotest/upload`       | Upload file `.wav`                       | `multipart: file`          | `{ok, file, size}`               |
| POST   | `/panel/api/audiotest/delete`       | Elimina file                             | `form: name`               | `{ok}`                           |
| GET    | `/panel/api/audiotest/play/<name>`  | Serve file al browser (stream HTTP)      | path param: nome file      | `audio/wav` stream               |
| POST   | `/panel/api/audiotest/run`          | Riproduce file nel canale Mumble         | `form: name`               | `{ok}`                           |

---

## 7. Sicurezza

| Minaccia                | Contromisura implementata                                                              |
|-------------------------|----------------------------------------------------------------------------------------|
| Path traversal          | `audioTestSafePath()`: rifiuta `/`, `\`, `.`, `..`; verifica prefisso assoluto         |
| Upload di file malevoli | Solo estensione `.wav` ammessa (server-side); caratteri nome file validati con whitelist|
| File di grandi dimensioni | Limite `10 << 20` (10 MB) su `ParseMultipartForm`; check client-side in JS             |
| Directory listing       | Handler lista solo file `.wav`, ignora directory e altri tipi                          |
| Header security         | `panelSecurityHeaders(w)` su tutti gli handler (già presente nel pattern del pannello) |

---

## 8. Dipendenze interne

| Simbolo               | File sorgente          | Tipo          | Note                                           |
|-----------------------|------------------------|---------------|------------------------------------------------|
| `playIntoStream`      | `stream.go:275`        | Method        | Usa `gumbleffmpeg` per trasmettere su Mumble   |
| `IsPlayStream`        | `xmlparser.go:364`     | `atomic.Bool` | Flag globale che abilita/disabilita lo stream  |
| `ConfigXMLFile`       | (globale)              | `string`      | Path del file XML di configurazione            |
| `panelSecurityHeaders`| `webpanel.go`          | Function      | Imposta header HTTP di sicurezza               |
| `confirmModal`        | `panel.js`             | Function JS   | Modale di conferma riutilizzabile              |
| `toastCenter`         | `panel.js`             | Function JS   | Toast notification center-screen              |
| `fmtBytes`            | `panel.js`             | Function JS   | Formatta byte in KB/MB                         |

---

## 9. Note operative

- **Evento "stream" nella config XML:** `playIntoStream` richiede che l'evento `"stream"` sia abilitato in `doorphoneserver.xml`. Se disabilitato, il test non produce audio su Mumble e viene loggato `warn: Sound Disabled by Config`.
- **Volume fisso:** Il test audio viene inviato sempre a volume `100` (massimo). Non è esposto un controllo volume nell'UI.
- **Riproduzione concorrente:** Se è già in corso una trasmissione stream, `playIntoStream` ferma quella in corso e avvia la nuova (comportamento ereditato dalla logica esistente in `stream.go`).
- **Player browser vs Mumble:** Sono due funzioni indipendenti. Il player browser (`atPlayBrowser`) usa `http.ServeFile` lato server e `<audio>` HTML5 lato client; non coinvolge in alcun modo Mumble o gumbleffmpeg.
- **Persistenza dei file:** I file caricati rimangono su disco finché non vengono eliminati manualmente dall'operatore tramite UI o filesystem. Non c'è TTL o pulizia automatica.

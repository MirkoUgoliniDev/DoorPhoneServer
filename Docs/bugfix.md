# DoorPhoneServer — Bugfix & Security Hardening Plan

## Panoramica

DoorPhoneServer è un client Mumble VoIP per Raspberry Pi scritto in Go 1.23, con controllo GPIO, HTTP API, MQTT, push notifications e TTS.  
Questa analisi è stata condotta il **14 Marzo 2026** sul branch `Bugfix`.

Il codice originale (pre-hardening) è stato salvato sul branch `main`.

---

## Criticità Trovate: 28 totali

| Gravità | Conteggio |
|---------|-----------|
| CRITICO — Sicurezza | 8 |
| CRITICO — Stabilità | 5 |
| ALTO — Architettura | 5 |
| MEDIO — Qualità Codice | 10 |

---

## CRITICO — Sicurezza

### 1. HTTP API senza autenticazione
- **File:** `httpapi.go` (riga 97+)
- **Problema:** Comandi come `unlockdoor`, `reboot_server`, `setdevicestatus` sono esposti senza alcuna autenticazione. È presente solo un rate limiter a 10 req/s.
- **Rischio:** Qualsiasi dispositivo sulla stessa rete può sbloccare porte, riavviare il server, controllare dispositivi IoT.
- **Fix:** Aggiungere autenticazione Bearer token / API key. Aggiungere security headers HTTP (CSP, X-Content-Type-Options, X-Frame-Options).

### 2. TLS disabilitata su MQTT
- **File:** `mqtt.go` (riga 34)
- **Codice:** `tlsConfig := &tls.Config{InsecureSkipVerify: true, ClientAuth: tls.NoClientCert}`
- **Rischio:** Attacchi Man-in-the-Middle possibili su tutte le connessioni MQTT.
- **Fix:** Rimuovere `InsecureSkipVerify: true`. Caricare certificati CA dal config XML.

### 3. Password MQTT loggata in chiaro
- **File:** `mqtt.go` (riga 22)
- **Codice:** `log.Printf("debug: MQTT password    : %s\n", ...MQTTPassword)`
- **Rischio:** Credenziali esposte nei log.
- **Fix:** Sostituire con `log.Printf("debug: MQTT password    : [REDACTED]")`.

### 4. Credenziali in chiaro nel config XML
- **File:** `doorphoneserver.xml`
- **Problema:** Password Mumble, token Pushover, chiave FCM, credenziali MQTT tutti in plaintext.
- **Fix:** Documentare che il file XML deve avere permessi `0600`. Il `.gitignore` già lo esclude dal repo.

### 5. Command injection
- **File:** `utils.go` (riga 226)
- **Codice:** `exec.Command("/bin/sh", "-c", "command -v "+name)`
- **Rischio:** Se `name` contiene caratteri speciali shell (`;`, `|`, `` ` ``), permette esecuzione di codice arbitrario.
- **Fix:** Sostituire con `exec.LookPath(name)` che è nativo Go e non invoca la shell.

### 6. Nessun HTTPS sull'HTTP API
- **File:** `client.go` (riga 181)
- **Codice:** `http.ListenAndServe(":"+...ListenPort, nil)`
- **Rischio:** Tutti i comandi API e le risposte viaggiano in chiaro sulla rete.
- **Fix:** Supportare TLS con `http.ListenAndServeTLS()` se certificati configurati nel XML.

### 7. Rischio XXE (XML External Entity)
- **File:** `xmlparser.go` (riga 388-406)
- **Codice:** `xml.Unmarshal(byteValue, &Config)` senza protezione da entità esterne.
- **Rischio:** Se il file config è compromesso: SSRF, information disclosure, DoS.
- **Fix:** Usare `xml.NewDecoder()` con `d.Strict = true`. Nota: Go `encoding/xml` non supporta DTD/entity expansion, quindi il rischio reale è limitato, ma è buona pratica usare il decoder.

### 8. HTTP per Google TTS
- **File:** `htgotts.go`, `utils.go`
- **Problema:** Richieste a Google Translate via `http://` invece di `https://`.
- **Rischio:** Audio TTS intercettabile in transito.
- **Fix:** Cambiare URL da `http://translate.google.com/...` a `https://translate.google.com/...`.

---

## CRITICO — Stabilità

### 9. panic() in MQTT
- **File:** `mqtt.go` (righe 39 e 44)
- **Codice:**
  ```go
  // riga 39 (OnConnect callback):
  panic(token.Error())
  // riga 44 (Connect):
  panic(token.Error())
  ```
- **Rischio:** L'intera applicazione crasha su qualsiasi errore MQTT.
- **Fix:** Sostituire `panic()` con `log.Printf("error: ...")` e `return`.

### 10. Race condition su variabili globali
- **File:** `xmlparser.go` (variabili globali), `client.go`, `gpio.go`, `onevent.go`, `clientcommands.go`
- **Variabili coinvolte:** `IsConnected`, `IsPlayStream`, `NowStreaming`, `IsTransmitting`, `KillHeartBeat`, `HTTPServRunning`
- **Rischio:** Data race, comportamento imprevedibile, crash intermittenti.
- **Fix:** Convertire le variabili booleane globali in `atomic.Bool` (Go 1.19+) oppure proteggere con `sync.Mutex`.

### 11. Mappa non thread-safe
- **File:** `xmlparser.go` (dichiarazione), `stream.go` (uso)
- **Codice:** `var StreamTracker = map[uint32]streamTrackerStruct{}`
- **Rischio:** Accesso concorrente da `OnAudioStream()` (goroutine) e `goStreamStats()` → crash runtime.
- **Fix:** Proteggere con `sync.RWMutex` o usare `sync.Map`.

### 12. Bug array bounds in MQTT
- **File:** `mqtt.go` (righe ~113-135)
- **Codice:**
  ```go
  if len(Command) == 3 {
      if Command[2] == "pulse" {
          _, Err = b.Call(funcs, mqttcommand.Action, "pulse", Command[3]) // BUG!
      }
  }
  ```
- **Rischio:** `len(Command) == 3` significa indici 0, 1, 2. `Command[3]` **non esiste** → panic garantito a runtime.
- **Fix:** Cambiare la condizione in `len(Command) == 4` oppure usare `Command[2]` per il parametro. Analizzare la logica attesa del protocollo "relay:1:pulse" vs "relay:1:pulse:duration".

### 13. Bug GPIO — rpio.Close() prima di NewInput()
- **File:** `gpio.go` (righe 129-132)
- **Codice:**
  ```go
  if P1ButtonUsed || P2ButtonUsed || P3ButtonUsed || PirButtonUsed {
      rpio.Close()  // ← chiude il driver GPIO
  }
  // Subito dopo:
  P1Button = gpio.NewInput(P1ButtonPin)  // ← usa GPIO dopo Close!
  ```
- **Rischio:** Tutti i GPIO input sono non funzionanti dopo l'inizializzazione.
- **Fix:** Rimuovere la chiamata `rpio.Close()` in quel punto. Spostare `rpio.Close()` nel cleanup dell'applicazione.

---

## ALTO — Architettura

### 14. Stato globale pervasivo
- **File:** `xmlparser.go` (righe 330-388)
- **Problema:** Centinaia di variabili globali mutabili (`Config`, `Name[]`, `Server[]`, `Password[]`, `IsConnected`, etc.). Nessuna encapsulation, nessuna dependency injection.
- **Rischio:** Impossibile testare, difficile diagnosticare bug, rende ogni goroutine potenzialmente pericolosa.
- **Fix:** (Lungo termine) Raggruppare le variabili in strutture con metodi protetti da mutex. Per ora, proteggere almeno le variabili booleane con atomic.

### 15. Goroutine leak
- **File:** `client.go` (riga 179 — HTTP server), `stream.go` (riga 103 — audio), `mqtt.go` (riga 52 — subscription)
- **Problema:** Nessun meccanismo di shutdown per le goroutine avviate. Si accumulano senza mai terminare.
- **Fix:** Usare `context.Context` con cancellation per gestire il lifecycle delle goroutine. L'HTTP server deve usare `http.Server` con `Shutdown()`.

### 16. Nessuna riconnessione MQTT
- **File:** `mqtt.go` (riga 52)
- **Codice:** `<-c` — blocca per sempre in attesa di un segnale OS.
- **Fix:** Aggiungere `SetAutoReconnect(true)` e `SetConnectionLostHandler()` nelle opzioni MQTT client.

### 17. reflect.Call senza validazione
- **File:** `crontab.go` (riga 31)
- **Codice:**
  ```go
  meth := reflect.ValueOf(m).MethodByName(fname)
  meth.Call(nil)  // panic se fname non esiste!
  ```
- **Fix:** Verificare `meth.IsValid()` prima di `.Call()`.

### 18. Ricorsione senza limite di profondità
- **File:** `clientcommands.go` (riga 347)
- **Codice:** `b.Scan()` chiama sé stessa ricorsivamente senza depth limit.
- **Rischio:** Stack overflow possibile con molti canali.
- **Fix:** Convertire in loop iterativo o aggiungere un parametro `maxDepth`.

---

## MEDIO — Qualità Codice

### 19. Versione commentata
- **File:** `version.go`
- **Problema:** L'intero contenuto è commentato. Nessun version tracking attivo. Ultima data: 2022.
- **Fix:** Decommentare e aggiornare la versione.

### 20. ioutil.ReadAll deprecato
- **File:** `xmlparser.go` (riga 400), `sonoff.go`
- **Problema:** `ioutil.ReadAll` è deprecato da Go 1.16.
- **Fix:** Sostituire con `io.ReadAll()`.

### 21. HTTP client senza timeout
- **File:** `custom_func.go` (riga 32)
- **Codice:** `client := http.Client{}`
- **Rischio:** Richieste HTTP che possono bloccare per sempre.
- **Fix:** Aggiungere `Timeout: 30 * time.Second`.

### 22. IP hardcoded
- **File:** `custom_func.go` (riga 14), `pushnotification_pushover.go` (riga 35), `sunrise_sunset.go` (riga 29)
- **Problema:** `192.168.1.54:8081` e coordinate GPS (Roma, 41.9028, 12.4964) sono hardcoded.
- **Fix:** Spostare nel config XML.

### 23. Config path hardcoded
- **File:** `cmd/doorphoneserver/main.go` (riga 19)
- **Problema:** `/home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/doorphoneserver.xml` hardcoded.
- **Fix:** Già gestito tramite flag `-config`, ma il default dovrebbe essere relativo.

### 24. Errori ignorati silenziosamente
- **File:** `commandkeys.go` (riga 54), `custom_func.go` (riga 137), `gpio.go` (riga 324)
- **Problema:** Errori loggati ma non propagati. L'operazione continua come se nulla fosse.
- **Fix:** Propagare gli errori o gestirli esplicitamente.

### 25. Dead code
- **File:** `version.go` (tutto commentato), `media.go` (`aplayLocal()` commentata), `crontab.go` (`test()`, `Func1()`, `Func2()` inutilizzati)
- **Fix:** Rimuovere il codice morto o decommentare se necessario.

### 26. Magic numbers
- **Dove:** Debounce 150ms (`gpio.go` riga 149), buffer audio 24 (`stream.go` riga 114), timeout 5s/10s (`custom_func.go`)
- **Fix:** Definire come costanti con nomi descrittivi o rendere configurabili nel XML.

### 27. Build script con sudo killall -9
- **File:** `tkbuild.sh`
- **Codice:** `sudo killall -q -s 9 doorphoneserver`
- **Rischio:** Nessun graceful shutdown. Richiede privilegi elevati.
- **Fix:** Usare `sudo systemctl restart doorphoneserver` o SIGTERM invece di SIGKILL.

### 28. Nessun security header HTTP
- **File:** `httpapi.go`
- **Problema:** Le risposte HTTP non contengono security headers.
- **Fix:** Aggiungere headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Content-Security-Policy`.

---

## Ordine di Esecuzione delle Fix

| Priorità | Task | File |
|----------|------|------|
| 1 | Rimuovere panic() MQTT, fix array bounds bug | `mqtt.go` |
| 2 | Aggiungere autenticazione HTTP API + security headers | `httpapi.go` |
| 3 | Eliminare command injection | `utils.go` |
| 4 | Proteggere StreamTracker con sync.RWMutex | `stream.go`, `xmlparser.go` |
| 5 | Fix rpio.Close() bug GPIO | `gpio.go` |
| 6 | Sostituire ioutil deprecato, usare xml decoder | `xmlparser.go` |
| 7 | HTTP server con graceful shutdown | `client.go` |
| 8 | Validare reflect.Call in crontab | `crontab.go` |
| 9 | Aggiungere timeout HTTP client | `custom_func.go` |
| 10 | Rimuovere logging password, fix TLS | `mqtt.go` |

---

## Note

- Il file `doorphoneserver.xml` è escluso dal repo via `.gitignore` per proteggere le credenziali.
- Il branch `main` contiene il codice originale non modificato.
- Tutte le fix verranno applicate sul branch `Bugfix`.
- Dopo ogni fix, il progetto verrà compilato per verificare che non ci siano regressioni.

---

## Stato Avanzamento Fix (aggiornato 15 Marzo 2026)

### Fix Completate (19/28)

| # | Descrizione | File |
|---|-------------|------|
| 7 | HTTP graceful shutdown | `client.go` |
| 8 | HTTP→HTTPS per Google TTS | `utils.go` |
| 9 | Rimosso panic() MQTT | `mqtt.go` |
| 10 | Race condition → atomic.Bool | `xmlparser.go`, `client.go`, `clientcommands.go`, `onevent.go`, `commandkeys.go`, `gpio.go`, `stream.go`, `htgotts.go` |
| 11 | StreamTracker thread-safe | `xmlparser.go`, `stream.go` |
| 12 | MQTT array bounds bug | `mqtt.go` |
| 13 | GPIO rpio.Close() bug | `gpio.go` |
| 15 | Goroutine leak → context.Context | `globalcontext.go`, `client.go`, `mqtt.go`, `gpio.go`, `cmd/doorphoneserver/main.go` |
| 16 | MQTT auto-reconnect | `mqtt.go` |
| 17 | reflect.Call validazione | `crontab.go` |
| 18 | Scan() ricorsione → iterativo | `clientcommands.go` |
| 19 | Versione decommentata/aggiornata | `version.go` |
| 20 | ioutil.ReadAll → io.ReadAll | `xmlparser.go`, `sonoff.go` |
| 21 | HTTP client timeout 30s | `custom_func.go` |
| 23 | Config path relativo | `cmd/doorphoneserver/main.go` |
| 24 | Errori propagati correttamente | `commandkeys.go` |
| 26 | Magic numbers → costanti | `gpio.go`, `stream.go`, `custom_func.go` |
| 27 | Build script SIGTERM | `tkbuild.sh` |
| 28 | Security headers HTTP | `httpapi.go` |

### Fix Da Fare (9/28)

| # | Descrizione | File | Gravità |
|---|-------------|------|---------|
| 1 | HTTP API autenticazione Bearer token | `httpapi.go` | CRITICO |
| 2 | TLS su MQTT (rimuovere InsecureSkipVerify) | `mqtt.go` | CRITICO |
| 3 | Password MQTT redacted nei log | `mqtt.go` | CRITICO |
| 4 | Documentare permessi 0600 su doorphoneserver.xml | documentazione | CRITICO |
| 5 | Command injection → exec.LookPath | `utils.go` | CRITICO |
| 6 | HTTPS sull'HTTP API (ListenAndServeTLS) | `client.go` | CRITICO |
| 14 | Stato globale pervasivo (lungo termine) | `xmlparser.go` | ALTO |
| 22 | IP hardcoded → config XML | `custom_func.go`, `pushnotification_pushover.go`, `sunrise_sunset.go` | MEDIO |
| 25 | Dead code da rimuovere | `media.go`, `crontab.go` | MEDIO |

---

## Sessione 6 Aprile 2026 — Web Panel Improvements

### Problemi risolti

#### A. Riproduzione suono dal Web Panel non funzionante
- **Problema:** Il pulsante "Play" nel pannello Sound non produceva audio sullo speaker.
- **Causa:** `handleSoundPlayPi` eseguiva `ffplay` in una goroutine fire-and-forget senza output. Inoltre, il mixer ALSA `Headphone Playback Switch` era impostato su `off` (mute). I pulsanti GPIO P1/P2/P3 chiamavano `cmdMuteUnmute("unmute")` prima della riproduzione, ma il web panel no.
- **Fix (webpanel.go — `handleSoundPlayPi`):**
  - Esecuzione sincrona con `context.WithTimeout(15s)` e `CombinedOutput()` per catturare l'output
  - Aggiunto `amixer -c 0 sset Headphone unmute` prima di `ffplay`
  - Flag `-loglevel warning` per ridurre output verboso
  - Risposta JSON con campi `ok`, `output`, `error`, `cmd` + tempo di esecuzione

#### B. Modale diagnostica per output ffplay
- **Problema:** Utente richiedeva di vedere l'output del comando ffplay durante la riproduzione.
- **Fix (panel.js — `playSoundPi`):**
  - Riscritto per aprire `sudoModal` mostrando il comando eseguito
  - In attesa della risposta, visualizza l'output con colorazione successo/errore

#### C. Pulsante Reboot non funzionante
- **Problema:** `sudo reboot` falliva perché il file sudoers autorizza solo `/bin/systemctl`.
- **Fix (custom_func.go — `cmdRebootServer`):** Cambiato `exec.Command("sudo", "reboot")` → `exec.Command("sudo", "systemctl", "reboot")`

#### D. Pulsante "Elimina Tutte" per le snapshot
- **Nuovo endpoint (webpanel.go):** `handleSnapshotDeleteAll` — elimina tutte le snapshot, opzionalmente filtrate per piano (parametro `floor`)
- **Nuova route:** `/panel/api/snapshots/deleteall`
- **JS (panel.js — `deleteAllSnapshots`):** Modale di conferma con avviso, invio POST con floor filter opzionale
- **HTML (panel.html):** Aggiunto pulsante `<button id="btnDeleteAll">` con `onclick="deleteAllSnapshots()"`

#### E. Stile pulsanti filtro piano attivo
- **Problema:** I pulsanti piano (Tutti, P1, P2, P3, P4) non mostravano lo stato attivo visivamente, nonostante il JS togliasse già la classe `active`.
- **Fix (panel.css):** Aggiunte regole `.floor-btn` e `.floor-btn.active` (sfondo accent, colore scuro, bordo, font-weight bold)

#### F. Testo dinamico pulsante "Elimina"
- **Fix (panel.js — `loadSnapshots`):** Il testo di `btnDeleteAll` si aggiorna dinamicamente ("Elimina Tutte" / "Elimina P1" / "Elimina P3" ecc.) in base al filtro piano selezionato

#### G. Rimosso badge P1/P2/P3 dalle card snapshot
- **Problema:** Ogni card mostrava un badge colorato (P1/P2/P3) prima del nome file — ridondante con il filtro piano.
- **Fix (panel.js — `loadSnapshots`):** Rimossa variabile `badge` e il relativo `<span>` dal rendering delle card

#### H. Conferma eliminazione nella modale invece del toast
- **Problema:** Dopo eliminazione singola, il messaggio "Deleted ..." appariva come toast verde in basso. Utente voleva la conferma nella stessa modale.
- **Fix (panel.js — `deleteSnapshot`):**
  - Dopo click "Delete", la modale resta aperta e mostra "Eliminazione in corso…"
  - Successo: icona ✅, titolo "Eliminata", messaggio con nome file, pulsante "Chiudi"
  - Errore: icona ❌, titolo "Errore", dettaglio errore, pulsante "Chiudi"
- **Fix (panel.js — `confirmModal`):** Aggiunto reset di `okBtn.style.display`, `cancelBtn.style.display` e `cancelBtn.textContent` all'apertura, per evitare che lo stato residuo della modale precedente impedisse il corretto funzionamento nelle eliminazioni successive

### File modificati in questa sessione
| File | Modifiche |
|------|-----------|
| `webpanel.go` | `handleSoundPlayPi` (sync, amixer unmute, output JSON), `handleSnapshotDeleteAll` (nuovo) |
| `webpanel_static/panel.js` | `playSoundPi` (modale output), `deleteSnapshot` (conferma in modale), `deleteAllSnapshots` (nuovo), `loadSnapshots` (testo dinamico, no badge), `confirmModal` (reset stato) |
| `webpanel_static/panel.html` | Pulsante `btnDeleteAll` |
| `webpanel_static/panel.css` | Regole `.floor-btn` e `.floor-btn.active` |
| `custom_func.go` | `cmdRebootServer` → `sudo systemctl reboot` |

### Commit
- `9f30dcd` — Web panel improvements: modal delete confirmation, sound playback fix, snapshot management, UI polish

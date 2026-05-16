# Analisi Client Android (Mumla) — Flusso Audio Citofono

Analisi del codice Android in `C:\Lavori\Android\mumlaO` per capire il flusso
audio completo citofono → appartamento e viceversa.

---

## 1. Flusso di Connessione Mumble

- **Avvio:** `BootReceiver.java` lancia l'app al boot; `MumlaActivity` fa binding a `MumlaService` con `BIND_AUTO_CREATE`
- **Server:** `Settings.java:200/218` — host `192.168.1.54`, porta `64738` (default); aggiornati dinamicamente da `RaspberryConfigFetcher.java` via HTTP `192.168.1.54:8080/config/`
- **Riconnessione automatica:** Sì — `MumlaService.java:149` registra `NetworkCallback`; ritardo `RECONNECT_DELAY = 10000ms`; `Settings.PREF_AUTO_RECONNECT = true` di default
- **Canale di default:** Root Channel — nessun `joinChannel()` esplicito, l'utente rimane sempre nel root

---

## 2. Meccanismo del Ring (Citofono → Appartamento)

- **Trasporto:** Mumble text message — il citofono invia un messaggio testuale Mumble con payload `"cmd-ring"`
- **Parser:** `MumlaService.java:305-353` — `onMessageLogged()` estrae il prefisso `"cmd-"` e chiama `Ring()`
- **Azione:** accende schermo (`wakeupScreen()`), lancia `VideoVLCActivity` con extra `EXTRA_RING=true`, riproduce suono campanello
- **Ridondanza:** secondo broadcast dopo 1500ms per coprire il caso "schermo spento durante wakeup"
- **Timeout:** 50 secondi — `VideoVLCActivity.java:815` con `handler_ring_timeout.postDelayed(..., 50000)`

---

## 3. Flusso Audio TX (Appartamento → Citofono)

- **Modalità PTT:** configurabile tra VAD (default), Push-to-Talk e Continuous — `Settings.java:41-48`
  - VAD usa energy threshold su frequenze audio (`ActivityInputMode.java:24-64`)
  - PTT riceve broadcast `BROADCAST_TALK` via `TalkBroadcastReceiver`
- **AudioRecord:** `AudioInput.java:43-68` — MONO, PCM 16bit
- **Codec:** Opus — 48000 Hz, 480 campioni/frame (10ms), default 40kbps, 2 frame/pacchetto (20ms)
- **Destinatario:** broadcast a **tutto il canale**, non solo al citofono

---

## 4. Flusso Audio RX (Citofono → Appartamento)

- **Riproduzione:** `AudioOutput.java:72-95` — `AudioTrack` in `MODE_STREAM`, 48000 Hz MONO
- **Background:** Sì — `MumlaService` è foreground service, l'audio continua anche con Activity stoppata
- **Self-mute:**
  - `openCall()` → `setSelfMuteDeafState(false, false)` (demute quando si risponde)
  - `closeCall()` / timeout → `setSelfMuteDeafState(true, true)` (remute dopo la chiamata)
  - Stato persistito in `SharedPreferences` (`Settings.java:500-505`)

---

## 5. Targeting per Piano (P1, P2, P3)

- **Unico canale:** tutti i piani sono nel Root Channel — non esistono canali separati per piano
- **`PREF_DOORPI_PIANO`** (`Settings.java:171-172`): usato solo come label nell'ActionBar, **non per routing**
- **Ring broadcast:** arriva a **tutti** gli utenti connessi nel canale — nessun indirizzamento selettivo
- **Nessun cambio canale automatico:** l'utente rimane sempre nel Root Channel

---

## 6. Gestione dello Stato Self-Muted

- **Alla connessione:** ripristina l'ultimo stato salvato in preferenze (`MumlaService.java:538-539`); il default factory è `false` (non mutato)
- **Al ring accettato:** demute automatico quando si preme CALL (`VideoVLCActivity.java:1298` → `mService.unmute()`)
- **Alla fine chiamata:** remute automatico (timeout 50s o pressione REJECT/END)

---

## 7. File/Classi Analizzati

| File | Cosa contiene |
|------|---------------|
| `BootReceiver.java` | Avvio app al boot |
| `MumlaService.java` | Servizio Mumble background, connessione, ring parser |
| `MumlaActivity.java` | Binding al service |
| `AudioOutput.java` | Riproduzione audio RX via AudioTrack |
| `AudioInput.java` | Cattura audio TX, MONO PCM 16bit |
| `ActivityInputMode.java` | VAD energy threshold |
| `VideoVLCActivity.java` | Schermata chiamata, timeout 50s |
| `Settings.java` | Config server, PTT mode, piano label |
| `RaspberryConfigFetcher.java` | Fetch config dinamica da HTTP |

---

## 8. Risposte alle Domande Chiave (Aggiornate)

| Domanda | Risposta |
|---------|----------|
| I piani sentono sempre il citofono? | **No** — in stato di riposo sono **muto+sordo** (self-muted + self-deafened): non sentono l'audio ma ricevono i messaggi testuali |
| Il ring è targeted o broadcast? | **Dipende:** se DoorPi invia il `cmd-ring` come **messaggio privato** → solo il piano target squilla; se inviato al canale → tutti squillano |
| Self-mute è automatico? | **Sì** — demute+undeaf automatico su risposta, remute+deaf automatico su fine/timeout (50s) |
| Il canale audio si apre solo su ring? | **Sì** — sia TX che RX sono inibiti finché il piano è sordo; si attivano solo alla risposta |

---

## 9. Implicazioni per il TestClient Go

**Scoperta chiave:** in stato di riposo i piani sono **muto+sordo**, non solo muti.
Questo cambia completamente le implicazioni per i test:

| Scenario | Impatto sui piani |
|----------|-------------------|
| TestClient trasmette audio nel canale | **Nessun disturbo** — i piani sono sordi, non sentono nulla |
| TestClient invia `cmd-ring` come messaggio privato a un piano | Solo quel piano squilla ✅ |
| TestClient invia `cmd-ring` al canale | Tutti i piani squillano ⚠️ |

**Conclusione:** il TestClient Go può trasmette audio nel Root Channel **senza disturbare nessuno** finché i piani sono in stato di riposo (sordi). Non serve un canale separato per i test audio.

Per testare il ring in modo selettivo, usare il **messaggio privato Mumble** indirizzato al solo piano target.

**Codec compatibilità:** il TestClient Go deve usare **Opus 48000 Hz, frame 480 campioni (10ms)** per essere compatibile con i client Android.

---

*Analisi completata — Claude Code sessione Raspberry Pi + Windows*

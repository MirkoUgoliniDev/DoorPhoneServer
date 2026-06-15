# Protocollo USB CDC — DoorPhoneServer ↔ ESP32-S3

Documento di riferimento del protocollo testuale scambiato tra il Raspberry Pi (Go) e i due ESP32-S3 via USB CDC.

---

## 1. Generalita del canale

| Parametro | Valore |
|-----------|--------|
| Baud rate | 115200 |
| Data bits | 8 |
| Parity | None |
| Stop bits | 1 |
| Flow control | Nessuno |
| Encoding | ASCII a 7 bit |
| Terminatore | `\n` (LF, 0x0A) |
| CR opzionale | `\r` prima di `\n` accettato e ignorato |

Ogni messaggio e una riga ASCII terminata da `\n`. Un messaggio non puo contenere `\n` al suo interno. Il Pi non usa symlink fissi: apre i device `/dev/ttyACM*` e determina dinamicamente il ruolo di ciascuno tramite il protocollo `GET-ROLE`/`HELLO` (vedi sezione 2). La regola udev assegna solo i permessi (gruppo `dialout`), non un nome specifico.

Entrambi i bridge USB sono istanze indipendenti dello stesso `USBBridge` Go. Non esiste comunicazione diretta tra ESP32-A ed ESP32-B: il Pi fa da intermediario.

---

## 2. Auto-identificazione e scoperta della porta

Il Pi non usa symlink fissi per identificare le schede. Al collegamento di qualsiasi device seriale USB, il Pi invia `GET-ROLE\n` e attende la risposta per determinare il ruolo della scheda.

### Sequenza di scoperta

```
Pi   →  GET-ROLE\n
ESP32 ← HELLO RFID\n      (se e ESP32-A)
ESP32 ← HELLO RELAY\n     (se e ESP32-B)
```

Il Pi esegue il probing su tutti i device `/dev/ttyACM*` e `/dev/ttyUSB*` disponibili, uno alla volta, con un mutex globale che impedisce a due bridge di aprire lo stesso device simultaneamente. Le porte gia in uso da un bridge connesso vengono saltate (tracking `portsInUse`), così il probe non interferisce con la sessione attiva dell'altro ESP. Ogni probing ha un timeout di 800 ms.

### Invio spontaneo al boot

Ogni ESP32 deve inviare `HELLO <ROLE>\n` autonomamente nel `setup()` o in `app_main()`, prima di qualsiasi altro messaggio, in modo che un Pi gia avviato che stava monitorando la porta lo rilevi immediatamente senza dover inviare `GET-ROLE`.

### Regole per il firmware

- Rispondere a `GET-ROLE` sempre e immediatamente, qualsiasi sia lo stato corrente del firmware.
- La risposta deve essere esattamente `HELLO RFID\n` o `HELLO RELAY\n` (maiuscolo, senza spazi extra).
- Linee non riconosciute devono essere ignorate silenziosamente.

---

## 3. Comportamento alla connessione (lato Pi)

Dopo aver aperto la porta e verificato il ruolo, il Pi invia immediatamente:

**Bridge RFID:**
```
GET-STATE\n
FLOOR-GET\n
```
Dopo 8 secondi dall'avvio del servizio esegue anche `EnsureKey()` che verifica la chiave AES.

**Bridge RELAY:**
```
GET-STATE\n
```

---

## 4. Watchdog PING/PONG

Il Pi invia `PING\n` ogni 5 secondi su entrambi i bridge. Ogni ESP32 deve rispondere con `PONG\n`.

Se il firmware non riceve `PING` entro 10 secondi (parametro `WATCHDOG_TIMEOUT_MS`), deve portare tutti gli output in safe state:

| Output | Safe state |
|--------|-----------|
| Rele portone | LOW (chiuso) |
| Alimentazione tablet | inalterata o OFF |
| Ventola PWM | 50% minimo |

Il Pi non blocca in attesa del `PONG`: il canale di ricezione e separato da quello di invio.

---

## 5. Protocollo ESP32-A (RFID)

### Pi → ESP32-A

| Comando | Descrizione |
|---------|-------------|
| `GET-ROLE` | Richiesta identificazione ruolo. Risposta: `HELLO RFID` |
| `PING` | Keepalive watchdog ogni 5s |
| `GET-STATE` | Richiesta stato corrente (ring flash, floor) |
| `TAG-SCAN` | Avvia auto-detect del tag NFC successivo. Risposta immediata: `ACK TAG-SCAN PENDING` |
| `TAG-ENROLL` | Registra in whitelist NVS il tag attualmente avvicinato (usato automaticamente dopo `TAG-FORMAT-OK`) |
| `TAG-DEL <uid>` | Rimuove un tag dalla whitelist NVS. `<uid>` = 14 caratteri hex maiuscoli |
| `TAG-LIST` | Elenca tutti i tag nella whitelist NVS |
| `TAG-CLEAR` | Cancella tutta la whitelist NVS. Risposta: `ACK TAG-CLEAR` |
| `KEY-STATUS` | Interroga stato chiave AES-128 DESFire |
| `KEY-GEN` | Genera chiave AES-128 via TRNG hardware (solo se assente) |
| `KEY-GEN FORCE` | Rigenera chiave AES-128 anche se gia presente (invalida tutti i badge DESFire) |
| `FLOOR-GET` | Richiesta testi occupanti correnti (P1/P2/P3, 4 slot per piano) |
| `FLOOR-SET P1 s1\|s2\|s3\|s4` | Imposta i 4 nominativi del piano 1 (pipe-separated, max 20 char ciascuno). Analogamente per P2, P3 |

### ESP32-A → Pi

| Messaggio | Descrizione |
|-----------|-------------|
| `HELLO RFID` | Risposta a `GET-ROLE` o invio spontaneo al boot |
| `PONG` | Risposta a `PING` |
| `EVT p1 0` / `EVT p2 0` / `EVT p3 0` | Pulsante piano premuto (active-low: `0` = premuto, `1` = rilasciato) |
| `RING-P1` / `RING-P2` / `RING-P3` | Notifica chiamata dal piano (LED verde nel pannello per 8s) |
| `EVT nfc <uid>` | Tag NFC letto in modalita normale (non durante TAG-SCAN) |
| `UID-OK` | Tessera NFC autenticata con successo (il Pi verifica poi la whitelist locale) |
| `UID-KO` | Tessera NFC rifiutata |
| `ACK TAG-SCAN PENDING` | Conferma ricezione `TAG-SCAN`, lettore in ascolto |
| `ACK TAG-ENROLL PENDING` | Conferma ricezione `TAG-ENROLL` |
| `ACK TAG-FORMAT PENDING` | Conferma ricezione `TAG-FORMAT` |
| `ACK TAG-CLEAR` | Conferma cancellazione whitelist |
| `TAG-INFO <uid> PLAIN` | Auto-scan: tag identificato come MIFARE Classic o NTAG |
| `TAG-INFO <uid> DESFIRE-CONFIGURED` | Auto-scan: DESFire gia configurato con la chiave di sistema |
| `TAG-INFO <uid> DESFIRE-NEW` | Auto-scan: DESFire vergine, verra inizializzato |
| `TAG-ENROLLED <uid> PLAIN` | Tag aggiunto alla whitelist NVS (tipo PLAIN) |
| `TAG-ENROLLED <uid> DESFIRE` | Tag aggiunto alla whitelist NVS (tipo DESFire) |
| `TAG-FORMAT-OK <uid>` | DESFire inizializzato — il Pi invia automaticamente `TAG-ENROLL` |
| `TAG-FORMAT-FAIL` | Errore inizializzazione DESFire (generico) |
| `TAG-FORMAT-FAIL NOT-DESFIRE` | Il tag non e un DESFire |
| `TAG-FORMAT-FAIL NO-KEY` | Chiave AES non ancora generata |
| `TAG-ENROLL-FAIL FULL` | Whitelist NVS piena (max 10 tag) |
| `TAG-ENROLL-FAIL AUTH` | Autenticazione AES fallita |
| `TAG-ENROLL-FAIL ALREADY` | UID gia presente in whitelist |
| `TAG-DEL-OK` | Conferma rimozione tag |
| `TAG-DEL-FAIL NOT-FOUND` | UID non trovato in whitelist |
| `TAG-LIST-START` | Inizio lista tag |
| `TAG-ENTRY <n> <uid>` | Voce numero `n` della lista, UID a 14 caratteri hex |
| `TAG-LIST-END <count>` | Fine lista, `count` = numero totale di tag |
| `KEY-STATUS EMPTY` | Chiave AES non ancora generata |
| `KEY-STATUS PRESENT FP:<hex8>` | Chiave presente; `hex8` = primi 4 byte SHA-256 della chiave in hex |
| `KEY-GEN-OK FP:<hex8>` | Chiave generata con successo |
| `KEY-GEN-EXISTS FP:<hex8>` | Chiave gia presente, fingerprint confermato |
| `FLOOR-P1 s1\|s2\|s3\|s4` | Risposta a `FLOOR-GET` per il piano 1 (poi P2, P3) |
| `ACK FLOOR-SET P1` | Conferma ricezione `FLOOR-SET` per il piano 1 (entro 3s). Analogamente P2, P3 |

### Formato UID

L'UID e sempre trasmesso come stringa esadecimale maiuscola a 14 caratteri (7 byte zero-padded a sinistra). Esempio: `1C7D223E000000`. Tag a 4 byte vengono estesi: `1A09C601` diventa `1A09C601000000`.

---

## 6. Protocollo ESP32-B (RELAY)

### Pi → ESP32-B

| Comando | Descrizione |
|---------|-------------|
| `GET-ROLE` | Richiesta identificazione ruolo. Risposta: `HELLO RELAY` |
| `PING` | Keepalive watchdog ogni 5s |
| `GET-STATE` | Richiesta stato corrente (fan + tablet) |
| `SET unlockdoor pulse` | Impulso rele portone (~200ms HIGH poi LOW) |
| `SET unlockdoor on` | Rele portone ON manuale |
| `SET unlockdoor off` | Rele portone OFF |
| `SET power_tablet on` | Alimentazione tablet ON |
| `SET power_tablet off` | Alimentazione tablet OFF |
| `TABLET-ON` | Alimentazione tablet ON (alternativa a `SET power_tablet on`) |
| `TABLET-OFF` | Alimentazione tablet OFF (alternativa a `SET power_tablet off`) |
| `FAN-XX` | Imposta ventola PWM al XX percento (es. `FAN-75`). Range: 0-100 |
| `PWM fan XX` | Alternativa a `FAN-XX` |

### ESP32-B → Pi

| Messaggio | Descrizione |
|-----------|-------------|
| `HELLO RELAY` | Risposta a `GET-ROLE` o invio spontaneo al boot |
| `PONG` | Risposta a `PING` |
| `STATE FAN:XX TABLET:ON\|OFF` | Risposta a `GET-STATE` — stato corrente ventola (0-100) e tablet |
| `ACK unlockdoor pulse` | Conferma impulso portone eseguito |
| `ACK unlockdoor on` / `ACK unlockdoor off` | Conferma cambio stato rele portone |
| `ACK FAN-XX` | Conferma impostazione ventola (XX = valore effettivo clampato a 0-100) |
| `ACK TABLET-ON` / `ACK TABLET-OFF` | Conferma cambio stato tablet |

---

## 7. Flusso apertura porta da badge NFC

```
Badge avvicinato
     |
     v
ESP32-A: autenticazione AES-128 DESFire EV3
     |
     +-- OK --> invia UID-OK
     +-- KO --> invia UID-KO (fine)
     |
     v (UID-OK)
Pi: verifica whitelist locale (nfc_whitelist.json)
     |
     +-- non in whitelist o disabilitato --> nessuna azione
     +-- autorizzato --> invia SET unlockdoor pulse a ESP32-B
                              |
                              v
                         ESP32-B: impulso rele ~200ms
                         risponde: ACK unlockdoor pulse
```

La chiave AES-128 non transita mai sulla seriale USB. L'ESP32-A la custodisce nella NVS flash e comunica al Pi solo il fingerprint (4 byte SHA-256).

---

## 8. Flusso enrollment badge (auto-detect)

```
Pi     → ESP32-A   TAG-SCAN
ESP32-A → Pi       ACK TAG-SCAN PENDING
                   [utente avvicina il badge]
ESP32-A → Pi       TAG-INFO <uid> <tipo>

  Caso PLAIN o DESFIRE-CONFIGURED (1 solo tap):
    ESP32-A → Pi   TAG-ENROLLED <uid> PLAIN|DESFIRE

  Caso DESFIRE-NEW (2 tap):
    ESP32-A → Pi   TAG-FORMAT-OK <uid>
    Pi     → ESP32-A  TAG-ENROLL          (automatico)
                   [utente toglie e riavvicina il badge]
    ESP32-A → Pi   TAG-ENROLLED <uid> DESFIRE
```

---

## 9. Tabella timeout attesi

| Operazione | Timeout lato Pi | Note |
|-----------|----------------|------|
| `GET-ROLE` / probing | 800 ms | Per ogni porta candidata |
| `PING` → `PONG` | 5 s ciclo | Nessun timeout esplicito; il watchdog agisce su 10s senza PING |
| `KEY-STATUS` | 3 s | Risposta sincrona attesa |
| `KEY-GEN` / `KEY-GEN FORCE` | 3 s | Generazione TRNG veloce |
| `TAG-DEL <uid>` | 3 s | |
| `FLOOR-SET Px` → `ACK FLOOR-SET Px` | 3 s | |
| `TAG-SCAN` → `TAG-ENROLLED` | 35 s | Include attesa interazione utente |
| `TAG-LIST` → `TAG-LIST-END` | 5 s | |
| `SET unlockdoor pulse` | fire-and-forget | Il Pi non attende ACK per apertura porta |
| `TABLET-ON` / `TABLET-OFF` | fire-and-forget | |
| `FAN-XX` | fire-and-forget | |

---

## 10. Note su concorrenza e architettura

- I due bridge (`USBBridge`) sono goroutine indipendenti, ciascuna con il proprio `sendCh`, `readLoop` e `writeLoop`. Non condividono stato.
- Un mutex globale (`discoveryMu`) serializza le sessioni di probing per impedire che i due bridge aprano la stessa porta simultaneamente.
- Il Pi non ritrasmette comandi in caso di mancato ACK, a eccezione dei retry automatici al riconnessione.
- I buffer di invio sono code di 32 elementi. Se la coda e piena, il messaggio viene scartato con un log di warning (send buffer pieno).
- Il buffer degli eventi ricevuti (GPIO, card, NFC) e di 64 elementi; eventi non consumati entro questo limite vengono scartati.
- I log USB sono ring buffer di 200 righe, esposti via API al pannello web.
- Alla disconnessione, i comandi stale in `sendCh` vengono svuotati prima di riconnettersi.
- L'ESP32-B non ha connettivita NFC; i comandi NFC inviati per errore al bridge RELAY vengono ignorati (riga non riconosciuta).

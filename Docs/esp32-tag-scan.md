# ESP32 — Implementare TAG-SCAN (auto-detect tipo tag)

**Branch:** GPIO-OVER-USB  
**Data:** 2026-06-03  
**Documenti correlati:** [gpio-over-usb-esp32-side.md](gpio-over-usb-esp32-side.md), [esp32-comandi-pi.md](esp32-comandi-pi.md)

---

## Contesto

Il Raspberry Pi invia ora `TAG-SCAN\n` al posto di dovere specificare il tipo di tag.
L'ESP32 deve rilevare il tipo del tag da solo, comunicarlo al Pi, e procedere automaticamente.

Il lato Raspberry Pi è già completamente aggiornato. Questo documento descrive solo cosa deve fare il firmware ESP32.

---

## Macchina a stati NFC — stati esistenti

Il `task_nfc` ha già questi tre stati in `g_nfc_state`:

| Stato | Quando si entra | Cosa fa quando arriva un tag |
|---|---|---|
| `NFC_STATE_NORMAL` | All'avvio e dopo ogni operazione completata | Verifica se l'UID è in whitelist NVS → autentica DESFire → manda `UID-OK` o `UID-KO` |
| `NFC_STATE_ENROLL` | Dopo ricezione comando `TAG-ENROLL` dal Pi | Salva l'UID in whitelist NVS → manda `TAG-ENROLLED <uid> <PLAIN\|DESFIRE>` → torna a NORMAL |
| `NFC_STATE_FORMAT` | Dopo ricezione comando `TAG-FORMAT` dal Pi | Formatta il tag DESFire con la nostra chiave AES → manda `TAG-FORMAT-OK <uid>` → entra in NFC_STATE_ENROLL per aspettare il secondo tap |

---

## Nuovo stato da aggiungere: `NFC_STATE_SCAN`

### Come si entra
Il Pi manda il comando `TAG-SCAN\n`. L'ESP32 deve:
- Impostare `g_nfc_state = NFC_STATE_SCAN`
- Rispondere subito con `ACK TAG-SCAN PENDING\n`

### Cosa fa quando arriva un tag

**Fase 1 — Identifica il tipo di tag:**

Il tag si identifica guardando il campo SAK della risposta ISO14443A:
- SAK con bit 5 settato (0x20) **e** UID di 7 byte → è un tag DESFire
- Qualsiasi altra combinazione → è un tag normale (MIFARE Classic, NTAG, ecc.)

Se è DESFire, bisogna ulteriormente distinguere:
- Tenta autenticazione AES con la nostra chiave → se riesce: è un DESFire **già configurato** con la nostra chiave
- Se l'autenticazione fallisce: è un DESFire **nuovo / vergine**

**Fase 2 — Informa il Pi del tipo rilevato:**

Manda subito uno di questi messaggi, prima di fare qualsiasi altra cosa:

| Tipo rilevato | Messaggio da mandare |
|---|---|
| Tag normale (MIFARE, NTAG) | `TAG-INFO <uid> PLAIN\n` |
| DESFire con la nostra chiave | `TAG-INFO <uid> DESFIRE-CONFIGURED\n` |
| DESFire nuovo/vergine | `TAG-INFO <uid> DESFIRE-NEW\n` |

Dove `<uid>` è l'UID del tag in formato esadecimale maiuscolo a 14 caratteri (7 byte, zero-padded a sinistra se necessario). Esempio: `AABBCCDDEE1122`.

**Fase 3 — Procedi automaticamente:**

A seconda del tipo rilevato:

- **PLAIN** → salva direttamente l'UID in whitelist NVS come tipo PLAIN → manda `TAG-ENROLLED <uid> PLAIN\n` → torna a `NFC_STATE_NORMAL`

- **DESFIRE-CONFIGURED** → salva direttamente l'UID in whitelist NVS come tipo DESFIRE → manda `TAG-ENROLLED <uid> DESFIRE\n` → torna a `NFC_STATE_NORMAL`

- **DESFIRE-NEW** → formatta il tag con la nostra chiave AES → manda `TAG-FORMAT-OK <uid>\n` → entra in `NFC_STATE_ENROLL` e aspetta che il Pi mandi `TAG-ENROLL` (lo manda in automatico lato Go) → aspetta il secondo tap dell'utente → salva in NVS → manda `TAG-ENROLLED <uid> DESFIRE\n` → torna a `NFC_STATE_NORMAL`

---

## Flusso completo per ogni tipo di tag

### Caso 1: Tag normale (PLAIN)
```
Pi     → ESP32   TAG-SCAN
ESP32  → Pi      ACK TAG-SCAN PENDING
                 [utente avvicina il tag]
ESP32  → Pi      TAG-INFO <uid> PLAIN
ESP32  → Pi      TAG-ENROLLED <uid> PLAIN
```

### Caso 2: DESFire già configurato
```
Pi     → ESP32   TAG-SCAN
ESP32  → Pi      ACK TAG-SCAN PENDING
                 [utente avvicina il tag]
ESP32  → Pi      TAG-INFO <uid> DESFIRE-CONFIGURED
ESP32  → Pi      TAG-ENROLLED <uid> DESFIRE
```

### Caso 3: DESFire nuovo (richiede secondo tap)
```
Pi     → ESP32   TAG-SCAN
ESP32  → Pi      ACK TAG-SCAN PENDING
                 [utente avvicina il tag - primo tap]
ESP32  → Pi      TAG-INFO <uid> DESFIRE-NEW
ESP32  → Pi      TAG-FORMAT-OK <uid>
Pi     → ESP32   TAG-ENROLL                  ← il Pi lo manda in automatico
                 [utente toglie e riavvicina il tag - secondo tap]
ESP32  → Pi      TAG-ENROLLED <uid> DESFIRE
```

### Caso 4: Errori
```
TAG-ENROLL-FAIL FULL       → whitelist NVS piena (massimo 10 tag)
TAG-ENROLL-FAIL ALREADY    → UID già presente in whitelist
TAG-ENROLL-FAIL AUTH       → autenticazione AES fallita
TAG-FORMAT-FAIL            → formato DESFire fallito (errore generico)
TAG-FORMAT-FAIL NOT-DESFIRE → il tag non è DESFire (SAK/UID non corrispondono)
```
In tutti i casi di errore, tornare a `NFC_STATE_NORMAL`.

---

## Tabella completa di tutti i messaggi NFC (vecchi + nuovi)

### Pi → ESP32

| Comando | Risposta immediata | Effetto |
|---|---|---|
| `TAG-SCAN` | `ACK TAG-SCAN PENDING` | **NUOVO** — entra in auto-detect mode |
| `TAG-ENROLL` | `ACK TAG-ENROLL PENDING` | Legacy — enroll manuale prossimo tap |
| `TAG-FORMAT` | `ACK TAG-FORMAT PENDING` | Legacy — format DESFire prossimo tap |
| `TAG-DEL <uid>` | `TAG-DEL-OK` oppure `TAG-DEL-FAIL NOT-FOUND` | Rimuove UID da NVS |
| `TAG-LIST` | `TAG-LIST-START` + righe + `TAG-LIST-END <count>` | Lista tutti gli UID in NVS |
| `TAG-CLEAR` | `ACK TAG-CLEAR` | Cancella tutta la whitelist NVS |

### ESP32 → Pi

| Messaggio | Quando |
|---|---|
| `TAG-INFO <uid> PLAIN` | **NUOVO** — tag identificato in NFC_STATE_SCAN |
| `TAG-INFO <uid> DESFIRE-CONFIGURED` | **NUOVO** — tag identificato in NFC_STATE_SCAN |
| `TAG-INFO <uid> DESFIRE-NEW` | **NUOVO** — tag identificato in NFC_STATE_SCAN |
| `TAG-ENROLLED <uid> PLAIN` | Enrollment completato (qualsiasi modo) |
| `TAG-ENROLLED <uid> DESFIRE` | Enrollment completato (qualsiasi modo) |
| `TAG-FORMAT-OK <uid>` | DESFire formattato OK, aspetta secondo tap |
| `TAG-FORMAT-FAIL` | Format DESFire fallito |
| `TAG-FORMAT-FAIL NOT-DESFIRE` | Il tag non è DESFire |
| `TAG-ENROLL-FAIL FULL` | Whitelist NVS piena |
| `TAG-ENROLL-FAIL AUTH` | Autenticazione AES fallita |
| `TAG-ENROLL-FAIL ALREADY` | UID già in whitelist |
| `EVT nfc <uid>` | Card letta in modalità normale (NORMAL) |
| `UID-OK` | Auth OK in modalità normale → portone apre |
| `UID-KO` | Auth fallita in modalità normale → portone chiuso |

---

## Cosa NON cambiare

- Il comportamento degli stati `NFC_STATE_NORMAL`, `NFC_STATE_ENROLL`, `NFC_STATE_FORMAT` — devono continuare a funzionare esattamente come prima per compatibilità con i comandi legacy
- La logica di autenticazione DESFire (`desfire_authenticate`) — non toccarla
- La logica NVS di whitelist (`nfc_nvs_add`, `nfc_nvs_has`, `nfc_nvs_del`) — non toccarla
- Il watchdog e il safe state — non toccarli

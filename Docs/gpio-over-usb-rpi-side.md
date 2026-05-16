# GPIO over USB — Lato Raspberry Pi

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini  
**Documento correlato:** [gpio-over-usb-esp32-side.md](gpio-over-usb-esp32-side.md)

---

## 1. Ruolo del Raspberry Pi

Il Pi è il **cervello applicativo**: riceve eventi semplici dall'ESP32-S3 via USB e decide cosa fare (chiama il piano, apre il portone, logga gli accessi). Non conosce nulla del protocollo crittografico né del circuito hardware — tutta quella complessità è delegata all'ESP32-S3.

```
ESP32-S3  ──USB──►  Pi riceve:
                     EVT p1 0       ← pulsante P1 premuto
                     EVT p2 1       ← pulsante P2 rilasciato
                     UID-OK         ← tessera DESFire autenticata
                     UID-KO         ← tessera rifiutata

Pi  ──USB──►  ESP32-S3 riceve:
                     SET unlockdoor pulse   ← apri portone
                     PWM fan 75             ← ventola al 75%
                     PING                   ← keepalive ogni 5s
```

---

## 2. Stack tecnologico lato Pi

| Componente | Tecnologia | Motivo |
|------------|-----------|--------|
| Comunicazione USB | USB CDC ACM | ESP32-S3 (ESP-IDF TinyUSB) appare come `/dev/ttyACM0` — nessun driver |
| Symlink stabile | udev rule | `/dev/gpio-esp32` — indipendente dall'ordine di boot |
| Libreria seriale | `go.bug.st/serial v1.6.4` | Cross-platform, attivamente mantenuta, zero dipendenze C |
| Protocollo | Testo ASCII `\n`-delimited | Leggibile a occhio, debug con qualsiasi terminale seriale |
| Firmware ESP32-S3 | ESP-IDF v5.x/6.x, C | Toolchain ufficiale Espressif — vedi documento correlato |

**Dipendenza da aggiungere al modulo Go:**

```bash
go get go.bug.st/serial@latest
```

Unica dipendenza nuova. Zero librerie NFC, zero crypto sul Pi.

---

## 3. Architettura software

### 3.1 Schema generale

```
/dev/gpio-esp32  (USB CDC ACM, 115200 baud)
        │
        │  righe di testo terminate da \n
        ▼
┌───────────────────────────────────────────┐
│            usb_bridge.go                  │
│                                           │
│  - Apre la porta seriale                  │
│  - Riconnessione automatica (hot-plug)    │
│  - Legge righe in loop (bufio.Scanner)    │
│  - Parsa il tipo di evento                │
│  - Dispatcha sui canali Go                │
│  - Espone Send() per inviare comandi      │
│  - Invia PING ogni 5s (watchdog)          │
│  - Mantiene esp32State (pin, card log)    │
└──────────────┬────────────────────────────┘
               │
       ┌───────┴────────┐
       │                │
       ▼                ▼
chan GPIOEvent    chan CardEvent
       │                │
       ▼                ▼
┌─────────────┐  ┌──────────────┐
│ gpio_usb.go │  │ smartcard.go │
│             │  │              │
│ Riceve EVT  │  │ Riceve       │
│ px N, chiama│  │ UID-OK/KO    │
│ cmdRingPiano│  │ Apre portone │
│ Espone      │  │ Logga        │
│ SetPin()    │  │ accessi      │
│ SetPWM()    │  │              │
└─────────────┘  └──────────────┘
```

### 3.2 `usb_bridge.go` — layer comune

È il cuore dell'integrazione. Gestisce tre responsabilità:

**a) Connessione seriale con hot-plug**

La porta USB può sparire e riapparire (reboot ESP32-S3, stacco cavo). Il bridge rileva la disconnessione e riprova ogni 2 secondi in modo trasparente per il resto dell'applicazione. Il log viene throttled (solo al 1° tentativo e poi ogni 15, ~30s) per evitare spam.

**b) Dispatch eventi sui canali**

Legge una riga alla volta e instrada sul canale corretto:

```
"EVT p1 0"  →  GPIOEvent{Pin:"p1", Value:0}  →  chan GPIOEvent
"EVT p2 1"  →  GPIOEvent{Pin:"p2", Value:1}  →  chan GPIOEvent
"UID-OK"    →  CardEvent{OK:true}             →  chan CardEvent
"UID-KO"    →  CardEvent{OK:false}            →  chan CardEvent
"PONG"      →  noop (keepalive confermato)
"ACK ..."   →  noop (conferma output loggata)
"ERR ..."   →  log warning
```

**c) Invio comandi verso ESP32-S3**

```go
bridge.Send("SET unlockdoor pulse\n")
bridge.Send("PWM fan 75\n")
```

`Send()` è non-bloccante: accoda su canale bufferizzato (32 slot). Se pieno, scarta con warning. I comandi accumulati durante una disconnessione vengono scartati alla riconnessione per evitare invii stale.

**Strutture dati principali:**

```go
type GPIOEvent struct {
    Pin   string  // "p1", "p2", "p3", "on_off"
    Value int     // 0 = premuto (active-low), 1 = rilasciato
}

type CardEvent struct {
    OK bool       // true = UID-OK, false = UID-KO
}

type ESP32CardLog struct {
    Time   time.Time `json:"time"`
    Result string    `json:"result"` // "OK" o "KO"
}
```

**Garanzie di robustezza:**

| Scenario | Comportamento |
|----------|--------------|
| ESP32-S3 non collegato all'avvio | Retry silenzioso ogni 2s |
| Cavo USB staccato a caldo | readLoop si sblocca, session termina, retry in 2s |
| Shutdown applicazione con device connesso | writeLoop chiude la porta su ctx.Done → readLoop si sblocca → exit pulito |
| Doppio Close() sulla porta | `sync.Once` in runSession — Close eseguito esattamente una volta |
| Comandi stale dopo riconnessione | `drainSendCh()` svuota sendCh prima della nuova sessione |
| Connessione silenziosamente morta | PING ogni 5s → writeLoop rileva errore scrittura → riconnessione |

### 3.3 `gpio_usb.go` — gestione GPIO

Consuma eventi da `chan GPIOEvent` e gestisce i comandi di output.

**Ricezione eventi (input):**

`Value == 0` = fronte discendente = pulsante premuto (active-low):

```
GPIOEvent{Pin:"p1", Value:0}      →  b.cmdRingPiano("P1")
GPIOEvent{Pin:"p2", Value:0}      →  b.cmdRingPiano("P2")
GPIOEvent{Pin:"p3", Value:0}      →  b.cmdRingPiano("P3")
GPIOEvent{Pin:"on_off", Value:0}  →  gestione on/off
```

**Modalità debug (attuale):** l'evento viene solo loggato — nessuna azione eseguita — per coesistere con `gpio.go` che gestisce già gli stessi GPIO fisici.

**Invio comandi output:**

```go
func (g *GPIOUsb) SetPin(name, state string)  // es. SetPin("heartbeat","on")
func (g *GPIOUsb) SetPWM(name string, duty int)  // es. SetPWM("fan", 75)
func (g *GPIOUsb) Pulse(name string)             // es. Pulse("unlockdoor")
```

### 3.4 `smartcard.go` — gestione accesso tessere

Consuma eventi da `chan CardEvent`. Non esegue nessuna validazione crittografica — quella è già avvenuta nell'ESP32-S3.

**Flusso su `UID-OK`:**

```
CardEvent{OK: true}
        │
Anti-spam (ignora se < 3s dall'ultimo OK)
        │
bridge.Send("SET unlockdoor pulse\n")   ← relè portone
        │
bridge.Send("SET led_ok on\n")          ← LED verde 3s
        │
logAccess("OK", "unlockdoor")           → preferences/access_log.jsonl
```

**Flusso su `UID-KO`:**

```
CardEvent{OK: false}
        │
bridge.Send("SET led_ko on\n")          ← LED rosso 2s
        │
logAccess("KO", "")                     → preferences/access_log.jsonl
```

**Log accessi (`preferences/access_log.jsonl`):**

```jsonl
{"ts":"2026-05-16T10:23:11Z","result":"OK","action":"unlockdoor"}
{"ts":"2026-05-16T10:45:02Z","result":"KO"}
```

---

## 4. Protocollo USB — riferimento completo

Testo ASCII, una riga per messaggio, terminata da `\n`. Leggibile con qualsiasi terminale seriale (115200 8N1).

### 4.1 ESP32-S3 → Pi

```
EVT <pin> <0|1>\n        cambio stato GPIO input (0=premuto, 1=rilasciato)
UID-OK\n                 tessera DESFire EV3 autenticata con successo
UID-KO\n                 tessera rifiutata (qualsiasi motivo)
PONG\n                   risposta al PING del Pi
ACK <pin> <stato>\n      conferma esecuzione comando SET/PWM
ERR <msg>\n              errore ESP32-S3
```

### 4.2 Pi → ESP32-S3

```
SET <pin> <on|off|pulse>\n   controlla output digitale
PWM <pin> <0-100>\n          imposta duty cycle ventola
GET <pin>\n                  leggi stato corrente pin
PING\n                       keepalive (ogni 5s)
```

### 4.3 Pin names nel protocollo

| Nome protocollo | Funzione |
|----------------|----------|
| `p1` | Pulsante piano 1 |
| `p2` | Pulsante piano 2 |
| `p3` | Pulsante piano 3 |
| `on_off` | Pulsante on/off |
| `unlockdoor` | Relè portone |
| `heartbeat` | LED heartbeat |
| `led_ok` | LED verde accesso OK |
| `led_ko` | LED rosso accesso KO |
| `fan` | Ventola PWM |

### 4.4 Watchdog

Se l'ESP32-S3 non riceve `PING` entro 10 secondi entra in safe state:
- `unlockdoor` → OFF (portone rimane chiuso)
- `heartbeat` → OFF
- `fan` → PWM 50% (ventilazione minima garantita)
- Autenticazione in corso → annullata, invia `UID-KO`

---

## 5. Struttura file lato Pi

```
/home/doorphoneserver/
├── gpio.go              ← GPIO fisici Pi (invariato, resta attivo in debug)
├── usb_bridge.go        ← seriale + dispatch canali + esp32State
├── gpio_usb.go          ← consumer GPIOEvent + comandi output
└── smartcard.go         ← consumer CardEvent + log accessi
```

**Integrazione in `client.go`:**

```go
usbBridge := NewUSBBridge(ctx)
b.USBBridge = usbBridge
go NewGPIOUsb(usbBridge).Run(ctx)
go NewSmartcard(usbBridge).Run(ctx)
```

---

## 6. Pannello web — tab ESP32

Il tab **ESP32** nel pannello web (`/panel`) espone:

| Controllo | Funzione | API |
|-----------|----------|-----|
| Indicatore connessione | Pallino verde/rosso + testo | `GET /panel/api/esp32/status` |
| Slider ventola PWM | Imposta duty 0–100% | `POST /panel/api/esp32/fan` |
| Pulsante Apri Portone | Invia `SET unlockdoor pulse` | `POST /panel/api/esp32/door` |
| LED P1/P2/P3 | Stato pulsanti in tempo reale | polling `status` ogni 2s |
| Log tessere | Ultimi 50 eventi UID-OK/KO | polling `status` ogni 2s |

I controlli vengono disabilitati automaticamente quando il device non è connesso.

---

## 7. Regola udev

```udev
# /etc/udev/rules.d/99-gpio-esp32.rules
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", \
    SYMLINK+="gpio-esp32", MODE="0660", GROUP="dialout"
```

Applicare:

```bash
sudo udevadm control --reload-rules && sudo udevadm trigger
```

Verificare:

```bash
ls -la /dev/gpio-esp32   # deve esistere quando ESP32-S3 è collegato
```

---

## 8. Roadmap — attività lato Pi

### Fase 2 (corrente) — Librerie Go + debug
- [x] `usb_bridge.go`: seriale + dispatch + hot-plug + robustezza
- [x] `gpio_usb.go`: consumer GPIOEvent (debug mode — solo log)
- [x] `smartcard.go`: consumer CardEvent (debug mode — solo log)
- [x] Pannello web tab ESP32
- [ ] Regola udev `/dev/gpio-esp32`
- [ ] Test integrazione con firmware Fase 1

### Fase 4 — Produzione
- [ ] `gpio_usb.go`: rimuovere debug mode, abilitare `cmdRingPiano()`
- [ ] `smartcard.go`: abilitare apertura portone + notifica Pushover
- [ ] Disabilitare `gpio.go` come backend primario (sostituito da `gpio_usb.go`)
- [ ] Log accessi strutturato con nome utente (da file JSON tessere)
- [ ] HMAC sul canale USB (anti-replay opzionale)

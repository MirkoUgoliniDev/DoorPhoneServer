# GPIO over USB — ESP32-S3 come espansore I/O + Lettore DESFire EV3

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini  
**Modalità implementazione:** Debug/parallelo — i GPIO fisici del Pi restano attivi

---

## 1. Visione generale

Il sistema aggiunge un **ESP32-S3** collegato via USB al Raspberry Pi. L'ESP32-S3 gestisce in autonomia:

- Lettura interrupt-driven dei pulsanti (P1, P2, P3, On/Off)
- Controllo output digitali (relè portone, LED heartbeat, alimentazione tablet)
- Controllo PWM della ventola di raffreddamento
- Lettura e autenticazione crittografica DESFire EV3 (tessere accesso)

Il Raspberry Pi riceve **eventi semplici** via USB e li gestisce senza conoscere nulla del protocollo crittografico o dei dettagli hardware:

```
ESP32-S3  ──USB──►  Pi riceve:
                     EVT p1 0       ← pulsante P1 premuto
                     EVT p2 1       ← pulsante P2 rilasciato
                     UID-OK         ← tessera DESFire autenticata
                     UID-KO         ← tessera rifiutata
```

I GPIO fisici del Pi restano invariati e operativi — il sistema USB opera in **parallelo** durante la fase di sviluppo e debug.

---

## 2. Stack tecnologico

| Layer | Tecnologia | Motivo |
|-------|-----------|--------|
| Firmware ESP32-S3 | **TinyGo** | Go su microcontrollore — stesso linguaggio, driver PN532 già pronto |
| Comunicazione USB | **USB CDC ACM** | ESP32-S3 appare come `/dev/ttyACM0` su Linux — nessun driver |
| Simlink stabile | **udev rule** | `/dev/gpio-esp32` — indipendente dall'ordine di boot |
| Libreria seriale Pi | **go.bug.st/serial** | Cross-platform, attivamente mantenuta, zero dipendenze C |
| Driver NFC | **tinygo-org/drivers pn532** | Driver già scritto e testato per TinyGo |
| Crittografia DESFire | **crypto/aes TinyGo** | AES hardware ESP32-S3 accessibile da TinyGo |

---

## 3. Architettura software lato Pi

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
│  - Riconnessione automatica               │
│  - Legge righe in loop                    │
│  - Parsa il tipo di evento                │
│  - Dispatcha sui canali Go                │
│  - Espone Send() per inviare comandi      │
│  - Invia PING ogni 5s (watchdog)          │
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
│ - Riceve    │  │ - Riceve     │
│   EVT px N  │  │   UID-OK/KO  │
│ - Chiama    │  │ - Valida con │
│   cmdRing   │  │   smartcards │
│   Piano()   │  │   .json      │
│ - Espone    │  │ - Apre porta │
│   SetPin()  │  │   se OK      │
│   SetPWM()  │  │ - Log        │
└─────────────┘  └──────────────┘
```

### 3.2 `usb_bridge.go` — layer comune

È il cuore dell'integrazione. Ha tre responsabilità:

**a) Gestione connessione seriale con riconnessione automatica**

La porta USB può sparire e riapparire (reboot ESP32-S3, stacco cavo). Il bridge gestisce questo in modo trasparente: quando la porta chiude, aspetta 10 secondi e riprova. Il resto dell'applicazione non si accorge di nulla.

**b) Dispatch eventi sui canali**

Legge una riga alla volta dal seriale e la instrada sul canale corretto:

```
riga "EVT p1 0"   →  GPIOEvent{Pin:"p1", Value:0}  →  chan GPIOEvent
riga "EVT p2 1"   →  GPIOEvent{Pin:"p2", Value:1}  →  chan GPIOEvent
riga "UID-OK"     →  CardEvent{OK:true}             →  chan CardEvent
riga "UID-KO"     →  CardEvent{OK:false}            →  chan CardEvent
riga "PONG"       →  aggiorna timestamp keepalive
riga "ACK ..."    →  aggiorna stato interno output
```

**c) Invio comandi verso ESP32-S3**

Espone un metodo `Send(msg string)` che scrive sulla porta seriale attraverso un canale bufferizzato (evita race condition tra goroutine che scrivono contemporaneamente).

```
gpio_usb.go chiama:   bridge.Send("SET heartbeat on\n")
smartcard.go chiama:  bridge.Send("SET unlockdoor pulse\n")
```

**Struttura dati:**

```go
type USBBridge struct {
    port    serial.Port
    sendCh  chan string       // comandi in uscita verso ESP32-S3
    GpioEvt chan GPIOEvent    // eventi GPIO in ingresso
    CardEvt chan CardEvent    // eventi smartcard in ingresso
}

type GPIOEvent struct {
    Pin   string  // "p1", "p2", "p3", "on_off"
    Value int     // 0 = premuto (active-low), 1 = rilasciato
}

type CardEvent struct {
    OK     bool    // true = UID-OK, false = UID-KO
}
```

### 3.3 `gpio_usb.go` — gestione GPIO

Consuma gli eventi dal canale `GpioEvt` e gestisce i comandi di output verso l'ESP32-S3.

**Ricezione eventi (input):**

Quando arriva un `GPIOEvent` con `Value == 0` (fronte discendente, pulsante premuto in active-low), chiama la stessa funzione già usata dai GPIO fisici del Pi:

```
GPIOEvent{Pin:"p1", Value:0}  →  b.cmdRingPiano("P1")
GPIOEvent{Pin:"p2", Value:0}  →  b.cmdRingPiano("P2")
GPIOEvent{Pin:"p3", Value:0}  →  b.cmdRingPiano("P3")
GPIOEvent{Pin:"on_off", Value:0}  →  b.handleOnOff()
```

In modalità debug: l'evento viene solo loggato, non eseguito — per non duplicare le azioni con il GPIO fisico già attivo.

**Invio comandi (output):**

Espone funzioni che il resto dell'applicazione usa per controllare gli output dell'ESP32-S3:

```go
func (g *GPIOUsb) SetPin(name string, state string)   // "heartbeat","on"
func (g *GPIOUsb) SetPWM(name string, duty int)       // "fan", 75
func (g *GPIOUsb) Pulse(name string)                  // "unlockdoor"
```

Internamente chiamano `bridge.Send("SET heartbeat on\n")` ecc.

**Sincronizzazione con gpio.go:**

In modalità debug, `gpio_usb.go` e `gpio.go` coesistono. Entrambi ricevono e loggano gli eventi, ma solo `gpio.go` esegue le azioni. Quando si passa in produzione, basta disabilitare `gpio.go` e rimuovere il flag debug.

### 3.4 `smartcard.go` — gestione accesso tessere

Consuma gli eventi dal canale `CardEvt`.

**Flusso su `UID-OK`:**

```
CardEvent{OK: true}
        │
        ▼
Log: "tessera accettata dall'ESP32-S3"
        │
        ▼
bridge.Send("SET unlockdoor pulse\n")   ← apri portone via ESP32-S3
        │
        ▼
GPIOOutPin("unlockdoor", "pulse")       ← apri portone via GPIO fisico Pi
        │                                  (in produzione: solo uno dei due)
        ▼
Log accesso su preferences/access_log.jsonl
```

**Flusso su `UID-KO`:**

```
CardEvent{OK: false}
        │
        ▼
Log: "tessera rifiutata dall'ESP32-S3"
        │
        ▼
bridge.Send("SET led_red on\n")   ← LED rosso sull'ESP32-S3
        │
        ▼
Log accesso negato su preferences/access_log.jsonl
```

**Nota importante:** `smartcard.go` **non fa nessuna validazione crittografica** — quella è già stata fatta dall'ESP32-S3. Il Pi si fida del risultato e gestisce solo le conseguenze applicative (apri porta, logga, notifica).

**Log accessi (`preferences/access_log.jsonl`):**

Ogni accesso viene registrato in formato JSONL (una riga JSON per evento):

```jsonl
{"ts":"2026-05-16T10:23:11Z","result":"OK","action":"unlockdoor"}
{"ts":"2026-05-16T10:45:02Z","result":"KO","reason":"AUTH_FAIL"}
```

---

## 4. Protocollo USB

### 4.1 Formato

Testo ASCII, una riga per messaggio, terminata da `\n`. Semplice da leggere con `bufio.Scanner` in Go e con la UART in TinyGo. Leggibile a occhio durante il debug con qualsiasi terminale seriale.

### 4.2 ESP32-S3 → Pi

```
EVT <pin> <0|1>\n          cambio stato GPIO input
UID-OK\n                   tessera DESFire EV3 autenticata
UID-KO\n                   tessera rifiutata (qualsiasi motivo)
PONG\n                     risposta al PING
ACK <pin> <valore>\n       conferma esecuzione comando SET/PWM
```

### 4.3 Pi → ESP32-S3

```
SET <pin> <on|off|pulse>\n   controlla output digitale
PWM <pin> <0-100>\n          imposta duty cycle ventola
GET <pin>\n                  leggi stato pin
PING\n                       keepalive (ogni 5s)
```

### 4.4 Watchdog

Se l'ESP32-S3 non riceve `PING` entro 10 secondi:
- `unlockdoor` → OFF (sicurezza: portone rimane chiuso)
- `heartbeat` → OFF
- `fan` → PWM 50% (ventilazione minima)
- Qualsiasi autenticazione in corso → annullata, invia `UID-KO`

---

## 5. Firmware ESP32-S3 (TinyGo)

### 5.1 Perché TinyGo

- Stesso linguaggio Go — un solo codebase, un solo programmatore
- Driver PN532 già pronto in `tinygo-org/drivers`
- `crypto/aes` disponibile con accesso all'acceleratore hardware ESP32-S3
- Goroutine e canali disponibili (con le limitazioni del runtime TinyGo)
- Nessun C, nessun ESP-IDF, nessun FreeRTOS da gestire manualmente

### 5.2 Struttura firmware

```
firmware/
├── main.go              ← entry point, init periferiche, avvia goroutine
├── gpio_handler.go      ← interrupt GPIO input, debounce, invio EVT
├── gpio_output.go       ← esegue SET/PWM dalla coda comandi
├── pwm_fan.go           ← controllo LEDC 25kHz duty 0-100%
├── pn532_handler.go     ← loop NFC, usa driver tinygo-org/drivers/pn532
├── desfire_auth.go      ← 3-pass AES mutual auth + TMAC verify DESFire EV3
├── usb_handler.go       ← legge comandi USB, scrive eventi, watchdog
└── key_store.go         ← carica chiavi AES da flash (NVS cifrato)
```

### 5.3 Goroutine e canali (TinyGo)

```go
// main.go — schema goroutine
func main() {
    initUSB()
    initGPIO()
    initPWM()
    initNFC()
    loadKeys()

    cmdCh := make(chan Command, 16)    // comandi USB → output GPIO
    evtCh := make(chan string,  16)    // eventi → USB out

    go usbRxLoop(cmdCh)               // legge comandi dal Pi
    go usbTxLoop(evtCh)               // invia eventi al Pi
    go gpioInputLoop(evtCh)           // interrupt GPIO → EVT
    go gpioOutputLoop(cmdCh)          // SET/PWM
    go nfcLoop(evtCh)                 // PN532 + DESFire → UID-OK/KO
    go watchdogLoop()                 // safe state se PING assente
    select {}                         // blocca main forever
}
```

### 5.4 GPIO interrupt-driven

In TinyGo su ESP32-S3, i pin supportano interrupt su fronte:

```go
// gpio_handler.go
func initGPIOInputs() {
    pins := []machine.Pin{PIN_P1, PIN_P2, PIN_P3, PIN_ON_OFF}
    for _, p := range pins {
        p.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
        p.SetInterrupt(machine.PinFalling|machine.PinRising, func(p machine.Pin) {
            // ISR: solo notifica, niente logica
            gpioIsrCh <- p
        })
    }
}

func gpioInputLoop(evtCh chan<- string) {
    prev := map[machine.Pin]bool{}
    for pin := range gpioIsrCh {
        time.Sleep(5 * time.Millisecond)    // debounce
        val := pin.Get()
        if val == prev[pin] { continue }     // glitch, ignora
        prev[pin] = val
        v := 0
        if val { v = 1 }
        evtCh <- fmt.Sprintf("EVT %s %d\n", pinName(pin), v)
    }
}
```

### 5.5 PWM ventola (LEDC)

```go
// pwm_fan.go
var fanPWM machine.PWM

func initPWM() {
    fanPWM = machine.PWM0
    fanPWM.Configure(machine.PWMConfig{Period: 40000}) // 25kHz
    fanPWM.Set(machine.PWM0.Channel(PIN_FAN), 0)
}

func setFanDuty(percent int) {
    if percent < 0  { percent = 0  }
    if percent > 100 { percent = 100 }
    top := fanPWM.Top()
    fanPWM.Set(machine.PWM0.Channel(PIN_FAN), uint32(top)*uint32(percent)/100)
}
```

### 5.6 NFC DESFire EV3

```go
// pn532_handler.go
func nfcLoop(evtCh chan<- string) {
    nfc := pn532.New(machine.SPI0, PIN_NFC_CS, PIN_NFC_IRQ)
    nfc.Configure()

    for {
        // Attendi interrupt IRQ dal PN532 (card present)
        <-nfcIrqCh

        uid, err := nfc.ReadPassiveTarget(pn532.ISO14443A, 100*time.Millisecond)
        if err != nil { continue }

        // Solo DESFire: UID 7 byte
        if len(uid) != 7 {
            evtCh <- "UID-KO\n"
            continue
        }

        // Autenticazione DESFire EV3 completa (desfire_auth.go)
        err = desfireAuthenticate(nfc, uid)
        if err != nil {
            evtCh <- "UID-KO\n"
        } else {
            evtCh <- "UID-OK\n"
        }

        // Attendi rimozione tessera
        waitCardRemoved(nfc)
    }
}
```

### 5.7 DESFire EV3 — autenticazione AES 3 passi

L'ESP32-S3 esegue l'intera sequenza crittografica. Il Pi non vede nulla di questo.

```
1. SelectApplication(AID)
        │
2. AuthenticateAES(KeyNo)  →  tessera risponde con RndB cifrato
        │
3. ESP32-S3:
   - AES_decrypt(AppKey, RndB_enc)  →  RndB
   - Genera RndA random
   - token = AES_CBC_encrypt(AppKey, RndA || rotate(RndB))
   - Invia token alla tessera
        │
4. Tessera risponde con AES_encrypt(AppKey, rotate(RndA))
        │
5. ESP32-S3 verifica RndA'  →  AUTENTICAZIONE MUTUA OK
        │
6. Deriva SessionKey = KDF(RndA, RndB)
        │
7. ReadFile(encrypted) + verifica CMAC/TMAC
        │
8. evtCh <- "UID-OK\n"   (oppure "UID-KO\n" su qualsiasi errore)
```

Le chiavi AES non lasciano mai l'ESP32-S3. Sono caricate dall'NVS cifrato all'avvio.

---

## 6. Mappa GPIO ESP32-S3

| Funzione | Direzione | GPIO | Circuito esterno |
|----------|-----------|------|------------------|
| Pulsante P1 | INPUT ISR | 4 | Pull-up interno, active-low |
| Pulsante P2 | INPUT ISR | 5 | Pull-up interno, active-low |
| Pulsante P3 | INPUT ISR | 6 | Pull-up interno, active-low |
| On/Off | INPUT ISR | 7 | Pull-up interno, active-low |
| LED Heartbeat | OUTPUT | 15 | 220Ω → LED |
| Relè portone | OUTPUT | 16 | BC547 NPN + 1N4007 |
| Power tablet | OUTPUT | 17 | BC547 NPN |
| LED power | OUTPUT | 18 | 220Ω → LED |
| Ventola PWM | OUTPUT PWM | 8 | IRLZ44N MOSFET |
| PN532 MOSI | SPI | 11 | |
| PN532 MISO | SPI | 13 | |
| PN532 SCK | SPI | 12 | |
| PN532 CS | OUTPUT | 10 | Active-low |
| PN532 IRQ | INPUT ISR | 9 | Interrupt card-present |
| LED accesso OK | OUTPUT | 14 | 220Ω → LED verde |
| LED accesso KO | OUTPUT | 21 | 220Ω → LED rosso |

---

## 7. Struttura file Go lato Pi

```
/home/doorphoneserver/
├── gpio.go              ← GPIO fisici Pi (invariato, resta attivo)
├── usb_bridge.go        ← NUOVO: seriale + dispatch canali
├── gpio_usb.go          ← NUOVO: consumer GPIOEvent + comandi output
└── smartcard.go         ← NUOVO: consumer CardEvent + log accessi
```

### Dipendenza da aggiungere

```bash
go get go.bug.st/serial@latest
```

Unica dipendenza nuova. Zero librerie NFC, zero librerie crypto sul Pi.

### Integrazione nell'applicazione

In `client.go` (o dove viene inizializzata l'app), aggiungere:

```go
bridge := NewUSBBridge(ctx)
go NewGPIOUsb(bridge, server).Run(ctx)    // debug mode: solo log
go NewSmartcard(bridge, server).Run(ctx)  // apre portone su UID-OK
```

---

## 8. Personalizzazione tessere DESFire EV3

Le tessere devono essere programmate una tantum con:
- Application ID (AID) — stesso configurato nel firmware ESP32-S3
- Application Key (AES-128) — stessa chiave nell'NVS dell'ESP32-S3
- Data File (opzionale) — può contenere metadati (nome utente, piano, ecc.)

Tool di personalizzazione: script Python su PC con lettore ACR122U o PN532 su USB.
Dettagli in Appendice B.

---

## 9. Sicurezza chiavi

| Dove | Come | Protezione |
|------|------|-----------|
| ESP32-S3 | NVS cifrato (Flash Encryption efuse) | Illeggibile anche con accesso fisico al chip |
| Tool personalizzazione | File locale sul PC di amministrazione | Fuori dal repo git, mai committato |
| Canale USB Pi↔ESP32-S3 | Testo in chiaro per ora | Accettabile: link fisico locale. HMAC aggiungibile in futuro |

---

## 10. Roadmap implementazione

### Fase 1 — Firmware TinyGo base
- [ ] Setup progetto TinyGo per ESP32-S3
- [ ] USB CDC funzionante (PING/PONG)
- [ ] GPIO interrupt-driven: EVT p1/p2/p3
- [ ] PWM ventola: comando PWM fan
- [ ] Relè portone: SET unlockdoor pulse

### Fase 2 — Librerie Go lato Pi
- [ ] `usb_bridge.go`: seriale + dispatch canali
- [ ] `gpio_usb.go`: consumer GPIOEvent (modalità debug — solo log)
- [ ] `smartcard.go`: consumer CardEvent (apre portone su UID-OK)
- [ ] Regola udev `/dev/gpio-esp32`
- [ ] Test integrazione Fase 1 + Fase 2

### Fase 3 — DESFire EV3
- [ ] Driver PN532 SPI in TinyGo (da tinygo-org/drivers)
- [ ] `desfire_auth.go`: SelectApp + 3-pass AES + TMAC
- [ ] NVS key storage + tool provisioning tessere
- [ ] Test con tessere reali
- [ ] UID-OK → portone apre end-to-end

### Fase 4 — Hardening (opzionale)
- [ ] Flash Encryption + Secure Boot ESP32-S3
- [ ] HMAC sul canale USB (anti-replay)
- [ ] Log accessi strutturato
- [ ] Migrazione produzione: `gpio_usb.go` diventa backend primario

---

## Appendice A — Regola udev

```udev
# /etc/udev/rules.d/99-gpio-esp32.rules
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", \
    SYMLINK+="gpio-esp32", MODE="0660", GROUP="dialout"
```

Ricaricare: `sudo udevadm control --reload-rules && sudo udevadm trigger`

## Appendice B — Tool personalizzazione tessere (Python, PC)

```python
# pip install nfcpy
# Hardware: ACR122U collegato al PC

import nfc

AID     = bytes([0xA5, 0xC3, 0x01])          # stesso AID del firmware
APP_KEY = bytes.fromhex("CAMBIA_CON_CHIAVE_REALE_32HEX")

def personalize(tag):
    app = nfc.tag.tt4.Application(tag, AID)
    app.select()
    app.authenticate(key_no=0, key=b'\x00'*16)   # master key default (prima volta)
    app.change_key(key_no=1, new_key=APP_KEY)
    print(f"OK: UID={tag.identifier.hex().upper()}")

with nfc.ContactlessFrontend('usb') as clf:
    clf.connect(rdwr={'on-connect': personalize})
```

## Appendice C — Struttura firmware TinyGo

```
firmware/
├── main.go           ← init + goroutine spawn
├── gpio_handler.go   ← interrupt input + debounce + EVT
├── gpio_output.go    ← SET on/off/pulse
├── pwm_fan.go        ← LEDC 25kHz
├── pn532_handler.go  ← PN532 SPI loop + IRQ
├── desfire_auth.go   ← AES 3-pass auth + TMAC
├── usb_handler.go    ← CDC read/write + watchdog
├── key_store.go      ← NVS AES keys
└── go.mod            ← tinygo-org/drivers
```

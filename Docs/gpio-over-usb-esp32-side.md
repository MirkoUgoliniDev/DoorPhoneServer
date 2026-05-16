# GPIO over USB — Lato ESP32-S3

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini  
**Documento correlato:** [gpio-over-usb-rpi-side.md](gpio-over-usb-rpi-side.md)

---

## 1. Ruolo dell'ESP32-S3

L'ESP32-S3 è il **layer hardware**: gestisce in autonomia tutti i circuiti fisici e i protocolli crittografici. Il Raspberry Pi non sa nulla di GPIO, NFC o DESFire — riceve solo messaggi di testo.

**L'ESP32-S3 gestisce:**
- Lettura interrupt-driven dei pulsanti (P1, P2, P3, On/Off) con debounce
- Controllo output digitali (relè portone, LED, alimentazione tablet)
- Controllo PWM della ventola di raffreddamento (LEDC 25kHz)
- Lettura NFC via PN532 (SPI) e autenticazione DESFire EV3 completa

**Non lascia mai l'ESP32-S3:**
- Le chiavi AES delle tessere
- Il risultato intermedio dell'autenticazione (RndA, RndB, SessionKey)
- Il contenuto dei data file cifrati sulla tessera

---

## 2. Stack tecnologico lato ESP32-S3

| Componente | Tecnologia | Motivo |
|------------|-----------|--------|
| Firmware | **TinyGo** | Go su microcontrollore — stesso linguaggio del Pi |
| Driver NFC | `tinygo-org/drivers/pn532` | Driver SPI già scritto e testato |
| Crittografia | `crypto/aes` (TinyGo) | Accesso all'acceleratore AES hardware ESP32-S3 |
| Comunicazione USB | USB CDC ACM | Appare come `/dev/ttyACM0` su Linux — zero driver |
| Key storage | NVS cifrato (Flash Encryption) | Chiavi illeggibili anche con accesso fisico al chip |
| Goroutine | Runtime TinyGo | Goroutine e canali disponibili (stack fisso) |

---

## 3. Struttura firmware

```
firmware/
├── main.go           ← init periferiche + spawn goroutine
├── gpio_handler.go   ← interrupt GPIO input + debounce + EVT
├── gpio_output.go    ← esegue SET on/off/pulse dalla coda comandi
├── pwm_fan.go        ← controllo LEDC 25kHz duty 0–100%
├── pn532_handler.go  ← loop NFC, driver pn532, IRQ card-present
├── desfire_auth.go   ← 3-pass AES mutual auth + TMAC DESFire EV3
├── usb_handler.go    ← CDC read comandi + write eventi + watchdog
├── key_store.go      ← carica chiavi AES da NVS cifrato
└── go.mod            ← tinygo-org/drivers
```

---

## 4. Goroutine e canali

```go
// main.go
func main() {
    initUSB()
    initGPIO()
    initPWM()
    initNFC()
    loadKeys()

    cmdCh := make(chan Command, 16)  // comandi USB → output GPIO
    evtCh := make(chan string, 16)   // eventi interni → USB out

    go usbRxLoop(cmdCh)             // legge comandi dal Pi (SET/PWM/GET/PING)
    go usbTxLoop(evtCh)             // invia eventi al Pi (EVT/UID-OK/KO/PONG)
    go gpioInputLoop(evtCh)         // interrupt GPIO → EVT px N
    go gpioOutputLoop(cmdCh)        // SET unlockdoor / PWM fan / ecc.
    go nfcLoop(evtCh)               // PN532 + DESFire → UID-OK/KO
    go watchdogLoop()               // safe state se PING assente > 10s
    select {}                       // blocca main (goroutine continuano)
}
```

**Note TinyGo:** le goroutine usano stack di dimensione fissa (default 1KB, configurabile). I canali funzionano ma `select` con molti case può avere overhead maggiore rispetto al Go standard. Tenere le goroutine semplici e i canali piccoli.

---

## 5. GPIO interrupt-driven

```go
// gpio_handler.go

const (
    PIN_P1     = machine.GPIO4
    PIN_P2     = machine.GPIO5
    PIN_P3     = machine.GPIO6
    PIN_ON_OFF = machine.GPIO7
)

var gpioIsrCh = make(chan machine.Pin, 8) // canale ISR → goroutine

func initGPIOInputs() {
    pins := []machine.Pin{PIN_P1, PIN_P2, PIN_P3, PIN_ON_OFF}
    for _, p := range pins {
        p.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
        // ISR: solo notifica sul canale, zero logica
        p.SetInterrupt(machine.PinFalling|machine.PinRising, func(p machine.Pin) {
            select {
            case gpioIsrCh <- p:
            default: // canale pieno: glitch ignorato
            }
        })
    }
}

func gpioInputLoop(evtCh chan<- string) {
    prev := map[machine.Pin]bool{}
    for pin := range gpioIsrCh {
        time.Sleep(5 * time.Millisecond) // debounce software
        val := pin.Get()
        if val == prev[pin] { continue } // glitch, ignora
        prev[pin] = val
        v := 0
        if val { v = 1 }
        evtCh <- fmt.Sprintf("EVT %s %d\n", pinName(pin), v)
    }
}

func pinName(p machine.Pin) string {
    switch p {
    case PIN_P1:     return "p1"
    case PIN_P2:     return "p2"
    case PIN_P3:     return "p3"
    case PIN_ON_OFF: return "on_off"
    default:         return "unknown"
    }
}
```

---

## 6. Controllo output GPIO

```go
// gpio_output.go

const (
    PIN_HEARTBEAT    = machine.GPIO15
    PIN_UNLOCKDOOR   = machine.GPIO16
    PIN_POWER_TABLET = machine.GPIO17
    PIN_LED_POWER    = machine.GPIO18
    PIN_LED_OK       = machine.GPIO14
    PIN_LED_KO       = machine.GPIO21
)

func gpioOutputLoop(cmdCh <-chan Command) {
    for cmd := range cmdCh {
        switch cmd.Type {
        case CmdSet:
            pin := nameToPin(cmd.Name)
            switch cmd.State {
            case "on":
                pin.High()
            case "off":
                pin.Low()
            case "pulse":
                pin.High()
                time.Sleep(200 * time.Millisecond)
                pin.Low()
            }
            usbTxCh <- fmt.Sprintf("ACK %s %s\n", cmd.Name, cmd.State)
        }
    }
}
```

---

## 7. PWM ventola (LEDC 25kHz)

```go
// pwm_fan.go

const PIN_FAN = machine.GPIO8

var fanPWM machine.PWM

func initPWM() {
    fanPWM = machine.PWM0
    fanPWM.Configure(machine.PWMConfig{Period: 40000}) // 25kHz
    ch, _ := fanPWM.Channel(PIN_FAN)
    fanPWM.Set(ch, 0)
}

func setFanDuty(percent int) {
    if percent < 0   { percent = 0   }
    if percent > 100 { percent = 100 }
    ch, _ := fanPWM.Channel(PIN_FAN)
    top := fanPWM.Top()
    fanPWM.Set(ch, uint32(top)*uint32(percent)/100)
}
```

**Circuito:** GPIO8 → resistenza gate 100Ω → IRLZ44N MOSFET → ventola 12V.  
Il MOSFET IRLZ44N è logic-level (Vgs(th) ≈ 2V) — compatibile con GPIO 3.3V.

---

## 8. NFC DESFire EV3 — flusso completo

### 8.1 Loop NFC

```go
// pn532_handler.go

const PIN_NFC_CS  = machine.GPIO10
const PIN_NFC_IRQ = machine.GPIO9

var nfcIrqCh = make(chan struct{}, 1)

func initNFC() {
    // IRQ dal PN532: card present
    PIN_NFC_IRQ.Configure(machine.PinConfig{Mode: machine.PinInputPullup})
    PIN_NFC_IRQ.SetInterrupt(machine.PinFalling, func(machine.Pin) {
        select {
        case nfcIrqCh <- struct{}{}:
        default:
        }
    })
}

func nfcLoop(evtCh chan<- string) {
    nfc := pn532.New(machine.SPI0, PIN_NFC_CS)
    nfc.Configure()

    for {
        <-nfcIrqCh // attendi interrupt card-present

        uid, err := nfc.ReadPassiveTarget(pn532.ISO14443A, 100*time.Millisecond)
        if err != nil { continue }

        // DESFire EV3: UID 7 byte (ISO14443-3 cascade level 2)
        if len(uid) != 7 {
            evtCh <- "UID-KO\n"
            waitCardRemoved(nfc)
            continue
        }

        if err := desfireAuthenticate(nfc, uid); err != nil {
            evtCh <- "UID-KO\n"
        } else {
            evtCh <- "UID-OK\n"
        }

        waitCardRemoved(nfc)
    }
}
```

### 8.2 Autenticazione DESFire EV3 — 3 passi AES

```
1. SelectApplication(AID)
        │
2. GetChallenge (AuthenticateAES, KeyNo=1)
   Tessera risponde con RndB_enc (AES-CBC cifrato)
        │
3. ESP32-S3:
   RndB     = AES_CBC_decrypt(AppKey, RndB_enc)
   RndA     = random_bytes(16)
   token    = AES_CBC_encrypt(AppKey, RndA || rotate_left(RndB, 1))
   Invia token alla tessera
        │
4. Tessera verifica RndB e risponde con:
   response = AES_CBC_encrypt(AppKey, rotate_left(RndA, 1))
        │
5. ESP32-S3 verifica response == rotate_left(RndA, 1)
   → AUTENTICAZIONE MUTUA OK
        │
6. SessionKey = KDF(RndA[0:8] || RndB[0:8] || RndA[8:16] || RndB[8:16])
        │
7. ReadFile(FileNo=1, encrypted)
   Decifra con SessionKey
   Verifica CMAC (TMAC) sulla risposta
        │
8. OK →  evtCh <- "UID-OK\n"
   KO →  evtCh <- "UID-KO\n"  (su qualsiasi errore in qualsiasi step)
```

```go
// desfire_auth.go (schema)

func desfireAuthenticate(nfc *pn532.Device, uid []byte) error {
    if err := nfc.SelectApplication(AID); err != nil {
        return err
    }
    rndBenc, err := nfc.AuthenticateAES(keyNo)
    if err != nil { return err }

    rndB := aesDecryptCBC(appKey, iv, rndBenc)
    rndA := randomBytes(16)
    token := aesEncryptCBC(appKey, iv, append(rndA, rotateLeft(rndB)...))

    response, err := nfc.SendAuthToken(token)
    if err != nil { return err }

    expected := aesEncryptCBC(appKey, iv, rotateLeft(rndA))
    if !bytes.Equal(response, expected) {
        return errAuthFailed
    }

    sessionKey := deriveSessionKey(rndA, rndB)
    return verifyDataFile(nfc, sessionKey)
}
```

**Le chiavi AES non lasciano mai l'ESP32-S3.** Sono caricate da NVS cifrato all'avvio. Nessuna parte del processo crittografico è visibile al Pi.

---

## 9. Watchdog USB

```go
// usb_handler.go

var lastPing time.Time

func usbRxLoop(cmdCh chan<- Command) {
    reader := bufio.NewReader(usbSerial)
    for {
        line, err := reader.ReadString('\n')
        if err != nil { continue }
        line = strings.TrimSpace(line)

        switch {
        case line == "PING":
            lastPing = time.Now()
            usbTxCh <- "PONG\n"
        case strings.HasPrefix(line, "SET "):
            cmdCh <- parseSetCmd(line)
        case strings.HasPrefix(line, "PWM "):
            cmdCh <- parsePWMCmd(line)
        }
    }
}

func watchdogLoop() {
    ticker := time.NewTicker(1 * time.Second)
    for range ticker.C {
        if time.Since(lastPing) > 10*time.Second {
            enterSafeState()
        }
    }
}

func enterSafeState() {
    PIN_UNLOCKDOOR.Low()          // portone chiuso (sicurezza)
    PIN_HEARTBEAT.Low()           // heartbeat spento
    setFanDuty(50)                // ventilazione minima garantita
    // autenticazione NFC in corso → il prossimo ciclo invierà UID-KO
}
```

---

## 10. Protocollo USB — riferimento completo

### 10.1 ESP32-S3 → Pi

```
EVT <pin> <0|1>\n        cambio stato GPIO (0=premuto active-low, 1=rilasciato)
UID-OK\n                 tessera autenticata con successo
UID-KO\n                 tessera rifiutata o errore
PONG\n                   risposta al PING
ACK <pin> <stato>\n      conferma esecuzione SET/PWM
ERR <msg>\n              errore interno
```

### 10.2 Pi → ESP32-S3

```
SET <pin> <on|off|pulse>\n   output digitale
PWM <pin> <0-100>\n          duty cycle (solo pin PWM)
GET <pin>\n                  leggi stato corrente
PING\n                       keepalive (ogni 5s — resetta watchdog)
```

### 10.3 Sequenza di avvio

```
[boot ESP32-S3]
        │
[Pi collega USB]
        │
Pi:  PING\n
ESP: PONG\n
        │  (da questo momento il watchdog è attivo)
        │
ESP: EVT p1 0\n          ← pulsante premuto
Pi:  SET heartbeat on\n  ← LED heartbeat acceso
```

---

## 11. Mappa GPIO ESP32-S3

| Funzione | Dir | GPIO | Circuito esterno |
|----------|-----|------|-----------------|
| Pulsante P1 | IN ISR | GPIO4 | Pull-up interno, active-low |
| Pulsante P2 | IN ISR | GPIO5 | Pull-up interno, active-low |
| Pulsante P3 | IN ISR | GPIO6 | Pull-up interno, active-low |
| On/Off | IN ISR | GPIO7 | Pull-up interno, active-low |
| PN532 IRQ | IN ISR | GPIO9 | Interrupt card-present |
| PN532 CS | OUT | GPIO10 | Active-low |
| PN532 SCK | SPI | GPIO12 | |
| PN532 MISO | SPI | GPIO13 | |
| LED accesso OK | OUT | GPIO14 | 220Ω → LED verde |
| LED Heartbeat | OUT | GPIO15 | 220Ω → LED |
| Relè portone | OUT | GPIO16 | BC547 NPN + 1N4007 |
| Power tablet | OUT | GPIO17 | BC547 NPN |
| LED power | OUT | GPIO18 | 220Ω → LED |
| Ventola PWM | OUT PWM | GPIO8 | IRLZ44N MOSFET |
| LED accesso KO | OUT | GPIO21 | 220Ω → LED rosso |
| PN532 MOSI | SPI | GPIO11 | |

---

## 12. Sicurezza chiavi AES

| Dove | Formato | Protezione |
|------|---------|-----------|
| ESP32-S3 NVS | AES-128, cifrato con Flash Encryption efuse | Illeggibile anche con accesso fisico al chip |
| Tool personalizzazione PC | File `.key` locale | Fuori dal repo git, mai committato, accesso solo admin |
| Canale USB Pi↔ESP32-S3 | Testo in chiaro | Accettabile: link fisico locale. HMAC aggiungibile come Fase 4 |

**Flash Encryption ESP32-S3:** una volta abilitata, il contenuto della flash è cifrato con una chiave AES derivata internamente dall'hardware (eFuse). Il chip non espone la chiave — nemmeno tramite JTAG o DFU.

---

## 13. Roadmap — attività lato ESP32-S3

### Fase 1 — Firmware base
- [ ] Setup progetto TinyGo per ESP32-S3 (`tinygo flash -target esp32s3 ...`)
- [ ] USB CDC funzionante: PING → PONG
- [ ] GPIO interrupt-driven: EVT p1/p2/p3
- [ ] Comandi output: SET on/off/pulse + ACK
- [ ] PWM ventola: comando PWM fan
- [ ] Watchdog: safe state dopo 10s senza PING

### Fase 3 — DESFire EV3
- [ ] PN532 SPI con TinyGo (`tinygo-org/drivers/pn532`)
- [ ] `desfire_auth.go`: SelectApp + 3-pass AES + TMAC verify
- [ ] `key_store.go`: load da NVS cifrato + tool provisioning
- [ ] Test con tessere reali (tool Python su PC con ACR122U)
- [ ] UID-OK → portone apre end-to-end con Pi

### Fase 4 — Hardening
- [ ] Flash Encryption + Secure Boot attivati
- [ ] HMAC SHA-256 sui messaggi USB (anti-replay, chiave condivisa Pi↔ESP32)
- [ ] OTA update firmware via USB (tool Python lato Pi)

---

## Appendice A — Personalizzazione tessere DESFire EV3 (tool Python, PC)

```python
# pip install nfcpy
# Hardware: ACR122U o PN532 USB collegato al PC di amministrazione

import nfc

AID     = bytes([0xA5, 0xC3, 0x01])               # deve coincidere con firmware
APP_KEY = bytes.fromhex("CHIAVE_REALE_32HEX_QUI")  # NON committare nel repo

def personalize(tag):
    # Seleziona (o crea) l'applicazione
    app = nfc.tag.tt4.Application(tag, AID)
    app.select()
    # Autentica con master key di default (prima personalizzazione)
    app.authenticate(key_no=0, key=b'\x00' * 16)
    # Imposta la chiave applicativa
    app.change_key(key_no=1, new_key=APP_KEY)
    print(f"Tessera programmata: UID={tag.identifier.hex().upper()}")

with nfc.ContactlessFrontend('usb') as clf:
    clf.connect(rdwr={'on-connect': personalize})
```

## Appendice B — Compilazione e flash TinyGo

```bash
# Installazione TinyGo (Linux arm64)
wget https://github.com/tinygo-org/tinygo/releases/download/v0.33.0/tinygo0.33.0.linux-arm64.tar.gz
tar -xzf tinygo0.33.0.linux-arm64.tar.gz -C /usr/local

# Flash firmware su ESP32-S3 via USB
cd firmware/
tinygo flash -target esp32s3 -port /dev/ttyACM0 .

# Monitor seriale per debug
tinygo monitor -port /dev/ttyACM0 -baudrate 115200
```

## Appendice C — Schema debounce

```
Fronte ISR  ──►  canale gpioIsrCh  ──►  goroutine gpioInputLoop
                                              │
                                    time.Sleep(5ms)   ← debounce
                                              │
                                    rileggi pin.Get()
                                              │
                               cambio reale? ─┴─ glitch? → ignora
                                    │
                            evtCh <- "EVT px N\n"
```

Il debounce da 5ms gestisce i rimbalzi meccanici tipici dei pulsanti (2–10ms). Se necessario, aumentare a 10ms per pulsanti di qualità inferiore.

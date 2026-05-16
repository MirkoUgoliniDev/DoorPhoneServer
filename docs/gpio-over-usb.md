# GPIO over USB — ESP32-S3 come espansore I/O + Lettore Smartcard

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini  
**Modalità implementazione:** Debug/parallelo — i GPIO fisici del Pi restano attivi

---

## 1. Architettura generale

```
┌─────────────────────────────────────────────────────────┐
│               Raspberry Pi 4                            │
│                                                         │
│  doorphoneserver (Go)                                   │
│  ├── gpio.go         ← GPIO fisici Pi (invariato)       │
│  ├── gpio_usb.go     ← bridge verso ESP32-S3 (nuovo)    │
│  └── smartcard.go    ← gestione accesso smartcard (nuovo)│
│                          │ USB CDC ACM                  │
│                          │ /dev/gpio-esp32              │
└──────────────────────────┼──────────────────────────────┘
                           │
┌──────────────────────────┼──────────────────────────────┐
│          ESP32-S3        │                              │
│                                                         │
│  ┌─────────┐  ┌────────┐  ┌──────────┐  ┌───────────┐ │
│  │ GPIO ISR│  │ PWM    │  │ PN532    │  │ USB CDC   │ │
│  │ inputs  │  │ fan    │  │ NFC/RFID │  │ ACM task  │ │
│  └────┬────┘  └────────┘  └────┬─────┘  └─────┬─────┘ │
│       │   FreeRTOS queues  │              │     │      │
│       └───────────────────►│  gpio_task  ◄─────┘      │
│                             └─────────────────────────► │
└─────────────────────────────────────────────────────────┘
                    │              │         │
               Pulsanti      Ventola    PN532 NFC
               P1/P2/P3       MOSFET    (I2C)
               Relè portone
```

**Modalità debug**: `gpio_usb.go` opera **in parallelo** a `gpio.go`. Gli eventi ricevuti dall'ESP32-S3 vengono loggati e usati per validare le smartcard, ma non sostituiscono il flusso GPIO fisico già funzionante.

---

## 2. Inventario GPIO attuale (Pi)

| Nome XML      | Dir.   | BCM | Descrizione             | Attivo |
|---------------|--------|-----|-------------------------|--------|
| `p1`          | input  | 22  | Pulsante piano 1        | ✓      |
| `p2`          | input  | 27  | Pulsante piano 2        | ✓      |
| `p3`          | input  | 17  | Pulsante piano 3        | ✓      |
| `on_off`      | input  | 18  | Accensione/Spegnimento  | ✓      |
| `heartbeat`   | output | 26  | LED keepalive           | ✓      |
| `unlockdoor`  | output | 5   | Relè portone principale | ✓      |
| `power_tablet`| output | 19  | Alimentazione tablet    | ✓      |
| `led_power`   | output | 23  | LED accensione          | ✗      |
| `fan`         | output | 16  | Ventola raffreddamento  | ✗      |

---

## 3. Hardware

### 3.1 ESP32-S3

| Caratteristica | Valore |
|----------------|--------|
| USB            | OTG nativo (GPIO19/20 = D−/D+), classe CDC ACM |
| GPIO           | 45 pin, PWM hardware su tutti |
| Interfacce     | I2C, SPI, UART, RMT, ADC, DAC |
| Alimentazione  | 5V via USB dal Pi (consumo tipico: 80–150mA) |
| Livelli logici | 3.3V (GPIO non tolleranti 5V) |
| Devboard suggerito | ESP32-S3-DevKitC-1 (USB-C nativo, no adattatori) |

### 3.2 Modulo NFC/RFID — PN532

Il **PN532** è il modulo NFC standard per applicazioni di accesso:

| Caratteristica | Valore |
|----------------|--------|
| Standard supportati | ISO14443A/B, Mifare Classic, Mifare DESFire, NTAG, FeliCa |
| Interfaccia verso ESP32-S3 | I2C (più semplice) o SPI |
| Tensione | 3.3V o 5V (regolatore on-board) |
| Range lettura | 3–7 cm (tipico per tessere accesso) |
| Costo | ~5–12€ (modulo Elechouse o compatibile) |
| Libreria firmware | `esp-idf-lib` PN532 component |

**Alternative più economiche:**
- **RC522** (~2€): solo Mifare Classic/NTAG, SPI. Sufficiente se si usano solo tessere Mifare.
- **PN5180** (~8€): più potente, supporta ISO15693 (tessere HID), ma firmware più complesso.

### 3.3 Mappa GPIO ESP32-S3

| Funzione         | Dir. FW      | GPIO | Note                                     |
|------------------|--------------|------|------------------------------------------|
| Pulsante P1      | INPUT / ISR  | 4    | Pull-up interno, active-low, interrupt   |
| Pulsante P2      | INPUT / ISR  | 5    | Pull-up interno, active-low, interrupt   |
| Pulsante P3      | INPUT / ISR  | 6    | Pull-up interno, active-low, interrupt   |
| On/Off           | INPUT / ISR  | 7    | Pull-up interno, active-low, interrupt   |
| LED Heartbeat    | OUTPUT       | 15   | 220Ω serie → LED 3.3V                    |
| Relè portone     | OUTPUT       | 16   | Transistor NPN (BC547) o optoisolatore   |
| Power tablet     | OUTPUT       | 17   | Transistor NPN                           |
| LED power        | OUTPUT       | 18   | 220Ω serie → LED 3.3V                    |
| Ventola PWM      | OUTPUT / PWM | 8    | MOSFET N-ch (IRLZ44N), 25kHz            |
| PN532 SDA (I2C)  | I2C          | 1    | Pull-up 4.7kΩ su 3.3V                   |
| PN532 SCL (I2C)  | I2C          | 2    | Pull-up 4.7kΩ su 3.3V                   |
| PN532 IRQ        | INPUT / ISR  | 3    | Interrupt "card present" dal PN532       |
| LED accesso verde| OUTPUT       | 10   | Feedback visivo autorizzato              |
| LED accesso rosso| OUTPUT       | 11   | Feedback visivo negato                   |

Pin riservati da evitare: GPIO19/20 (USB), GPIO39–42 (JTAG), GPIO0 (boot mode).

### 3.4 Schema circuito relè portone

```
GPIO16 (3.3V) ──[1kΩ]──► Base BC547
                          Collettore ──► Bobina relè ──► 5V
                          Emettitore ──► GND
                          [Diodo 1N4007 in antiparallelo sulla bobina]
```

### 3.5 Schema controllo ventola PWM

```
GPIO8 (PWM 25kHz) ──[100Ω]──► Gate IRLZ44N
                              Drain ──► Ventola ──► 12V
                              Source ──► GND
                              [Condensatore 100nF Gate-Source]
```

---

## 4. Protocollo USB

### 4.1 Livello fisico

```
Raspberry Pi (host)  ←─── USB CDC ACM ───►  ESP32-S3 (device)
/dev/gpio-esp32, 115200 baud, 8N1, no flow control
```

Canale full-duplex. Il Pi invia **comandi**, l'ESP32-S3 risponde con **ack** e invia **eventi** asincroni.

### 4.2 Messaggi Pi → ESP32-S3

```
SET <pin> <on|off|pulse>\n      — imposta output digitale
PWM <pin> <0-100>\n             — imposta duty cycle ventola
GET <pin>\n                     — leggi stato pin
PING\n                          — keepalive (risposta: PONG)
```

Esempi:
```
SET heartbeat on
SET unlockdoor pulse
SET fan off
PWM fan 75
GET p1
PING
```

### 4.3 Messaggi ESP32-S3 → Pi

```
ACK <pin> <valore>\n                    — conferma SET/PWM
VAL <pin> <valore>\n                    — risposta GET
EVT <pin> <0|1>\n                       — cambio stato GPIO input
EVT CARD_UID <uid_hex> <tipo>\n         — tessera NFC rilevata
EVT CARD_REMOVED\n                      — tessera rimossa
PONG\n                                  — risposta PING
ERR <codice> <msg>\n                    — errore
```

Esempi:
```
ACK heartbeat on
VAL p1 0
EVT p1 0              ← P1 premuto  (active-low → fronte discendente)
EVT p1 1              ← P1 rilasciato
EVT CARD_UID 04A3F211B2 MIFARE_CLASSIC
EVT CARD_REMOVED
PONG
ERR 01 unknown_pin
ERR 02 card_read_fail
```

### 4.4 Gestione `pulse` (relè portone)

Il Pi invia `SET unlockdoor pulse`. L'ESP32-S3 esegue autonomamente la sequenza:
```
GPIO16 → HIGH  (relay ON)
vTaskDelay(500ms)
GPIO16 → LOW   (relay OFF)
```
Il Pi non gestisce timing. Questa è la stessa logica già presente in `gpio.go` (`GPIOOutPin` con comando `"pulse"`), semplicemente spostata in firmware.

### 4.5 Watchdog

Se l'ESP32-S3 non riceve `PING` entro **10 secondi**:
- `unlockdoor` → OFF (portone chiuso — sicurezza)
- `heartbeat` → OFF
- `fan` → PWM 50% (ventilazione minima)
- `power_tablet` → invariato

---

## 5. Firmware ESP32-S3

### 5.1 Stack e task FreeRTOS

```
┌─────────────────────────────────────────────────────────┐
│ app_main                                                │
│   ├── usb_cdc_init()     ← TinyUSB CDC                 │
│   ├── gpio_init()        ← pin config + ISR install    │
│   ├── pwm_init()         ← LEDC 25kHz                  │
│   ├── nfc_init()         ← PN532 I2C                   │
│   │                                                     │
│   ├── xTaskCreate(usb_rx_task,  2048, 5)  ← comandi   │
│   ├── xTaskCreate(gpio_task,    2048, 4)  ← output     │
│   ├── xTaskCreate(nfc_task,     4096, 3)  ← smartcard  │
│   └── xTaskCreate(watchdog_task, 1024, 6) ← sicurezza  │
└─────────────────────────────────────────────────────────┘
```

### 5.2 GPIO interrupt-driven (INPUT)

Anziché polling, si usano **interrupt hardware** del ESP32-S3. Latenza di rilevamento: <100µs (vs 150ms del polling attuale sul Pi).

```c
static void IRAM_ATTR gpio_isr_handler(void *arg) {
    uint32_t pin = (uint32_t)arg;
    BaseType_t woken = pdFALSE;
    gpio_event_t evt = {.pin = pin, .level = gpio_get_level(pin)};
    xQueueSendFromISR(gpio_evt_queue, &evt, &woken);
    if (woken) portYIELD_FROM_ISR();
}

void gpio_init(void) {
    gpio_config_t cfg = {
        .pin_bit_mask = INPUT_PIN_MASK,
        .mode         = GPIO_MODE_INPUT,
        .pull_up_en   = GPIO_PULLUP_ENABLE,
        .intr_type    = GPIO_INTR_ANYEDGE,   // fronte salita E discesa
    };
    gpio_config(&cfg);
    gpio_install_isr_service(0);
    // Registra ISR per ogni pin di input
    gpio_isr_handler_add(GPIO_P1,     gpio_isr_handler, (void*)GPIO_P1);
    gpio_isr_handler_add(GPIO_P2,     gpio_isr_handler, (void*)GPIO_P2);
    gpio_isr_handler_add(GPIO_P3,     gpio_isr_handler, (void*)GPIO_P3);
    gpio_isr_handler_add(GPIO_ON_OFF, gpio_isr_handler, (void*)GPIO_ON_OFF);
}

void gpio_task(void *arg) {
    gpio_event_t evt;
    for (;;) {
        if (xQueueReceive(gpio_evt_queue, &evt, pdMS_TO_TICKS(100)) == pdTRUE) {
            // Debounce software: riletto dopo 5ms per conferma
            vTaskDelay(pdMS_TO_TICKS(5));
            uint8_t confirmed = gpio_get_level(evt.pin);
            if (confirmed == evt.level) {
                char msg[32];
                snprintf(msg, sizeof(msg), "EVT %s %d\n",
                         pin_name(evt.pin), evt.level);
                usb_cdc_write(msg);
            }
        }
    }
}
```

### 5.3 PWM ventola (LEDC)

Il modulo LEDC dell'ESP32-S3 permette PWM hardware preciso senza occupare la CPU.

```c
void pwm_init(void) {
    ledc_timer_config_t timer = {
        .speed_mode      = LEDC_LOW_SPEED_MODE,
        .duty_resolution = LEDC_TIMER_10_BIT,   // 0–1023
        .timer_num       = LEDC_TIMER_0,
        .freq_hz         = 25000,                // 25kHz — silenzioso per ventole 4-pin
        .clk_cfg         = LEDC_AUTO_CLK,
    };
    ledc_timer_config(&timer);

    ledc_channel_config_t ch = {
        .gpio_num   = GPIO_FAN,
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .channel    = LEDC_CHANNEL_0,
        .timer_sel  = LEDC_TIMER_0,
        .duty       = 0,
        .hpoint     = 0,
    };
    ledc_channel_config(&ch);
}

void set_fan_pwm(uint8_t percent) {  // 0–100
    uint32_t duty = (percent * 1023) / 100;
    ledc_set_duty(LEDC_LOW_SPEED_MODE, LEDC_CHANNEL_0, duty);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, LEDC_CHANNEL_0);
}
```

### 5.4 Lettore NFC/RFID (PN532)

```c
void nfc_task(void *arg) {
    pn532_init_i2c(GPIO_SDA, GPIO_SCL);   // Init PN532 via I2C
    pn532_wake_up();
    pn532_SAMConfiguration();             // Passive mode

    uint8_t uid[7];
    uint8_t uid_len;
    for (;;) {
        // IRQ pin LOW = carta presente (interrupt-driven, non blocking poll)
        if (xSemaphoreTake(nfc_irq_sem, pdMS_TO_TICKS(500)) == pdTRUE) {
            if (pn532_readPassiveTargetID(PN532_MIFARE_ISO14443A,
                                          uid, &uid_len, 100)) {
                // Costruisci stringa UID hex
                char uid_str[15] = {0};
                for (int i = 0; i < uid_len; i++)
                    snprintf(uid_str + i*2, 3, "%02X", uid[i]);

                // Identifica tipo carta
                const char *card_type = uid_len == 4 ? "MIFARE_CLASSIC"
                                      : uid_len == 7 ? "MIFARE_ULTRALIGHT"
                                      : "UNKNOWN";

                // Invia evento al Pi
                char msg[64];
                snprintf(msg, sizeof(msg), "EVT CARD_UID %s %s\n",
                         uid_str, card_type);
                usb_cdc_write(msg);

                // Attendi rimozione carta
                while (pn532_readPassiveTargetID(PN532_MIFARE_ISO14443A,
                                                  uid, &uid_len, 50)) {
                    vTaskDelay(pdMS_TO_TICKS(100));
                }
                usb_cdc_write("EVT CARD_REMOVED\n");
            }
        }
    }
}

// ISR per GPIO IRQ del PN532
static void IRAM_ATTR nfc_irq_handler(void *arg) {
    BaseType_t woken = pdFALSE;
    xSemaphoreGiveFromISR(nfc_irq_sem, &woken);
    if (woken) portYIELD_FROM_ISR();
}
```

---

## 6. Lato Pi — Go

### 6.1 File nuovi (modalità debug/parallelo)

```
gpio_usb.go      — apre /dev/gpio-esp32, legge eventi, logga
                   per ora non influenza il flusso GPIO fisico
smartcard.go     — riceve EVT CARD_UID, valida UID, comanda relè
```

`gpio.go` resta invariato — continua a gestire i GPIO fisici del Pi.

### 6.2 gpio_usb.go (struttura)

```go
// Avviato in una goroutine separata da initGPIO() o dall'init dell'applicazione.
// In modalità debug: logga tutto, non interferisce con gpio.go.
func (b *DoorPhoneServer) runUSBGPIODebug(ctx context.Context) {
    port, err := openSerialWithRetry("/dev/gpio-esp32", 115200)
    if err != nil {
        log.Printf("warn: ESP32-S3 non disponibile: %v", err)
        return
    }
    defer port.Close()

    go b.usbPingLoop(port, ctx)     // invia PING ogni 5s
    b.usbReadLoop(port, ctx)        // legge eventi
}

func (b *DoorPhoneServer) usbReadLoop(port io.Reader, ctx context.Context) {
    scanner := bufio.NewScanner(port)
    for scanner.Scan() {
        select {
        case <-ctx.Done():
            return
        default:
        }
        line := strings.TrimSpace(scanner.Text())
        log.Printf("[USB-DEBUG] ricevuto: %s", line)

        fields := strings.Fields(line)
        if len(fields) < 2 { continue }

        switch fields[0] {
        case "EVT":
            b.handleUSBEvent(fields[1:])
        case "PONG":
            // keepalive ok
        case "ERR":
            log.Printf("[USB-DEBUG] errore ESP32: %s", line)
        }
    }
}

func (b *DoorPhoneServer) handleUSBEvent(fields []string) {
    switch fields[0] {
    case "CARD_UID":
        if len(fields) >= 2 {
            uid := fields[1]
            cardType := ""
            if len(fields) >= 3 { cardType = fields[2] }
            b.handleCardPresented(uid, cardType)
        }
    case "CARD_REMOVED":
        log.Printf("[USB-DEBUG] tessera rimossa")
    default:
        // Evento GPIO: "p1 0", "p2 1", ecc.
        pin, val := fields[0], fields[1]
        log.Printf("[USB-DEBUG] GPIO event: pin=%s val=%s", pin, val)
        // In debug mode: solo log. In produzione chiamerebbe b.cmdRingPiano(pin)
    }
}
```

### 6.3 smartcard.go — flusso validazione

```
ESP32-S3: EVT CARD_UID 04A3F211B2 MIFARE_CLASSIC
                    │
                    ▼
       smartcard.handleCardPresented(uid, type)
                    │
                    ▼
       Leggi preferences/smartcards.json
       { "04A3F211B2": {"name":"Mario Rossi","floor":"all"}, ... }
                    │
           ┌────────┴────────┐
           │ UID trovato?    │
          YES               NO
           │                 │
           ▼                 ▼
    GPIOOutPin(           Log accesso negato
    "unlockdoor",         SET access_led red (ESP32)
    "pulse")              Pushover notify (opzionale)
    SET access_led green
    Log accesso OK
```

Il file `preferences/smartcards.json` contiene la lista dei UID autorizzati e verrà gestito dalla stessa infrastruttura di `preferences/alarms.json`.

### 6.4 Struttura `preferences/smartcards.json`

```json
{
  "04A3F211B2": {
    "name": "Mario Rossi",
    "floors": ["P1", "P2", "P3"],
    "enabled": true,
    "note": "Proprietario appartamento 3"
  },
  "04B9C322D1": {
    "name": "Anna Verdi",
    "floors": ["P2"],
    "enabled": true,
    "note": "Affittuario piano 2"
  }
}
```

### 6.5 Gestione porta seriale con riconnessione

```go
func openSerialWithRetry(path string, baud int) (io.ReadWriteCloser, error) {
    for {
        port, err := serial.Open(path, &serial.Mode{BaudRate: baud})
        if err == nil {
            log.Printf("info: ESP32-S3 connesso su %s", path)
            return port, nil
        }
        log.Printf("warn: ESP32-S3 non raggiungibile (%v), riprovo in 10s...", err)
        time.Sleep(10 * time.Second)
    }
}
```

Se il cavo USB viene scollegato, `usbReadLoop` esce, la goroutine viene riavviata con `openSerialWithRetry` — nessun crash dell'applicazione principale.

---

## 7. Stima di fattibilità

### Complessità totale: MEDIA

| Componente | Difficoltà | Dipendenze |
|------------|------------|------------|
| Firmware ESP32-S3 base (GPIO ISR + PWM + USB CDC) | Media | ESP-IDF v5.x |
| Firmware PN532 I2C + NFC task | Media | `esp-idf-lib` PN532 |
| `gpio_usb.go` debug mode | Bassa | `go.bug.st/serial` |
| `smartcard.go` validazione UID | Bassa | — |
| Regola udev symlink | Minima | — |
| Hardware e cablaggio | Bassa | PN532 + transistori/MOSFET |

### Stima ore di lavoro (modalità debug)

| Attività | Ore |
|----------|-----|
| Setup ESP-IDF, USB CDC funzionante | 2–3 h |
| GPIO interrupt + PWM fan | 3–4 h |
| PN532 I2C + NFC task | 3–5 h |
| `gpio_usb.go` + `smartcard.go` (Go) | 4–6 h |
| `smartcards.json` + logica validazione | 2–3 h |
| Regola udev + test end-to-end | 2–3 h |
| **Totale modalità debug** | **16–24 h** |
| Migrazione produzione (post-debug) | +8–12 h |

### Rischi

| Rischio | Prob. | Mitigazione |
|---------|-------|-------------|
| PN532 I2C inaffidabile (address conflict) | Media | Usare modalità SPI come fallback |
| `/dev/ttyACM0` cambia ordine al reboot | Media | Regola udev VID/PID → `/dev/gpio-esp32` |
| Latenza lettura NFC > 500ms | Bassa | PN532 in modalità interrupt (IRQ pin), non polling |
| Tessera non riconosciuta (tipo non supportato) | Media | Testare con le tessere effettive, PN532 supporta tutti i tipi comuni |
| Riconnessione USB lenta dopo reboot Pi | Bassa | `openSerialWithRetry` con backoff |

---

## 8. Roadmap implementazione

### Fase 1 — Debug (branch corrente GPIO-OVER-USB)
- [ ] Progetto firmware ESP-IDF: USB CDC, GPIO ISR, PWM fan
- [ ] PN532 I2C + NFC task + EVT CARD_UID
- [ ] `gpio_usb.go`: connessione seriale, lettura eventi, log
- [ ] Regola udev `/dev/gpio-esp32`
- [ ] Test: premere pulsante → log su Pi; avvicinare tessera → log UID

### Fase 2 — Smartcard accesso
- [ ] `smartcard.go`: lettura `smartcards.json`, validazione UID
- [ ] Integrazione con `GPIOOutPin("unlockdoor", "pulse")`
- [ ] LED verde/rosso ESP32-S3 per feedback visivo
- [ ] Log accessi (chi, quando, esito)
- [ ] Test end-to-end: tessera autorizzata → portone apre

### Fase 3 — Migrazione produzione (opzionale, post-validazione)
- [ ] `gpio_usb.go` diventa il backend primario (sostituisce `gpio.go`)
- [ ] `gpio.go` rimosso o lasciato come fallback
- [ ] Aggiornamento `doorphoneserver.xml` con `type="usb"`

---

## Appendice A — Regola udev

```udev
# /etc/udev/rules.d/99-gpio-esp32.rules
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", \
    SYMLINK+="gpio-esp32", MODE="0660", GROUP="dialout"
```

`303a:1001` = Espressif ESP32-S3 CDC ACM. Verificare VID/PID con `lsusb` sul devboard specifico — alcuni venditori usano PID diversi.

Dopo aver salvato la regola: `sudo udevadm control --reload-rules && sudo udevadm trigger`

## Appendice B — Dipendenze Go da aggiungere

```bash
go get go.bug.st/serial          # porta seriale cross-platform
```

## Appendice C — Struttura progetto firmware

```
firmware/
├── main/
│   ├── main.c            ← app_main, init task
│   ├── gpio_handler.c    ← ISR, gpio_task
│   ├── pwm_fan.c         ← LEDC init + set_fan_pwm()
│   ├── nfc_pn532.c       ← I2C PN532, nfc_task
│   ├── usb_cdc.c         ← TinyUSB CDC, usb_rx_task
│   ├── protocol.c        ← parser comandi, serializer eventi
│   └── watchdog.c        ← watchdog_task, safe_state()
├── CMakeLists.txt
├── sdkconfig.defaults    ← USB OTG abilitato, stack size
└── partitions.csv
```

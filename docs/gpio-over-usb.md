# GPIO over USB — ESP32-S3 come espansore I/O

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini

---

## 1. Motivazione

L'implementazione attuale usa l'interfaccia sysfs GPIO del kernel Linux direttamente dal Raspberry Pi. Questa soluzione presenta alcuni limiti:

- **sysfs GPIO è deprecato** dal kernel 5.x (sostituito da `/dev/gpiochipN` + libgpiod). Il workaround con `gpioSysfsOffset` (base 512 su kernel 6.x) è fragile e dipende dalla numerazione interna del kernel.
- **Jitter OS**: il polling GPIO da userspace su un OS general-purpose introduce latenza variabile (decine di ms). Per il debounce dei pulsanti si compensa con un delay fisso da 150ms, ma non è deterministico.
- **Cablaggio rigido**: i pin BCM del Pi sono fissi sul connettore a 40 pin. Un'espansione hardware richiede di toccare il connettore.
- **Fan PWM**: il controllo PWM della ventola tramite sysfs GPIO è grezzo (on/off), non modulato.

Spostare la gestione GPIO su un **ESP32-S3 collegato via USB** risolve tutti questi punti mantenendo la compatibilità con la configurazione XML esistente.

---

## 2. Inventario GPIO attuale

Dal file `doorphoneserver.xml`:

| Nome XML     | Direzione | BCM | Descrizione              | Abilitato |
|--------------|-----------|-----|--------------------------|-----------|
| `p1`         | input     | 22  | Pulsante piano 1         | ✓         |
| `p2`         | input     | 27  | Pulsante piano 2         | ✓         |
| `p3`         | input     | 17  | Pulsante piano 3         | ✓         |
| `on_off`     | input     | 18  | Accensione/Spegnimento   | ✓         |
| `heartbeat`  | output    | 26  | LED keepalive            | ✓         |
| `unlockdoor` | output    | 5   | Relè portone principale  | ✓         |
| `power_tablet`| output   | 19  | Alimentazione tablet     | ✓         |
| `led_power`  | output    | 23  | LED accensione           | ✗         |
| `fan`        | output    | 16  | Ventola raffreddamento   | ✗         |

**Totale: 4 input + 5 output = 9 pin** (7 attivi, 2 disabilitati).

---

## 3. Hardware proposto

### ESP32-S3

Il modulo **ESP32-S3** è la scelta naturale per questo progetto:

- **USB OTG nativo** via GPIO19/GPIO20 (D−/D+) senza chip USB-to-UART esterno
- Su Linux appare come `/dev/ttyACM0` (classe CDC ACM) — nessun driver da installare
- 45 GPIO programmabili, supporto PWM hardware su tutti i pin
- Alimentato direttamente via USB dal Pi (5V, max 500mA — sufficiente per ESP32-S3 + logica)
- Tolleranza 3.3V sui GPIO (attenzione: relè e LED esterni potrebbero richiedere transistor/optoisolatore)

### Mappa GPIO ESP32-S3 proposta

| Funzione        | Direzione FW | GPIO ESP32-S3 | Note                          |
|-----------------|--------------|---------------|-------------------------------|
| Pulsante P1     | INPUT        | GPIO4         | Pull-up interno, active-low   |
| Pulsante P2     | INPUT        | GPIO5         | Pull-up interno, active-low   |
| Pulsante P3     | INPUT        | GPIO6         | Pull-up interno, active-low   |
| On/Off          | INPUT        | GPIO7         | Pull-up interno, active-low   |
| LED Heartbeat   | OUTPUT       | GPIO15        | 3.3V, resistore serie 220Ω    |
| Relè portone    | OUTPUT       | GPIO16        | Tramite transistor NPN / optoisolatore |
| Power tablet    | OUTPUT       | GPIO17        | Tramite transistor NPN        |
| LED power       | OUTPUT       | GPIO18        | 3.3V, resistore serie 220Ω    |
| Ventola (PWM)   | OUTPUT PWM   | GPIO8         | PWM 25kHz, MOSFET N-ch        |

I numeri GPIO ESP32-S3 sono orientativi — la mappa definitiva dipende dal PCB/devboard scelto evitando i pin riservati a USB (GPIO19/20) e JTAG (GPIO39–42).

---

## 4. Protocollo USB

### Livello fisico

```
Raspberry Pi (USB host)  ←→  ESP32-S3 (USB device, CDC ACM)
/dev/ttyACM0, 115200 baud, 8N1
```

Il canale è full-duplex. Il Pi invia **comandi**, l'ESP32-S3 risponde con **ack** e invia **eventi** asincroni quando cambia lo stato degli input.

### Formato messaggi (testo, terminati da `\n`)

#### Pi → ESP32-S3 (comandi)

```
SET <nome> <valore>\n
GET <nome>\n
PWM <nome> <duty_0-100>\n
PING\n
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

#### ESP32-S3 → Pi (risposte e eventi)

```
ACK <nome> <valore>\n       — risposta a SET/PWM
VAL <nome> <valore>\n       — risposta a GET
EVT <nome> <valore>\n       — evento asincrono (cambio stato input)
PONG\n                      — risposta a PING
ERR <codice> <messaggio>\n  — errore
```

Esempi:
```
ACK heartbeat on
VAL p1 0
EVT p1 0           ← pulsante P1 premuto (active-low)
EVT p1 1           ← pulsante P1 rilasciato
PONG
ERR 01 unknown_pin
```

#### Valori

| Tipo        | Valori           |
|-------------|------------------|
| Digitale    | `0` / `1`        |
| Named       | `on` / `off` / `pulse` |
| PWM duty    | `0`–`100` (%)    |

#### Comando `pulse`

L'ESP32-S3 gestisce autonomamente la temporizzazione del pulse (es. relè portone: LOW 50ms → HIGH 500ms → LOW 50ms) con parametri configurati via firmware. Il Pi non deve gestire timing critici.

---

## 5. Firmware ESP32-S3 (schema)

### Stack

- **Framework**: ESP-IDF v5.x (o Arduino con libreria USB CDC ufficiale)
- **Componente USB**: `tinyusb` (incluso in ESP-IDF), device class CDC ACM
- **Task RTOS**: 3 task FreeRTOS
  - `usb_rx_task` — legge comandi dalla USB, li accoda
  - `gpio_task` — esegue comandi GPIO, legge input, invia eventi
  - `heartbeat_task` — watchdog: se nessun PING in 10s, resetta gli output a stato sicuro

### Pseudocodice GPIO task

```c
void gpio_task(void *arg) {
    uint8_t prev_state[NUM_INPUTS] = {1, 1, 1, 1};  // pull-up, active-low
    for (;;) {
        // Processa comandi in coda
        gpio_cmd_t cmd;
        while (xQueueReceive(cmd_queue, &cmd, 0) == pdTRUE) {
            execute_command(&cmd);
        }
        // Leggi input con debounce hardware (lettura 2 volte a 5ms di distanza)
        for (int i = 0; i < NUM_INPUTS; i++) {
            uint8_t state = gpio_get_level(input_pins[i]);
            if (state != prev_state[i]) {
                vTaskDelay(pdMS_TO_TICKS(5));
                if (gpio_get_level(input_pins[i]) == state) {
                    prev_state[i] = state;
                    send_event(input_names[i], state);
                }
            }
        }
        vTaskDelay(pdMS_TO_TICKS(10));  // poll a 100Hz
    }
}
```

### Stato sicuro (watchdog)

Se il Pi non invia `PING` entro 10 secondi (es. crash o riavvio):
- `heartbeat` → OFF
- `unlockdoor` → OFF (portone chiuso)
- `fan` → PWM 50% (ventilazione minima di sicurezza)
- `power_tablet` → invariato

---

## 6. Modifiche lato Pi (Go)

### Nuovo file `gpio_usb.go`

Sostituisce `gpio.go` mantenendo la stessa interfaccia pubblica (`GPIOOutPin`, `GPIOOutAll`, `initGPIO`, etc.) in modo che il resto del codice non cambi.

```
gpio.go      →  gpio_usb.go   (nuova implementazione seriale)
             +  gpio_serial.go (gestione porta seriale + parser protocollo)
```

Dipendenza Go aggiuntiva: `github.com/tarm/serial` (o `go.bug.st/serial`) per la porta seriale.

### Logica evento input

```
ESP32-S3 invia: EVT p1 0
           ↓
gpio_serial.go riceve, parsa, mette in canale Go
           ↓
gpio_usb.go goroutine legge dal canale
           ↓
chiama b.cmdRingPiano("P1")  ← stessa logica attuale
```

### Compatibilità XML

La configurazione in `doorphoneserver.xml` resta **invariata** — i nomi dei pin (`p1`, `heartbeat`, `unlockdoor`, etc.) vengono usati direttamente nel protocollo USB. Aggiungere solo un attributo `type="usb"` per distinguere i pin USB da eventuali GPIO diretti residui:

```xml
<pin name="p1" direction="input" device="pushbutton" type="usb" enabled="true" .../>
```

---

## 7. Stima di fattibilità

### Complessità tecnica: MEDIA

| Componente          | Difficoltà | Note |
|---------------------|------------|------|
| Firmware ESP32-S3   | Media      | ESP-IDF CDC ACM ben documentato; gestione GPIO banale |
| gpio_usb.go (Go)    | Bassa      | Lettura seriale + parser semplice; interfaccia pubblica invariata |
| gpio_serial.go (Go) | Bassa-Media| Goroutine read loop + canali Go |
| Hardware/cablaggio  | Bassa      | ESP32-S3 devboard + fili; circuiti opzionali per relè/fan |
| Test e debug        | Media      | Bisogna testare watchdog, reconnect USB, edge case |

### Stima ore di lavoro

| Attività                            | Ore stimate |
|-------------------------------------|-------------|
| Firmware ESP32-S3 (base)            | 4–6 h       |
| Firmware: watchdog + PWM fan        | 2–3 h       |
| Go: gpio_usb.go + gpio_serial.go    | 4–6 h       |
| Test integrazione Pi ↔ ESP32-S3     | 3–4 h       |
| Cablaggio e verifica hardware       | 2–3 h       |
| **Totale**                          | **15–22 h** |

### Rischi

| Rischio | Probabilità | Mitigazione |
|---------|-------------|-------------|
| `/dev/ttyACM0` cambia nome al reboot | Media | Usare regola udev con VID/PID ESP32-S3 per symlink fisso `/dev/gpio-esp32` |
| Latenza USB > attesa per relè portone | Bassa | CDC ACM ha latenza <5ms; pulse gestito in firmware |
| ESP32-S3 si resetta durante il pulse del relè | Bassa | Watchdog hardware ESP32 + stato sicuro firmware |
| Conflitto USB se Pi ha altri device CDC | Bassa | Regola udev su VID:PID specifico |
| 3.3V GPIO insufficienti per relè/fan | Alta | Già richiede transistor/optoisolatore — invariato rispetto a oggi |

### Pro/Contro rispetto a GPIO diretto

| | GPIO diretto (attuale) | GPIO over USB (proposto) |
|---|---|---|
| Complessità setup | Bassa | Media (richiede ESP32-S3) |
| Robustezza kernel | Fragile (sysfs deprecato) | Stabile (porta seriale standard) |
| Debounce | Software (150ms fisso) | Hardware in firmware (5ms preciso) |
| PWM ventola | No (on/off) | Sì (0–100%, 25kHz) |
| Isolamento elettrico | No | Parziale (USB ≠ isolamento galvanico, ma separazione logica) |
| Recovery da crash Pi | — | Watchdog firmware porta output a stato sicuro |
| Latenza input→evento | ~150ms (debounce) | ~15ms (debounce 5ms × 2 + USB) |
| Costo hardware | 0 (pin già sul Pi) | ~5–10€ (ESP32-S3 devboard) |

---

## 8. Prossimi passi

1. **Scegliere il devboard** — ESP32-S3-DevKitC-1 (USB-C nativo) o modulo custom
2. **Creare il progetto firmware** — struttura ESP-IDF, configurare USB CDC, GPIO task
3. **Scrivere `gpio_serial.go`** — gestione porta seriale con reconnect automatico
4. **Scrivere `gpio_usb.go`** — rimpiazzo drop-in di `gpio.go`
5. **Aggiungere regola udev** su Pi per symlink stabile `/dev/gpio-esp32`
6. **Test end-to-end** — pulsanti, relè, LED heartbeat, PWM fan
7. **Aggiornare `doorphoneserver.xml`** — aggiungere `type="usb"` ai pin

---

## Appendice — Regola udev

```udev
# /etc/udev/rules.d/99-gpio-esp32.rules
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", \
    SYMLINK+="gpio-esp32", MODE="0660", GROUP="dialout"
```

VID `303a` = Espressif Systems, PID `1001` = ESP32-S3 CDC ACM (verificare con `lsusb` sul devboard specifico).

Il codice Go usa `/dev/gpio-esp32` come path fisso — non dipende dall'ordine di enumerazione USB.

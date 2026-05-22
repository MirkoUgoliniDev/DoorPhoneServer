# GPIO over USB — Lato ESP32-S3 (ESP-IDF v5.x / 6.x)

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini  
**Toolchain:** ESP-IDF v5.2+ / v6.x, C99  
**Documento correlato:** [gpio-over-usb-rpi-side.md](gpio-over-usb-rpi-side.md)

---

## 1. Ruolo dell'ESP32-S3

L'ESP32-S3 è il **layer hardware**: gestisce tutti i circuiti fisici e i protocolli crittografici. Il Raspberry Pi riceve solo stringhe di testo — non conosce nulla di GPIO, NFC o DESFire.

**Gestisce in autonomia:**
- Pulsanti con interrupt GPIO + debounce via timer
- Output digitali (relè portone, LED, alimentazione tablet)
- Ventola PWM 25kHz via LEDC
- Lettore NFC PN532 via SPI + autenticazione DESFire EV3 (3-pass AES-128)
- Watchdog USB: safe state se PING assente > 10s

---

## 2. Stack tecnologico

| Componente | ESP-IDF API | Note |
|------------|------------|------|
| USB CDC ACM | `tinyusb` + `tusb_cdc_acm` | Appare come `/dev/ttyACM*` su Linux, symlink stabile `/dev/esp32` via udev |
| GPIO interrupt | `driver/gpio.h` | `gpio_isr_handler_add()` |
| Timer debounce | `esp_timer.h` | One-shot timer per pin |
| PWM ventola | `driver/ledc.h` | LEDC timer 25kHz |
| SPI PN532 | `driver/spi_master.h` | SPI2_HOST (HSPI) |
| Crittografia AES | `mbedtls/aes.h` | AES-128 CBC, acceleratore HW |
| NVS chiavi | `nvs_flash.h` / `nvs.h` | Namespace cifrato |
| Task / Code | FreeRTOS | `xTaskCreate`, `xQueueCreate` |

---

## 3. Struttura progetto

```
firmware/
├── CMakeLists.txt
├── sdkconfig.defaults          ← USB CDC abilitato, flash encryption
├── partitions.csv              ← NVS partition dedicata alle chiavi
└── main/
    ├── CMakeLists.txt
    ├── main.c                  ← init periferiche + spawn task
    ├── usb_cdc.c / .h          ← USB CDC read/write + watchdog
    ├── gpio_input.c / .h       ← interrupt + debounce + EVT
    ├── gpio_output.c / .h      ← SET on/off/pulse + ACK
    ├── pwm_fan.c / .h          ← LEDC 25kHz duty 0–100%
    ├── pn532.c / .h            ← driver SPI PN532
    ├── desfire_auth.c / .h     ← 3-pass AES-128 DESFire EV3
    └── key_store.c / .h        ← load/store chiavi AES da NVS
```

---

## 4. Mappa GPIO

| Funzione | Dir | GPIO | Circuito esterno |
|----------|-----|------|-----------------|
| Pulsante P1 | IN ISR | GPIO4 | Pull-up interno, active-low |
| Pulsante P2 | IN ISR | GPIO5 | Pull-up interno, active-low |
| Pulsante P3 | IN ISR | GPIO6 | Pull-up interno, active-low |
| On/Off | IN ISR | GPIO7 | Pull-up interno, active-low |
| PN532 IRQ | IN ISR | GPIO9 | Interrupt card-present |
| PN532 CS | OUT | GPIO10 | Active-low |
| PN532 MOSI | SPI | GPIO11 | SPI2_HOST |
| PN532 SCK | SPI | GPIO12 | SPI2_HOST |
| PN532 MISO | SPI | GPIO13 | SPI2_HOST |
| LED accesso OK | OUT | GPIO14 | 220Ω → LED verde |
| LED Heartbeat | OUT | GPIO15 | 220Ω → LED |
| Relè portone | OUT | GPIO16 | BC547 NPN + 1N4007 |
| Power tablet | OUT | GPIO17 | BC547 NPN |
| LED power | OUT | GPIO18 | 220Ω → LED |
| LED accesso KO | OUT | GPIO21 | 220Ω → LED rosso |
| Ventola PWM | OUT PWM | GPIO8 | IRLZ44N MOSFET gate |

---

## 5. `main.c` — init e task

```c
// main/main.c
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "nvs_flash.h"
#include "usb_cdc.h"
#include "gpio_input.h"
#include "gpio_output.h"
#include "pwm_fan.h"
#include "pn532.h"
#include "desfire_auth.h"
#include "key_store.h"

// Code condivise tra task
QueueHandle_t cmd_queue;   // comandi USB → gpio_output
QueueHandle_t evt_queue;   // eventi GPIO/NFC → usb_tx

void app_main(void)
{
    // NVS: necessario per WiFi/BT e key_store
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        nvs_flash_erase();
        nvs_flash_init();
    }

    cmd_queue = xQueueCreate(16, sizeof(cmd_t));
    evt_queue = xQueueCreate(16, sizeof(char[64]));

    key_store_load();       // carica chiavi AES da NVS cifrato
    usb_cdc_init();         // inizializza TinyUSB CDC
    gpio_input_init();      // configura GPIO input + ISR
    gpio_output_init();     // configura GPIO output
    pwm_fan_init();         // LEDC 25kHz
    pn532_init();           // SPI + reset PN532

    // Spawn task FreeRTOS
    xTaskCreate(task_usb_rx,     "usb_rx",     4096, NULL, 5, NULL);
    xTaskCreate(task_usb_tx,     "usb_tx",     4096, NULL, 5, NULL);
    xTaskCreate(task_gpio_input, "gpio_in",    2048, NULL, 4, NULL);
    xTaskCreate(task_gpio_output,"gpio_out",   2048, NULL, 4, NULL);
    xTaskCreate(task_nfc,        "nfc",        8192, NULL, 3, NULL);
    xTaskCreate(task_watchdog,   "watchdog",   2048, NULL, 6, NULL);
}
```

---

## 6. `usb_cdc.c` — USB CDC + parser comandi + watchdog

```c
// main/usb_cdc.c
#include <string.h>
#include <stdio.h>
#include "tinyusb.h"
#include "tusb_cdc_acm.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "esp_timer.h"
#include "usb_cdc.h"
#include "gpio_output.h"
#include "pwm_fan.h"

#define WATCHDOG_TIMEOUT_MS  10000

static volatile int64_t last_ping_us = 0;

void usb_cdc_init(void)
{
    tinyusb_config_t tusb_cfg = {
        .device_descriptor = NULL,  // usa descriptor di default ESP-IDF
        .string_descriptor = NULL,
        .external_phy = false,
    };
    tinyusb_driver_install(&tusb_cfg);

    tinyusb_config_cdcacm_t acm_cfg = {
        .usb_dev = TINYUSB_USBDEV_0,
        .cdc_port = TINYUSB_CDC_ACM_0,
        .rx_unread_buf_sz = 256,
        .callback_rx = NULL,        // usiamo lettura polling in task_usb_rx
        .callback_rx_wanted_char = NULL,
        .callback_line_state_changed = NULL,
        .callback_line_coding_changed = NULL,
    };
    tusb_cdc_acm_init(&acm_cfg);
}

// Invia una stringa sull'USB CDC (chiamata da qualsiasi task tramite evt_queue)
void usb_send(const char *msg)
{
    size_t len = strlen(msg);
    tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (const uint8_t *)msg, len);
    tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, pdMS_TO_TICKS(10));
}

// Task TX: preleva eventi dalla coda e li invia via USB
void task_usb_tx(void *arg)
{
    char msg[64];
    for (;;) {
        if (xQueueReceive(evt_queue, msg, portMAX_DELAY) == pdTRUE) {
            usb_send(msg);
        }
    }
}

// Task RX: legge righe dall'USB CDC e le dispatcha
void task_usb_rx(void *arg)
{
    uint8_t buf[256];
    char    line[128];
    int     line_pos = 0;

    for (;;) {
        size_t rx_size = 0;
        esp_err_t ret = tinyusb_cdcacm_read(
            TINYUSB_CDC_ACM_0, buf, sizeof(buf), &rx_size);

        if (ret == ESP_OK && rx_size > 0) {
            for (size_t i = 0; i < rx_size; i++) {
                char c = (char)buf[i];
                if (c == '\n' || c == '\r') {
                    if (line_pos > 0) {
                        line[line_pos] = '\0';
                        usb_dispatch_line(line);
                        line_pos = 0;
                    }
                } else if (line_pos < (int)sizeof(line) - 1) {
                    line[line_pos++] = c;
                }
            }
        }
        vTaskDelay(pdMS_TO_TICKS(5));
    }
}

// Parsa una riga e la instrada al task corretto
void usb_dispatch_line(const char *line)
{
    if (strcmp(line, "PING") == 0) {
        last_ping_us = esp_timer_get_time();
        xQueueSend(evt_queue, "PONG\n", 0);
        return;
    }

    cmd_t cmd = {0};

    // SET <pin> <on|off|pulse>
    if (sscanf(line, "SET %31s %15s", cmd.name, cmd.state) == 2) {
        cmd.type = CMD_SET;
        xQueueSend(cmd_queue, &cmd, 0);
        return;
    }

    // PWM <pin> <0-100>
    if (sscanf(line, "PWM %31s %d", cmd.name, &cmd.duty) == 2) {
        cmd.type = CMD_PWM;
        xQueueSend(cmd_queue, &cmd, 0);
        return;
    }

    // GET <pin> — risponde con stato corrente
    if (sscanf(line, "GET %31s", cmd.name) == 1) {
        cmd.type = CMD_GET;
        xQueueSend(cmd_queue, &cmd, 0);
        return;
    }
}

// Watchdog: safe state se nessun PING entro WATCHDOG_TIMEOUT_MS
void task_watchdog(void *arg)
{
    last_ping_us = esp_timer_get_time(); // inizializza al boot

    for (;;) {
        vTaskDelay(pdMS_TO_TICKS(1000));
        int64_t now = esp_timer_get_time();
        int64_t elapsed_ms = (now - last_ping_us) / 1000;

        if (elapsed_ms > WATCHDOG_TIMEOUT_MS) {
            gpio_output_safe_state();   // portone OFF, heartbeat OFF
            pwm_fan_set_duty(50);       // ventilazione minima
            // log interno — il Pi non è raggiungibile
        }
    }
}
```

---

## 7. `gpio_input.c` — interrupt + debounce

```c
// main/gpio_input.c
#include "driver/gpio.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "gpio_input.h"

#define DEBOUNCE_MS  10

typedef struct {
    gpio_num_t  pin;
    const char *name;
} pin_map_t;

static const pin_map_t PINS[] = {
    { GPIO_NUM_4,  "p1"     },
    { GPIO_NUM_5,  "p2"     },
    { GPIO_NUM_6,  "p3"     },
    { GPIO_NUM_7,  "on_off" },
};
#define NPIN (sizeof(PINS) / sizeof(PINS[0]))

// Coda ISR → task (solo il numero del pin)
static QueueHandle_t isr_queue;

static void IRAM_ATTR gpio_isr_handler(void *arg)
{
    gpio_num_t pin = (gpio_num_t)(intptr_t)arg;
    // Non-blocking: se la coda è piena il glitch viene ignorato
    xQueueSendFromISR(isr_queue, &pin, NULL);
}

void gpio_input_init(void)
{
    isr_queue = xQueueCreate(16, sizeof(gpio_num_t));

    gpio_config_t cfg = {
        .mode         = GPIO_MODE_INPUT,
        .pull_up_en   = GPIO_PULLUP_ENABLE,
        .pull_down_en = GPIO_PULLDOWN_DISABLE,
        .intr_type    = GPIO_INTR_ANYEDGE,
    };

    for (int i = 0; i < (int)NPIN; i++) {
        cfg.pin_bit_mask = 1ULL << PINS[i].pin;
        gpio_config(&cfg);
        gpio_isr_handler_add(PINS[i].pin, gpio_isr_handler,
                             (void *)(intptr_t)PINS[i].pin);
    }

    gpio_install_isr_service(0);
}

static const char *pin_to_name(gpio_num_t pin)
{
    for (int i = 0; i < (int)NPIN; i++) {
        if (PINS[i].pin == pin) return PINS[i].name;
    }
    return "unknown";
}

// Task: riceve pin dalla coda ISR, attende debounce, invia EVT
void task_gpio_input(void *arg)
{
    gpio_num_t pin;
    // Stato precedente per rilevare glitch
    int prev[GPIO_NUM_MAX] = {[0 ... GPIO_NUM_MAX-1] = -1};

    for (;;) {
        if (xQueueReceive(isr_queue, &pin, portMAX_DELAY) != pdTRUE) continue;

        vTaskDelay(pdMS_TO_TICKS(DEBOUNCE_MS)); // debounce

        int level = gpio_get_level(pin);
        if (level == prev[pin]) continue;       // glitch, ignora
        prev[pin] = level;

        char msg[32];
        snprintf(msg, sizeof(msg), "EVT %s %d\n", pin_to_name(pin), level);
        xQueueSend(evt_queue, msg, 0);
    }
}
```

---

## 8. `gpio_output.c` — SET / ACK / safe state

```c
// main/gpio_output.c
#include <string.h>
#include <stdio.h>
#include "driver/gpio.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "gpio_output.h"

typedef struct { gpio_num_t pin; const char *name; } out_pin_t;

static const out_pin_t OUT_PINS[] = {
    { GPIO_NUM_14, "led_ok"      },
    { GPIO_NUM_15, "heartbeat"   },
    { GPIO_NUM_16, "unlockdoor"  },
    { GPIO_NUM_17, "power_tablet"},
    { GPIO_NUM_18, "led_power"   },
    { GPIO_NUM_21, "led_ko"      },
};
#define NOUT (sizeof(OUT_PINS) / sizeof(OUT_PINS[0]))

void gpio_output_init(void)
{
    gpio_config_t cfg = {
        .mode         = GPIO_MODE_OUTPUT,
        .pull_up_en   = GPIO_PULLUP_DISABLE,
        .pull_down_en = GPIO_PULLDOWN_DISABLE,
        .intr_type    = GPIO_INTR_DISABLE,
    };
    for (int i = 0; i < (int)NOUT; i++) {
        cfg.pin_bit_mask = 1ULL << OUT_PINS[i].pin;
        gpio_config(&cfg);
        gpio_set_level(OUT_PINS[i].pin, 0);
    }
}

static gpio_num_t name_to_pin(const char *name)
{
    for (int i = 0; i < (int)NOUT; i++) {
        if (strcmp(OUT_PINS[i].name, name) == 0)
            return OUT_PINS[i].pin;
    }
    return GPIO_NUM_NC;
}

void task_gpio_output(void *arg)
{
    cmd_t cmd;
    char ack[48];

    for (;;) {
        if (xQueueReceive(cmd_queue, &cmd, portMAX_DELAY) != pdTRUE) continue;

        if (cmd.type == CMD_PWM) {
            // delegato a pwm_fan se pin == "fan"
            if (strcmp(cmd.name, "fan") == 0) {
                pwm_fan_set_duty(cmd.duty);
                snprintf(ack, sizeof(ack), "ACK fan %d\n", cmd.duty);
                xQueueSend(evt_queue, ack, 0);
            }
            continue;
        }

        if (cmd.type == CMD_SET) {
            gpio_num_t pin = name_to_pin(cmd.name);
            if (pin == GPIO_NUM_NC) continue;

            if (strcmp(cmd.state, "on") == 0) {
                gpio_set_level(pin, 1);
            } else if (strcmp(cmd.state, "off") == 0) {
                gpio_set_level(pin, 0);
            } else if (strcmp(cmd.state, "pulse") == 0) {
                gpio_set_level(pin, 1);
                vTaskDelay(pdMS_TO_TICKS(200));
                gpio_set_level(pin, 0);
            }
            snprintf(ack, sizeof(ack), "ACK %s %s\n", cmd.name, cmd.state);
            xQueueSend(evt_queue, ack, 0);
        }
    }
}

// Chiamato dal watchdog se PING assente
void gpio_output_safe_state(void)
{
    gpio_set_level(GPIO_NUM_16, 0); // unlockdoor OFF
    gpio_set_level(GPIO_NUM_15, 0); // heartbeat OFF
}
```

---

## 9. `pwm_fan.c` — LEDC 25kHz

```c
// main/pwm_fan.c
#include "driver/ledc.h"
#include "pwm_fan.h"

#define FAN_GPIO       GPIO_NUM_8
#define FAN_TIMER      LEDC_TIMER_0
#define FAN_CHANNEL    LEDC_CHANNEL_0
#define FAN_FREQ_HZ    25000
#define FAN_RESOLUTION LEDC_TIMER_10_BIT   // 0–1023

void pwm_fan_init(void)
{
    ledc_timer_config_t timer = {
        .speed_mode      = LEDC_LOW_SPEED_MODE,
        .timer_num       = FAN_TIMER,
        .duty_resolution = FAN_RESOLUTION,
        .freq_hz         = FAN_FREQ_HZ,
        .clk_cfg         = LEDC_AUTO_CLK,
    };
    ledc_timer_config(&timer);

    ledc_channel_config_t channel = {
        .gpio_num   = FAN_GPIO,
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .channel    = FAN_CHANNEL,
        .timer_sel  = FAN_TIMER,
        .duty       = 0,
        .hpoint     = 0,
    };
    ledc_channel_config(&channel);
}

// percent: 0–100
void pwm_fan_set_duty(int percent)
{
    if (percent < 0)   percent = 0;
    if (percent > 100) percent = 100;
    uint32_t max_duty = (1 << FAN_RESOLUTION) - 1;  // 1023
    uint32_t duty = (max_duty * (uint32_t)percent) / 100;
    ledc_set_duty(LEDC_LOW_SPEED_MODE, FAN_CHANNEL, duty);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, FAN_CHANNEL);
}
```

---

## 10. `pn532.c` — driver SPI

```c
// main/pn532.c  (driver minimale per ESP-IDF)
#include <string.h>
#include "driver/spi_master.h"
#include "driver/gpio.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "pn532.h"

#define SPI_HOST     SPI2_HOST
#define PIN_MOSI     GPIO_NUM_11
#define PIN_MISO     GPIO_NUM_13
#define PIN_SCK      GPIO_NUM_12
#define PIN_CS       GPIO_NUM_10
#define PIN_IRQ      GPIO_NUM_9
#define SPI_FREQ_HZ  (1 * 1000 * 1000)  // 1MHz

static spi_device_handle_t spi_dev;
static QueueHandle_t       irq_queue;

static void IRAM_ATTR pn532_irq_handler(void *arg)
{
    uint8_t dummy = 1;
    xQueueSendFromISR(irq_queue, &dummy, NULL);
}

void pn532_init(void)
{
    irq_queue = xQueueCreate(4, sizeof(uint8_t));

    spi_bus_config_t bus_cfg = {
        .mosi_io_num   = PIN_MOSI,
        .miso_io_num   = PIN_MISO,
        .sclk_io_num   = PIN_SCK,
        .quadwp_io_num = -1,
        .quadhd_io_num = -1,
    };
    spi_bus_initialize(SPI_HOST, &bus_cfg, SPI_DMA_CH_AUTO);

    spi_device_interface_config_t dev_cfg = {
        .clock_speed_hz = SPI_FREQ_HZ,
        .mode           = 0,
        .spics_io_num   = PIN_CS,
        .queue_size     = 4,
    };
    spi_bus_add_device(SPI_HOST, &dev_cfg, &spi_dev);

    // IRQ dal PN532: segnala card presente (active-low)
    gpio_config_t irq_cfg = {
        .pin_bit_mask = 1ULL << PIN_IRQ,
        .mode         = GPIO_MODE_INPUT,
        .pull_up_en   = GPIO_PULLUP_ENABLE,
        .intr_type    = GPIO_INTR_NEGEDGE,
    };
    gpio_config(&irq_cfg);
    gpio_install_isr_service(0);
    gpio_isr_handler_add(PIN_IRQ, pn532_irq_handler, NULL);

    pn532_reset_and_configure();
}

// Trasmette cmd e riceve resp via SPI
esp_err_t pn532_transceive(const uint8_t *cmd, size_t cmd_len,
                            uint8_t *resp, size_t *resp_len,
                            TickType_t timeout_ticks)
{
    spi_transaction_t t = {
        .length    = cmd_len * 8,
        .tx_buffer = cmd,
        .rx_buffer = resp,
        .rxlength  = (*resp_len) * 8,
    };
    esp_err_t ret = spi_device_transmit(spi_dev, &t);
    if (ret == ESP_OK) *resp_len = t.rxlength / 8;
    return ret;
}

// Attende IRQ dal PN532 (card present) con timeout
bool pn532_wait_for_card(TickType_t timeout_ticks)
{
    uint8_t dummy;
    return xQueueReceive(irq_queue, &dummy, timeout_ticks) == pdTRUE;
}
```

---

## 11. `desfire_auth.c` — 3-pass AES-128 DESFire EV3

```c
// main/desfire_auth.c
#include <string.h>
#include <stdlib.h>
#include "mbedtls/aes.h"
#include "esp_random.h"
#include "desfire_auth.h"
#include "pn532.h"

// AID applicazione — deve coincidere con le tessere programmate
static const uint8_t APP_AID[3] = {0xA5, 0xC3, 0x01};

// Ruota a sinistra di 1 byte (in-place)
static void rotate_left(uint8_t *buf, size_t len)
{
    uint8_t first = buf[0];
    memmove(buf, buf + 1, len - 1);
    buf[len - 1] = first;
}

// AES-128 CBC decrypt con IV = 0
static void aes_cbc_decrypt(const uint8_t *key, const uint8_t *in,
                             uint8_t *out, size_t len)
{
    mbedtls_aes_context ctx;
    mbedtls_aes_init(&ctx);
    mbedtls_aes_setkey_dec(&ctx, key, 128);
    uint8_t iv[16] = {0};
    mbedtls_aes_crypt_cbc(&ctx, MBEDTLS_AES_DECRYPT, len, iv, in, out);
    mbedtls_aes_free(&ctx);
}

// AES-128 CBC encrypt con IV = 0
static void aes_cbc_encrypt(const uint8_t *key, const uint8_t *in,
                             uint8_t *out, size_t len)
{
    mbedtls_aes_context ctx;
    mbedtls_aes_init(&ctx);
    mbedtls_aes_setkey_enc(&ctx, key, 128);
    uint8_t iv[16] = {0};
    mbedtls_aes_crypt_cbc(&ctx, MBEDTLS_AES_ENCRYPT, len, iv, in, out);
    mbedtls_aes_free(&ctx);
}

/*
 * Autenticazione DESFire EV3 — 3 passi AES-128
 *
 * 1. SelectApplication(AID)
 * 2. AuthenticateAES(KeyNo) → tessera risponde con ENC(AppKey, RndB)
 * 3. Decrypt → RndB
 *    Genera RndA random
 *    Invia ENC(AppKey, RndA || rotate_left(RndB))
 * 4. Tessera risponde con ENC(AppKey, rotate_left(RndA))
 * 5. Verifica → autenticazione mutua OK
 *
 * Ritorna ESP_OK se autenticazione riuscita, ESP_FAIL altrimenti.
 */
esp_err_t desfire_authenticate(const uint8_t *app_key, uint8_t key_no)
{
    uint8_t apdu[64], resp[64];
    size_t  resp_len;

    // --- Passo 1: SelectApplication ---
    uint8_t select_app[] = {
        0x90, 0x5A, 0x00, 0x00, 0x03,
        APP_AID[0], APP_AID[1], APP_AID[2],
        0x00
    };
    resp_len = sizeof(resp);
    if (pn532_transceive(select_app, sizeof(select_app),
                         resp, &resp_len, pdMS_TO_TICKS(200)) != ESP_OK)
        return ESP_FAIL;
    // Status OK = 0x91 0x00
    if (resp_len < 2 || resp[resp_len-2] != 0x91 || resp[resp_len-1] != 0x00)
        return ESP_FAIL;

    // --- Passo 2: AuthenticateAES ---
    uint8_t auth_cmd[] = {0x90, 0xAA, 0x00, 0x00, 0x01, key_no, 0x00};
    resp_len = sizeof(resp);
    if (pn532_transceive(auth_cmd, sizeof(auth_cmd),
                         resp, &resp_len, pdMS_TO_TICKS(200)) != ESP_OK)
        return ESP_FAIL;
    // Status: 0x91 0xAF (AF = additional frame, OK so far)
    if (resp_len < 18 || resp[resp_len-2] != 0x91 || resp[resp_len-1] != 0xAF)
        return ESP_FAIL;

    // resp[0..15] = ENC(AppKey, RndB)
    uint8_t rnd_b_enc[16];
    memcpy(rnd_b_enc, resp, 16);

    // --- Passo 3: decrypt RndB, genera RndA, invia token ---
    uint8_t rnd_b[16], rnd_b_rot[16];
    aes_cbc_decrypt(app_key, rnd_b_enc, rnd_b, 16);
    memcpy(rnd_b_rot, rnd_b, 16);
    rotate_left(rnd_b_rot, 16);

    uint8_t rnd_a[16];
    esp_fill_random(rnd_a, 16);  // TRNG hardware ESP32-S3

    uint8_t token_plain[32];
    memcpy(token_plain,      rnd_a,     16);
    memcpy(token_plain + 16, rnd_b_rot, 16);

    uint8_t token_enc[32];
    aes_cbc_encrypt(app_key, token_plain, token_enc, 32);

    // Invia token
    uint8_t auth2[40];
    auth2[0] = 0x90; auth2[1] = 0xAF; auth2[2] = 0x00;
    auth2[3] = 0x00; auth2[4] = 0x20;  // 32 bytes
    memcpy(auth2 + 5, token_enc, 32);
    auth2[37] = 0x00;

    resp_len = sizeof(resp);
    if (pn532_transceive(auth2, 38, resp, &resp_len,
                         pdMS_TO_TICKS(200)) != ESP_OK)
        return ESP_FAIL;
    // Status: 0x91 0x00 = OK, con 16 byte di risposta
    if (resp_len < 18 || resp[resp_len-2] != 0x91 || resp[resp_len-1] != 0x00)
        return ESP_FAIL;

    // --- Passo 4: verifica rotate_left(RndA) ---
    uint8_t rnd_a_rot_enc[16], rnd_a_rot_expected[16], rnd_a_rot_recv[16];
    memcpy(rnd_a_rot_enc, resp, 16);
    aes_cbc_decrypt(app_key, rnd_a_rot_enc, rnd_a_rot_recv, 16);

    memcpy(rnd_a_rot_expected, rnd_a, 16);
    rotate_left(rnd_a_rot_expected, 16);

    if (memcmp(rnd_a_rot_recv, rnd_a_rot_expected, 16) != 0)
        return ESP_FAIL;

    // Autenticazione mutua OK
    return ESP_OK;
}
```

---

## 12. `key_store.c` — chiavi AES in NVS cifrato

```c
// main/key_store.c
#include <string.h>
#include "nvs_flash.h"
#include "nvs.h"
#include "esp_log.h"
#include "key_store.h"

#define NVS_NAMESPACE  "desfire_keys"
#define NVS_KEY_APP    "app_key"
#define NVS_KEY_NO     "key_no"

static uint8_t g_app_key[16] = {0};
static uint8_t g_key_no      = 1;

static const char *TAG = "key_store";

void key_store_load(void)
{
    nvs_handle_t handle;
    esp_err_t ret = nvs_open(NVS_NAMESPACE, NVS_READONLY, &handle);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "NVS namespace '%s' non trovato — chiavi non caricate",
                 NVS_NAMESPACE);
        return;
    }

    size_t len = sizeof(g_app_key);
    ret = nvs_get_blob(handle, NVS_KEY_APP, g_app_key, &len);
    if (ret != ESP_OK || len != 16) {
        ESP_LOGE(TAG, "app_key non valida in NVS");
        memset(g_app_key, 0, 16);
    }

    uint8_t kno = 1;
    nvs_get_u8(handle, NVS_KEY_NO, &kno);
    g_key_no = kno;

    nvs_close(handle);
    ESP_LOGI(TAG, "chiave AES caricata da NVS, key_no=%d", g_key_no);
}

const uint8_t *key_store_get_app_key(void) { return g_app_key; }
uint8_t        key_store_get_key_no(void)  { return g_key_no;  }
```

**Scrittura chiave (una tantum, da tool provisioning):**

```c
// Eseguire solo durante la fase di provisioning, poi ricompilare senza questo blocco
void key_store_provision(const uint8_t *new_key, uint8_t key_no)
{
    nvs_handle_t handle;
    nvs_open(NVS_NAMESPACE, NVS_READWRITE, &handle);
    nvs_set_blob(handle, NVS_KEY_APP, new_key, 16);
    nvs_set_u8(handle, NVS_KEY_NO, key_no);
    nvs_commit(handle);
    nvs_close(handle);
}
```

---

## 13. `task_nfc` — loop NFC completo

```c
// in pn532.c o main.c
void task_nfc(void *arg)
{
    const uint8_t *app_key = key_store_get_app_key();
    uint8_t        key_no  = key_store_get_key_no();

    for (;;) {
        // Attendi IRQ card-present (max 500ms poi riprova)
        if (!pn532_wait_for_card(pdMS_TO_TICKS(500))) continue;

        // Leggi UID passivo ISO14443A
        uint8_t uid[7];
        uint8_t uid_len = 0;
        if (pn532_read_passive_target(uid, &uid_len, pdMS_TO_TICKS(100)) != ESP_OK)
            continue;

        // DESFire EV3: UID 7 byte (ISO14443-3 cascade level 2)
        if (uid_len != 7) {
            xQueueSend(evt_queue, "UID-KO\n", 0);
            pn532_wait_card_removed();
            continue;
        }

        // Autenticazione 3-pass AES
        esp_err_t auth = desfire_authenticate(app_key, key_no);
        if (auth == ESP_OK) {
            xQueueSend(evt_queue, "UID-OK\n", 0);
        } else {
            xQueueSend(evt_queue, "UID-KO\n", 0);
        }

        pn532_wait_card_removed();
    }
}
```

---

## 14. `sdkconfig.defaults` — configurazione build

```ini
# USB CDC ACM via TinyUSB
CONFIG_TINYUSB_ENABLED=y
CONFIG_TINYUSB_CDC_ENABLED=y
CONFIG_TINYUSB_CDC_RX_BUFSIZE=256
CONFIG_TINYUSB_CDC_TX_BUFSIZE=256

# Porta USB OTG (non JTAG)
CONFIG_ESP_CONSOLE_NONE=y
CONFIG_USB_OTG_SUPPORTED=y

# Stack FreeRTOS
CONFIG_FREERTOS_HZ=1000
CONFIG_FREERTOS_USE_TRACE_FACILITY=n

# Flash Encryption (attivare in produzione)
# CONFIG_FLASH_ENCRYPTION_ENABLED=y
# CONFIG_NVS_ENCRYPTION=y

# Ottimizzazione
CONFIG_COMPILER_OPTIMIZATION_PERF=y
```

---

## 15. `CMakeLists.txt`

```cmake
cmake_minimum_required(VERSION 3.16)
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(doorphoneserver_esp32)
```

```cmake
# main/CMakeLists.txt
idf_component_register(
    SRCS
        "main.c"
        "usb_cdc.c"
        "gpio_input.c"
        "gpio_output.c"
        "pwm_fan.c"
        "pn532.c"
        "desfire_auth.c"
        "key_store.c"
    INCLUDE_DIRS "."
    REQUIRES
        tinyusb
        driver
        esp_timer
        nvs_flash
        mbedtls
        esp_hw_support
)
```

---

## 16. Compilazione e flash

```bash
# Installa ESP-IDF (una volta)
git clone --recursive https://github.com/espressif/esp-idf.git
cd esp-idf && git checkout v6.0   # o v5.4
./install.sh esp32s3
source export.sh

# Compila
cd firmware/
idf.py set-target esp32s3
idf.py build

# Flash
idf.py -p /dev/ttyUSB0 flash

# Monitor seriale (debug)
idf.py -p /dev/ttyUSB0 monitor
```

---

## 17. Watchdog e safe state

Se il Pi non invia `PING` entro **10 secondi** (es. crash, riavvio, cavo USB staccato):

| Output | Azione | Motivo |
|--------|--------|--------|
| `unlockdoor` | LOW | Sicurezza: portone rimane chiuso |
| `heartbeat` | LOW | |
| `fan` PWM | 50% | Ventilazione minima garantita |
| NFC auth in corso | Annullata | Invia `UID-KO` al prossimo tentativo |

---

## 18. Roadmap firmware

### Fase 1 — Base
- [ ] Setup progetto ESP-IDF v5.x/6.x per ESP32-S3
- [ ] USB CDC: PING → PONG funzionante
- [ ] GPIO interrupt P1/P2/P3: EVT
- [ ] SET on/off/pulse + ACK
- [ ] PWM ventola LEDC 25kHz
- [ ] Watchdog 10s → safe state

### Fase 3 — DESFire EV3
- [ ] Driver PN532 SPI completo
- [ ] `desfire_auth.c`: 3-pass AES-128 con tessere reali
- [ ] `key_store.c`: NVS + tool provisioning (Python + ACR122U)
- [ ] Test UID-OK → apertura portone end-to-end

### Fase 4 — Hardening
- [ ] Attivare Flash Encryption + NVS Encryption
- [ ] Secure Boot v2
- [ ] HMAC SHA-256 sui messaggi USB (anti-replay)

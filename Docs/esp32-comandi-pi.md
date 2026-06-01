# ESP32 — Nuovi comandi ricevuti dal Pi via USB

**Branch:** GPIO-OVER-USB  
**Documento correlato:** [gpio-over-usb-esp32-side.md](gpio-over-usb-esp32-side.md)

---

## Contesto

Il Raspberry Pi invia comandi testuali all'ESP32-S3 via USB CDC (`/dev/esp32`, 115200 baud).  
Ogni comando è una riga terminata con `\n`.  
Il parsing avviene in `usb_dispatch_line()` dentro `main/usb_cdc.c`.

Questi quattro comandi **sostituiscono** i vecchi `SET` e `PWM fan`:

| Comando ricevuto | Azione hardware | GPIO | Note |
|-----------------|----------------|------|------|
| `TABLET-ON`     | GPIO17 → HIGH  | `GPIO_NUM_17` (`power_tablet`) | Alimentazione tablet |
| `TABLET-OFF`    | GPIO17 → LOW   | `GPIO_NUM_17` (`power_tablet`) | |
| `UNLOCK-DOOR`   | Pulse GPIO16 200ms | `GPIO_NUM_16` (`unlockdoor`) | HIGH → delay 200ms → LOW |
| `FAN-XX`        | PWM ventola XX% | `GPIO_NUM_8` (LEDC CH0) | XX = 0..100, es. `FAN-75` |

---

## Modifica da fare in `usb_dispatch_line()` (usb_cdc.c)

Sostituire il blocco di parsing esistente con questo:

```c
void usb_dispatch_line(const char *line)
{
    // ── PING / PONG watchdog ──────────────────────────────────────────────
    if (strcmp(line, "PING") == 0) {
        last_ping_us = esp_timer_get_time();
        xQueueSend(evt_queue, "PONG\n", 0);
        return;
    }

    // ── TABLET-ON ─────────────────────────────────────────────────────────
    if (strcmp(line, "TABLET-ON") == 0) {
        gpio_set_level(GPIO_NUM_17, 1);
        xQueueSend(evt_queue, "ACK TABLET-ON\n", 0);
        return;
    }

    // ── TABLET-OFF ────────────────────────────────────────────────────────
    if (strcmp(line, "TABLET-OFF") == 0) {
        gpio_set_level(GPIO_NUM_17, 0);
        xQueueSend(evt_queue, "ACK TABLET-OFF\n", 0);
        return;
    }

    // ── UNLOCK-DOOR (pulse 200ms) ─────────────────────────────────────────
    if (strcmp(line, "UNLOCK-DOOR") == 0) {
        gpio_set_level(GPIO_NUM_16, 1);
        vTaskDelay(pdMS_TO_TICKS(200));
        gpio_set_level(GPIO_NUM_16, 0);
        xQueueSend(evt_queue, "ACK UNLOCK-DOOR\n", 0);
        return;
    }

    // ── FAN-XX (ventola PWM 0-100%) ───────────────────────────────────────
    // Formato: "FAN-" seguito da numero intero 0..100
    if (strncmp(line, "FAN-", 4) == 0) {
        int pct = atoi(line + 4);
        if (pct < 0)   pct = 0;
        if (pct > 100) pct = 100;
        pwm_fan_set_duty(pct);
        char ack[24];
        snprintf(ack, sizeof(ack), "ACK FAN-%d\n", pct);
        xQueueSend(evt_queue, ack, 0);
        return;
    }

    // ── TAG-LIST (NFC) ────────────────────────────────────────────────────
    // ... resto del parsing NFC invariato ...
}
```

---

## Nota importante: UNLOCK-DOOR blocca il task RX

`vTaskDelay(200ms)` dentro `task_usb_rx` blocca la ricezione per 200ms.  
Se questo è un problema (altri comandi in arrivo durante il pulse), spostare
il pulse in un task dedicato tramite la `cmd_queue`:

```c
// Alternativa: invia alla cmd_queue invece di eseguire inline
if (strcmp(line, "UNLOCK-DOOR") == 0) {
    cmd_t cmd = { .type = CMD_PULSE_DOOR };
    xQueueSend(cmd_queue, &cmd, 0);
    return;
}
```

E in `task_gpio_output`:
```c
case CMD_PULSE_DOOR:
    gpio_set_level(GPIO_NUM_16, 1);
    vTaskDelay(pdMS_TO_TICKS(200));
    gpio_set_level(GPIO_NUM_16, 0);
    xQueueSend(evt_queue, "ACK UNLOCK-DOOR\n", 0);
    break;
```

---

## Risposte attese dal Pi

Il Pi **non aspetta ACK** per questi comandi (fire-and-forget), ma le risposte
vengono loggati nel "Log USB seriale" del pannello web.

| Comando     | Risposta ESP32       |
|------------|----------------------|
| `TABLET-ON`  | `ACK TABLET-ON`    |
| `TABLET-OFF` | `ACK TABLET-OFF`   |
| `UNLOCK-DOOR`| `ACK UNLOCK-DOOR`  |
| `FAN-75`     | `ACK FAN-75`       |

---

## Safe state (watchdog)

Se PING assente > 10s, `gpio_output_safe_state()` deve includere:

```c
void gpio_output_safe_state(void)
{
    gpio_set_level(GPIO_NUM_16, 0);  // portone chiuso
    gpio_set_level(GPIO_NUM_17, 0);  // tablet spento
    gpio_set_level(GPIO_NUM_15, 0);  // heartbeat off
    pwm_fan_set_duty(50);             // ventilazione minima
}
```

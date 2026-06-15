# ESP32-B (RELAY) — Modifiche firmware per auto-identificazione

Questo documento descrive **solo le modifiche da aggiungere** al firmware ESP32-B esistente per supportare il protocollo di auto-identificazione richiesto da DoorPhoneServer.

Per il protocollo completo Pi ↔ ESP32 vedere [`esp32-firmware-protocol.md`](esp32-firmware-protocol.md).

---

## Contesto

Il bridge Go invia `GET-ROLE\n` a ogni porta `/dev/ttyACM*` per scoprire quale device è l'RFID e quale il relay. ESP32-B deve rispondere `HELLO RELAY\n`.

Il bridge invia `GET-ROLE` ad ogni nuova sessione USB, non solo al primo avvio.

---

## Modifica 1 — Invia `HELLO RELAY` all'avvio

Nel task o nella funzione di inizializzazione USB, **dopo** che la CDC è pronta e prima di qualsiasi altro messaggio:

```c
/* attendi che la CDC sia pronta */
vTaskDelay(pdMS_TO_TICKS(200));

/* annuncia il ruolo */
const char *hello = "HELLO RELAY\n";
tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (const uint8_t *)hello, strlen(hello));
tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);
```

Usa la stessa primitiva che già usi nel tuo firmware per scrivere sulla seriale USB.

---

## Modifica 2 — Handler per `GET-ROLE` nel parser comandi

Dove il firmware parsa i comandi ricevuti dal Pi, aggiungi:

```c
if (strncmp(cmd, "GET-ROLE", 8) == 0) {
    const char *reply = "HELLO RELAY\n";
    tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (const uint8_t *)reply, strlen(reply));
    tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);
}
```

Posizionarlo prima degli handler dei comandi relè, così risponde anche durante l'inizializzazione.

---

## Verifica

```bash
echo "GET-ROLE" | sudo tee /dev/ttyACM1
sudo cat /dev/ttyACM1
# deve stampare: HELLO RELAY
```

Nel log di DoorPhoneServer deve comparire:
```
[USB-RELAY] ESP32-RELAY connesso su /dev/ttyACM1
```

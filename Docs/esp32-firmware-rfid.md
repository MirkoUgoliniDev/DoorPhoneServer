# ESP32-A (RFID) — Modifiche firmware per auto-identificazione

Questo documento descrive **solo le modifiche da aggiungere** al firmware ESP32-A esistente per supportare il protocollo di auto-identificazione richiesto da DoorPhoneServer.

Per il protocollo completo Pi ↔ ESP32 vedere [`esp32-firmware-protocol.md`](esp32-firmware-protocol.md).

---

## Contesto

Il bridge Go invia `GET-ROLE\n` a ogni porta `/dev/ttyACM*` per scoprire quale device è l'RFID e quale il relay. ESP32-A deve rispondere `HELLO RFID\n`.

Il bridge invia anche `GET-ROLE` ogni volta che si riconnette dopo una disconnessione, quindi il firmware deve gestirlo in ogni sessione, non solo all'avvio.

---

## Modifica 1 — Invia `HELLO RFID` all'avvio

Nel task o nella funzione di inizializzazione USB, **dopo** che la CDC è pronta e prima di qualsiasi altro messaggio:

```c
/* attendi che la CDC sia pronta (dipende dal tuo driver) */
vTaskDelay(pdMS_TO_TICKS(200));

/* annuncia il ruolo */
const char *hello = "HELLO RFID\n";
tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (const uint8_t *)hello, strlen(hello));
tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);
```

Se nel tuo firmware usi una primitiva diversa per scrivere sulla seriale USB (es. `usb_serial_jtag_write_bytes`, o una wrapper), usa quella al posto di `tinyusb_cdcacm_write_queue`.

---

## Modifica 2 — Handler per `GET-ROLE` nel parser comandi

Dove il firmware legge e parsa i comandi ricevuti dal Pi (il blocco `if/else if` o `switch` sui comandi in arrivo), aggiungi:

```c
if (strncmp(cmd, "GET-ROLE", 8) == 0) {
    const char *reply = "HELLO RFID\n";
    tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (const uint8_t *)reply, strlen(reply));
    tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);
}
```

Posizionarlo **prima** degli handler dei comandi NFC/TAG, così il bridge riceve risposta anche se arriva prima che il sistema NFC sia inizializzato.

---

## Verifica

Con le due modifiche applicate, collegando ESP32-A al Pi e inviando manualmente `GET-ROLE`:

```bash
echo "GET-ROLE" | sudo tee /dev/ttyACM0
# attendi la risposta:
sudo cat /dev/ttyACM0
# deve stampare: HELLO RFID
```

Nel log di DoorPhoneServer deve comparire:
```
[USB-RFID] ESP32-RFID connesso su /dev/ttyACM0
```

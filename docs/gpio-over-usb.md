# GPIO over USB — ESP32-S3 come espansore I/O + Lettore DESFire EV3

**Branch:** GPIO-OVER-USB  
**Data:** 2026-05-16  
**Autore:** Mirko Ugolini  
**Modalità implementazione:** Debug/parallelo — i GPIO fisici del Pi restano attivi

---

## 1. Architettura generale

```
┌──────────────────────────────────────────────────────────────┐
│                    Raspberry Pi 4                            │
│                                                              │
│  doorphoneserver (Go)                                        │
│  ├── gpio.go          ← GPIO fisici Pi (invariato)           │
│  ├── gpio_usb.go      ← bridge seriale ESP32-S3 (nuovo)      │
│  └── smartcard.go     ← validazione accesso post-auth (nuovo)│
│                            │ USB CDC ACM /dev/gpio-esp32     │
└────────────────────────────┼─────────────────────────────────┘
                             │  protocollo testuale linea/linea
┌────────────────────────────┼─────────────────────────────────┐
│          ESP32-S3          │                                  │
│                                                               │
│  ┌──────────┐ ┌─────────┐ ┌──────────────────────────────┐  │
│  │ GPIO ISR │ │LEDC PWM │ │  DESFire EV3 Engine           │  │
│  │ inputs   │ │ fan     │ │  ├── ISO 14443-4 (PN532)      │  │
│  └────┬─────┘ └─────────┘ │  ├── 3-pass AES mutual auth   │  │
│       │                   │  ├── Session key derivation    │  │
│       │    FreeRTOS        │  ├── File read + decrypt       │  │
│       └──────────────────►│  └── CMAC/TMAC verify          │  │
│                            └──────────────┬───────────────┘  │
│                                           │ EVT CARD_OK/DENIED│
└───────────────────────────────────────────┼──────────────────┘
                         │            │     │
                    Pulsanti      Ventola  PN532
                    P1/P2/P3      MOSFET   (ISO14443-4)
                    Relè portone
```

---

## 2. Perché DESFire EV3 (e non UID semplice)

Un sistema basato sul solo UID della tessera è **vulnerabile per design**:

| Attacco | UID semplice | DESFire EV3 |
|---------|-------------|-------------|
| Clonazione tessera (copiatrice ~20€) | ✗ Triviale | ✓ Impossibile (chiavi nel secure element) |
| Replay attack | ✗ Sì | ✓ No (TMAC rotante, ogni transazione è unica) |
| Relay attack (estendi range NFC) | ✗ Sì | ✓ No (Proximity Check EV3) |
| Brute-force chiave | N/A | ✓ No (lockout dopo N tentativi falliti) |
| Lettura passiva a distanza | ✗ Sì | ✓ No (dati cifrati in aria) |

**DESFire EV3** (NXP MIFARE DESFire EV3) è lo standard de facto per controllo accessi professionale:
- Autenticazione mutua a 3 passi con **AES-128** (o AES-256 in EV3)
- **Transaction MAC (TMAC)**: ogni operazione ha un MAC univoco, i replay sono impossibili
- **Proximity Check**: il chip misura il tempo di risposta RF per rilevare relay attack
- **Secure Messaging**: tutti i dati in aria cifrati con session key derivata
- Common Criteria **EAL5+** certificato
- ISO/IEC 14443-4 (livello trasporto) + ISO 7816-4 (APDU)

---

## 3. Hardware

### 3.1 ESP32-S3

| Feature | Valore |
|---------|--------|
| USB | OTG nativo CDC ACM (no driver Linux) |
| AES accelerator | Hardware AES-128/256 (fondamentale per DESFire) |
| Flash encryption | Protegge le chiavi AES memorizzate in NVS |
| Secure Boot V2 | Impedisce firmware non firmato |
| GPIO | 45 pin, PWM hardware su tutti |
| Devboard | ESP32-S3-DevKitC-1 (USB-C nativo) |

L'**acceleratore AES hardware** è il motivo per cui l'ESP32-S3 è adatto: l'autenticazione DESFire richiede 2–4 operazioni AES per transazione, completabili in <1ms.

### 3.2 Modulo NFC — PN532

Il PN532 gestisce solo il livello RF (ISO 14443-4). La crittografia DESFire viene eseguita sull'ESP32-S3.

| Feature | Valore |
|---------|--------|
| Standard RF | ISO 14443-A/B (DESFire usa 14443-A) |
| Livello trasporto | ISO 14443-4 (T=CL, blocchi I/R/S) |
| APDU forwarding | `InDataExchange` — il PN532 trasmette APDU grezzi alla carta |
| Interfaccia verso ESP32-S3 | SPI (più robusto di I2C per sequenze APDU lunghe) |
| Range | 3–7 cm (tessere standard) |
| Costo | ~5–12€ |

> **Nota**: il PN532 NON esegue la crittografia DESFire. Funziona come "antenna intelligente": riceve APDU dall'ESP32-S3, li trasmette alla tessera, restituisce la risposta. Tutta la logica crittografica è nell'ESP32-S3.

### 3.3 Tessere DESFire EV3

Le tessere devono essere **personalizzate** (programmate) prima dell'uso:
- Creazione di una **Application** con AID scelto (es. `0xA5C301`)
- Impostazione di **Master Key** (AES-128, 16 byte) e **Read Key**
- Creazione di un **Data File** (es. file 0x01, 16 byte, accesso con Read Key) contenente l'ID utente
- Ogni tessera avrà le stesse chiavi applicazione ma UID hardware diverso

La personalizzazione richiede un tool separato (vedi Appendice D).

### 3.4 Mappa GPIO ESP32-S3

| Funzione | Dir. FW | GPIO | Note |
|----------|---------|------|------|
| Pulsante P1 | INPUT / ISR | 4 | Pull-up, active-low, ANYEDGE |
| Pulsante P2 | INPUT / ISR | 5 | Pull-up, active-low, ANYEDGE |
| Pulsante P3 | INPUT / ISR | 6 | Pull-up, active-low, ANYEDGE |
| On/Off | INPUT / ISR | 7 | Pull-up, active-low, ANYEDGE |
| LED Heartbeat | OUTPUT | 15 | 220Ω → LED |
| Relè portone | OUTPUT | 16 | Transistor NPN / optoisolatore |
| Power tablet | OUTPUT | 17 | Transistor NPN |
| LED power | OUTPUT | 18 | 220Ω → LED |
| Ventola PWM | OUTPUT/PWM | 8 | MOSFET N-ch, 25kHz |
| PN532 MOSI (SPI) | SPI | 11 | |
| PN532 MISO (SPI) | SPI | 13 | |
| PN532 SCK (SPI) | SPI | 12 | |
| PN532 SS/CS | OUTPUT | 10 | Active-low |
| PN532 IRQ | INPUT / ISR | 9 | Interrupt card-present |
| LED accesso verde | OUTPUT | 14 | Autorizzato |
| LED accesso rosso | OUTPUT | 21 | Negato |

---

## 4. Protocollo DESFire EV3 — flusso autenticazione

### 4.1 Stack protocolli

```
doorphoneserver (Go)
    │
    │  USB CDC ACM (linee testo)
    │
ESP32-S3  ←── gestisce tutto il livello crittografico
    │
    │  PN532 SPI   →  APDU ISO 14443-4
    │
Tessera DESFire EV3
```

### 4.2 Sequenza autenticazione AES (3 passi)

```
ESP32-S3                              DESFire EV3
    │                                      │
    │── APDU: SelectApplication(AID) ─────►│
    │◄─ SW 9000 (OK) ──────────────────────│
    │                                      │
    │── APDU: AuthenticateAES(KeyNo=1) ───►│
    │◄─ RndB_enc (16 byte AES cifrati) ────│  ← sfida dalla tessera
    │                                      │
    │  AES_decrypt(AppReadKey, RndB_enc)   │  ← ESP32-S3 decifra
    │  Genera RndA (16 byte random)        │
    │  token = AES_encrypt(AppReadKey,     │
    │           RndA || rotate_left(RndB)) │
    │                                      │
    │── APDU: token (32 byte) ────────────►│
    │◄─ AES_encrypt(AppReadKey,            │  ← tessera conferma
    │    rotate_left(RndA)) ───────────────│
    │                                      │
    │  Verifica RndA' → AUTENTICAZIONE OK  │
    │  session_key = KDF(RndA, RndB)       │  ← session key per questa transazione
    │                                      │
    │── APDU: ReadData(FileNo=1,           │
    │         offset=0, length=16) ───────►│
    │◄─ AES_encrypt(session_key, data)     │  ← dati cifrati con session key
    │                                      │
    │  AES_decrypt(session_key, data)      │
    │  Verifica CMAC/TMAC                  │  ← replay protection
    │  Estrai user_id dai dati             │
    │                                      │
    │── USB: EVT CARD_OK <user_id> <uid>  ─────────────► Pi
```

### 4.3 Gestione fallimenti

| Evento | Risposta ESP32-S3 |
|--------|-------------------|
| Carta non DESFire EV3 | `EVT CARD_DENIED WRONG_TYPE` |
| AID non trovato sulla carta | `EVT CARD_DENIED NO_APP` |
| Autenticazione fallita (chiave errata) | `EVT CARD_DENIED AUTH_FAIL` |
| TMAC non valido (replay/manomissione) | `EVT CARD_DENIED TMAC_FAIL` |
| Carta rimossa durante auth | `EVT CARD_DENIED CARD_REMOVED` |
| Carta bloccata (troppi tentativi) | `EVT CARD_DENIED CARD_LOCKED` |
| Errore PN532 / RF | `EVT CARD_DENIED RF_ERROR` |

---

## 5. Protocollo USB (aggiornato)

### 5.1 Pi → ESP32-S3

```
SET <pin> <on|off|pulse>\n
PWM <pin> <0-100>\n
GET <pin>\n
PING\n
```

### 5.2 ESP32-S3 → Pi

```
ACK <pin> <valore>\n
VAL <pin> <valore>\n
EVT <pin> <0|1>\n                          — cambio stato GPIO input
EVT CARD_OK <user_id_hex> <uid_hex>\n      — auth DESFire completata OK
EVT CARD_DENIED <reason>\n                 — auth fallita o tipo errato
EVT CARD_REMOVED\n                         — tessera rimossa
PONG\n
ERR <codice> <msg>\n
```

`user_id_hex` = contenuto del Data File della tessera (16 byte hex), interpretato dal Pi.

### 5.3 Watchdog

Se nessun `PING` in 10 secondi:
- `unlockdoor` → OFF
- `heartbeat` → OFF
- `fan` → PWM 50%
- Auth in corso → annullata, `EVT CARD_DENIED WATCHDOG`

---

## 6. Firmware ESP32-S3

### 6.1 Task FreeRTOS

```
app_main
 ├── usb_cdc_init()        ← TinyUSB CDC ACM
 ├── gpio_init()           ← ISR su tutti gli input
 ├── pwm_init()            ← LEDC 25kHz
 ├── spi_init()            ← PN532 SPI
 ├── nfc_pn532_init()      ← wake-up, SAMConfiguration
 ├── nvs_load_keys()       ← carica AES keys da NVS cifrato
 │
 ├── task: usb_rx_task     prio 5  ← legge comandi da Pi
 ├── task: gpio_out_task   prio 4  ← esegue SET/PWM dalla coda
 ├── task: gpio_isr_task   prio 4  ← processa eventi ISR input
 ├── task: nfc_task        prio 3  ← polling tessera + auth DESFire
 └── task: watchdog_task   prio 6  ← safe_state() se PING assente
```

### 6.2 GPIO interrupt-driven

```c
static void IRAM_ATTR gpio_isr_handler(void *arg) {
    gpio_event_t evt = {
        .pin   = (uint32_t)arg,
        .level = gpio_get_level((uint32_t)arg),
    };
    xQueueSendFromISR(gpio_evt_queue, &evt, NULL);
}

// gpio_isr_task: debounce 5ms in software dopo ISR
void gpio_isr_task(void *arg) {
    gpio_event_t evt;
    for (;;) {
        if (xQueueReceive(gpio_evt_queue, &evt, portMAX_DELAY)) {
            vTaskDelay(pdMS_TO_TICKS(5));
            if (gpio_get_level(evt.pin) == evt.level) {  // confermato
                char msg[32];
                snprintf(msg, sizeof(msg), "EVT %s %d\n",
                         pin_name(evt.pin), evt.level);
                usb_cdc_write(msg);
            }
        }
    }
}
```

### 6.3 PWM ventola (LEDC)

```c
void pwm_fan_set(uint8_t percent) {
    // LEDC_TIMER_10_BIT → range 0–1023
    uint32_t duty = ((uint32_t)percent * 1023) / 100;
    ledc_set_duty(LEDC_LOW_SPEED_MODE, LEDC_CHANNEL_0, duty);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, LEDC_CHANNEL_0);
}
// Frequenza: 25kHz — inaudibile, compatibile con ventole PWM 4-pin
```

### 6.4 DESFire EV3 — autenticazione AES in firmware

```c
// Struttura configurazione applicazione (da NVS)
typedef struct {
    uint8_t aid[3];          // Application ID, es. {0xA5, 0xC3, 0x01}
    uint8_t key_no;          // Numero chiave per auth (es. 1)
    uint8_t app_key[16];     // AES-128 application read key
    uint8_t file_no;         // Numero file dati (es. 0x01)
    uint8_t file_len;        // Lunghezza dati utente (es. 16)
} desfire_app_config_t;

esp_err_t desfire_authenticate_and_read(pn532_t *dev,
                                         desfire_app_config_t *cfg,
                                         uint8_t *out_user_id,
                                         uint8_t *out_uid,
                                         uint8_t *out_uid_len)
{
    uint8_t apdu[64], resp[64];
    int resp_len;

    // 1. Select Application
    apdu[0]=0x90; apdu[1]=0x5A; apdu[2]=0x00; apdu[3]=0x00;
    apdu[4]=0x03;
    memcpy(apdu+5, cfg->aid, 3);
    apdu[8]=0x00;
    if (!pn532_in_data_exchange(dev, apdu, 9, resp, &resp_len))
        return ESP_FAIL;
    if (resp[resp_len-2] != 0x91 || resp[resp_len-1] != 0x00)
        return ESP_ERR_NOT_FOUND;   // AID non trovato

    // 2. AuthenticateAES → ricevi RndB cifrato
    apdu[0]=0x90; apdu[1]=0xAA; apdu[2]=0x00; apdu[3]=0x00;
    apdu[4]=0x01; apdu[5]=cfg->key_no; apdu[6]=0x00;
    if (!pn532_in_data_exchange(dev, apdu, 7, resp, &resp_len))
        return ESP_FAIL;
    // resp contiene RndB_enc (16 byte) + 0x91 0xAF

    // 3. Decifra RndB con AES hardware
    uint8_t rnd_b[16], rnd_a[16], token[32];
    esp_aes_context aes_ctx;
    esp_aes_init(&aes_ctx);
    esp_aes_setkey(&aes_ctx, cfg->app_key, 128);
    esp_aes_crypt_ecb(&aes_ctx, ESP_AES_DECRYPT, resp, rnd_b);

    // 4. Genera RndA random, costruisci token
    esp_fill_random(rnd_a, 16);
    uint8_t rnd_b_rot[16];
    rotate_left(rnd_b, rnd_b_rot, 16);          // RndB ruotato di 1 byte a sinistra
    memcpy(token, rnd_a, 16);
    memcpy(token+16, rnd_b_rot, 16);
    // Cifra in CBC con IV=0
    uint8_t iv[16] = {0};
    esp_aes_setkey(&aes_ctx, cfg->app_key, 128);
    esp_aes_crypt_cbc(&aes_ctx, ESP_AES_ENCRYPT, 32, iv, token, token);

    // 5. Invia token, ricevi conferma RndA'
    apdu[0]=0x90; apdu[1]=0xAF; apdu[2]=0x00; apdu[3]=0x00;
    apdu[4]=0x20;
    memcpy(apdu+5, token, 32);
    apdu[37]=0x00;
    if (!pn532_in_data_exchange(dev, apdu, 38, resp, &resp_len))
        return ESP_FAIL;

    // 6. Verifica RndA' (risposta della tessera)
    uint8_t rnd_a_prime[16];
    esp_aes_crypt_ecb(&aes_ctx, ESP_AES_DECRYPT, resp, rnd_a_prime);
    uint8_t rnd_a_rot[16];
    rotate_left(rnd_a, rnd_a_rot, 16);
    if (memcmp(rnd_a_prime, rnd_a_rot, 16) != 0) {
        esp_aes_free(&aes_ctx);
        return ESP_ERR_INVALID_MAC;  // AUTH_FAIL
    }

    // 7. Deriva session key: primi 4 byte RndA + primi 4 RndB + ultimi 4 RndA + ultimi 4 RndB
    uint8_t session_key[16];
    memcpy(session_key,    rnd_a,   4);
    memcpy(session_key+4,  rnd_b,   4);
    memcpy(session_key+8,  rnd_a+12,4);
    memcpy(session_key+12, rnd_b+12,4);

    // 8. ReadData con session key + verifica CMAC
    // (dettagli omessi per brevità — segue standard AN10609 NXP)
    esp_err_t ret = desfire_read_file_verified(dev, &aes_ctx,
                                               session_key, cfg->file_no,
                                               cfg->file_len, out_user_id);
    esp_aes_free(&aes_ctx);
    return ret;
}
```

### 6.5 nfc_task — orchestrazione

```c
void nfc_task(void *arg) {
    uint8_t uid[7], uid_len;
    uint8_t user_id[16];

    for (;;) {
        // Attendi IRQ dal PN532 (card present) — interrupt-driven
        xSemaphoreTake(nfc_irq_sem, portMAX_DELAY);

        if (!pn532_read_passive_target_id(dev, &uid, &uid_len, 100))
            continue;

        // Verifica che sia DESFire (UID 7 byte = DESFire/Ultralight)
        if (uid_len != 7) {
            usb_cdc_write("EVT CARD_DENIED WRONG_TYPE\n");
            continue;
        }

        esp_err_t result = desfire_authenticate_and_read(
            dev, &app_cfg, user_id, uid, &uid_len);

        char msg[80];
        if (result == ESP_OK) {
            // Converti user_id e uid in hex
            char uid_hex[15]={0}, user_hex[33]={0};
            bytes_to_hex(uid, uid_len, uid_hex);
            bytes_to_hex(user_id, 16, user_hex);
            snprintf(msg, sizeof(msg),
                     "EVT CARD_OK %s %s\n", user_hex, uid_hex);
        } else {
            const char *reason = desfire_err_to_str(result);
            snprintf(msg, sizeof(msg),
                     "EVT CARD_DENIED %s\n", reason);
        }
        usb_cdc_write(msg);

        // Attendi rimozione tessera
        while (pn532_read_passive_target_id(dev, &uid, &uid_len, 50))
            vTaskDelay(pdMS_TO_TICKS(100));
        usb_cdc_write("EVT CARD_REMOVED\n");
    }
}
```

### 6.6 Gestione chiavi — NVS con flash encryption

Le chiavi AES non sono mai in chiaro nel codice sorgente. Vengono scritte nell'NVS durante la fase di provisioning (una tantum):

```c
// Provisioning (eseguito una sola volta via tool seriale o OTA)
nvs_handle_t nvs;
nvs_open("desfire_keys", NVS_READWRITE, &nvs);
nvs_set_blob(nvs, "app_key", app_key_bytes, 16);
nvs_set_blob(nvs, "aid",     aid_bytes,     3);
nvs_commit(nvs);
nvs_close(nvs);
```

Con **Flash Encryption** abilitato (ESP32-S3 efuse), l'NVS è cifrato a riposo. Le chiavi sono illeggibili anche con accesso fisico al chip (JTAG disabilitato via efuse in produzione).

---

## 7. Lato Pi — Go

### 7.1 File nuovi

```
gpio_usb.go      — gestione porta seriale, loop lettura eventi
smartcard.go     — validazione post-auth DESFire, log accessi
```

### 7.2 smartcard.go — logica validazione

Quando il Pi riceve `EVT CARD_OK <user_id_hex> <uid_hex>`:

```go
func (b *DoorPhoneServer) handleCardOK(userIDHex, uidHex string) {
    users := loadSmartcards()          // legge preferences/smartcards.json
    user, found := users[userIDHex]

    if !found || !user.Enabled {
        log.Printf("[SMARTCARD] accesso negato: UID=%s userID=%s", uidHex, userIDHex)
        b.sendUSB("SET access_led red\n")
        // Pushover notify opzionale
        return
    }

    log.Printf("[SMARTCARD] accesso OK: %s (UID=%s)", user.Name, uidHex)
    b.sendUSB("SET access_led green\n")
    GPIOOutPin("unlockdoor", "pulse")     // apre il portone (gpio.go invariato)
    logAccess(userIDHex, uidHex, user.Name, true)
}

func (b *DoorPhoneServer) handleCardDenied(reason string) {
    log.Printf("[SMARTCARD] accesso negato dal firmware: %s", reason)
    b.sendUSB("SET access_led red\n")
    logAccess("", "", "", false)
}
```

### 7.3 `preferences/smartcards.json`

La chiave del dizionario è il **user_id_hex** letto dal Data File della tessera (non l'UID hardware, che è immodificabile ma non è un segreto).

```json
{
  "4D617269 6F526F73 73690000 00000001": {
    "name": "Mario Rossi",
    "floors": ["P1", "P2", "P3"],
    "enabled": true,
    "note": "Proprietario appartamento 3"
  },
  "416E6E61 56657264 69000000 00000002": {
    "name": "Anna Verdi",
    "floors": ["P2"],
    "enabled": true,
    "note": "Affittuario piano 2"
  }
}
```

> La chiave nel JSON è il contenuto del Data File — stabilito al momento della personalizzazione della tessera. Non deve essere l'UID hardware (quello è solo un seriale, non un segreto).

### 7.4 Log accessi — `preferences/access_log.jsonl`

```jsonl
{"ts":"2026-05-16T10:23:11Z","user":"Mario Rossi","uid":"04A3F211B2","result":"OK","action":"unlockdoor"}
{"ts":"2026-05-16T10:45:02Z","user":"","uid":"04CC221133","result":"DENIED","reason":"AUTH_FAIL"}
```

---

## 8. Sicurezza — analisi

| Layer | Meccanismo | Protezione |
|-------|-----------|------------|
| RF (aria) | AES-128 session key (cambio ad ogni transazione) | Sniffing inutile |
| Tessera | Mutual authentication + TMAC | Clonazione impossibile, replay impossibile |
| Relay attack | Proximity Check EV3 | Estensione range NFC bloccata |
| Chiavi in ESP32-S3 | NVS + Flash Encryption + efuse | Estrazione fisica bloccata |
| Firmware | Secure Boot V2 | Solo firmware firmato eseguito |
| Protocollo USB | Testo in chiaro (locale, fisico) | Accettabile: il canale USB è locale |
| Chiavi nel repo | MAI — solo in NVS hardware | Nessun rischio leakage su git |

**Punto debole residuo**: il canale USB tra Pi e ESP32-S3 trasmette `EVT CARD_OK <user_id>` in chiaro. Poiché è un bus fisico locale (non di rete), il rischio è accettabile. Se richiesto, si può aggiungere un HMAC sul messaggio USB firmato con una shared secret Pi↔ESP32-S3.

---

## 9. Stima fattibilità

### Complessità: MEDIA-ALTA

| Componente | Difficoltà | Note |
|------------|------------|------|
| Firmware GPIO ISR + PWM | Media | Standard ESP-IDF |
| Firmware PN532 SPI | Media | Driver disponibile in esp-idf-lib |
| **Firmware DESFire EV3 AES auth** | **Alta** | Implementazione manuale protocollo NXP AN10609 |
| Firmware NVS + flash encryption | Media | Documentato in ESP-IDF Security Guide |
| Go gpio_usb.go + smartcard.go | Bassa-Media | Seriale + parsing semplice |
| Personalizzazione tessere | Media | Tool Python su PC con PN532/ACR122U |

### Stima ore

| Attività | Ore |
|----------|-----|
| Firmware base (USB CDC, GPIO ISR, PWM) | 4–6 h |
| Driver PN532 SPI + lettura ISO14443-4 | 3–5 h |
| **DESFire EV3: SelectApp + 3-pass AES auth** | **8–14 h** |
| DESFire: ReadFile + CMAC/TMAC verify | 4–6 h |
| NVS key storage + provisioning tool | 3–4 h |
| Flash Encryption + Secure Boot setup | 2–4 h |
| Go: gpio_usb.go + smartcard.go | 4–6 h |
| Tool personalizzazione tessere (Python) | 3–5 h |
| Test integrazione end-to-end | 4–6 h |
| **Totale** | **35–56 h** |

Il range è ampio perché il fattore dominante è l'implementazione del protocollo DESFire: esiste codice di riferimento (libfreefare, AN10609) ma il porting su ESP32-S3 richiede attenzione ai dettagli (endianness, padding CBC, rotazioni).

### Rischi principali

| Rischio | Probabilità | Mitigazione |
|---------|-------------|-------------|
| Implementazione AES-CBC con errori subtili | Alta | Testare con vettori di test NXP AN10609 prima di testare su carta reale |
| PN532 instabile su SPI ad alta velocità | Media | Clock SPI a 1MHz (invece di max 5MHz) nella fase di debug |
| Versione tessere non EV3 (es. EV1/EV2) | Media | EV1/EV2 usano lo stesso protocollo AES — compatibile con minime differenze |
| Chiavi perse / NVS corrotto | Bassa | Backup chiavi in vault separato; procedura di re-provisioning |
| Flash encryption irrecuperabile | Bassa | Non abilitare in produzione finché non testato a fondo in debug |

---

## 10. Roadmap

### Fase 1 — Debug parallelo (GPIO-OVER-USB)
- [ ] Firmware: USB CDC + GPIO ISR + PWM fan funzionanti
- [ ] Firmware: PN532 SPI, lettura UID grezzo (senza DESFire)
- [ ] Go: `gpio_usb.go` — connessione seriale, log eventi GPIO
- [ ] Test: pulsanti, ventola PWM, relè pulse

### Fase 2 — DESFire EV3
- [ ] Implementa `desfire_authenticate_and_read()` con test vector NXP
- [ ] Tool Python di personalizzazione tessere (PC + ACR122U/PN532)
- [ ] Personalizza 2–3 tessere di test
- [ ] Test autenticazione completa + TMAC
- [ ] Go: `smartcard.go` con `smartcards.json`

### Fase 3 — Hardening
- [ ] NVS provisioning + Flash Encryption
- [ ] Secure Boot V2
- [ ] Log accessi su Pi
- [ ] Test Proximity Check (anti-relay)

### Fase 4 — Migrazione produzione (opzionale)
- [ ] `gpio_usb.go` diventa backend primario
- [ ] Rimozione `gpio.go` (post-validazione)

---

## Appendice A — Regola udev

```udev
# /etc/udev/rules.d/99-gpio-esp32.rules
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", \
    SYMLINK+="gpio-esp32", MODE="0660", GROUP="dialout"
```

## Appendice B — Dipendenze Go

```bash
go get go.bug.st/serial    # porta seriale
```

## Appendice C — Struttura firmware

```
firmware/
├── main/
│   ├── main.c             ← app_main, task spawn
│   ├── gpio_handler.c     ← ISR + gpio_isr_task
│   ├── gpio_output.c      ← SET/PWM handler
│   ├── pwm_fan.c          ← LEDC 25kHz
│   ├── pn532_spi.c        ← driver PN532 via SPI
│   ├── desfire_auth.c     ← 3-pass AES auth + TMAC (cuore del progetto)
│   ├── desfire_file.c     ← ReadData con session key
│   ├── nfc_task.c         ← orchestrazione NFC + invio eventi USB
│   ├── usb_cdc.c          ← TinyUSB CDC + parser comandi
│   ├── key_store.c        ← NVS load/save chiavi
│   └── watchdog.c         ← safe_state()
├── CMakeLists.txt
└── sdkconfig.defaults     ← USB OTG, Flash Encryption off (debug)
```

## Appendice D — Tool personalizzazione tessere (Python, PC)

```python
# Richiede: pip install nfcpy  o  libreria freefare via pyscard
# Hardware: ACR122U o PN532 su USB-UART (collegato al PC, non al Pi)

import nfc
import struct

AID        = bytes([0xA5, 0xC3, 0x01])
APP_KEY    = bytes.fromhex("000102030405060708090A0B0C0D0E0F")  # CAMBIARE!
USER_ID    = b"MarioRossi\x00\x00\x00\x00\x00\x01"            # 16 byte

def personalize_card(tag):
    app = nfc.tag.tt4.Application(tag, AID)
    app.select()
    app.authenticate(key_no=0, key=b'\x00'*16)  # master key default
    app.change_key(key_no=1, new_key=APP_KEY)    # imposta read key
    app.create_std_data_file(file_no=1, size=16,
                              access=(0x11, 0xFF, 0xFF, 0xFF))
    # Autenticati con la nuova chiave per scrivere
    app.authenticate(key_no=1, key=APP_KEY)
    app.write_data(file_no=1, offset=0, data=USER_ID)
    print(f"Tessera personalizzata: UID={tag.identifier.hex().upper()}")

with nfc.ContactlessFrontend('usb') as clf:
    clf.connect(rdwr={'on-connect': personalize_card})
```

> **Attenzione**: le chiavi nel tool di personalizzazione devono corrispondere esattamente a quelle caricate nell'NVS dell'ESP32-S3. Conservare le chiavi in un vault sicuro (non nel repo).

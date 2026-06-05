# ESP32 — Occupanti Piano: protocollo e salvataggio su LittleFS

Guida per implementare sul firmware ESP32-S3 la gestione dei testi occupanti (3 piani × 4 slot), la comunicazione con il Raspberry Pi via USB seriale, e la persistenza su LittleFS.

---

## Protocollo USB seriale (Pi ↔ ESP32)

La comunicazione avviene su `Serial` a **115200 baud**, un comando per riga terminata da `\n`.  
Ogni piano ha **4 slot** di testo (max 20 caratteri ciascuno), separati da `|` sulla seriale.

### Comandi

| Direzione   | Comando                        | Significato                            |
|-------------|--------------------------------|----------------------------------------|
| Pi → ESP32  | `FLOOR-GET`                    | chiede i valori correnti di tutti i piani |
| ESP32 → Pi  | `FLOOR-P1 s1\|s2\|s3\|s4`     | risposta: 4 slot del piano 1 (poi P2, P3) |
| Pi → ESP32  | `FLOOR-SET P1 s1\|s2\|s3\|s4` | imposta i 4 slot del piano 1           |
| ESP32 → Pi  | `ACK FLOOR-SET P1`             | conferma ricezione (il Pi aspetta max 3s) |

### Regole formato

- Sempre **4 slot** separati da `|`, anche se vuoti: `Mario||Bianchi|`
- Slot vuoto = stringa vuota tra i `|`
- Il Pi invia `FLOOR-GET` automaticamente ad ogni connessione USB

### Esempio di sessione

```
→ FLOOR-GET
← FLOOR-P1 Mario Rossi|Lucia Rossi||
← FLOOR-P2 Bianchi|||
← FLOOR-P3 |||

→ FLOOR-SET P2 Bianchi|Verdi||
← ACK FLOOR-SET P2
```

---

## File di persistenza su LittleFS

Il firmware salva i valori in `/floors.json` con questo formato:

```json
{
  "p1": ["Mario Rossi", "Lucia Rossi", "", ""],
  "p2": ["Bianchi", "", "", ""],
  "p3": ["", "", "", ""]
}
```

I valori vengono caricati all'avvio da `loadFloors()` e salvati ad ogni `FLOOR-SET` da `saveFloors()`.

---

## 1. Librerie necessarie

### PlatformIO — `platformio.ini`

```ini
[env:esp32-s3]
platform  = espressif32
board     = esp32-s3-devkitc-1
framework = arduino

board_build.filesystem = littlefs
board_build.partitions = default_8MB.csv   ; scegli in base alla tua board

lib_deps =
    bblanchon/ArduinoJson @ ^7.0.0
    ; LittleFS è già nell'ESP32 Arduino Core
```

Schemi di partizione disponibili in:
```
~/.platformio/packages/framework-arduinoespressif32/tools/partitions/
```
Lo schema `default` (4MB) include una partizione LittleFS da 1.5MB — più che sufficiente.

### Arduino IDE

- **ArduinoJson v7**: Sketch → Includi libreria → Gestisci librerie → cerca `ArduinoJson` (Benoit Blanchon) → installa v7.x
- **LittleFS**: già inclusa. Se il tuo core è vecchio: File → Preferenze → URL aggiuntivi → `https://raw.githubusercontent.com/espressif/arduino-esp32/gh-pages/package_esp32_index.json`
- **Partition Scheme**: Strumenti → Partition Scheme → scegli voce con "SPIFFS" o "LittleFS"

---

## 2. Variabili globali

```cpp
#include <Arduino.h>
#include <LittleFS.h>
#include <ArduinoJson.h>

// 3 piani × 4 slot, max 20 caratteri per slot
String floorSlots[3][4] = {
    {"", "", "", ""},   // P1
    {"", "", "", ""},   // P2
    {"", "", "", ""}    // P3
};

const char* FLOORS_FILE = "/floors.json";
```

---

## 3. `loadFloors()` — legge da LittleFS

Chiama **una volta sola** nel `setup()`, dopo `LittleFS.begin()`.

```cpp
void loadFloors() {
    if (!LittleFS.exists(FLOORS_FILE)) {
        Serial.println("[FS] floors.json non trovato, parto con slot vuoti");
        return;
    }

    File f = LittleFS.open(FLOORS_FILE, "r");
    if (!f) {
        Serial.println("[FS] ERR: impossibile aprire floors.json in lettura");
        return;
    }

    JsonDocument doc;
    DeserializationError err = deserializeJson(doc, f);
    f.close();

    if (err) {
        Serial.print("[FS] ERR JSON deserialize: ");
        Serial.println(err.c_str());
        return;
    }

    // Formato: {"p1":["s1","s2","s3","s4"], "p2":[...], "p3":[...]}
    const char* keys[3] = {"p1", "p2", "p3"};
    for (int p = 0; p < 3; p++) {
        for (int s = 0; s < 4; s++) {
            floorSlots[p][s] = doc[keys[p]][s] | "";
        }
    }

    // Log di conferma
    for (int p = 0; p < 3; p++) {
        Serial.print("[FS] P" + String(p + 1) + ": ");
        for (int s = 0; s < 4; s++) {
            if (s) Serial.print("|");
            Serial.print(floorSlots[p][s]);
        }
        Serial.println();
    }
}
```

---

## 4. `saveFloors()` — scrive su LittleFS

Chiama dopo ogni `FLOOR-SET` ricevuto.

```cpp
void saveFloors() {
    File f = LittleFS.open(FLOORS_FILE, "w");
    if (!f) {
        Serial.println("[FS] ERR: impossibile aprire floors.json in scrittura");
        return;
    }

    // Formato: {"p1":["s1","s2","s3","s4"], "p2":[...], "p3":[...]}
    JsonDocument doc;
    const char* keys[3] = {"p1", "p2", "p3"};
    for (int p = 0; p < 3; p++) {
        JsonArray arr = doc[keys[p]].to<JsonArray>();
        for (int s = 0; s < 4; s++) {
            arr.add(floorSlots[p][s]);
        }
    }

    size_t written = serializeJson(doc, f);
    f.close();

    if (written == 0) {
        Serial.println("[FS] ERR: 0 byte scritti su floors.json");
    } else {
        Serial.println("[FS] salvato floors.json (" + String(written) + " bytes)");
    }
}
```

---

## 5. `setup()`

```cpp
void setup() {
    Serial.begin(115200);
    delay(500);

    if (!LittleFS.begin(true)) {   // true = formatta automaticamente se corrotto
        Serial.println("[FS] ERR: mount LittleFS fallito");
    } else {
        Serial.println("[FS] LittleFS montato OK");
        loadFloors();
    }

    updateDisplay();   // aggiorna il display con i valori caricati
    // ... resto del tuo setup (WiFi, OLED, ecc.)
}
```

---

## 6. Handler seriale

Integra nel punto del tuo firmware dove già gestisci i comandi seriali dal Pi (`GET-STATE`, `TABLET-ON`, `FAN-xx`, ecc.).

```cpp
// Lettura riga-per-riga nel loop()
String _serialBuf = "";

void loop() {
    while (Serial.available()) {
        char c = Serial.read();
        if (c == '\n') {
            _serialBuf.trim();
            if (_serialBuf.length() > 0) handlePiCommand(_serialBuf);
            _serialBuf = "";
        } else if (c != '\r') {
            _serialBuf += c;
        }
    }
    // ... resto del tuo loop
}

void handlePiCommand(String line) {

    // ── FLOOR-GET ─────────────────────────────────────────────────────────────
    // Risponde con i valori correnti di tutti e 3 i piani
    if (line == "FLOOR-GET") {
        for (int p = 0; p < 3; p++) {
            String resp = "FLOOR-P" + String(p + 1) + " ";
            for (int s = 0; s < 4; s++) {
                if (s > 0) resp += "|";
                resp += floorSlots[p][s];
            }
            Serial.println(resp);
        }
        return;
    }

    // ── FLOOR-SET P1|P2|P3 s1|s2|s3|s4 ──────────────────────────────────────
    // Imposta i 4 slot di un piano e salva su LittleFS
    if (line.startsWith("FLOOR-SET ")) {
        // "FLOOR-SET P2 Bianchi|Verdi||"
        //  0         10
        String rest  = line.substring(10);       // "P2 Bianchi|Verdi||"
        String floor = rest.substring(0, 2);     // "P2"
        String data  = rest.length() > 3 ? rest.substring(3) : "|||";  // "Bianchi|Verdi||"

        int idx = -1;
        if      (floor == "P1") idx = 0;
        else if (floor == "P2") idx = 1;
        else if (floor == "P3") idx = 2;

        if (idx < 0) {
            Serial.println("ERR FLOOR-SET piano non valido: " + floor);
            return;
        }

        // Split per '|' — raccoglie esattamente 4 slot
        int slotIdx = 0, start = 0;
        for (int c = 0; c <= (int)data.length() && slotIdx < 4; c++) {
            if (c == (int)data.length() || data[c] == '|') {
                floorSlots[idx][slotIdx++] = data.substring(start, c);
                start = c + 1;
            }
        }

        saveFloors();     // persiste su LittleFS
        updateDisplay();  // aggiorna display fisico
        Serial.println("ACK FLOOR-SET " + floor);   // obbligatorio entro 3s
        return;
    }

    // ... altri comandi (GET-STATE, TABLET-ON, FAN-xx, PING, ecc.)
}
```

---

## 7. Flusso alla connessione USB

Il Pi invia i seguenti comandi in sequenza subito dopo la connessione:

| # | Pi → ESP32   | ESP32 → Pi                           |
|---|-------------|--------------------------------------|
| 1 | `GET-STATE`  | `STATE FAN:0 TABLET:OFF`             |
| 2 | `FLOOR-GET`  | `FLOOR-P1 Mario Rossi\|Lucia\|\|`    |
|   |              | `FLOOR-P2 Bianchi\|\|\|`             |
|   |              | `FLOOR-P3 \|\|\|`                    |

Quando l'utente modifica dal pannello web:

| # | Pi → ESP32                    | ESP32 → Pi          |
|---|-------------------------------|---------------------|
| 1 | `FLOOR-SET P2 Bianchi\|Verdi\|\|` | `ACK FLOOR-SET P2` |

---

## 8. Debug e reset

### Stampa il contenuto del file salvato

```cpp
void debugFloorFile() {
    if (!LittleFS.exists(FLOORS_FILE)) {
        Serial.println("[DBG] floors.json non esiste"); return;
    }
    File f = LittleFS.open(FLOORS_FILE, "r");
    Serial.print("[DBG] floors.json → ");
    while (f.available()) Serial.write(f.read());
    Serial.println();
    f.close();
}
```

Aggiungi `debugFloorFile()` nel `setup()` subito dopo `loadFloors()` per verificare al primo avvio.

### Reset completo degli slot

```cpp
void resetFloors() {
    for (int p = 0; p < 3; p++)
        for (int s = 0; s < 4; s++)
            floorSlots[p][s] = "";
    LittleFS.remove(FLOORS_FILE);
    saveFloors();
    Serial.println("[FS] floors resettato");
}
```

### Log USB atteso nel pannello web

Alla connessione:
```
→ FLOOR-GET
← FLOOR-P1 Mario Rossi|Lucia Rossi||
← FLOOR-P2 Bianchi|||
← FLOOR-P3 |||
```

Dopo un aggiornamento dal pannello:
```
→ FLOOR-SET P1 Mario|Lucia|Marco|
← ACK FLOOR-SET P1
```

 <div align="center">
  <img src="logo.svg" alt="DoorPhoneServer Logo" width="160"/>
</div>

# DoorPhoneServer

Sistema di citofonia IP basato su Mumble per Raspberry Pi. Gestisce chiamate audio bidirezionali, controllo relè porta, telecamera IP, notifiche push e accesso NFC/RFID. Supporta due backend IO intercambiabili: GPIO diretti del Raspberry Pi oppure **due schede ESP32-S3 via USB** — una dedicata a NFC e pulsanti campanelli, l'altra a relè porta, alimentazione tablet e ventola PWM.

---

## Ecosistema DoorPhone

| Repository | Descrizione |
|---|---|
| **[DoorPhoneServer](https://github.com/MirkoUgoliniDev/DoorPhoneServer)** ← *sei qui* | Server Go per Raspberry Pi: audio, relè, telecamera, notifiche |
| **[DoorPhoneAndroidApp](https://github.com/MirkoUgoliniDev/DoorPhoneAndroidApp)** | App Android per tablet a parete — display citofono con video, apertura porta e gestione chiamate Mumble |
| **[DoorPhoneServerUSBInterface](https://github.com/MirkoUgoliniDev/DoorPhoneServerUSBInterface)** ⚠ *in sviluppo* | Due schede ESP32-S3: lettore RFID/NFC + pulsanti (ESP32-A) e relè + ventola (ESP32-B) |

---

## Architettura dual ESP32-S3

Invece di collegare relè, pulsanti e lettore RFID direttamente ai GPIO del Raspberry Pi, il sistema usa **due ESP32-S3 separati** via USB CDC (115200 baud, ASCII line-oriented):

| Device | Ruolo | Responsabilità |
|--------|-------|----------------|
| **ESP32-A** | `RFID` | Lettore NFC DESFire EV3, pulsanti P1/P2/P3, display occupanti, chiave AES-128 |
| **ESP32-B** | `RELAY` | Relè apertura porta, alimentazione tablet Android, ventola PWM 25kHz |

### Auto-identificazione — nessuna configurazione manuale

Il Pi **non usa path USB fissi** (`/dev/esp32-rfid`, `/dev/esp32-relay`). All'avvio di ogni connessione, il bridge Go invia `GET-ROLE\n` a ciascun device `/dev/ttyACM*`. Ogni ESP32 risponde con il proprio ruolo:

```
Pi → ESP32-A:  GET-ROLE
ESP32-A → Pi:  HELLO RFID

Pi → ESP32-B:  GET-ROLE
ESP32-B → Pi:  HELLO RELAY
```

Il bridge assegna automaticamente le porte. Se un device viene ricollegato su una porta diversa, al prossimo riavvio del servizio viene identificato di nuovo senza alcuna modifica di configurazione.

La regola udev è **generica** — valida per tutti i device Espressif senza seriali:
```
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", MODE="0660", GROUP="dialout"
```

> Questa regola copre l'USB CDC nativo dell'ESP32-S3 (`/dev/ttyACM*`, VID `303a`). Per board con chip USB-UART esterno (CP2102 `10c4`, CH340 `1a86`, FTDI `0403`) — che appaiono come `/dev/ttyUSB*` — il file `99-esp32.rules` contiene già le righe corrispondenti pronte da decommentare.

Nel pannello web (tab ESP32) i campi `rfid_port` e `relay_port` mostrano su quale `/dev/ttyACM*` ciascun ruolo è stato assegnato, utile per diagnostica.

### Apertura porta con badge NFC

```
Badge DESFire → ESP32-A: auth AES-128 OK → EVT nfc <UID>
Pi: verifica whitelist → SET unlockdoor pulse → ESP32-B: impulso relè
```

Modello **crypto-only**: solo tessere DESFire (AES-128) sono accettate. L'ESP32-A non tiene alcuna whitelist locale — emette `EVT nfc <UID>` **solo** per tessere che superano l'auth AES (i cloni PLAIN dell'UID vengono scartati a bordo e non arrivano mai al Pi). I due ESP32 non comunicano mai direttamente: è il Pi che riceve `EVT nfc <UID>` da ESP32-A, verifica la whitelist locale, e invia `SET unlockdoor pulse` a ESP32-B. `UID-OK`/`UID-KO` restano solo come diagnostica dell'auth a bordo.

---

## Indice

1. [Requisiti hardware](#1-requisiti-hardware)
2. [Parte A — Flash del sistema operativo](#2-parte-a--flash-del-sistema-operativo)
3. [Parte B — Primo avvio e connessione SSH](#3-parte-b--primo-avvio-e-connessione-ssh)
4. [Parte C — Preparazione del sistema](#4-parte-c--preparazione-del-sistema)
5. [Parte D — Setup Wizard: avvio e navigazione](#5-parte-d--setup-wizard-avvio-e-navigazione)
6. [Parte E — Setup Wizard: i passi spiegati](#6-parte-e--setup-wizard-i-passi-spiegati)
7. [Parte F — Dopo l'installazione](#7-parte-f--dopo-linstallazione)
8. [Configurazione doorphoneserver.xml](#8-configurazione-doorphoneserverxml)
9. [Backend IO: RPi vs ESP32](#9-backend-io-rpi-vs-esp32)
10. [Variabili d'ambiente (.env)](#10-variabili-dambiente-env)
11. [Struttura file sul sistema](#11-struttura-file-sul-sistema)
12. [Comandi utili](#12-comandi-utili)
13. [Aggiornamento e rebuild](#13-aggiornamento-e-rebuild)
14. [Problemi comuni](#14-problemi-comuni)
15. [Pannello Web — NFC Whitelist](#15-pannello-web--nfc-whitelist)
16. [Pannello Web — Tab ESP32: Occupanti Piano](#16-pannello-web--tab-esp32-occupanti-piano)
17. [Chiave AES DESFire — gestione e sicurezza](#17-chiave-aes-desfire--gestione-e-sicurezza)
18. [Firmware ESP32 — sviluppo e protocollo](#18-firmware-esp32--sviluppo-e-protocollo)

---

## 1. Requisiti hardware

| Componente | Requisito | Note |
|---|---|---|
| Raspberry Pi | **4B 2GB+** (consigliato) | Funziona su 3B+ e 5; il 4B è il più testato |
| microSD | **16 GB minimo**, classe 10 o UHS-1 | 32 GB consigliati per margine |
| Scheda audio USB | **C-Media CM108** o compatibile | Necessaria — il jack del Pi non è sufficiente |
| Alimentatore | **Ufficiale 5V/3A** per Pi 4 | Un alimentatore debole causa crash casuali |
| Connessione di rete | Ethernet (consigliato) o WiFi 2.4/5 GHz | L'Ethernet evita problemi durante l'install |
| PC con terminale | Qualsiasi OS | Per connettersi via SSH durante l'install |

**Hardware opzionale:**
- **ESP32-A** ⚠ *firmware in sviluppo* — lettore NFC DESFire EV3, pulsanti di piano P1/P2/P3, display occupanti; vedi [Docs/esp32-firmware-rfid.md](Docs/esp32-firmware-rfid.md)
- **ESP32-B** ⚠ *firmware in sviluppo* — relè apertura porta, alimentazione tablet Android, ventola PWM 25kHz; vedi [Docs/esp32-firmware-relay.md](Docs/esp32-firmware-relay.md)
- Modulo relè GPIO 5V per controllo elettroserratura (alternativa alla scheda ESP32-B)
- Tablet Android con [DoorPhoneAndroidApp](https://github.com/MirkoUgoliniDev/DoorPhoneAndroidApp) — display citofono a parete
- Telecamera IP con stream RTSP (testata: Reolink)

---

## 2. Parte A — Flash del sistema operativo

### A1. Scarica Raspberry Pi Imager

Vai su **[raspberrypi.com/software](https://www.raspberrypi.com/software/)** e scarica l'Imager per il tuo sistema operativo.

### A2. Scegli il sistema operativo

Nel menu **"Scegli OS"**, naviga in:
```
Raspberry Pi OS (other)
  → Raspberry Pi OS Lite (64-bit)
```

> Scegli **Lite** (senza desktop): DoorPhoneServer gira come servizio di sistema. Scegli la variante **64-bit**.

### A3. Configura le impostazioni avanzate (FONDAMENTALE)

Prima di scrivere, clicca l'icona **⚙ (Impostazioni avanzate)** e compila:

| Campo | Valore consigliato |
|---|---|
| **Nome host** | `doorphoneserver` |
| **Abilita SSH** | ✅ Sì — autenticazione tramite password |
| **Username** | `pi` |
| **Password** | Una password sicura |
| **Configura WiFi** | SSID e password della tua rete |
| **Paese WiFi** | IT |
| **Fuso orario** | Europe/Rome |
| **Layout tastiera** | it |

Clicca **Salva**, poi **Scrivi**. Attendi 3–5 minuti.

### A4. Inserisci la SD e accendi il Pi

Il Pi impiega **60–90 secondi** per completare il primo avvio.

---

## 3. Parte B — Primo avvio e connessione SSH

### B1. Trova l'IP del Pi

```bash
ping doorphoneserver.local   # Linux/macOS
```

Oppure controlla la lista dispositivi del router.

### B2. Connettiti via SSH

```bash
ssh pi@doorphoneserver.local
# oppure
ssh pi@192.168.1.XXX
```

Al primo collegamento digita `yes` per accettare la chiave host. Inserisci la password impostata nell'Imager.

---

## 4. Parte C — Preparazione del sistema

### C1. Aggiornamento del sistema operativo

```bash
sudo apt-get update && sudo apt-get -y full-upgrade
sudo reboot
```

Riconnettiti dopo il riavvio.

### C2. Installa git, pip e Flask

```bash
sudo apt install git python3-pip python3-flask -y
```

### C3. Collega la scheda audio USB

```bash
aplay -l   # deve mostrare "USB Audio Device"
```

### C4. Abilita sudo senza password

Il wizard usa `sudo -n` (non-interattivo). Su alcune immagini Raspberry Pi OS è già attivo. Se non lo è:

```bash
echo "pi ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/010_pi-nopasswd
sudo chmod 440 /etc/sudoers.d/010_pi-nopasswd
sudo -n true && echo "OK"
```

### C5. Clona il repository

```bash
git clone https://github.com/MirkoUgoliniDev/DoorPhoneServer ~/doorphoneserver-setup
cd ~/doorphoneserver-setup
```

---

## 5. Parte D — Setup Wizard: avvio e navigazione

### D1. Avvia il wizard

```bash
python3 setup/wizard.py --web
```

Il wizard stampa l'indirizzo da aprire nel browser (es. `http://192.168.1.151:8888`).

> Il terminale SSH deve restare aperto mentre usi il wizard nel browser.

### D2. Modalità DRY-RUN

Il toggle **DRY-RUN** in alto simula tutta l'installazione senza modificare nulla. Spegnilo per installare davvero.

---

## 6. Parte E — Setup Wizard: i passi spiegati

### Passo 1 — Controllo Sistema
Verifica modello Pi, OS, spazio disco (minimo 3 GB liberi), connessione internet.

### Passo 2 — Hostname
Imposta il nome host con `hostnamectl` e aggiorna `/etc/hosts`.

### Passo 3 — Utente di Sistema
Crea l'utente `doorphoneserver` e lo aggiunge ai gruppi `audio`, `gpio`, `dialout`.

### Passo 4 — Credenziali .env
Scrive `/home/doorphoneserver/.env` con le credenziali inserite nel form (permessi 600).

| Campo | Cosa inserire |
|---|---|
| Mumble Username | Nome sul server Mumble (default: `Doorpi`) |
| Mumble Password | Password Mumble locale |
| Camera Username / Password | Credenziali telecamera IP |
| Pushover API Token / User Key | Per notifiche push (opzionale) |
| OpenRouter API Key | Per funzionalità AI (opzionale) |

### Passo 5 — Pacchetti APT
Installa ~15 pacchetti: librerie audio, ffmpeg, mplayer, mumble-server, python3-flask, rsync. **3–8 minuti.**

### Passo 6 — Go Language
Scarica e installa Go 1.24.4 (~130 MB). Se già presente alla versione corretta, salta il download. **2–5 minuti.**

### Passo 7 — Configurazione Audio
Rileva le schede audio e scrive `/etc/asound.conf`. Seleziona la scheda USB (non "bcm2835").

### Passo 8 — Mumble Server
Configura e avvia `mumble-server`.

### Passo 9 — Config Boot RPi
Modifica `/boot/firmware/config.txt`: audio BCM off, Bluetooth off, GPU memory 16 MB.

### Passo 10 — Clone & Build
Clona il repository in `/home/doorphoneserver/` e compila il binario. **Il passo più lungo: 10–25 minuti su Pi 4.**

### Passo 11 — Directory & Certificati
Crea `preferences/`, genera il certificato TLS Mumble.

### Passo 12 — Regola udev ESP32-S3

Installa `/etc/udev/rules.d/99-esp32.rules` — una **singola regola generica** per tutti i device Espressif (VID `303a`):

```
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", MODE="0660", GROUP="dialout"
```

Non richiede numeri seriali, non richiede di collegare i device durante il setup. Il bridge Go identifica automaticamente quale porta è RFID e quale è RELAY tramite il protocollo `GET-ROLE`/`HELLO`. Il passo:
1. Scrive la regola (incluse le righe commentate per chip UART esterni CP2102/CH340/FTDI)
2. `udevadm control --reload-rules && udevadm trigger` (warning non bloccante se falliscono — la regola entra in vigore al reboot)
3. Logga informativamente i device `ttyACM*` già presenti (non bloccante)

### Passo 13 — Servizio Systemd
Installa e abilita `doorphoneserver.service`. Configura sudoers per `systemctl` senza password.

### Passo 14 — Log2Ram (opzionale)
Installa Log2Ram: mantiene i log in RAM, prolunga la vita della microSD.

### Passo 15 — Pulizia
Rimuove cache Go e APT. Pianifica la rimozione della cartella di setup temporanea (`~/doorphoneserver-setup`) non appena il wizard viene chiuso.

---

### Se un passo fallisce

1. Leggi il log nella card del passo
2. Risolvi il problema (connessione internet, spazio disco, ecc.)
3. Clicca di nuovo **▶ Avvia Installazione** — i passi sono idempotenti

---

## 7. Parte F — Dopo l'installazione

### F1. Configura doorphoneserver.xml

```bash
nano /home/doorphoneserver/doorphoneserver.xml
```

Oppure via pannello web: `http://<ip-pi>:8080/panel` → tab **Config**.

Parametri da personalizzare:

```xml
<!-- IP telecamera -->
<endpoint>rtsp://192.168.1.XXX:554/Preview_01_sub</endpoint>

<!-- Backend IO: rpi (GPIO del Pi) oppure esp32 (due ESP32-S3) -->
<io backend="esp32">
```

### F2. Riavvio finale

```bash
sudo reboot
```

Dopo il riavvio `mumble-server` e `doorphoneserver` partono automaticamente.

### F3. Verifica che tutto funzioni

```bash
systemctl status doorphoneserver
systemctl status mumble-server
journalctl -u doorphoneserver -n 50
```

Con backend ESP32 collegato, il log deve mostrare:
```
[USB-RFID] ESP32-RFID connesso su /dev/ttyACM0
[USB-RELAY] ESP32-RELAY connesso su /dev/ttyACM1
[GPIO-USB] avviato
```

### F4. Connetti il client Mumble

| Campo | Valore |
|---|---|
| Indirizzo | IP del Raspberry Pi |
| Porta | `64738` |
| Username | Quello nel `.env` |
| Password | Password Mumble inserita nel wizard |

---

## 8. Configurazione doorphoneserver.xml

### Account Mumble

```xml
<accounts>
  <account name="doorphoneserver-community" default="true">
    <serverandport>127.0.0.1:64738</serverandport>
    <insecure>true</insecure>
    <ident>DoorPi</ident>
    <certificate>/home/doorphoneserver/mumble.pem</certificate>
    <channel/>
  </account>
</accounts>
```

### Settings

```xml
<settings>
  <outputdevice>PCM</outputdevice>
  <outputvolcontroldevice>PCM</outputvolcontroldevice>
  <outputmutecontroldevice>PCM</outputmutecontroldevice>
  <logfilenameandpath>/home/doorphoneserver/doorphoneserver.log</logfilenameandpath>
  <logging>fileonly</logging>
  <loglevel>info</loglevel>
  <logmaxsizemb>5</logmaxsizemb>
  <logretentiondays>7</logretentiondays>
  <streamonstart>false</streamonstart>
  <txonstart>false</txonstart>
</settings>
```

Per trovare il nome del controllo audio della tua scheda USB:
```bash
amixer -c 1 scontents | grep "Simple mixer"
```

### Telecamera IP

```xml
<camera debug="false">
  <video enabled="true">
    <endpoint>rtsp://192.168.1.XXX:554/Preview_01_sub</endpoint>
  </video>
  <snapshot enabled="true">
    <method>ffmpeg</method>
    <endpoint>http://192.168.1.XXX/cgi-bin/api.cgi?cmd=Snap&amp;channel=0&amp;rs=abc</endpoint>
    <maxsnapshots>100</maxsnapshots>
    <retentiondays>30</retentiondays>
    <dir>/home/doorphoneserver/snapshots</dir>
  </snapshot>
</camera>
```

### Pin GPIO — sezione `<io>`

```xml
<io backend="rpi">    <!-- rpi = GPIO Raspberry Pi | esp32 = due ESP32-S3 -->

  <pulse leadingmsecs="1000" pulsemsecs="1000" trailingmsecs="1000"/>

  <pins enabled="true">
    <input>
      <pin name="p1" pinno="22" enabled="true" log="false" desc="Pulsante P1"/>
      <pin name="p2" pinno="27" enabled="true" log="false" desc="Pulsante P2"/>
      <pin name="p3" pinno="17" enabled="true" log="false" desc="Pulsante P3"/>
    </input>
    <output>
      <pin name="unlockdoor"   pinno="5"  enabled="true" log="false" desc="Unlock Main Door"/>
      <pin name="power_tablet" pinno="19" enabled="true" log="false" desc="Power Tablet"/>
    </output>
  </pins>

</io>
```

I numeri `pinno` sono numeri BCM. Con `backend="esp32"` i `pinno` vengono ignorati — il routing avviene per nome (`unlockdoor` e `power_tablet` → ESP32-B, tutto il resto → ESP32-A).

---

## 9. Backend IO: RPi vs ESP32

```xml
<io backend="rpi">    <!-- GPIO del Raspberry Pi (default) -->
<io backend="esp32">  <!-- Due ESP32-S3 via USB, auto-scoperta per ruolo -->
```

### Confronto comportamenti

| Aspetto | `backend="rpi"` | `backend="esp32"` |
|---|---|---|
| **Pulsanti P1/P2/P3** | Loop GPIO polling (BCM, active-low, debounce 150ms) | Evento seriale `EVT p1 0` da ESP32-A |
| **Relè porta** | `gpio.NewOutput()` sul pin BCM `unlockdoor` | `SET unlockdoor pulse` su ESP32-B |
| **Power tablet** | GPIO pin `power_tablet` | `TABLET-ON` / `TABLET-OFF` su ESP32-B |
| **Ventola PWM** | Non disponibile | `FAN-XX` su ESP32-B |
| **Bridge USB** | Non avviato | Due bridge, scoperta automatica via `GET-ROLE`/`HELLO` |
| **NFC/Smartcard** | Non disponibile | Disponibile via ESP32-A |
| **Chiave AES DESFire** | Non applicabile | Generata e custodita su ESP32-A (mai trasmessa su USB) |
| **Tab ESP32 e NFC nel pannello** | Nascosti | Visibili |

### Routing comandi con backend ESP32

| Pin / Comando | Bridge | Trasporto |
|---|---|---|
| `unlockdoor` | ESP32-B | `SET unlockdoor pulse\|on\|off` |
| `power_tablet` | ESP32-B | `SET power_tablet on\|off` |
| Fan PWM | ESP32-B | `FAN-XX` |
| `p1`, `p2`, `p3` (input) | ricevuto da ESP32-A | `EVT p1\|p2\|p3 0\|1` |
| NFC / TAG-* / KEY-* | ESP32-A | vari |
| FLOOR-GET / FLOOR-SET | ESP32-A | protocollo display occupanti |

### Protocollo ESP32-A (RFID)

**Pi → ESP32-A:**

| Comando | Azione |
|---------|--------|
| `GET-ROLE` | Richiesta ruolo → `HELLO RFID` |
| `PING` | Watchdog keepalive (ogni 5s) → `PONG` |
| `GET-STATE` | Richiesta stato corrente |
| `TAG-SCAN` | Avvia auto-detect + enroll DESFire del prossimo tag |
| `TAG-INFO` | Modalità lettura one-shot: identifica il prossimo tag (diagnostica) |
| `KEY-STATUS` | Stato chiave AES-128 DESFire |
| `KEY-GEN` | Genera chiave AES (solo se assente) |
| `KEY-GEN FORCE` | Rigenera chiave (invalida tutti i badge) |
| `FLOOR-GET` | Richiesta testi occupanti P1/P2/P3 |
| `FLOOR-SET P1 s1\|s2\|s3\|s4` | Imposta 4 nominativi piano 1 |

**ESP32-A → Pi:**

| Messaggio | Significato |
|-----------|-------------|
| `HELLO RFID` | Auto-identificazione (boot + risposta GET-ROLE) |
| `EVT p1 0` / `EVT p2 0` / `EVT p3 0` | Pulsante piano premuto (active-low) |
| `RING-P1` / `RING-P2` / `RING-P3` | Chiamata dal piano |
| `EVT nfc <uid>` | Tessera DESFire con auth AES OK → Pi verifica whitelist e apre porta su ESP32-B |
| `UID-OK` | Diagnostica: auth a bordo riuscita (l'apertura è decisa dal Pi su `EVT nfc`) |
| `UID-KO` | Diagnostica: tessera non DESFire o auth fallita (nessun `EVT nfc` emesso) |
| `TAG-INFO <uid> PLAIN\|DESFIRE-CONFIGURED\|DESFIRE-NEW` | Tag identificato (auto-scan o modalità lettura) |
| `TAG-ENROLLED <uid> DESFIRE` | Tag enrollato → Pi lo aggiunge alla whitelist |
| `TAG-FORMAT-OK <uid>` | DESFire inizializzato — riavvicinare il tag |
| `TAG-FORMAT-FAIL [NOT-DESFIRE\|NO-KEY]` | Errore inizializzazione |
| `TAG-ENROLL-FAIL [NOT-DESFIRE\|NO-KEY\|AUTH]` | Errore enroll (no DESFire / chiave assente / auth fallita) |
| `KEY-STATUS EMPTY` | Chiave AES non generata |
| `KEY-STATUS PRESENT FP:<hex8>` | Chiave presente, fingerprint SHA-256[:4] |
| `KEY-GEN-OK FP:<hex8>` | Chiave generata |
| `FLOOR-P1 s1\|s2\|s3\|s4` | Testi occupanti piano 1 |
| `ACK FLOOR-SET P1` | Conferma `FLOOR-SET` (entro 3s) |
| `PONG` | Risposta al PING |

### Protocollo ESP32-B (RELAY)

**Pi → ESP32-B:**

| Comando | Azione |
|---------|--------|
| `GET-ROLE` | Richiesta ruolo → `HELLO RELAY` |
| `PING` | Watchdog keepalive → `PONG` |
| `GET-STATE` | Richiesta stato corrente (fan + tablet) |
| `SET unlockdoor pulse` | Impulso relè portone ~200ms |
| `SET unlockdoor on\|off` | Relè portone on/off diretto |
| `TABLET-ON` / `TABLET-OFF` | Alimentazione tablet |
| `FAN-XX` | Ventola PWM al XX% (es. `FAN-75`) |

**ESP32-B → Pi:**

| Messaggio | Significato |
|-----------|-------------|
| `HELLO RELAY` | Auto-identificazione (boot + risposta GET-ROLE) |
| `STATE FAN:75 TABLET:ON` | Risposta a `GET-STATE` |
| `ACK FAN-XX` | Conferma impostazione ventola |
| `ACK TABLET-ON` / `ACK TABLET-OFF` | Conferma cambio tablet |
| `PONG` | Risposta al PING |

### Passare da RPi a ESP32

1. Collega ESP32-A e ESP32-B a due porte USB libere
2. Modifica `doorphoneserver.xml`: `backend="rpi"` → `backend="esp32"`
3. `sudo systemctl restart doorphoneserver`
4. Verifica nel log:
   ```
   [USB-RFID] ESP32-RFID connesso su /dev/ttyACM0
   [USB-RELAY] ESP32-RELAY connesso su /dev/ttyACM1
   ```

### Passare da ESP32 a RPi

1. Modifica `doorphoneserver.xml`: `backend="esp32"` → `backend="rpi"`
2. `sudo systemctl restart doorphoneserver`
3. Verifica nel log: `info: IO backend = RPi — ESP32/USB/NFC disabilitati`

---

## 10. Variabili d'ambiente (.env)

`/home/doorphoneserver/.env` — scritto dal wizard, permessi 600. Non versionato.

```bash
MUMBLE_USERNAME=Doorpi
MUMBLE_PASSWORD=la-tua-password

CAMERA_USERNAME=admin
CAMERA_PASSWORD=password-camera

PUSHOVER_API_TOKEN=
PUSHOVER_USER_KEY=

OPENROUTER_API_KEY=
```

Per modificarlo dopo l'installazione:
```bash
sudo nano /home/doorphoneserver/.env
sudo systemctl restart doorphoneserver
```

---

## 11. Struttura file sul sistema

```
/home/doorphoneserver/              ← home utente di sistema E repo Git
├── *.go                            ← sorgenti Go
│   ├── bridge_discovery.go         ← scoperta automatica ESP32 via GET-ROLE/HELLO
│   ├── usb_bridge.go               ← bridge seriale USB (un'istanza per ruolo)
│   ├── gpio_usb.go                 ← routing comandi RFID/RELAY
│   └── client.go                   ← inizializzazione due bridge
├── cmd/doorphoneserver/main.go     ← entrypoint binario
├── setup/
│   ├── wizard.py                   ← entry point wizard
│   ├── steps/udev_esp32.py         ← regola udev generica (VID 303a)
│   └── udev/
│       ├── 99-esp32.rules          ← regola generica installata in /etc/udev/rules.d/
│       └── find-esp32-serials.sh   ← diagnostico: mostra device ESP32 collegati
├── Docs/
│   ├── esp32-firmware-protocol.md  ← protocollo USB CDC completo (GET-ROLE, tutti i comandi)
│   ├── esp32-firmware-rfid.md      ← firmware ESP32-A (NFC, pulsanti, chiave AES)
│   └── esp32-firmware-relay.md     ← firmware ESP32-B (relè, tablet, fan)
├── bin/doorphoneserver             ← binario compilato
├── doorphoneserver.xml             ← configurazione principale
├── .env                            ← credenziali (600, non versionato)
├── mumble.pem                      ← certificato TLS Mumble
├── preferences/
│   ├── nfc_whitelist.json          ← whitelist NFC (unica lista UID autorizzati)
│   ├── floors.json                 ← occupanti piano P1/P2/P3
│   └── key_state.json              ← fingerprint chiave AES + flag re_enroll_needed
└── snapshots/                      ← foto dalla telecamera

/etc/
├── asound.conf                     ← ALSA (generato dal wizard)
├── openal/alsoft.conf
├── mumble-server.ini
├── systemd/system/doorphoneserver.service
├── sudoers.d/doorphoneserver-panel
├── udev/rules.d/99-esp32.rules     ← regola generica VID 303a (generata dal wizard)
└── log2ram.conf                    ← se Log2Ram installato

/boot/firmware/config.txt           ← BCM audio off, BT off, headless
```

---

## 12. Comandi utili

### Gestione servizio

```bash
sudo systemctl start doorphoneserver
sudo systemctl stop doorphoneserver
sudo systemctl restart doorphoneserver
systemctl status doorphoneserver
journalctl -u doorphoneserver -f        # log live
journalctl -u doorphoneserver -n 100    # ultimi 100 log
```

### ESP32 — diagnostica

```bash
# Elenca device Espressif collegati
bash /home/doorphoneserver/setup/udev/find-esp32-serials.sh

# Verifica regola udev installata
cat /etc/udev/rules.d/99-esp32.rules

# Ricarica regole udev (se la regola viene modificata a mano)
sudo udevadm control --reload-rules && sudo udevadm trigger --subsystem-match=tty

# Monitor seriale ESP32-A in tempo reale (Ctrl+C per uscire)
sudo screen /dev/ttyACM0 115200
# Test manuale GET-ROLE:
echo "GET-ROLE" | sudo tee /dev/ttyACM0

# Permesso accesso seriale senza sudo (se l'utente è già in dialout)
ls -l /dev/ttyACM*

# Quale porta è stata assegnata a ciascun ruolo (campi rfid_port / relay_port)
curl -s http://localhost:8080/panel/api/esp32/status | grep -o '"[a-z_]*port":"[^"]*"'
```

### Audio

```bash
aplay -l                                # lista schede output
arecord -l                              # lista schede input
amixer -c 1 scontents                   # controlli mixer scheda 1
speaker-test -c 1 -t wav -D plughw:1   # test speaker
```

### Mumble server

```bash
systemctl status mumble-server
sudo systemctl restart mumble-server
ss -tlnp | grep 64738
```

### Setup wizard

```bash
python3 setup/wizard.py --web           # Web UI (consigliato)
python3 setup/wizard.py --tui           # TUI testuale
python3 setup/wizard.py --audio-setup   # solo audio
python3 setup/wizard.py --dry-run --web # simulazione
```

### Log2Ram

```bash
systemctl status log2ram
sudo log2ram sync
```

---

## 13. Aggiornamento e rebuild

```bash
# Aggiorna i sorgenti
sudo -u doorphoneserver git -C /home/doorphoneserver pull

# Ricompila e installa
sudo bash /home/doorphoneserver/setup/scripts/build.sh

# Riavvia
sudo systemctl start doorphoneserver
```

Il build script ferma il servizio, compila (~5–15 minuti su Pi 4), installa il nuovo binario.

---

## 14. Problemi comuni

### `ModuleNotFoundError: No module named 'flask'`
```bash
sudo apt-get install -y python3-flask
```

### Nessuna scheda audio nel wizard
```bash
aplay -l   # la scheda USB deve comparire
```
Prova una porta USB diversa o riavvia il Pi con la scheda già collegata.

### Il servizio non trasmette audio
```bash
id doorphoneserver   # deve includere "audio" e "dialout"
sudo usermod -aG audio,dialout doorphoneserver
sudo systemctl restart doorphoneserver
```

### ESP32 non rilevato dal bridge

```bash
# Verifica che il device sia visibile
ls /dev/ttyACM*

# Verifica che la regola udev sia installata
cat /etc/udev/rules.d/99-esp32.rules

# Verifica che l'utente di sistema sia nel gruppo dialout
id doorphoneserver

# Test manuale GET-ROLE (deve rispondere HELLO RFID o HELLO RELAY)
sudo screen /dev/ttyACM0 115200
# poi digita: GET-ROLE
```

Se il device risponde con `HELLO RFID` o `HELLO RELAY`, il firmware è OK. Se non risponde, il firmware non implementa il protocollo GET-ROLE — vedi [sezione 18](#18-firmware-esp32--sviluppo-e-protocollo).

### Il log mostra "nessun device con ruolo RFID trovato"

Il bridge Go non trova nessun device che risponda `HELLO RFID`. Possibili cause:
- ESP32-A non collegato
- Firmware non ancora flashato o non implementa GET-ROLE
- Permessi di accesso al device (`/dev/ttyACM*` deve essere leggibile da `doorphoneserver`)

### Il build fallisce
```bash
/usr/local/go/bin/go version   # deve essere >= 1.24.x
```

### Reinstallazione mantenendo le credenziali
```bash
cp /home/doorphoneserver/.env ~/backup.env
cp /home/doorphoneserver/doorphoneserver.xml ~/backup.xml
# ... reinstalla ...
sudo cp ~/backup.env /home/doorphoneserver/.env
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/.env
sudo chmod 600 /home/doorphoneserver/.env
```

---

## 15. Pannello Web — NFC Whitelist

La pagina **NFC Whitelist** del pannello (`http://<ip-pi>:8080/panel`, tab "NFC Whitelist") permette di gestire i badge autorizzati. Richiede `backend="esp32"` e ESP32-A collegato.

### Architettura di sicurezza — doppio livello

```
Badge DESFire → ESP32-A (Livello 1: gate crittografico) → EVT nfc <UID> → Pi (Livello 2: gate autorizzazione) → ESP32-B: relè
```

Modello **crypto-only**: solo tessere DESFire (AES-128). L'ESP32-A **non tiene alcuna whitelist locale** — la lista degli UID autorizzati vive interamente sul Pi.

**Livello 1 — ESP32-A (gate crittografico):**
- Autentica la tessera con la master key AES-128 (challenge-response a 3 passi)
- Solo se l'auth riesce emette `EVT nfc <UID>`. Tag PLAIN, cloni UID e tessere di altri impianti falliscono l'auth e **non vengono mai riportati** al Pi

**Livello 2 — Pi (gate autorizzazione):**
- UID in whitelist JSON?
- Tag non disabilitato?
- Solo se entrambi → `SET unlockdoor pulse` a ESP32-B

### Aggiungere un tag — flusso auto-detect

Il pannello chiede solo il nome del titolare, poi invia `TAG-SCAN`. ESP32-A formatta/verifica la DESFire e riporta l'UID; il Pi salva l'associazione UID→nome nel proprio JSON. I tag non-DESFire vengono rifiutati (`TAG-ENROLL-FAIL NOT-DESFIRE`).

| Tipo tag | Tap richiesti | Flusso |
|---|---|---|
| DESFire già configurato | 1 | Nome → Attesa → Aggiunto |
| DESFire nuovo / vergine | 2 | Nome → Attesa → Formato → 2° tap → Aggiunto |

### Leggi tessera (modalità diagnostica)

Il pulsante **"Leggi tessera"** invia `TAG-INFO` e legge una tessera one-shot **senza aprire il portone**. Incrocia lo stato crittografico riportato dall'ESP32 (`DESFIRE-CONFIGURED` / `DESFIRE-NEW` / `PLAIN`) con la presenza in whitelist, dando un verdetto: operativa, valida ma non autorizzata, disabilitata, da inizializzare o non supportata. Utile per capire perché una tessera non apre o se è già registrata.

### Abilitare / Disabilitare

**Disabilita** blocca l'accesso al Livello 2 (policy Pi) senza rimuovere il tag. Utile per sospensioni temporanee. **Rimuovi** elimina il tag dal JSON del Pi (unica lista esistente).

### Persistenza

| Dove | File | Note |
|---|---|---|
| Pi | `preferences/nfc_whitelist.json` | **Unica** whitelist: UID → nome, tipo, abilitato, contatori |
| ESP32-A | — | Nessuna whitelist locale (solo la master key AES in NVS) |

---

## 16. Pannello Web — Tab ESP32: Occupanti Piano

Il tab **ESP32** del pannello include la card **Occupanti Piano**: 3 colonne (P1/P2/P3), 4 campi di testo per piano (max 20 caratteri), pulsante Invia per piano.

### Persistenza

| Dove | Formato |
|---|---|
| Pi | `preferences/floors.json` — `{"p1":[...],"p2":[...],"p3":[...]}` |
| ESP32-A | `/floors.json` su LittleFS — stesso formato |

Al collegamento USB il Pi invia `FLOOR-GET`; l'ESP32 risponde con `FLOOR-P1/P2/P3 s1|s2|s3|s4`. Vedi [Docs/esp32-floor-display.md](Docs/esp32-floor-display.md).

---

## 17. Chiave AES DESFire — gestione e sicurezza

### Architettura

La master key AES-128 per l'autenticazione DESFire EV3 **non transita mai su USB**. Viene generata da ESP32-A tramite TRNG hardware e resta nella NVS. Il Pi conosce solo il **fingerprint**: primi 4 byte del SHA-256 della chiave (8 caratteri hex), salvato in `preferences/key_state.json`.

### Ciclo di vita

All'avvio del servizio (dopo 8s), il Pi esegue `EnsureKey()`:

1. Invia `KEY-STATUS` a ESP32-A
2. Se `KEY-STATUS EMPTY` → genera: `KEY-GEN` → `KEY-GEN-OK FP:<hex8>`
3. Se `KEY-STATUS PRESENT FP:<hex8>`:
   - FP uguale al salvato → tutto OK
   - FP diverso → imposta `re_enroll_needed = true` nel JSON

### Pannello web — card "Chiave AES DeSFire"

| Elemento | Funzione |
|---|---|
| Badge fingerprint | FP corrente (8 hex char) |
| Pulsante "Genera chiave" | `KEY-GEN` (solo se assente) |
| Pulsante "Rigenera (FORCE)" | `KEY-GEN FORCE` (invalida tutti i badge, richiede conferma) |
| Banner warning re-enroll | Appare quando `re_enroll_needed = true`; scompare dopo conferma operatore |

### Cosa fare dopo una rigenerazione forzata

1. Tutti i badge DESFire esistenti diventano inutilizzabili
2. Re-enrollare ogni badge (viene rilevato come `DESFIRE-NEW` e reinizializzato con la nuova chiave)
3. Cliccare "Ho ri-enrollato tutto" nel banner per azzerare il flag `re_enroll_needed` (è una **conferma manuale**: nulla lo azzera automaticamente, nemmeno un accesso riuscito o il riavvio)

### Sicurezza fisica

> ⚠ La chiave in NVS è leggibile tramite JTAG/UART con accesso fisico alla scheda. Per protezione completa abilitare la **Flash Encryption** sull'ESP32-A prima del deployment definitivo. Operazione irreversibile — pianificare con attenzione.

---

## 18. Firmware ESP32 — sviluppo e protocollo

La documentazione completa del firmware è in [Docs/](Docs/):

| File | Contenuto |
|---|---|
| [esp32-firmware-protocol.md](Docs/esp32-firmware-protocol.md) | Protocollo USB CDC completo: formato messaggi, GET-ROLE/HELLO, tutti i comandi, timeout, note su concorrenza |
| [esp32-firmware-rfid.md](Docs/esp32-firmware-rfid.md) | Firmware ESP32-A: `platformio.ini`, `main.cpp` completo con NFC, pulsanti, chiave AES, display occupanti |
| [esp32-firmware-relay.md](Docs/esp32-firmware-relay.md) | Firmware ESP32-B: `platformio.ini`, `main.cpp` completo con relè, tablet, fan PWM, watchdog |

### Requisito minimo firmware

Ogni ESP32 deve implementare il protocollo di auto-identificazione:

```cpp
// In setup() — inviato all'avvio prima di qualsiasi altro messaggio
Serial.println("HELLO RFID");   // per ESP32-A
// oppure
Serial.println("HELLO RELAY");  // per ESP32-B

// Nel loop(), handler per GET-ROLE
if (command == "GET-ROLE") {
    Serial.println("HELLO RFID");   // o "HELLO RELAY"
}
```

Senza questo, il bridge Go non può identificare il device e tenterà la connessione ogni 2 secondi loggando:
```
[USB-RFID] nessun device con ruolo RFID trovato (tentativo N)
```

### Integrazione in firmware ESP-IDF esistente

Le modifiche al firmware sono minime — **2 punti nel codice**:

**1. All'avvio**, dopo l'inizializzazione della CDC USB, invia il ruolo:
```c
// ESP32-A
tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (uint8_t*)"HELLO RFID\n", 11);
tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);

// ESP32-B
tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (uint8_t*)"HELLO RELAY\n", 12);
tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);
```

**2. Nel parser dei comandi**, aggiungi il case `GET-ROLE`:
```c
if (strcmp(cmd, "GET-ROLE") == 0) {
    // stesso output del punto 1
    tinyusb_cdcacm_write_queue(TINYUSB_CDC_ACM_0, (uint8_t*)"HELLO RFID\n", 11);
    tinyusb_cdcacm_write_flush(TINYUSB_CDC_ACM_0, 0);
}
```

Se usi `uart_write_bytes` su UART0 ridiretta a USB, sostituisci con la tua primitiva di scrittura seriale. Il protocollo è lo stesso indipendentemente dal framework.

---

*DoorPhoneServer — Setup Wizard v2.0.0 — Go 1.24.4 — Backend IO: RPi GPIO / dual ESP32-S3 (auto-discovery GET-ROLE/HELLO)*

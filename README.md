<div align="center">
  <img src="logo.svg" alt="DoorPhoneServer Logo" width="160"/>
</div>

# DoorPhoneServer

Sistema di citofonia IP basato su Mumble per Raspberry Pi. Gestisce chiamate audio bidirezionali, controllo relè porta, telecamera IP, notifiche push e automazione domestica tramite MQTT/Tasmota.

---

## Ecosistema DoorPhone

DoorPhoneServer è il cuore del sistema, ma fa parte di un ecosistema più ampio:

| Repository | Descrizione |
|---|---|
| **[DoorPhoneServer](https://github.com/MirkoUgoliniDev/DoorPhoneServer)** ← *sei qui* | Server Go per Raspberry Pi: audio, relè, telecamera, notifiche |
| **[DoorPhoneAndroidApp](https://github.com/MirkoUgoliniDev/DoorPhoneAndroidApp)** | App Android per tablet a parete — display citofono con video, apertura porta e gestione chiamate Mumble |
| **[DoorPhoneServerUSBInterface](https://github.com/MirkoUgoliniDev/DoorPhoneServerUSBInterface)** ⚠ *in sviluppo* | Scheda di espansione USB con ESP32-S3: lettore RFID, pulsanti dei piani, relè apertura porta — alternativa ai GPIO del Pi per un'installazione più pulita ed espandibile |

### DoorPhoneAndroidApp

L'app trasforma un tablet Android montato a parete nell'interfaccia del citofono: mostra il feed della telecamera, gestisce le chiamate Mumble in entrata, permette di aprire la porta con un tocco e visualizza notifiche. Non richiede un account cloud — comunica direttamente con il server Mumble sulla LAN.

### DoorPhoneServerUSBInterface — scheda ESP32-S3 ⚠ Work in progress

Invece di collegare relè, pulsanti e lettore RFID direttamente ai GPIO del Raspberry Pi, questa scheda si interpone come **periferica USB HID/CDC**. Il Pi la vede come un dispositivo USB standard; la scheda gestisce tutta la parte hardware sul campo.

**Vantaggi rispetto ai GPIO diretti:**
- Nessun rischio di danneggiare il Pi con tensioni esterne
- Cablaggio più pulito: un solo cavo USB tra Pi e quadro elettrico
- Espandibile: la scheda ESP32-S3 può gestire molti più ingressi/uscite di quanti ne offrano i GPIO del Pi
- Sostituibile senza toccare il Pi: basta re-flashare l'ESP32

**Cosa gestisce:**
- Lettore RFID (accesso con badge/tessera)
- Pulsanti di piano (più appartamenti)
- Relè per apertura porta/cancello
- Eventuali futuri sensori o attuatori

> ⚠ **Repository in sviluppo** — la scheda e il firmware non sono ancora completati. Il protocollo lato server è sviluppato nel branch `GPIO-OVER-USB` di DoorPhoneServer, ma l'integrazione completa è ancora in corso. Non usare in produzione.

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
9. [Variabili d'ambiente (.env)](#9-variabili-dambiente-env)
10. [Struttura file sul sistema](#10-struttura-file-sul-sistema)
11. [Comandi utili](#11-comandi-utili)
12. [Aggiornamento e rebuild](#12-aggiornamento-e-rebuild)
13. [Problemi comuni](#13-problemi-comuni)

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
- **Scheda USB Interface (ESP32-S3)** ⚠ *in sviluppo* — lettore RFID, pulsanti dei piani, relè porta via USB; vedi [DoorPhoneServerUSBInterface](https://github.com/MirkoUgoliniDev/DoorPhoneServerUSBInterface)
- Modulo relè GPIO 5V per controllo elettroserratura (alternativa alla scheda USB)
- Tablet Android con [DoorPhoneAndroidApp](https://github.com/MirkoUgoliniDev/DoorPhoneAndroidApp) — display citofono a parete
- Telecamera IP con stream RTSP (testata: Reolink)
- Dongle WiFi USB se vuoi WiFi ridondante

---

## 2. Parte A — Flash del sistema operativo

### A1. Scarica Raspberry Pi Imager

Vai su **[raspberrypi.com/software](https://www.raspberrypi.com/software/)** e scarica l'Imager per il tuo sistema operativo (Windows, macOS, Linux). Installalo e aprilo.

### A2. Scegli il sistema operativo

Nel menu **"Scegli OS"**, naviga in:
```
Raspberry Pi OS (other)
  → Raspberry Pi OS Lite (64-bit)
```

> Scegli sempre l'**ultima versione disponibile** — l'Imager la evidenzia in automatico come "Recommended". Scegli **Lite** (senza desktop): DoorPhoneServer gira come servizio di sistema e non ha bisogno di interfaccia grafica. Scegli la variante **64-bit**.

### A3. Scegli la microSD

Nel menu **"Scegli Storage"**, seleziona la tua microSD.

> Attenzione: tutto il contenuto della SD verrà cancellato.

### A4. Configura le impostazioni avanzate (FONDAMENTALE)

Prima di scrivere, clicca l'icona **⚙ (Impostazioni avanzate)** in basso a destra. Questa finestra permette di pre-configurare il Pi senza dover collegare monitor e tastiera.

Compila tutti i campi:

| Campo | Valore consigliato |
|---|---|
| **Nome host** | `doorphoneserver` |
| **Abilita SSH** | ✅ Sì — usa autenticazione tramite password |
| **Username** | `pi` |
| **Password** | Una password sicura (annotala!) |
| **Configura WiFi** | Inserisci SSID e password della tua rete (anche se usi Ethernet, configuralo per sicurezza) |
| **Paese WiFi** | IT |
| **Fuso orario** | Europe/Rome |
| **Layout tastiera** | it |

Clicca **Salva**, poi **Scrivi**. Attendi il completamento (3–5 minuti).

### A5. Inserisci la SD e accendi il Pi

Inserisci la microSD nel Pi, collega il cavo Ethernet (se disponibile) e il cavo di alimentazione.

Il Pi impiega **60–90 secondi** per completare il primo avvio e diventare raggiungibile in rete.

---

## 3. Parte B — Primo avvio e connessione SSH

### B1. Trova l'IP del Pi

**Metodo 1 — Nome host (il più semplice):**
Il nome host impostato nell'Imager è accessibile direttamente:
```
doorphoneserver.local
```

**Metodo 2 — Dal router:**
Accedi all'interfaccia del tuo router (solitamente `192.168.1.1`) e cerca nella lista dei dispositivi connessi il nome `doorphoneserver`.

**Metodo 3 — Da terminale sul PC (Linux/macOS):**
```bash
ping doorphoneserver.local
```
L'IP viene mostrato nell'output, es. `PING doorphoneserver.local (192.168.1.151)`.

### B2. Connettiti via SSH

**Su Linux / macOS**, apri il Terminale e digita:
```bash
ssh pi@doorphoneserver.local
```

**Su Windows**, usa Windows Terminal o PowerShell:
```powershell
ssh pi@doorphoneserver.local
```

> Se `.local` non funziona usa l'IP diretto: `ssh pi@192.168.1.XXX`

**Al primo collegamento** vedrai questo messaggio — digita `yes` e premi Invio:
```
The authenticity of host 'doorphoneserver.local' can't be established.
ED25519 key fingerprint is SHA256:...
Are you sure you want to continue connecting (yes/no/[fingerprint])? yes
```

Inserisci la password che hai impostato nell'Imager. Se tutto è corretto, vedrai il prompt:
```
pi@doorphoneserver:~ $
```

Sei dentro. Da qui in poi tutti i comandi vanno digitati in questo terminale SSH.

---

## 4. Parte C — Preparazione del sistema

### C1. Aggiornamento del sistema operativo

Il primo passo è aggiornare tutti i pacchetti all'ultima versione. Questo è importante per avere le dipendenze corrette e le patch di sicurezza:

```bash
sudo apt-get update && sudo apt-get -y full-upgrade
```

- `apt-get update` scarica la lista aggiornata dei pacchetti (~30 secondi)
- `full-upgrade` installa tutti gli aggiornamenti (~2–5 minuti su connessione normale)

Output atteso alla fine:
```
0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.
```
oppure una lista di pacchetti aggiornati.

**Riavvia** per applicare eventuali aggiornamenti al kernel:
```bash
sudo reboot
```

Attendi 30–60 secondi e riconnettiti via SSH:
```bash
ssh pi@doorphoneserver.local
```

### C2. Verifica che git e python3 siano presenti

Su Raspberry Pi OS sono quasi sempre già installati. Verificalo:
```bash
git --version
python3 --version
```

Output atteso:
```
git version 2.39.x
Python 3.11.x
```

### C3. Installa le dipendenze del wizard

Il Setup Wizard usa Flask per l'interfaccia web. Installalo:
```bash
sudo apt-get install -y git python3-flask
```

> `git` potrebbe già essere installato — `apt-get` lo salta senza errori se è già presente.

### C4. Collega la scheda audio USB

**Prima di procedere**, collega la scheda audio USB al Pi.

Verifica che il sistema operativo la riconosca:
```bash
aplay -l
```

Output atteso (esempio):
```
card 0: vc4hdmi0 [vc4-hdmi-0], device 0: MAI PCM i2s-hifi-0 []
card 1: Device [USB Audio Device], device 0: USB Audio [USB Audio]
```

Deve comparire una voce con "USB Audio" o il nome della tua scheda. Se non compare:
- Prova una porta USB diversa
- Verifica che la scheda sia alimentata (alcune richiedono USB 3.0)
- Riavvia il Pi con la scheda già collegata

### C5. Clona il repository

Clona il repository di DoorPhoneServer nella cartella home dell'utente `pi`:

```bash
git clone https://github.com/MirkoUgoliniDev/DoorPhoneServer ~/doorphoneserver-setup
```

Questo crea la cartella `~/doorphoneserver-setup/` con tutto il codice sorgente e il wizard di installazione. Richiede ~10–30 secondi a seconda della connessione.

Spostati nella cartella clonata:
```bash
cd ~/doorphoneserver-setup
```

Verifica che i file siano presenti:
```bash
ls
```

Dovresti vedere: `cmd/`, `setup/`, `go.mod`, `doorphoneserver.xml`, `preferences/`, ecc.

---

## 5. Parte D — Setup Wizard: avvio e navigazione

### D1. Avvia il wizard in modalità Web

```bash
python3 setup/wizard.py --web
```

Il wizard stampa l'indirizzo da aprire nel browser, ad esempio:
```
 DoorPhoneServer Setup Wizard v2.0.0
 → Web UI in ascolto su http://192.168.1.151:8888
 Premi Ctrl+C per uscire
```

**Apri quell'indirizzo nel browser** del tuo PC, tablet o telefono connesso alla stessa rete WiFi/LAN.

> Il terminale SSH deve restare aperto mentre usi il wizard nel browser.
> Se chiudi il terminale SSH, il wizard si ferma.

### D2. Interfaccia del wizard — panoramica

L'interfaccia ha tre aree:

**Barra laterale sinistra** — lista di tutti i passi con stato (cerchio vuoto = in attesa, spunta = completato, X = fallito).

**Area centrale in alto** — toggle DRY-RUN, pulsanti Avvia/Interrompi, barra di progresso.

**Area centrale — card dei passi** — ogni passo mostra il suo form di configurazione e, durante l'esecuzione, il log in tempo reale.

### D3. Modalità DRY-RUN

In alto a sinistra c'è un toggle **DRY-RUN attivo** (acceso di default).

In modalità DRY-RUN il wizard simula tutta l'installazione senza modificare nulla sul sistema. Utile per capire cosa farà.

**Per installare davvero**, spegni il DRY-RUN cliccando il toggle — il badge cambia da "SIMULAZIONE" a "INSTALLAZIONE REALE".

---

## 6. Parte E — Setup Wizard: i passi spiegati

Prima di cliccare **Avvia Installazione**, compila i campi nei passi configurabili. Scorri le card dall'alto in basso.

### Credenziali .env (compilare PRIMA di avviare)

Questo passo scrive il file `/home/doorphoneserver/.env` con le credenziali sensibili.

| Campo | Cosa inserire |
|---|---|
| **Mumble Username** | Il nome che apparirà sul server Mumble. Default: `Doorpi`. Lascialo così. |
| **Mumble Password** | Scegli una password per il server Mumble locale. Annotala — servirà anche nel client. |
| **Camera Username** | Username della telecamera IP (es. `admin`). Lascia vuoto se non hai la telecamera. |
| **Camera Password** | Password della telecamera IP. |
| **Pushover API Token** | Token dell'app Pushover per notifiche push. Lascia vuoto se non lo usi. |
| **Pushover User Key** | Chiave utente Pushover. |
| **OpenRouter API Key** | API key per funzionalità AI. Lascia vuoto se non lo usi. |

### Hostname (opzionale)

Il nome del Pi in rete. Default: `doorphoneserver`. Cambialo solo se hai un motivo specifico.

### Configurazione Audio

Il wizard rileva automaticamente le schede audio presenti al momento dell'avvio.

- **AUDIO OUTPUT** — seleziona la scheda USB (non "bcm2835" che è l'audio integrato del Pi, inutilizzabile per citofonia)
- **AUDIO INPUT** — seleziona la stessa scheda USB

Se i dropdown mostrano "(nessuna scheda rilevata)":
1. Assicurati che la scheda USB sia collegata
2. Clicca **Aggiorna schede**
3. Se ancora non compare, apri un secondo terminale SSH e digita `aplay -l` per diagnosticare

### Log2Ram (opzionale ma consigliato)

Log2Ram mantiene i file di log in RAM e li sincronizza su disco periodicamente, riducendo drasticamente le scritture sulla microSD. Estende significativamente la vita della SD.

- **Dimensione RAM per log** — default 128M, sufficiente per uso normale
- Lascia le altre opzioni ai valori predefiniti

---

### Avvia l'installazione

Quando hai compilato tutti i campi:

1. **Disattiva il DRY-RUN** (toggle in cima)
2. Clicca **▶ Avvia Installazione**
3. Il pulsante Avvia si disabilita e si abilita "Interrompi"

Il wizard esegue i passi in sequenza. Per ciascuno vedi il log in tempo reale nella card corrispondente.

**Tempo totale atteso:** 20–40 minuti. Il passo più lungo è **Clone & Build** (compilazione Go).

---

### Cosa fa ogni passo — dettaglio

#### Passo 1 — Controllo Sistema
Verifica: modello Pi, architettura, versione OS, spazio disco (servono almeno 3 GB liberi), presenza di sudo, connessione internet.
Se questo passo fallisce, controlla spazio su disco (`df -h`) e connessione internet.

#### Passo 2 — Hostname
Imposta il nome host con `hostnamectl` e aggiorna `/etc/hosts`.
Nessuna interazione richiesta.

#### Passo 3 — Utente di Sistema
Crea l'utente di sistema `doorphoneserver` (senza login, usato solo dal servizio) e lo aggiunge ai gruppi `audio`, `gpio`, `dialout`.
Se l'utente esiste già (reinstallazione), aggiorna solo i gruppi.

#### Passo 4 — Credenziali .env
Scrive `/home/doorphoneserver/.env` con le credenziali inserite nel form, con permessi `600` (leggibile solo dall'utente di sistema).

#### Passo 5 — Pacchetti APT
Esegue `apt-get update` e installa ~15 pacchetti: librerie audio (libopenal, libopus, libasound2, alsa-utils), strumenti di build, ffmpeg, mplayer, mumble-server, python3-flask, rsync, lz4.
Questo passo richiede **3–8 minuti** a seconda della velocità di internet.

#### Passo 6 — Go Language
Scarica e installa Go 1.24.4 da golang.org (~130 MB) in `/usr/local/go/`.
Se Go è già installato alla versione corretta, il passo lo rileva e salta il download.
Richiede **2–5 minuti** di download.

#### Passo 7 — Configurazione Audio
Rileva le schede audio presenti e scrive `/etc/asound.conf` con la configurazione ALSA corretta (dmix + dsnoop per condivisione del device).
Aggiorna anche il parametro `outputdevice` in `doorphoneserver.xml` con il nome del controllo mixer rilevato.

#### Passo 8 — Mumble Server
Configura `/etc/mumble-server.ini` con la password scelta e avvia il servizio `mumble-server`.
Verifica che il servizio sia attivo dopo l'avvio (attende fino a 15 secondi).

#### Passo 9 — Config Boot RPi
Esegue `setup/scripts/setup_configs.sh` che modifica `/boot/firmware/config.txt`:
- Disabilita audio BCM integrato (`dtparam=audio=off`)
- Disabilita Bluetooth (`dtoverlay=disable-bt`)
- Imposta GPU memory a 16 MB (headless)
- Disabilita rilevamento display automatico
Scrive anche `/etc/openal/alsoft.conf` e le blacklist modprobe per WiFi USB concorrenti.

#### Passo 10 — Clone & Build
Clona il repository da GitHub in `/home/doorphoneserver/gocode/src/github.com/doorphoneserver/doorphoneserver/` e compila il binario `doorphoneserver`.
**Questo è il passo più lungo: 10–25 minuti su Pi 4.**
Il log mostra i pacchetti Go compilati in tempo reale. È normale che sia silenzioso per lunghi periodi.

#### Passo 11 — Directory & Certificati
Crea la cartella `preferences/`, genera il certificato TLS per Mumble (`openssl`) e copia `doorphoneserver.xml` nella home dell'utente di sistema con il path del certificato aggiornato.

#### Passo 12 — Servizio Systemd
Installa `/etc/systemd/system/doorphoneserver.service`, lo abilita all'avvio automatico e configura il file sudoers per permettere al servizio di controllare systemctl senza password.
Installa anche il crontab per i riavvii notturni programmati.

#### Passo 13 — Log2Ram (opzionale)
Aggiunge il repository APT di azlux.fr, installa log2ram, configura `/etc/log2ram.conf` con le dimensioni scelte e ottimizza `journald.conf` per storage volatile.

#### Passo 14 — Pulizia
Rimuove la cache Go dall'utente pi (`~/go/pkg/`) e pulisce la cache APT. Mostra un messaggio con il comando da eseguire per rimuovere la cartella di setup dopo aver chiuso il wizard.

---

### Se un passo fallisce

I passi non critici (Log2Ram) vengono saltati senza bloccare l'installazione.
I passi critici mostrano `✗` e il nome del passo appare nella lista "Passi falliti" al termine.

Se un passo critico fallisce:
1. Leggi il log del passo per capire il motivo
2. Risolvi il problema (es. connessione internet, spazio su disco)
3. Clicca di nuovo **▶ Avvia Installazione** — il wizard ri-esegue dall'inizio (è idempotente: rieseguire passi già completati è sicuro)

---

## 7. Parte F — Dopo l'installazione

### F1. Configura doorphoneserver.xml

Al termine del wizard, **prima di riavviare**, aggiorna la configurazione principale:

```bash
nano /home/doorphoneserver/doorphoneserver.xml
```

Parametri da personalizzare:

**IP telecamera** — cerca e sostituisci `192.168.1.124` con l'IP reale della tua telecamera:
```xml
<endpoint>rtsp://192.168.1.XXX:554/Preview_01_sub</endpoint>
<endpoint>http://192.168.1.XXX/cgi-bin/api.cgi?cmd=Snap...</endpoint>
```

**Dispositivi Tasmota** — se hai prese/relè Tasmota, abilita e configura:
```xml
<device name="device1" type="tasmota" url="http://192.168.1.XXX" enabled="true" desc="Elettroserratura"/>
```

Per salvare in nano: `Ctrl+O` → Invio → `Ctrl+X`

> Il wizard ha già aggiornato automaticamente: `outputdevice`, `certificate`, `serverandport`.
> Non toccare questi campi.

### F2. Verifica i gruppi utente

Verifica che l'utente di sistema sia nei gruppi corretti:

```bash
id doorphoneserver
```

L'output deve includere `audio`, `dialout`, `gpio`:
```
uid=1001(doorphoneserver) gid=1001(doorphoneserver) groups=1001(doorphoneserver),29(audio),20(dialout),986(gpio)
```

Se `audio` o `dialout` mancano, aggiungili manualmente:
```bash
sudo usermod -aG audio,dialout doorphoneserver
```

### F3. Riavvio finale

```bash
sudo reboot
```

Il Pi si riavvia (~45 secondi). Dopo il riavvio:
- `mumble-server` parte automaticamente
- `doorphoneserver` parte automaticamente (dopo 15 secondi di attesa per la rete)
- Se installato, `log2ram` monta il filesystem in RAM

### F4. Verifica che tutto funzioni

Riconnettiti via SSH e controlla i servizi:

```bash
# Servizio principale
systemctl status doorphoneserver
```

Deve mostrare `Active: active (running)`. Se mostra `failed` o `inactive`:
```bash
journalctl -u doorphoneserver -n 50
```

```bash
# Mumble server
systemctl status mumble-server
# Deve mostrare: active (running), porta 64738
```

```bash
# Schede audio (verifica che la USB sia presente)
aplay -l
# Deve mostrare la scheda USB oltre a eventuali schede HDMI
```

### F5. Connetti il client Mumble

Dal client Mumble (smartphone, PC, tablet), connettiti al server con:

| Campo | Valore |
|---|---|
| Indirizzo | IP del Raspberry Pi (es. `192.168.1.151`) |
| Porta | `64738` |
| Username | Quello scelto (es. `Doorpi`) |
| Password | La password Mumble inserita nel wizard |

Se la connessione riesce, l'installazione è completata con successo.

### F6. Pulizia della cartella di setup

Una volta verificato che tutto funziona, puoi rimuovere la cartella del wizard:

```bash
rm -rf ~/doorphoneserver-setup
```

> Conserva il `README.md` a portata di mano (es. apri questa pagina su GitHub) per riferimenti futuri.

---

## 8. Configurazione doorphoneserver.xml

Il file principale è `/home/doorphoneserver/doorphoneserver.xml`. Sezioni chiave:

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

### Audio

```xml
<outputdevice>Headphone</outputdevice>
<outputvolcontroldevice>Headphone</outputvolcontroldevice>
<outputmutecontroldevice>Headphone</outputmutecontroldevice>
```

Il nome (`Headphone`, `Speaker`, `PCM`…) dipende dalla scheda USB. Per trovarlo:
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
    <endpoint>http://192.168.1.XXX/cgi-bin/api.cgi?cmd=Snap&amp;channel=0&amp;rs=abc</endpoint>
    <dir>/home/doorphoneserver/snapshots</dir>
  </snapshot>
</camera>
```

### HTTP API

```xml
<http listenport="8080" enabled="true">
```

API REST sulla porta 8080. Comandi disponibili: `powertablet_on/off`, `relay`, `takesnapshot`, `listapi`.

### Dispositivi Tasmota

```xml
<device name="device1" type="tasmota" url="http://192.168.1.XXX" enabled="true" desc="Elettroserratura"/>
```

---

## 9. Variabili d'ambiente (.env)

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

## 10. Struttura file sul sistema

```
/home/doorphoneserver/              ← home utente di sistema E repo Git
├── *.go                            ← sorgenti Go (repo clonata qui)
├── cmd/doorphoneserver/main.go     ← entrypoint binario
├── setup/                          ← wizard di installazione
├── go.mod / go.sum                 ← moduli Go
├── bin/
│   └── doorphoneserver             ← binario compilato
├── doorphoneserver.xml             ← configurazione principale
├── .env                            ← credenziali (600, non versionato)
├── mumble.pem                      ← certificato TLS Mumble
├── cert.pem / nopasskey.pem        ← componenti certificato
├── preferences/
│   ├── alarms.json
│   └── ai.json
├── snapshots/                      ← foto dalla telecamera
└── gocode/                         ← GOPATH (cache build, NON i sorgenti)
    └── pkg/                        ← pacchetti Go pre-compilati (cache)

/etc/
├── asound.conf                     ← ALSA (generato dal wizard)
├── openal/alsoft.conf              ← OpenAL
├── mumble-server.ini               ← Mumble server
├── systemd/system/doorphoneserver.service
├── sudoers.d/doorphoneserver-panel ← systemctl senza password
└── log2ram.conf                    ← se Log2Ram installato

/boot/firmware/config.txt           ← BCM audio off, BT off, headless
```

---

## 11. Comandi utili

### Gestione servizio

```bash
sudo systemctl start doorphoneserver
sudo systemctl stop doorphoneserver
sudo systemctl restart doorphoneserver
systemctl status doorphoneserver
journalctl -u doorphoneserver -f        # log live
journalctl -u doorphoneserver -n 100    # ultimi 100 log
```

### Audio

```bash
aplay -l                                # lista schede output
arecord -l                              # lista schede input
amixer -c 1 scontents                   # controlli mixer scheda 1
speaker-test -c 1 -t wav -D plughw:1   # test speaker
```

### Setup wizard (riconfigura)

```bash
cd ~/doorphoneserver-setup
python3 setup/wizard.py --web           # Web UI
python3 setup/wizard.py --audio-setup  # solo audio
python3 setup/wizard.py --dry-run --web # simulazione
```

### Mumble server

```bash
systemctl status mumble-server
sudo systemctl restart mumble-server
cat /etc/mumble-server.ini
ss -tlnp | grep 64738                   # verifica porta in ascolto
```

### Log2Ram

```bash
systemctl status log2ram
sudo log2ram sync                       # forza sync RAM → disco
```

---

## 12. Aggiornamento e rebuild

Per aggiornare il software dopo l'installazione iniziale:

```bash
# Aggiorna i sorgenti (il repo è nella home dell'utente di sistema)
sudo -u doorphoneserver git -C /home/doorphoneserver pull

# Ricompila e installa il binario
sudo bash /home/doorphoneserver/setup/scripts/build.sh

# Riavvia il servizio
sudo systemctl start doorphoneserver
```

Il build script ferma il servizio, compila (~5–15 minuti su Pi 4), installa il nuovo binario e mostra la dimensione del file.

> Il codice sorgente Go vive direttamente in `/home/doorphoneserver/` (la home è la repo).
> La cartella `gocode/` è solo la cache di build GOPATH — non contiene i sorgenti.

---

## 13. Problemi comuni

### `ModuleNotFoundError: No module named 'flask'`
```bash
sudo apt-get install -y python3-flask
```

### Nessuna scheda audio nel wizard
1. Verifica: `aplay -l` — la scheda USB deve comparire
2. Prova una porta USB diversa
3. Riavvia il Pi con la scheda già collegata

### Il servizio non trasmette audio
```bash
id doorphoneserver   # verifica che "audio" e "dialout" siano presenti
sudo usermod -aG audio,dialout doorphoneserver
sudo systemctl restart doorphoneserver
```

### Il servizio si avvia ma crasha subito
```bash
journalctl -u doorphoneserver -n 50
```
Cause frequenti: XML malformato, path certificato errato, Mumble server non raggiungibile.

### Mumble server non risponde
```bash
sudo systemctl restart mumble-server
ss -tlnp | grep 64738   # deve mostrare il processo in ascolto
```

### Il build fallisce
```bash
/usr/local/go/bin/go version   # deve essere >= 1.24.x
# Se Go non è presente o è vecchio, ri-esegui il wizard
```

### Reinstallazione mantenendo le credenziali
```bash
cp /home/doorphoneserver/.env ~/backup.env
cp /home/doorphoneserver/doorphoneserver.xml ~/backup.xml

cd ~/doorphoneserver-setup && git pull
python3 setup/wizard.py --web

# Dopo l'installazione, ripristina se necessario:
sudo cp ~/backup.env /home/doorphoneserver/.env
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/.env
sudo chmod 600 /home/doorphoneserver/.env
```

---

*DoorPhoneServer — Setup Wizard v2.0.0 — Go 1.24.4*

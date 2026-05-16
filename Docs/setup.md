# DoorPhoneServer — Setup Completo Raspberry Pi

Documentazione tecnica completa dell'installazione, configurazione e ottimizzazione di DoorPhoneServer su Raspberry Pi. Copre tutto il processo dalla scheda vergine al sistema funzionante.

---

## Indice

1. [Setup Wizard — Installazione Automatica](#1-setup-wizard--installazione-automatica)
2. [Struttura del Progetto](#2-struttura-del-progetto)
3. [Configurazione Boot `/boot/config.txt`](#3-configurazione-boot-bootconfigtxt)
4. [Configurazione Audio ALSA](#4-configurazione-audio-alsa)
5. [Configurazione OpenAL](#5-configurazione-openal)
6. [Mumble Server (Murmur)](#6-mumble-server-murmur)
7. [Variabili d'Ambiente — File `.env`](#7-variabili-dambiente--file-env)
8. [Servizio Systemd](#8-servizio-systemd)
9. [Sudoers per Web Panel](#9-sudoers-per-web-panel)
10. [Log2Ram — Protezione microSD](#10-log2ram--protezione-microsd)
11. [Go Language — Installazione](#11-go-language--installazione)
12. [Build del Binario](#12-build-del-binario)
13. [Blacklist Driver WiFi](#13-blacklist-driver-wifi)
14. [Hostname del Dispositivo](#14-hostname-del-dispositivo)
15. [Verifica Post-Installazione](#15-verifica-post-installazione)
16. [Troubleshooting](#16-troubleshooting)

---

## 1. Setup Wizard — Installazione Automatica

Il modo più rapido per installare DoorPhoneServer da zero è usare il wizard incluso nel repository.

### Prerequisiti minimi sul Pi vergine

```bash
sudo apt-get update && sudo apt-get -y full-upgrade
sudo reboot
# Dopo il riavvio:
sudo apt-get install -y git python3
```

### Clonare il repository

```bash
git clone https://github.com/MirkoUgoliniDev/DoorPhoneServer
cd DoorPhoneServer
```

### Eseguire il wizard

```bash
# Simulazione (DRY-RUN) — nessuna modifica, solo mostra cosa farebbe
python3 setup/setup_wizard.py --dry-run --tui

# Installazione reale con interfaccia testuale (headless/SSH)
python3 setup/setup_wizard.py --tui

# Installazione con GUI grafica (se display o SSH -X disponibile)
python3 setup/setup_wizard.py --gui
```

### Cosa fa il wizard (13 passi in ordine)

| # | Passo | Descrizione |
|---|-------|-------------|
| 1 | Controllo Sistema | Verifica modello Pi, OS, spazio disco (min 3 GB), sudo, internet |
| 2 | Hostname | Imposta il nome host del dispositivo (default: `doorphoneserver`) |
| 3 | Utente di Sistema | Crea utente `doorphoneserver`, lo aggiunge ai gruppi audio/gpio/video/dialout/sudo |
| 4 | Pacchetti APT | Installa tutte le dipendenze di sistema |
| 5 | Go Language | Scarica e installa Go 1.24.4 da golang.org |
| 6 | Configurazione Audio | Rileva schede audio, genera `/etc/asound.conf`, copia `/etc/openal/alsoft.conf` |
| 7 | Mumble Server | Copia configurazione Murmur, abilita e avvia il servizio |
| 8 | Config Boot RPi | Copia `/boot/config.txt`, applica blacklist moduli WiFi |
| 9 | Clone & Build | Clona il repo da GitHub, compila il binario `doorphoneserver` |
| 10 | Directory & Certificati | Crea `/var/lib/doorphoneserver/data`, genera certificato TLS |
| 11 | Servizio Systemd | Installa `doorphoneserver.service`, configura sudoers |
| 12 | Log2Ram *(opzionale)* | Installa Log2Ram per ridurre le scritture sulla microSD |
| 13 | VSCode Server *(opzionale)* | Installa code-server per accedere a VSCode dal browser |

### Funzionalità di sicurezza del wizard

- **`--dry-run`**: simula ogni operazione senza toccare il sistema, log completo in `/tmp/doorphoneserver-setup.log`
- **Lock file**: impedisce l'esecuzione parallela di due istanze del wizard
- **Retry automatico**: 3 tentativi per ogni operazione di rete (wget, git, apt)
- **Check spazio disco**: verifica disponibilità prima di APT, download Go e build
- **Signal handler**: Ctrl+C gestito in modo pulito sia in GUI che in TUI
- **Threading sicuro**: dialogo "continua dopo errore" usa `threading.Event` (nessun sleep/race condition)
- **Audio lazy**: le schede audio vengono rilevate dopo l'installazione di `alsa-utils`
- **Hostname validato**: solo caratteri alfanumerici e trattini, max 63 caratteri

### Percorso log

```
/tmp/doorphoneserver-setup.log
```

---

## 2. Struttura del Progetto

```
/home/doorphoneserver/
├── bin/
│   └── doorphoneserver                  ← binario compilato
└── gocode/
    └── src/github.com/MirkoUgoliniDev/doorphoneserver/
        ├── doorphoneserver.xml          ← configurazione principale (in .gitignore)
        ├── .env                     ← credenziali sensibili (in .gitignore)
        ├── go.mod / go.sum          ← dipendenze Go
        ├── cmd/doorphoneserver/main.go  ← entry point
        ├── webpanel.go              ← web panel e API REST
        ├── xmlparser.go             ← parsing XML + caricamento .env
        ├── stream.go                ← audio streaming
        ├── Configurazioni/          ← file di config da copiare sul sistema
        │   ├── boot/config.txt
        │   └── etc/
        │       ├── asound.conf
        │       ├── openal/alsoft.conf
        │       ├── mumble-server.ini
        │       └── modprobe.d/
        ├── setup/
        │   ├── setup_wizard.py      ← wizard installazione
        │   └── INSTALL.md           ← guida installazione
        ├── scripts/
        │   ├── tkbuild.sh           ← script di compilazione
        │   └── gencert.sh           ← generazione certificato TLS
        └── Docs/                    ← documentazione tecnica

/etc/
├── systemd/system/doorphoneserver.service
├── sudoers.d/doorphoneserver-panel
├── asound.conf
├── openal/alsoft.conf
├── mumble-server.ini
├── modprobe.d/blacklist-8192cu.conf
├── modprobe.d/blacklist-rtl8xxxu.conf
└── log2ram.conf                     ← se installato

/var/lib/doorphoneserver/data/           ← dati runtime
/var/log/                            ← su tmpfs con Log2Ram
/var/hdd.log/                        ← backup log su SD con Log2Ram
```

---

## 3. Configurazione Boot `/boot/config.txt`

### Percorso del file

- **Bullseye e precedenti**: `/boot/config.txt`
- **Bookworm e successivi**: `/boot/firmware/config.txt`

Il wizard rileva automaticamente il percorso corretto.

### Parametri configurati e motivazione

```ini
# ── Audio ──────────────────────────────────────────────────────────────────
dtparam=audio=off                  # Disabilita BCM2835 (audio on-board)
dtoverlay=vc4-kms-v3d,noaudio     # Driver grafico vc4 senza audio HDMI
# → Entrambi necessari: audio=off disabilita il modulo, noaudio evita
#   che il driver vc4 registri un secondo dispositivo audio HDMI.
#   Senza queste impostazioni la scheda USB potrebbe non essere card 0
#   e causare problemi con asound.conf.

# ── Bluetooth ──────────────────────────────────────────────────────────────
dtoverlay=disable-bt               # Disabilita Bluetooth
# → Riduce interferenze RF con la scheda audio USB e libera la UART.

# ── Camera e display ───────────────────────────────────────────────────────
camera_auto_detect=0               # NON cerca camera CSI al boot
display_auto_detect=0              # NON cerca display DSI al boot
# → Il sistema usa una camera IP esterna (nessuna camera CSI connessa).
#   Con =1 il kernel cerca la camera ad ogni avvio causando un delay
#   inutile di 3-5 secondi e messaggi di errore nel kernel log.

# ── Performance e stabilità ────────────────────────────────────────────────
avoid_warnings=1                   # Sopprime messaggi di throttling
# → Evita che i warning di under-voltage/temperatura vengano stampati
#   sulla console e nei log, che potrebbero disturbare timing audio.

[pi4]
arm_boost=1                        # Abilita max clock su Pi4 (1.8 GHz)

[all]
gpu_mem=16                         # GPU usa solo 16 MB RAM (sistema headless)
enable_uart=0                      # UART disabilitata

# ── Interfacce disabilitate ────────────────────────────────────────────────
dtparam=i2c_arm=off                # I2C disabilitato (non usato)
dtparam=i2s=off                    # I2S disabilitato (audio digitale non usato)
dtparam=spi=off                    # SPI disabilitato (non usato)

# WiFi on-board mantenuto attivo:
#dtoverlay=disable-wifi            # ← commentato, WiFi onboard in uso
```

### Applicare le modifiche

```bash
sudo cp Configurazioni/boot/config.txt /boot/config.txt
sudo reboot
```

---

## 4. Configurazione Audio ALSA

### Perché dmix/dsnoop

Il sistema usa OpenAL (via `go-openal`) per la riproduzione e ALSA per la registrazione. Senza `dmix`, un solo processo alla volta può accedere alla scheda audio hardware. Con `dmix` più processi condividono l'output; con `dsnoop` condividono l'input.

### File `/etc/asound.conf`

Il wizard genera questo file dinamicamente in base alla scheda selezionata:

```
/etc/asound.conf
```

**Parametri chiave:**

| Parametro | Valore | Note |
|-----------|--------|------|
| `card` | `1` (default) | Indice scheda USB — verificare con `aplay -l` |
| `device` | `0` | Primo device della scheda |
| `rate` | `48000` | Frequenza di campionamento richiesta da Mumble/Opus |
| `format` | `S16_LE` | 16 bit signed little-endian |
| `channels` (output) | `2` | Stereo out |
| `channels` (input) | `1` | Mono in (microfono) |
| `ipc_key` | `1024` / `1025` | Chiavi IPC per dmix e dsnoop |

**Nota critica sul numero di card:**
Con `dtparam=audio=off` l'audio on-board è disabilitato, quindi la scheda USB diventa tipicamente `card 1` (dopo il dispositivo HDMI che rimane `card 0`). Se sul Pi vengono aggiunte altre schede, il numero potrebbe cambiare — verificare sempre con `aplay -l` prima del setup.

### Verifica audio

```bash
aplay -l                                              # lista schede output
arecord -l                                            # lista schede input
aplay -D dmixed /usr/share/sounds/alsa/Front_Center.wav  # test dmix
arecord -D dsnooped -d 3 -f S16_LE /tmp/test.wav    # test dsnoop 3 secondi
aplay /tmp/test.wav                                   # riascolto test
```

### Copiare manualmente (senza wizard)

```bash
sudo cp Configurazioni/etc/asound.conf /etc/asound.conf
```

---

## 5. Configurazione OpenAL

OpenAL Soft è il backend audio 3D usato da `go-openal`. Deve essere configurato per usare ALSA (non PulseAudio) e la scheda corretta.

```bash
sudo mkdir -p /etc/openal
sudo cp Configurazioni/etc/openal/alsoft.conf /etc/openal/alsoft.conf
```

Il file configura: driver ALSA, frequenza 48000 Hz, HRTF disabilitato (non necessario per PTT radio), buffer ottimizzati per bassa latenza.

---

## 6. Mumble Server (Murmur)

### Installazione

Il pacchetto `mumble-server` viene installato dal wizard tramite APT.

### Configurazione

```bash
sudo cp Configurazioni/etc/mumble-server.ini /etc/mumble-server.ini
sudo systemctl enable mumble-server
sudo systemctl start mumble-server
```

### Parametri principali in `mumble-server.ini`

| Parametro | Valore | Note |
|-----------|--------|------|
| `port` | `64738` | Porta TCP/UDP standard Mumble |
| `database` | `/var/lib/mumble-server/mumble-server.sqlite` | Database utenti e canali |
| `serverpassword` | *(configurare)* | Password accesso al server |
| `bandwidth` | `72000` | Bandwidth max per client (bps) |
| `users` | `100` | Max utenti connessi |

### Gestione servizio

```bash
sudo systemctl status mumble-server
sudo systemctl restart mumble-server
sudo journalctl -u mumble-server -f
```

---

## 7. Variabili d'Ambiente — File `.env`

### Perché esiste il file `.env`

`doorphoneserver.xml` contiene la configurazione del sistema ma viene escluso da git (`.gitignore`) perché può contenere credenziali. Il file `.env` separa ulteriormente i segreti dalla configurazione, permettendo di versionare `doorphoneserver.xml` con valori placeholder e sovrascriverli a runtime tramite variabili d'ambiente.

### Come funziona

Il file viene caricato da `xmlparser.go` alla funzione `loadDotEnv()` che:
1. Legge il file `.env` dalla stessa directory di `doorphoneserver.xml`
2. Imposta ogni variabile nell'ambiente del processo
3. Non sovrascrive variabili già presenti nell'ambiente

Poi `applyEnvOverrides()` applica i valori ai campi della struttura di configurazione in memoria.

### Posizione del file

```
/home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/.env
```

### Variabili supportate

| Variabile | Descrizione | Esempio |
|-----------|-------------|---------|
| `OPENROUTER_API_KEY` | API key per OpenRouter (funzioni AI) | `sk-or-v1-...` |
| `MUMBLE_USERNAME` | Username per la connessione al server Mumble | `doorpi` |
| `MUMBLE_PASSWORD` | Password per la connessione al server Mumble | `password123` |
| `CAMERA_USERNAME` | Username per la camera IP | `admin` |
| `CAMERA_PASSWORD` | Password per la camera IP | `campassword` |
| `PUSHOVER_API_TOKEN` | Token API app Pushover (notifiche push) | `abcdef123...` |
| `PUSHOVER_USER_KEY` | Chiave utente Pushover | `uvwxyz789...` |

### Formato del file

```bash
OPENROUTER_API_KEY="sk-or-v1-xxxxxxxxxxxxx"

# Mumble
MUMBLE_USERNAME="nomeutente"
MUMBLE_PASSWORD="password"

# Camera IP
CAMERA_USERNAME="admin"
CAMERA_PASSWORD="password"

# Pushover notifiche
PUSHOVER_API_TOKEN="tokenapp"
PUSHOVER_USER_KEY="chiaveutente"
```

### Note di sicurezza

- Il file `.env` è incluso in `.gitignore` — **non viene mai committato**
- Permessi consigliati: `chmod 600 .env && chown doorphoneserver:doorphoneserver .env`
- Non condividere il file `.env` — contiene credenziali reali

### Creare il file `.env` manualmente

```bash
cat > /home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/.env << 'EOF'
OPENROUTER_API_KEY=""
MUMBLE_USERNAME=""
MUMBLE_PASSWORD=""
CAMERA_USERNAME=""
CAMERA_PASSWORD=""
PUSHOVER_API_TOKEN=""
PUSHOVER_USER_KEY=""
EOF
chmod 600 /home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/.env
chown doorphoneserver:doorphoneserver /home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/.env
```

---

## 8. Servizio Systemd

### File di servizio

```
/etc/systemd/system/doorphoneserver.service
```

### Contenuto

```ini
[Unit]
Description=DoorPhoneServer Radio Service
Requires=network.target sound.target
After=network-online.target sound.target mumble-server.service

[Service]
User=doorphoneserver
Group=doorphoneserver
Type=simple
WorkingDirectory=/home/doorphoneserver
ExecStartPre=/bin/sleep 15
ExecStart=/home/doorphoneserver/bin/doorphoneserver \
    -config=/home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/doorphoneserver.xml
Restart=always
RestartSec=10
PrivateTmp=true
ProtectSystem=false

[Install]
WantedBy=multi-user.target
```

**Note:**
- `ExecStartPre=/bin/sleep 15` — attende 15 secondi al boot per garantire che rete e audio siano pronti
- `Restart=always` — in caso di crash, il servizio riparte automaticamente dopo 10 secondi
- `After=mumble-server.service` — garantisce che Murmur sia avviato prima di doorphoneserver
- Gira come utente `doorphoneserver` (non root)

### Comandi di gestione

```bash
sudo systemctl start doorphoneserver
sudo systemctl stop doorphoneserver
sudo systemctl restart doorphoneserver
sudo systemctl status doorphoneserver
sudo systemctl enable doorphoneserver      # avvio automatico al boot
sudo systemctl disable doorphoneserver     # rimuovi avvio automatico

# Log in tempo reale
sudo journalctl -u doorphoneserver -f

# Ultimi 50 log
sudo journalctl -u doorphoneserver -n 50
```

---

## 9. Sudoers per Web Panel

Il web panel deve poter avviare/fermare/riavviare i servizi systemd tramite HTTP, ma il processo gira come utente `doorphoneserver` (non root). Serve una regola sudoers NOPASSWD.

### Installazione

```bash
echo "doorphoneserver ALL=(ALL) NOPASSWD: /bin/systemctl" \
  | sudo tee /etc/sudoers.d/doorphoneserver-panel
sudo chmod 440 /etc/sudoers.d/doorphoneserver-panel
```

### Verifica

```bash
sudo -u doorphoneserver sudo -n systemctl status mumble-server
# Non deve chiedere password
```

### Servizi controllati dal web panel

| Servizio | Nome systemd | Endpoint API |
|----------|-------------|--------------|
| DoorPhoneServer | `doorphoneserver` | `/panel/api/service` |
| Mumble Server | `mumble-server` | `/panel/api/mumble` |

> **Nota:** `mjpeg_streamer` è stato rimosso dalla lista — il sistema usa una camera IP esterna, non una camera CSI con streaming MJPEG locale.

---

## 10. Log2Ram — Protezione microSD

### Motivazione

I log di sistema vengono scritti continuamente su `/var/log`. Su una microSD questo causa:
- ~4 GB/giorno di scritture → usura rapida
- Vita media microSD: 1-3 anni in scrittura continua

Con Log2Ram i log vengono tenuti in RAM e sincronizzati sulla SD solo periodicamente.

### Installazione (via wizard o manuale)

```bash
# Aggiunta repository azlux
wget -qO /usr/share/keyrings/azlux-archive-keyring.gpg https://azlux.fr/repo.gpg
echo "deb [signed-by=/usr/share/keyrings/azlux-archive-keyring.gpg] \
  http://packages.azlux.fr/debian/ bookworm main" \
  | sudo tee /etc/apt/sources.list.d/azlux.list
sudo apt-get update && sudo apt-get install -y log2ram
```

### Configurazione `/etc/log2ram.conf`

| Parametro | Valore | Descrizione |
|-----------|--------|-------------|
| `SIZE` | `128M` | Dimensione tmpfs in RAM per i log |
| `PATH_DISK` | `/var/log` | Cartella montata in RAM |
| `LOG_DISK_SIZE` | `200M` | Spazio backup su SD |
| `USE_RSYNC` | `true` | Sync efficiente |
| `CLEAN` | `false` | Mantieni log al reboot |

### Journald volatile (`/etc/systemd/journald.conf`)

```ini
[Journal]
Storage=volatile
RuntimeMaxUse=50M
RuntimeMaxFileSize=10M
MaxRetentionSec=2week
```

Con `Storage=volatile` il journal scrive solo in RAM — nessuna scrittura su SD.

### Risultato

| Metrica | Senza Log2Ram | Con Log2Ram |
|---------|--------------|-------------|
| Scritture SD/giorno | ~4 GB | ~50 MB |
| % I/O tempo | 76% | <5% |
| Vita microSD stimata | 1-3 anni | 15-30 anni |

### Verifica

```bash
log2ram status          # stato montaggio
df -h /var/log          # deve mostrare tmpfs
cat /etc/log2ram.conf   # configurazione attiva
```

---

## 11. Go Language — Installazione

### Perché non usare `apt install golang`

La versione Go disponibile via APT su Raspbian/Debian è solitamente 1-2 major version indietro rispetto all'ultima stabile. DoorPhoneServer richiede Go 1.24+.

### Installazione da golang.org

```bash
# Arch mapping
# aarch64 (Pi 3/4/5 a 64 bit) → arm64
# armv7l  (Pi 3/4 a 32 bit)   → armv6l
# x86_64  (PC/VM)             → amd64

GOARCH="arm64"   # sostituire se necessario
GO_VERSION="1.24.4"

wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" -O /tmp/go.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz
```

### Aggiungere Go al PATH

```bash
# In /etc/environment aggiungere :/usr/local/go/bin alla fine di PATH
# Oppure per la sessione corrente:
export PATH=$PATH:/usr/local/go/bin

# Verifica
go version
```

### Variabili build

| Variabile | Valore | Descrizione |
|-----------|--------|-------------|
| `GOPATH` | `/home/doorphoneserver/gocode` | Directory workspace Go |
| `GOBIN` | `/home/doorphoneserver/bin` | Directory binari compilati |
| `TMPDIR` | `/var/tmp` | Evita limite 64 MB di `/tmp` durante la build |

---

## 12. Build del Binario

### Script `tkbuild.sh`

```bash
cd /home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver
bash tkbuild.sh
```

### Comando manuale

```bash
export GOPATH=/home/doorphoneserver/gocode
export PATH=$PATH:/usr/local/go/bin
export TMPDIR=/var/tmp

go build -v -trimpath \
  -ldflags="-s -w" \
  -o /home/doorphoneserver/bin/doorphoneserver \
  ./cmd/doorphoneserver
```

**Flag spiegati:**
- `-trimpath` — rimuove path assoluti dal binario (sicurezza)
- `-ldflags="-s -w"` — rimuove debug info e symbol table (binario ~40% più piccolo)

### Dipendenze di sistema richieste per la build

```bash
sudo apt-get install -y \
  libopenal-dev \    # OpenAL headers
  libopus-dev \      # Opus codec headers
  libasound2-dev \   # ALSA headers
  build-essential    # gcc, make
```

---

## 13. Blacklist Driver WiFi

Alcuni driver WiFi USB Realtek interferiscono con altri dispositivi USB (incluse le schede audio USB). I moduli vengono blacklistati preventivamente.

```bash
sudo cp Configurazioni/etc/modprobe.d/blacklist-8192cu.conf /etc/modprobe.d/
sudo cp Configurazioni/etc/modprobe.d/blacklist-rtl8xxxu.conf /etc/modprobe.d/
sudo update-initramfs -u
sudo reboot
```

**Moduli blacklistati:**
- `8192cu` — driver vecchio per dongle Realtek RTL8192CU
- `rtl8xxxu` — driver generico Realtek USB WiFi (rtl8188, rtl8192, rtl8723 series)

---

## 14. Hostname del Dispositivo

Il wizard chiede all'utente il nome host durante l'installazione. Default: `doorphoneserver`.

### Impostare manualmente

```bash
sudo hostnamectl set-hostname doorphoneserver

# Aggiornare anche /etc/hosts
sudo nano /etc/hosts
# Aggiungere/modificare la riga:
# 127.0.1.1    doorphoneserver
```

### Verifica

```bash
hostname
hostnamectl status
```

### Accesso dalla LAN

Dopo il riavvio il dispositivo è raggiungibile come:
```
ssh pi@doorphoneserver.local
http://doorphoneserver.local:8080    ← web panel
```

---

## 15. Verifica Post-Installazione

### Checklist dopo il primo riavvio

```bash
# 1. Hostname
hostname
# → doorphoneserver

# 2. Servizi attivi
sudo systemctl status doorphoneserver
sudo systemctl status mumble-server
# → entrambi active (running)

# 3. Audio
aplay -l
# → deve mostrare la scheda USB (non bcm2835)

# 4. Log2Ram montato
df -h /var/log
# → Filesystem: tmpfs

# 5. Go installato
/usr/local/go/bin/go version
# → go version go1.24.4 linux/arm64

# 6. Binario presente
ls -lh /home/doorphoneserver/bin/doorphoneserver
# → file ELF ARM64

# 7. Web panel
curl -s http://localhost:8080/panel/
# → risponde con HTML

# 8. Sudoers funzionante
sudo -u doorphoneserver sudo -n systemctl status doorphoneserver
# → non chiede password
```

---

## 16. Troubleshooting

### Il servizio doorphoneserver non parte

```bash
sudo journalctl -u doorphoneserver -n 100
# Cercare: "connection refused", "no such file", "permission denied"
```

Cause comuni:
- Server Mumble non raggiungibile → verificare IP/porta in `doorphoneserver.xml`
- `.env` mancante o credenziali vuote
- Scheda audio non trovata → verificare `aplay -l` e `asound.conf`

### La scheda audio non viene rilevata

```bash
aplay -l          # lista schede output disponibili
lsusb             # verifica che la scheda USB sia riconosciuta dal kernel
dmesg | grep -i audio   # cerca messaggi kernel relativi all'audio
```

Se la scheda USB appare come `card 0` invece di `card 1`:
```bash
# Verificare che dtparam=audio=off sia attivo
vcgencmd get_config audio
# Modificare il numero card in /etc/asound.conf
```

### Log2Ram: errore repo azlux su Trixie

```bash
sudo sed -i 's/trixie/bookworm/' /etc/apt/sources.list.d/azlux.list
sudo apt-get update && sudo apt-get install -y log2ram
```

### Build Go fallisce (out of memory)

```bash
# Aggiungere swap temporaneo
sudo fallocate -l 1G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
# Ricompilare
bash tkbuild.sh
# Dopo la build rimuovere lo swap
sudo swapoff /swapfile && sudo rm /swapfile
```

### Web panel: bottoni Start/Stop non funzionano

```bash
# Verificare sudoers
sudo cat /etc/sudoers.d/doorphoneserver-panel
# Deve contenere: doorphoneserver ALL=(ALL) NOPASSWD: /bin/systemctl
sudo -u doorphoneserver sudo -n systemctl status doorphoneserver
```

### Doppia istanza wizard in esecuzione

```bash
sudo rm /var/run/doorphoneserver-setup.lock
```

---

*Documentazione generata per DoorPhoneServer v3.0.0 — Setup Wizard v1.1.0*

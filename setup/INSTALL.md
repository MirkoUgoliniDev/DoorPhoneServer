# DoorPhoneServer — Installazione su Raspberry Pi Vergine

Questa guida ti porta da un Raspberry Pi appena flashato a DoorPhoneServer funzionante.

---

## Requisiti hardware

| Componente | Minimo |
|------------|--------|
| Raspberry Pi | 3B+ / 4 / 5 |
| microSD | 16 GB classe 10 |
| Scheda audio USB | C-Media o compatibile |
| Connessione internet | Ethernet o WiFi |

---

## Passo 0 — Flash del sistema operativo

Usa **Raspberry Pi Imager** ([raspberrypi.com/software](https://www.raspberrypi.com/software/)) e scegli:

```
Raspberry Pi OS Lite (64-bit)   ← CONSIGLIATO (no desktop, più leggero)
oppure
Raspberry Pi OS (64-bit)        ← se vuoi il desktop
```

> **Importante:** prima di scrivere la SD, clicca l'icona ⚙ (impostazioni avanzate) e configura:
> - Nome host (es. `doorphoneserver`)
> - Utente e password (es. `pi` / la tua password)
> - Abilita SSH
> - WiFi (SSID e password) se non usi Ethernet

Inserisci la SD nel Pi e accendilo.

---

## Passo 1 — Connettiti al Pi

Da terminale sul tuo PC:

```bash
ssh pi@doorphoneserver.local
# oppure usa l'IP del Pi se .local non funziona
ssh pi@192.168.x.x
```

---

## Passo 2 — Aggiornamento iniziale del sistema

```bash
sudo apt-get update && sudo apt-get -y full-upgrade
sudo reboot
```

Riconnettiti dopo il riavvio.

---

## Passo 3 — Installa git, pip e Flask

Questi pacchetti sono necessari per clonare il repository e avviare il wizard:

```bash
sudo apt install git -y
sudo apt install python3-pip -y
sudo apt install python3-flask -y
```

---

## Passo 4 — Clona il repository

```bash
git clone https://github.com/MirkoUgoliniDev/DoorPhoneServer ~/doorphoneserver
cd ~/doorphoneserver
```

### Perché due copie del repository?

Durante l'installazione il codice finisce su disco in **due posizioni diverse**. Non è un errore: è dovuto al fatto che il programma gira come un **utente di servizio dedicato** (`doorphoneserver`), diverso da `pi`.

| | Copia #1 (questa) | Copia #2 |
|---|---|---|
| **Percorso** | `~/doorphoneserver` (= `/home/pi/doorphoneserver`) | `/home/doorphoneserver/` |
| **Proprietario** | `pi` | `doorphoneserver` (utente di servizio) |
| **Scopo** | far **partire il wizard** | dove il binario viene **compilato ed eseguito** in produzione |
| **Durata** | **temporanea** | **permanente** |

Il motivo è un problema dell'uovo e la gallina:

1. Quando fai SSH come `pi`, l'utente `doorphoneserver` e la sua home **non esistono ancora**: li crea il wizard più avanti. Quindi l'unico posto dove puoi mettere il codice per avviare il wizard è la home di `pi` → **questa copia (#1)**.
2. Durante l'installazione, il passo **Crea utente di servizio** crea l'utente `doorphoneserver` e la sua home.
3. Solo a quel punto il passo **Clone & Build** può popolare `/home/doorphoneserver/` con il codice (copia #2), compilare il binario ed eseguirlo come utente di servizio. La home **è** il repository git, identica a una macchina di sviluppo.

> **La copia #1 è usa-e-getta.** A fine installazione, quando fermi il wizard con `Ctrl+C`, viene **rimossa automaticamente** dall'ultimo passo (Pulizia). Non serve cancellarla a mano. La copia #2 in `/home/doorphoneserver/` è quella definitiva e resta.
>
> *(Il wizard gira da dentro questa cartella, quindi non può cancellarla mentre è in esecuzione: la rimozione viene pianificata e parte appena chiudi il wizard.)*

---

## Passo 5 — Avvia il wizard

Il wizard ha tre modalità di interfaccia:

### ✅ Web UI (consigliata) — funziona da qualsiasi browser sulla LAN

```bash
python3 setup/wizard.py --web
```

Poi apri nel browser del tuo PC/tablet/telefono:

```
http://<IP-del-Pi>:8888
```

L'IP viene stampato nel terminale all'avvio.

### TUI testuale — per SSH senza browser

```bash
python3 setup/wizard.py --tui
```

### GUI grafica — solo se hai display o X11 forwarding

```bash
python3 setup/wizard.py
```

---

## Passo 6 — Configura le opzioni nel wizard

| Opzione | Consiglio |
|---------|-----------|
| Hostname | Nome del Pi in rete (default: `doorphoneserver`) |
| Scheda audio OUTPUT | Seleziona la scheda USB (non "bcm2835") |
| Scheda audio INPUT  | Stessa scheda USB dell'output |
| Installare Log2Ram? | **Sì** — protegge la microSD dall'usura |
| Installare code-server (VSCode)? | Opzionale, occupa ~500 MB |

Clicca **Avvia Installazione** e monitora il progresso in tempo reale.

---

## Passo 7 — Regole sudoers (build senza password)

Il build script e i bottoni VSCode usano `sudo` per fermare il servizio e ripulire i log.
Installa le regole che permettono all'utente `pi` di farlo senza digitare la password ogni volta:

```bash
sudo bash setup/scripts/setup_sudoers.sh
```

Permessi concessi (solo questi, nient'altro):

| Comando | Motivo |
|---|---|
| `rm /var/log/doorphoneserver.log` | il log è di root quando il servizio gira come root |
| `systemctl stop/start/restart doorphoneserver` | gestione servizio |
| `killall -s 15 doorphoneserver` | fallback nel build script |

---

## Passo 8 — Riavvio finale

Al termine del wizard:

```bash
sudo reboot
```

Dopo il riavvio DoorPhoneServer parte automaticamente come servizio systemd.

---

## Verifica post-installazione

```bash
# Stato del servizio
systemctl status doorphoneserver

# Log in tempo reale
journalctl -u doorphoneserver -f

# Stato Mumble server
systemctl status mumble-server
```

---

## Sviluppo sul Raspberry Pi

Dopo il setup, `/home/doorphoneserver` è a tutti gli effetti un clone del repository
pronto per lo sviluppo. Entra come utente di servizio per lavorare direttamente sul codice:

```bash
sudo -i -u doorphoneserver
cd ~              # /home/doorphoneserver — il repo è qui
```

`GOBIN` e il PATH di Go sono già configurati in `~/.bashrc`. Per compilare e riavviare:

```bash
go build -trimpath -ldflags='-s -w' -o bin/doorphoneserver ./cmd/doorphoneserver
sudo systemctl restart doorphoneserver
```

> **Attenzione:** `webpanel_static/` è **embedded nel binario** a compile-time
> (via `//go:embed`). Ogni modifica ai file JS/HTML/CSS richiede un rebuild
> per essere visibile nel pannello web.

Per ri-eseguire il wizard (es. aggiornare la configurazione audio):

```bash
cd /home/doorphoneserver
python3 setup/wizard.py --web
```

---

## Comandi utili

```bash
# Avvia / ferma / riavvia doorphoneserver
sudo systemctl start doorphoneserver
sudo systemctl stop doorphoneserver
sudo systemctl restart doorphoneserver

# Riconfigura solo la scheda audio (dopo aver collegato la scheda USB)
python3 setup/wizard.py --audio-setup

# Dry-run — simula l'installazione senza modificare nulla
python3 setup/wizard.py --dry-run --tui
```

---

## Struttura file installati

```
/home/doorphoneserver/               ← è anche il repository git (clone completo)
├── bin/doorphoneserver              ← binario compilato
├── doorphoneserver.xml              ← configurazione principale
├── snapshots/                       ← snapshot telecamera (owner doorphoneserver, 775)
├── go/                              ← cache moduli Go (GOPATH)
├── cmd/doorphoneserver/             ← entry point sorgente
├── webpanel_static/                 ← frontend web (embedded nel binario a compile-time)
└── setup/                           ← wizard di installazione

/var/lib/doorphoneserver/data/       ← dati runtime (alarms.json, audio_calls_history.json)

/etc/
├── asound.conf                  ← ALSA (generato dal wizard)
├── openal/alsoft.conf           ← OpenAL
├── mumble-server.ini            ← server Mumble
├── systemd/system/doorphoneserver.service
├── sudoers.d/doorphoneserver          ← build/service senza password (Passo 7)
└── log2ram.conf                 ← se installato

/boot/firmware/config.txt        ← audio onboard disabilitato (Bookworm+)
/boot/config.txt                 ← (su sistemi più vecchi)
```

---

## Problemi comuni

### Flask non trovato all'avvio del wizard

```bash
sudo apt-get install -y python3-flask
```

### Il wizard non trova la scheda audio

```bash
aplay -l    # lista schede output
arecord -l  # lista schede input
```

Collega la scheda USB **prima** di avviare il wizard, oppure riconfigura dopo:

```bash
python3 setup/wizard.py --audio-setup
```

### Il build chiede la password sudo

Esegui il Passo 7 per installare le regole sudoers:

```bash
sudo bash setup/scripts/setup_sudoers.sh
```

### Servizio non parte dopo il riavvio

```bash
journalctl -u doorphoneserver -n 50
```

Verifica che `/home/doorphoneserver/doorphoneserver.xml` sia presente e configurato con IP/credenziali del server Mumble corretti.

### Snapshot falliscono con "Could not open file ... Input/output error"

ffmpeg non riesce a scrivere nella directory snapshot perché non appartiene
all'utente di servizio `doorphoneserver` o non ha permesso di scrittura.
Ricrea la directory con i permessi corretti:

```bash
sudo bash setup/scripts/setup_data_dir.sh
```

In alternativa, manualmente:

```bash
sudo mkdir -p /home/doorphoneserver/snapshots
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/snapshots
sudo chmod 775 /home/doorphoneserver/snapshots
```

> Nota: su ffmpeg 7.x la cattura di un singolo JPEG richiede il flag `-update 1`
> (già incluso nel codice). Se aggiorni ffmpeg e gli snapshot smettono di
> funzionare con "does not contain an image sequence pattern", il problema è quel flag.

---

## Configurazione doorphoneserver.xml

Il file di configurazione principale si trova in:

```
/home/doorphoneserver/doorphoneserver.xml
```

I parametri minimi da modificare:

```xml
<server>
    <ip>127.0.0.1</ip>        <!-- IP del server Mumble, 127.0.0.1 se locale -->
    <port>64738</port>
    <username>NOME_UTENTE</username>
    <password>PASSWORD</password>
    <channel>CANALE_PREDEFINITO</channel>
</server>
```

Il server Mumble locale è già installato e avviato dal wizard sulla stessa macchina.

---

*DoorPhoneServer Setup Wizard v2.0.0*

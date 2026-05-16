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

## Passo 3 — Installa git e python3

Su Raspberry Pi OS sono quasi sempre già presenti. Verifica:

```bash
git --version
python3 --version
```

Se mancano:

```bash
sudo apt-get install -y git python3
```

---

## Passo 4 — Clona il repository

```bash
git clone https://github.com/MirkoUgoliniDev/DoorPhoneServer ~/doorphoneserver-setup
cd ~/doorphoneserver-setup
```

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

## Passo 7 — Riavvio finale

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
/home/doorphoneserver/
├── bin/doorphoneserver              ← binario compilato
├── doorphoneserver.xml              ← configurazione principale
└── gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/

/etc/
├── asound.conf                  ← ALSA (generato dal wizard)
├── openal/alsoft.conf           ← OpenAL
├── mumble-server.ini            ← server Mumble
├── systemd/system/doorphoneserver.service
├── sudoers.d/doorphoneserver-panel
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

### Errore "permission denied" durante il build

Assicurati di lanciare il wizard come utente normale (non root).
Il wizard usa `sudo` solo dove necessario.

### Servizio non parte dopo il riavvio

```bash
journalctl -u doorphoneserver -n 50
```

Verifica che `/home/doorphoneserver/doorphoneserver.xml` sia presente e configurato con IP/credenziali del server Mumble corretti.

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

# Migrazione doorphoneserver: dal Raspberry vecchio al nuovo

Procedura passo-passo per **clonare il certificato Mumble e tutti i dati** dal Raspberry
di produzione (installazione vecchia) a un Raspberry nuovo installato con il wizard,
in modo che **i tablet non si accorgano del cambio di server**.

> Utente di servizio: `doorphoneserver` — home: `/home/doorphoneserver`
> Tutti i comandi "sul Pi vecchio" vanno eseguiti mentre l'immagine vecchia è operativa.

---

## Cosa viene clonato

| Dato | Percorso (Pi vecchio) | Perché serve |
|------|----------------------|--------------|
| Certificato + chiave server Mumble | DB `/var/lib/mumble-server/mumble-server.sqlite` (chiavi `certificate` / `key`) | i tablet hanno "pinnato" questo certificato |
| Password Mumble | `/etc/mumble-server.ini` → `serverpassword` | i tablet la usano per connettersi |
| Config principale | `/home/doorphoneserver/doorphoneserver.xml` | RTSP camera, GPIO, piani, ecc. |
| Segreti / API key | `/home/doorphoneserver/.env` (permessi 660) | chiavi OpenRouter/OpenAI |
| Allarmi / AI / piani | `/home/doorphoneserver/preferences/{alarms,ai,floors}.json` | preferenze applicative |
| Whitelist NFC | `/home/doorphoneserver/preferences/nfc_whitelist.json` | tessere abilitate |
| Suoni personalizzati | `/home/doorphoneserver/soundfiles/events/` | file caricati dall'utente |
| APK (opzionale) | `/home/doorphoneserver/apk/` | gestione tablet |

> NON serve clonare: `snapshots/` (transitori), certificato client `~/mumble.pem`
> (rigenerato dal setup), crontab (ricreato dal setup).

---

## FASE 1 — Backup dal Raspberry VECCHIO

Esegui questi comandi sul Pi vecchio (con il citofono funzionante).

### 1.1 — Estrai il certificato del server Mumble
```bash
sudo apt-get install -y sqlite3   # se manca

sudo sqlite3 /var/lib/mumble-server/mumble-server.sqlite \
  "SELECT value FROM config WHERE key='certificate';" > /tmp/mumble_cert.pem
sudo sqlite3 /var/lib/mumble-server/mumble-server.sqlite \
  "SELECT value FROM config WHERE key='key';" > /tmp/mumble_key.pem
```

Verifica che siano PEM validi:
```bash
head -1 /tmp/mumble_cert.pem   # atteso: -----BEGIN CERTIFICATE-----
head -1 /tmp/mumble_key.pem    # atteso: -----BEGIN ... PRIVATE KEY-----
openssl x509 -in /tmp/mumble_cert.pem -noout -fingerprint -sha256
```
Annota il fingerprint: è esattamente ciò che i tablet hanno memorizzato.

> Se i file risultano VUOTI: il vecchio server usava già certificati su file. Trova dove:
> `sudo grep -E "sslCert|sslKey" /etc/mumble-server.ini` e copia quei due file al posto
> dei comandi sqlite qui sopra.

### 1.2 — Annota la password Mumble
```bash
sudo grep serverpassword /etc/mumble-server.ini
```
Segnala questa password al wizard del nuovo Pi (passo "Mumble Server").

### 1.3 — Crea un archivio con tutti i dati applicativi
```bash
cd /home/doorphoneserver
tar -czf /tmp/doorphone_data.tar.gz \
  doorphoneserver.xml \
  .env \
  preferences/ \
  soundfiles/ \
  apk/ 2>/dev/null
ls -lh /tmp/doorphone_data.tar.gz
```
(Se `apk/` non esiste, l'errore è innocuo grazie a `2>/dev/null`.)

### 1.4 — Copia tutto fuori dal Pi
Copia su chiavetta USB o su un altro PC via scp:
```bash
# da un altro PC, sostituendo IP_PI_VECCHIO:
scp pi@IP_PI_VECCHIO:/tmp/mumble_cert.pem   ./
scp pi@IP_PI_VECCHIO:/tmp/mumble_key.pem    ./
scp pi@IP_PI_VECCHIO:/tmp/doorphone_data.tar.gz ./
```

A questo punto hai 3 file al sicuro: `mumble_cert.pem`, `mumble_key.pem`, `doorphone_data.tar.gz`.

---

## FASE 2 — Installazione del Raspberry NUOVO

1. Installa con il wizard web come da [INSTALL.md](INSTALL.md) (`python3 setup/wizard.py --web`).
2. Al passo **Mumble Server**, usa la **stessa password** annotata al punto 1.2.
3. Nella sezione **Certificato server (opzionale)** carica i due file
   `mumble_cert.pem` e `mumble_key.pem` estratti nella Fase 1: il wizard li
   installa e applica automaticamente il pinning (`sslCert`/`sslKey`).
   → Se carichi i certificati qui, la **Fase 3.2 manuale NON serve**.
   → Se lasci i campi vuoti, il setup genera un certificato nuovo (i tablet
     dovranno riaccettarlo).
4. Completa il wizard fino in fondo (crea utente, servizio, cartelle, ecc.).
5. **Non avviare ancora a regime**: prima ripristina i restanti dati (Fase 3).

---

## FASE 3 — Ripristino sul Raspberry NUOVO

Copia i 3 file sul Pi nuovo (es. in `/tmp`), poi:

### 3.1 — Ferma i servizi
```bash
sudo systemctl stop doorphoneserver
sudo systemctl stop mumble-server
```

### 3.2 — Ripristina il certificato del server Mumble (il punto chiave)
```bash
sudo mkdir -p /etc/mumble-server
sudo cp /tmp/mumble_cert.pem /etc/mumble-server/cert.pem
sudo cp /tmp/mumble_key.pem  /etc/mumble-server/key.pem
sudo chown mumble-server:mumble-server /etc/mumble-server/cert.pem /etc/mumble-server/key.pem
sudo chmod 644 /etc/mumble-server/cert.pem
sudo chmod 600 /etc/mumble-server/key.pem
```

Aggiungi le righe `sslCert`/`sslKey` all'ini (se non già presenti):
```bash
sudo grep -q '^sslCert=' /etc/mumble-server.ini || \
  echo 'sslCert=/etc/mumble-server/cert.pem' | sudo tee -a /etc/mumble-server.ini
sudo grep -q '^sslKey='  /etc/mumble-server.ini || \
  echo 'sslKey=/etc/mumble-server/key.pem'  | sudo tee -a /etc/mumble-server.ini
```

### 3.3 — Ripristina i dati applicativi
```bash
cd /home/doorphoneserver
sudo tar -xzf /tmp/doorphone_data.tar.gz -C /home/doorphoneserver
sudo chown -R doorphoneserver:doorphoneserver /home/doorphoneserver
# .env deve restare 660 (vedi regola permessi)
sudo chmod 660 /home/doorphoneserver/.env
```

### 3.4 — Riavvia i servizi
```bash
sudo systemctl start mumble-server
sudo systemctl start doorphoneserver
```

---

## FASE 4 — Verifica

### 4.1 — Il certificato presentato è quello vecchio
```bash
echo | openssl s_client -connect 127.0.0.1:64738 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```
Il fingerprint deve coincidere con quello annotato al punto 1.1.

### 4.2 — I tablet si connettono senza errori
Avvia Mumble sui tablet (SENZA toccare la configurazione): devono connettersi
direttamente, senza avvisi di certificato. Controlla il log lato server:
```bash
sudo journalctl -u mumble-server -f
```
Devi vedere `New connection` seguito da `Authenticated` (e non più
`certificate unknown`). Nel pannello web → tab **Utenti Mumble** compariranno i tablet.

> Nota: se l'IP del Pi nuovo è diverso da quello vecchio, i tablet vanno comunque
> aggiornati con il nuovo indirizzo (l'IP non è coperto dal certificato). Ideale:
> assegnare al Pi nuovo lo **stesso IP** del vecchio (DHCP statico sul router).

---

## Riepilogo: cosa rende la migrazione "trasparente" per i tablet
1. **Stesso certificato server** (Fase 3.2) → niente avviso "certificate unknown".
2. **Stessa password Mumble** (Fase 2.2) → niente "wrong password".
3. **Stesso IP** (nota Fase 4.2) → i tablet non vanno riconfigurati affatto.

Se questi tre coincidono, i tablet si riconnettono al Pi nuovo senza che nessuno
debba toccarli.

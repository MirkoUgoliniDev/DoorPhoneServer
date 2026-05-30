# Fix-Setup — Problemi riscontrati dopo il setup e relative soluzioni

Problemi trovati la prima volta che si tenta di avviare il servizio dopo un setup pulito su Pi nuovo.

---

## 1. `doorphoneserver.xml` mancante

**Sintomo:** il servizio crasha immediatamente con:
```
Fatal error: error opening file /home/doorphoneserver/doorphoneserver.xml: no such file or directory
```

**Causa:** il file non viene creato dal wizard di setup — deve essere presente nel repo.
Se il file è stato rimosso per errore è ancora in git.

**Fix:**
```bash
git restore doorphoneserver.xml
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/doorphoneserver.xml
```

---

## 2. `doorphoneserver.log` con permessi sbagliati

**Sintomo:** crash con:
```
Fatal error: Cannot open log file: permission denied
```

**Causa:** il file di log esiste ma è di proprietà di `pi:pi`; il servizio gira come `doorphoneserver` e non può scriverci.

**Fix:**
```bash
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/doorphoneserver.log
```

Se il file non esiste ancora, crearlo prima:
```bash
sudo touch /home/doorphoneserver/doorphoneserver.log
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/doorphoneserver.log
```

---

## 3. Directory `/home/doorphoneserver/` non scrivibile dall'utente `doorphoneserver`

**Sintomo:** il servizio parte, carica il config, ma poi logga:
```
Another Instance of doorphoneserver is already running!!, Killing this Instance
```
...anche quando nessuna altra istanza gira e il lock file non esiste.

**Causa:** la directory è di proprietà `pi:pi` con permessi `rwxr-xr-x`. L'utente `doorphoneserver` ha solo `r-x` e non può creare nuovi file (incluso `doorphoneserver.lock`). La libreria `go-singleinstance` usa `flock`; se `os.OpenFile` fallisce con `EACCES`, interpreta l'errore come "altra istanza in esecuzione".

**Fix:**
```bash
sudo chgrp doorphoneserver /home/doorphoneserver/
sudo chmod g+w /home/doorphoneserver/
```

---

## 4. Certificato `mumble.pem` mancante

**Sintomo:** crash con:
```
Fatal error: Certificate Error open /home/doorphoneserver/mumble.pem: no such file or directory
```

**Causa:** il certificato TLS per la connessione Mumble non viene generato automaticamente dal wizard.

**Fix:** generare un certificato self-signed senza passphrase (necessario per il servizio automatico):
```bash
openssl genrsa -out /tmp/key.pem 2048
openssl req -new -x509 -key /tmp/key.pem -out /tmp/cert.pem -days 1095 -subj "/CN=doorphoneserver"
cat /tmp/key.pem /tmp/cert.pem > /home/doorphoneserver/mumble.pem
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/mumble.pem
rm /tmp/key.pem /tmp/cert.pem
```

Lo script originale `setup/scripts/gencert.sh` richiede input interattivo e non è adatto all'avvio automatico.

---

## 5. `ExecStop` nel service systemd con `$MAINPID` vuoto

**Sintomo:** loop di restart infinito, nei log di systemd:
```
doorphoneserver.service: Referenced but unset environment variable evaluates to an empty string: MAINPID
```

**Causa:** la riga `ExecStop=/bin/kill -s SIGTERM $MAINPID` nel service file fallisce perché `$MAINPID` è vuoto quando il processo principale è già uscito prima che ExecStop venga eseguito. Questo impedisce la pulizia corretta tra un restart e l'altro.

**Fix:** rimuovere la riga `ExecStop` e aggiungere la pulizia del lock file in `ExecStartPre`.
systemd invia automaticamente SIGTERM al processo principale senza bisogno di un `ExecStop` esplicito.

File `/etc/systemd/system/doorphoneserver.service` — sezione `[Service]`:
```ini
ExecStartPre=-/bin/rm -f /home/doorphoneserver/doorphoneserver.lock
ExecStartPre=/bin/sleep 15
ExecStart=/home/doorphoneserver/bin/doorphoneserver -config=/home/doorphoneserver/doorphoneserver.xml
# ExecStop rimosso: systemd gestisce SIGTERM automaticamente
```

Dopo la modifica:
```bash
sudo systemctl daemon-reload
sudo systemctl restart doorphoneserver.service
```

---

## Fix completo one-shot (nuovo Pi dopo setup wizard)

Da eseguire una volta sola dopo aver completato il wizard:

```bash
# 1. Ripristina il config XML se mancante
[ -f /home/doorphoneserver/doorphoneserver.xml ] || git -C /home/doorphoneserver restore doorphoneserver.xml

# 2. Permessi corretti su file e directory
sudo chgrp doorphoneserver /home/doorphoneserver/
sudo chmod g+w /home/doorphoneserver/
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/doorphoneserver.xml
sudo touch /home/doorphoneserver/doorphoneserver.log
sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/doorphoneserver.log

# 3. Genera il certificato Mumble se mancante
if [ ! -f /home/doorphoneserver/mumble.pem ]; then
  openssl genrsa -out /tmp/key.pem 2048
  openssl req -new -x509 -key /tmp/key.pem -out /tmp/cert.pem -days 1095 -subj "/CN=doorphoneserver"
  cat /tmp/key.pem /tmp/cert.pem > /home/doorphoneserver/mumble.pem
  sudo chown doorphoneserver:doorphoneserver /home/doorphoneserver/mumble.pem
  rm /tmp/key.pem /tmp/cert.pem
fi

# 4. Rimuovi lock stale e avvia
sudo rm -f /home/doorphoneserver/doorphoneserver.lock
sudo systemctl daemon-reload
sudo systemctl restart doorphoneserver.service
```

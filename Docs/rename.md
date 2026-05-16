# Migration Plan: DoorPhoneServer → DoorPhoneServer

**Scope**: eliminare ogni riferimento a "doorphoneserver" / "DoorPhoneServer" (binario, package Go,
servizio systemd, utente di sistema, percorsi, config, script, documentazione) e sostituirlo con
"doorphoneserver" / "DoorPhoneServer".

---

## Indice delle fasi

| # | Fase | File coinvolti | Rischio |
|---|------|---------------|---------|
| 1 | Preparazione e branch | — | basso |
| 2 | Go: package e modulo | tutti i `.go`, `go.mod` | alto |
| 3 | Go: struct, metodi, funzioni | tutti i `.go` | medio |
| 4 | Go: stringhe runtime | `client.go`, `xmlparser.go`, `webpanel.go`, `doorphoneserver.go` | medio |
| 5 | File di configurazione XML | `doorphoneserver.xml`, backup, Configurazioni/ | basso |
| 6 | Systemd service | `conf/systemd/doorphoneserver.service` | alto |
| 7 | Script shell | `tkbuild.sh`, `scripts/*.sh` | medio |
| 8 | Web panel | `webpanel_static/panel.html`, `panel.js` | basso |
| 9 | File Python (setup wizard) | `setup/*.py` | basso |
| 10 | Documentazione | `Docs/*.md`, `setup/INSTALL.md`, `plans/*.md` | basso |
| 11 | `.gitignore` e workspace | `.gitignore`, `*.code-workspace` | basso |
| 12 | Rinomina file e cartelle | file e directory con "doorphoneserver" nel nome | medio |
| 13 | Build, test e deployment | — | — |

---

## Fase 1 — Preparazione

```bash
git checkout -b rename/doorphoneserver-to-doorphoneserver
```

Creare un commit iniziale vuoto come punto di rollback sicuro.

---

## Fase 2 — Go: package e modulo

### 2.1 `go.mod` — dichiarazione del modulo

```diff
- module github.com/MirkoUgoliniDev/doorphoneserver
+ module github.com/MirkoUgoliniDev/doorphoneserver
```

Le dipendenze `github.com/doorphoneserver/*` **non cambiano** (restano come import path
"storici" dei fork). I blocchi `replace` puntano già a `github.com/MirkoUgoliniDev/*`,
quindi funzionano senza modifiche.

### 2.2 Package declaration — tutti i file `.go`

Sostituire in tutti i file Go:

```diff
- package doorphoneserver
+ package doorphoneserver
```

File interessati (22 file): `alarms.go`¹, `baichuan.go`, `client.go`, `clientcommands.go`,
`commandkeys.go`, `custom_func.go`, `globalcontext.go`, `gpio.go`, `htgotts.go`,
`httpapi.go`, `logrotate.go`, `media.go`, `monitoring.go`, `mqtt.go`, `onevent.go`,
`pushover.go`, `sonoff.go`, `stream.go`, `doorphoneserver.go`, `utils.go`, `version.go`,
`webpanel.go`, `webrtc_stream.go`, `xmlparser.go`.

> ¹ Non presente nella root al momento dell'analisi ma potrebbe esistere in build diverse.

**Comando suggerito:**
```bash
find . -name "*.go" -exec sed -i 's/^package doorphoneserver$/package doorphoneserver/' {} +
```

### 2.3 Import path interni

Se il modulo viene rinominato (punto 2.1), tutti gli import che referenziano
`github.com/MirkoUgoliniDev/doorphoneserver/...` devono essere aggiornati:

```diff
- import "github.com/MirkoUgoliniDev/doorphoneserver/..."
+ import "github.com/MirkoUgoliniDev/doorphoneserver/..."
```

**Comando suggerito:**
```bash
find . -name "*.go" -exec sed -i \
  's|github.com/MirkoUgoliniDev/doorphoneserver|github.com/MirkoUgoliniDev/doorphoneserver|g' {} +
```

Poi eseguire:
```bash
go mod tidy
```

---

## Fase 3 — Go: struct, metodi, funzioni

### 3.1 Tipo principale

In [client.go](../client.go) (riga ~51):

```diff
- type DoorPhoneServer struct {
+ type DoorPhoneServer struct {
```

Tutti i metodi nel codebase usano `b *DoorPhoneServer` come receiver — vanno aggiornati:

```diff
- func (b *DoorPhoneServer) NomeMetodo(...) {
+ func (b *DoorPhoneServer) NomeMetodo(...) {
```

File con receiver `*DoorPhoneServer`:
- [client.go](../client.go)
- [clientcommands.go](../clientcommands.go)
- [commandkeys.go](../commandkeys.go)
- [gpio.go](../gpio.go)
- [media.go](../media.go)
- [monitoring.go](../monitoring.go)
- [mqtt.go](../mqtt.go)
- [onevent.go](../onevent.go)
- [stream.go](../stream.go)
- [webpanel.go](../webpanel.go)
- [webrtc_stream.go](../webrtc_stream.go)
- (e altri file con metodi sul tipo)

**Comando suggerito:**
```bash
find . -name "*.go" -exec sed -i \
  's/\*DoorPhoneServer/\*DoorPhoneServer/g; s/DoorPhoneServer{/DoorPhoneServer{/g' {} +
```

### 3.2 Funzioni con "doorphoneserver" nel nome

In [doorphoneserver.go](../doorphoneserver.go):
- `doorphoneserverAcknowledgements()` → `doorphoneserverAcknowledgements()`
- `doorphoneserverMenu()` → `doorphoneserverMenu()`

In [commandkeys.go](../commandkeys.go):
- `cmdQuitDoorPhoneServer()` → `cmdQuitDoorPhoneServer()`

**Comando suggerito:**
```bash
find . -name "*.go" -exec sed -i \
  's/doorphoneserverAcknowledgements/doorphoneserverAcknowledgements/g;
   s/doorphoneserverMenu/doorphoneserverMenu/g;
   s/cmdQuitDoorPhoneServer/cmdQuitDoorPhoneServer/g' {} +
```

---

## Fase 4 — Go: stringhe runtime

Queste stringhe appaiono nei log, TTS, lock file, MQTT — vanno cambiate manualmente
verificando il contesto per non rompere integrazioni esterne (es. topic MQTT).

### 4.1 Lock file — [client.go](../client.go)

```diff
- "/tmp/doorphoneserver.lock"
+ "/tmp/doorphoneserver.lock"
```

### 4.2 TTS events — [client.go](../client.go) e [onevent.go](../onevent.go)

```diff
- "quitdoorphoneserver"
+ "quitdoorphoneserver"
- "doorphoneserverloaded"
+ "doorphoneserverloaded"
```

### 4.3 Default config path — [xmlparser.go](../xmlparser.go)

```diff
- "./doorphoneserver"       (nome binario di default)
+ "./doorphoneserver"
- "doorphoneserver.xml"    (nome config di default)
+ "doorphoneserver.xml"
```

### 4.4 Percorsi log e snapshot — [webpanel.go](../webpanel.go), [xmlparser.go](../xmlparser.go)

```diff
- "/home/doorphoneserver/doorphoneserver.log"
+ "/home/doorphoneserver/doorphoneserver.log"
- "/home/doorphoneserver/snapshots"
+ "/home/doorphoneserver/snapshots"
- "/var/lib/doorphoneserver/data"
+ "/var/lib/doorphoneserver/data"
```

### 4.5 Username generato con MAC — [client.go](../client.go)

Il codice genera username tipo `doorphoneserver-AABBCCDDEEFF`. Valutare se cambiare a
`doorphoneserver-AABBCCDDEEFF` o usare un prefisso più breve (es. `dps-`).

### 4.6 Version string — [version.go](../version.go) e [doorphoneserver.go](../doorphoneserver.go)

Aggiornare le stringhe di versione / banner che mostrano "DoorPhoneServer".

### 4.7 MQTT topic — [mqtt.go](../mqtt.go) e `doorphoneserver.xml`

> **Attenzione**: il topic MQTT `thailand/bangkok/company/doorphoneserver` e il client ID
> `doorphoneserver002` sono configurati nell'XML. Se ci sono subscriber/publisher esterni che
> usano questi topic, coordinare la migrazione prima di cambiarli.

---

## Fase 5 — File di configurazione XML

### File da rinominare e aggiornare

| File attuale | File nuovo |
|---|---|
| `doorphoneserver.xml` | `doorphoneserver.xml` |
| `doorphoneserver.xml.backup` | `doorphoneserver.xml.backup` |
| `Configurazioni/doorphoneserver.xml` | `Configurazioni/doorphoneserver.xml` |

### Contenuto XML da aggiornare

Dentro ogni XML:

```diff
- <account name="doorphoneserver-community">
+ <account name="doorphoneserver-community">

- <logfilename>/home/doorphoneserver/doorphoneserver.log</logfilename>
+ <logfilename>/home/doorphoneserver/doorphoneserver.log</logfilename>

- <snapshotspath>/home/doorphoneserver/snapshots</snapshotspath>
+ <snapshotspath>/home/doorphoneserver/snapshots</snapshotspath>

- <!-- topic MQTT: valutare se cambiare (vedi §4.7) -->
- thailand/bangkok/company/doorphoneserver
+ thailand/bangkok/company/doorphoneserver
```

---

## Fase 6 — Systemd service

### 6.1 Rinominare il file

```bash
mv conf/systemd/doorphoneserver.service conf/systemd/doorphoneserver.service
```

### 6.2 Contenuto da aggiornare

```diff
[Unit]
- Description=DoorPhoneServer Radio Service
+ Description=DoorPhoneServer Service

[Service]
- User=doorphoneserver
- Group=doorphoneserver
+ User=doorphoneserver
+ Group=doorphoneserver
- WorkingDirectory=/home/doorphoneserver
+ WorkingDirectory=/home/doorphoneserver
- ExecStart=/home/doorphoneserver/bin/doorphoneserver \
-   -config=/home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/doorphoneserver.xml
+ ExecStart=/home/doorphoneserver/bin/doorphoneserver \
+   -config=/home/doorphoneserver/gocode/src/github.com/MirkoUgoliniDev/doorphoneserver/doorphoneserver.xml
```

### 6.3 Sul sistema (eseguire manualmente dopo la build)

```bash
sudo systemctl stop doorphoneserver
sudo systemctl disable doorphoneserver
sudo cp conf/systemd/doorphoneserver.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable doorphoneserver
sudo systemctl start doorphoneserver
```

---

## Fase 7 — Script shell

### 7.1 File da rinominare

| File attuale | File nuovo |
|---|---|
| `tkbuild.sh` | `build.sh` (oppure `doorphoneserver-build.sh`) |
| `scripts/tkbuild.sh` | `scripts/doorphoneserver-build.sh` |
| `scripts/update-doorphoneserver.sh` | `scripts/update-doorphoneserver.sh` |
| `scripts/setup-data-dir.sh` | invariato (generico) o `scripts/setup-data-dir-doorphoneserver.sh` |

### 7.2 Sostituzioni interne negli script

In tutti gli script `.sh` sostituire:

| Da | A |
|---|---|
| `doorphoneserver` (utente/gruppo) | `doorphoneserver` |
| `/home/doorphoneserver` | `/home/doorphoneserver` |
| `/var/lib/doorphoneserver` | `/var/lib/doorphoneserver` |
| `doorphoneserver` (nome binario) | `doorphoneserver` |
| `doorphoneserver.service` | `doorphoneserver.service` |
| `doorphoneserver.xml` | `doorphoneserver.xml` |
| `TK_USER="doorphoneserver"` | `TK_USER="doorphoneserver"` |
| `TK_GROUP="doorphoneserver"` | `TK_GROUP="doorphoneserver"` |

**Comando suggerito:**
```bash
find . -name "*.sh" -exec sed -i \
  's/doorphoneserver/doorphoneserver/g; s/DoorPhoneServer/DoorPhoneServer/g' {} +
```

> Verificare manualmente ogni script dopo la sostituzione automatica.

---

## Fase 8 — Web panel

### [webpanel_static/panel.html](../webpanel_static/panel.html)

```diff
- <h2>doorphoneserver.xml Configuration</h2>
+ <h2>doorphoneserver.xml Configuration</h2>
```

### [webpanel_static/panel.js](../webpanel_static/panel.js)

```diff
- "DoorPhoneServer"   (label del modale)
+ "DoorPhoneServer"
- systemctl start doorphoneserver
+ systemctl start doorphoneserver
- systemctl stop doorphoneserver
+ systemctl stop doorphoneserver
```

---

## Fase 9 — File Python (setup wizard)

File in `setup/`:

| File | Cosa cambia |
|---|---|
| `constants.py` | `TK_USER = "doorphoneserver"`, `TK_GROUP = "doorphoneserver"` |
| `data_dir.py` | percorsi `/var/lib/doorphoneserver`, `/home/doorphoneserver` |
| `clone_build.py` | path del binario, path del repo |
| `systemd.py` | nome service, sudoers entry |
| `wizard.py` | messaggi UI, step names |
| `create_user.py` | nome utente di sistema |

---

## Fase 10 — Documentazione Markdown

File da aggiornare (ricerca-e-sostituisci + revisione manuale):

- [Docs/setup.md](setup.md)
- [Docs/Log2Ram.md](Log2Ram.md)
- `setup/INSTALL.md`
- `plans/*.md`
- Qualsiasi altro `.md` nella root o nelle sottocartelle

**Comando suggerito:**
```bash
find . -name "*.md" -exec sed -i \
  's/doorphoneserver/doorphoneserver/g; s/DoorPhoneServer/DoorPhoneServer/g; s/DoorPhoneServer/DoorPhoneServer/g' {} +
```

> Revisione manuale raccomandata per preservare il senso del testo.

---

## Fase 11 — `.gitignore` e file workspace

### [.gitignore](../.gitignore)

```diff
- doorphoneserver.xml.bak.*
+ doorphoneserver.xml.bak.*
- doorphoneserver.lock
+ doorphoneserver.lock
- doorphoneserver          # binario
+ doorphoneserver
```

### `DoorPhoneServer.code-workspace`

Aggiornare eventuali `remoteCommand` e build task che referenziano `doorphoneserver` nel nome
del binario o nel percorso del repository.

---

## Fase 12 — Rinomina file e directory fisici

```bash
# Config
mv doorphoneserver.xml doorphoneserver.xml
mv doorphoneserver.xml.backup doorphoneserver.xml.backup
mv Configurazioni/doorphoneserver.xml Configurazioni/doorphoneserver.xml

# Systemd
mv conf/systemd/doorphoneserver.service conf/systemd/doorphoneserver.service

# Script
mv scripts/update-doorphoneserver.sh scripts/update-doorphoneserver.sh

# File Go principale (opzionale — Go non richiede nome file = package)
# mv doorphoneserver.go doorphoneserver.go  ← valutare
```

> Il file principale `doorphoneserver.go` può essere rinominato `doorphoneserver.go` per
> coerenza, ma non è obbligatorio per la compilazione Go.

---

## Fase 13 — Build, test e deployment

### Utente di sistema

Se si usa un utente dedicato, crearlo **prima** di riavviare il servizio:

```bash
sudo useradd -r -m -d /home/doorphoneserver -s /bin/bash doorphoneserver
sudo groupadd doorphoneserver   # solo se non creato da useradd
```

Copiare i file di configurazione e il binario nella nuova home:

```bash
sudo mkdir -p /home/doorphoneserver/bin
sudo cp doorphoneserver /home/doorphoneserver/bin/
sudo cp doorphoneserver.xml /home/doorphoneserver/
sudo chown -R doorphoneserver:doorphoneserver /home/doorphoneserver
```

### Build

```bash
go build -o doorphoneserver .
```

### Verifica

```bash
./doorphoneserver -config=doorphoneserver.xml --version
./doorphoneserver -config=doorphoneserver.xml
```

### Attivare il nuovo servizio

```bash
sudo systemctl daemon-reload
sudo systemctl enable doorphoneserver
sudo systemctl start doorphoneserver
sudo systemctl status doorphoneserver
```

---

## Checklist finale

- [ ] `go.mod`: modulo rinominato
- [ ] Tutti i `.go`: `package doorphoneserver`
- [ ] Struct `DoorPhoneServer` e receiver aggiornati
- [ ] Funzioni con "doorphoneserver" nel nome rinominate
- [ ] Stringhe runtime aggiornate (lock, TTS, log path, XML path)
- [ ] `doorphoneserver.xml` creato e aggiornato
- [ ] `conf/systemd/doorphoneserver.service` aggiornato
- [ ] Script shell rinominati e aggiornati
- [ ] Web panel aggiornato
- [ ] File Python aggiornati
- [ ] Documentazione aggiornata
- [ ] `.gitignore` aggiornato
- [ ] `go mod tidy` eseguito senza errori
- [ ] `go build` eseguito senza errori
- [ ] Servizio systemd attivo e funzionante
- [ ] Vecchio servizio `doorphoneserver` disabilitato e rimosso

---

## Note critiche

1. **MQTT topic e client ID** (`doorphoneserver002`): se ci sono dispositivi o server che
   si abbonano a questi topic, la migrazione deve essere coordinata — non è un semplice
   rename locale.

2. **Utente di sistema**: il rename dell'utente OS richiede che il vecchio utente
   `doorphoneserver` non abbia processi attivi. Usare `usermod -l doorphoneserver doorphoneserver`
   oppure creare un utente nuovo e migrare i file.

3. **GitHub remote**: se il repo viene spostato da `github.com/MirkoUgoliniDev/doorphoneserver`
   a `github.com/MirkoUgoliniDev/doorphoneserver`, aggiornare il remote git:
   ```bash
   git remote set-url origin git@github.com:MirkoUgoliniDev/doorphoneserver.git
   ```

4. **Dipendenze fork** (`github.com/doorphoneserver/gumble`, etc.): i blocchi `replace` in
   `go.mod` puntano già a `MirkoUgoliniDev/*` — non è necessario rinominare questi import
   path a meno che non si voglia eliminare completamente il namespace `doorphoneserver`.

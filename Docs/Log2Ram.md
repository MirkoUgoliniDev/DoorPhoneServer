# Log2Ram — Analisi Approfondita, Piano di Installazione e Tab Web

---

## 1. Contesto del Sistema

| Parametro | Valore |
|-----------|--------|
| Hardware | Raspberry Pi 4 (ARM64 — aarch64) |
| Kernel | 6.1.34-v8+ |
| OS | Raspbian GNU/Linux 11 (bullseye) |
| Storage | microSD Samsung SC16G — 16 GB |
| Partizione root | `/dev/mmcblk0p2` — 14.6 GB (usati 7.7 GB / 57%) |
| RAM totale | 3.844 MB |
| RAM disponibile | ~2.695 MB (70% libera) |
| Swap | 100 MB su file (`/var/swap`) — non in uso |
| Stato throttling | `0x0` — nessun problema termico |
| Riavvii schedulati | 4 al giorno (00:00, 00:05, 06:00, 06:05 via crontab) |
| Riavvii storici tracciati | 9 (da journal) |

---

## 2. Analisi Scritture sulla microSD — SITUAZIONE CRITICA

### 2.1 Dati reali rilevati (sessione di 8.3 ore)

```
Settori scritti:        2.944.642 settori × 512 byte
MB scritti totali:      1.437 MB in 8.3 ore
Proiezione giornaliera: ~4.140 MB/giorno ≈ 4 GB/giorno
```

### 2.2 Indicatore critico: tempo I/O in scrittura

```
Tempo totale scrittura SD:  22.940.460 ms = 6.3 ore
Uptime sistema:             8.3 ore
% tempo occupato in I/O:    76.3% ← CRITICO
```

> **Il sistema passa il 76% del suo tempo a scrivere sulla SD.**
> Questo significa che la microSD è il collo di bottiglia principale del sistema.

### 2.3 Processi che scrivono su `/var/log`

| Processo | File scritti | Impatto |
|---------|--------------|---------|
| `rsyslogd` (28 handle aperti) | syslog, daemon.log, auth.log, ecc. | Alto — scrittura continua |
| `systemd-journald` (6 handle) | `/var/log/journal/` (125 MB su SD) | Alto — scrive ad ogni evento |
| `murmurd` (3 handle) | `/var/log/mumble-server/` | Medio |
| `cupsd` (1 handle) | `/var/log/cups/` | Basso |

### 2.4 Volumi di log attuali

| File / Directory | Dimensione | Note |
|----------------|------------|------|
| `/var/log/journal/` | **125 MB** | Persistente su SD — journald |
| `/var/log/syslog.1` | **16 MB** | Sessione precedente |
| `/var/log/daemon.log.1` | **16 MB** | Sessione precedente |
| `/var/log/mumble-server/` | **~22 KB attivo** | OK |
| `/var/log/doorphoneserver_error.log` | **0 B** | Vuoto |
| **TOTALE `/var/log`** | **~171 MB** | Dopo pulizia uvcdynctrl |

### 2.5 Stato journal: persistente su SD

```
/var/log/journal/f09644d4ecf846049c22a93833af9120/   ← ESISTE → scrive su SD
/run/log/journal/                                     ← ESISTE ma vuoto → volatile non attivo
```

Il journal è configurato in modalità **persistente** (`Storage=auto` con `/var/log/journal/` presente):
ogni log di sistema finisce direttamente su SD in tempo reale.

### 2.6 Configurazione journald attuale (non ottimale)

```ini
[Journal]
SystemMaxUse=100M        # limite dimensione — OK
SystemKeepFree=500M      # spazio libero minimo — OK
SystemMaxFileSize=20M    # dimensione per file — OK
MaxRetentionSec=2week    # retention 2 settimane — OK come valore
# Storage NON specificato → default "auto" → PERSISTENTE su SD
```

---

## 3. Analisi del Rischio sulla microSD

### 3.1 Tecnologia NAND Flash — cicli P/E

| Tipo NAND | Cicli P/E | Schede tipiche |
|-----------|-----------|----------------|
| SLC | 100.000 | Industriali |
| MLC | 3.000–10.000 | Consumer medio-alte |
| TLC | 500–3.000 | Consumer economiche |
| QLC | 100–1.000 | Grandi capacità economiche |

La Samsung SC16G è classificata come **MLC consumer** (~3.000–5.000 cicli P/E).

### 3.2 Stima vita utile senza Log2Ram

```
Scritture/giorno rilevate:  ~4.000 MB = 4 GB
Area partizione root:        7.7 GB usati su 14.6 GB
Wear leveling stimato:       il controller distribuisce su ~14 GB

Cicli/cella stimati/anno:
  4.000 MB/giorno × 365 giorni = 1.460.000 MB/anno ≈ 1.460 GB/anno
  Con wear leveling su 14.6 GB: ~100 cicli/anno per cella

A 3.000 cicli max → vita stimata: ~30 anni (ottimistico con wear leveling perfetto)
```

**Però**: le scritture di log sono **piccole e casuali** (non sequenziali), il che:
- Riduce l'efficienza del wear leveling
- Causa **write amplification** (il controller deve leggere/cancellare blocchi interi da 128–512 KB per scrivere pochi KB)
- **Moltiplica l'usura reale** di un fattore 10–50×

```
Write amplification stimata: 10–50×
Cicli reali/anno:            1.000–5.000 cicli/anno
Vita reale stimata:          0.6 – 3 anni ← RISCHIO CONCRETO
```

### 3.3 Rischio corruzione da blackout

Con scritture attive per il 76% del tempo, la probabilità che un'interruzione
di corrente colpisca una scrittura in corso è **molto alta**.
Una corruzione del journal o di syslog può rendere il sistema non avviabile.

---

## 4. Soluzione: Log2Ram

### 4.1 Come funziona

```
SENZA log2ram:
  rsyslogd / journald → scrive → /var/log (SD) ← continuo, 76% del tempo

CON log2ram:
  rsyslogd / journald → scrive → /var/log (RAM, tmpfs) ← istantaneo
                                      │
                              sync solo a shutdown/reboot
                                      ↓
                              /var/hdd.log (SD) ← 4 volte/giorno
```

### 4.2 Benefici concreti

| Beneficio | Impatto Attuale | Con Log2Ram |
|-----------|----------------|-------------|
| Scritture SD/giorno | ~4.000 MB continui | ~50 MB (4 sync) |
| % tempo I/O SD | **76.3%** | **< 5%** |
| Rischio corruzione blackout | **Alto** | **Minimo** |
| Latenza scrittura log | ~ms (SD lenta) | ~μs (RAM) |
| Durata SD stimata | 1–3 anni | 15–30 anni |
| RAM necessaria | — | 128 MB (su 2.695 liberi) |

### 4.3 Sinergia con crontab esistente

I riavvii schedulati già presenti triggerano automaticamente la sync RAM→SD:

```
00:00  shutdown -r  → sync log2ram → riavvio
00:05  restart_tablet.sh
06:00  shutdown -r  → sync log2ram → riavvio
06:05  restart_tablet.sh
```

I log non vengono mai persi per più di 6 ore.

---

## 5. Installazione — IMPLEMENTATA nel Web Panel ✅

L'installazione avviene tramite il tab **Log2Ram** del pannello web.
Quando log2ram non è installato, appare il bottone verde **"Installa Log2Ram"**.

### 5.1 Script di installazione

**Percorso:** `/usr/local/sbin/doorphoneserver-log2ram-install.sh` (root, chmod 755)

Lo script esegue automaticamente i seguenti step:

#### Step 1 — Configura journald in modalità volatile

**File modificato:** `/etc/systemd/journald.conf`

```ini
[Journal]
Storage=volatile
RuntimeMaxUse=50M
RuntimeMaxFileSize=10M
SystemMaxUse=100M
SystemKeepFree=500M
MaxRetentionSec=2week
```

Poi esegue: `systemctl restart systemd-journald`

#### Step 2 — Rimuove journal persistente da SD

```bash
rm -rf /var/log/journal/
# Libera ~125 MB su SD
```

#### Step 3 — Installa Log2Ram dal repo azlux

```bash
wget -qO /usr/share/keyrings/azlux-archive-keyring.gpg https://azlux.fr/repo.gpg
echo "deb [signed-by=/usr/share/keyrings/azlux-archive-keyring.gpg] \
  http://packages.azlux.fr/debian/ bullseye main" > /etc/apt/sources.list.d/azlux.list
apt-get update -q
DEBIAN_FRONTEND=noninteractive apt-get install -y log2ram
```

#### Step 4 — Configura Log2Ram

**File creato:** `/etc/log2ram.conf`

```ini
SIZE=128M
MAIL=false
PATH_DISK=/var/log
LOG_DISK_SIZE=200M
USE_RSYNC=true
CLEAN=false
```

#### Step 5 — Riavvio necessario

Dopo l'installazione il pannello mostra un toast: **"Log2Ram installato! Riavvia il sistema per attivarlo."**

Usare il bottone **Reboot** nella Dashboard oppure:

```bash
sudo reboot

# Dopo il riavvio, verifica:
mount | grep log2ram          # deve mostrare: tmpfs on /var/log
systemctl status log2ram      # deve essere: active (exited)
df -h /var/log                # deve mostrare tmpfs, non /dev/root
ls /var/hdd.log/              # backup su SD
```

### 5.2 Sudoers configurati

**File:** `/etc/sudoers.d/doorphoneserver-log2ram`

```
doorphoneserver ALL=(root) NOPASSWD: /usr/local/sbin/doorphoneserver-log2ram-install.sh
doorphoneserver ALL=(root) NOPASSWD: /usr/sbin/log2ram
doorphoneserver ALL=(root) NOPASSWD: /usr/bin/log2ram
doorphoneserver ALL=(root) NOPASSWD: /usr/local/bin/log2ram
```

> Nota: `systemctl restart log2ram` è già coperto da `/etc/sudoers.d/doorphoneserver-panel`
> (`doorphoneserver ALL=(ALL) NOPASSWD: /bin/systemctl`)

### 5.3 Bug fix: rilevamento binario log2ram

Log2Ram v1.7.2 installa il binario in `/usr/local/bin/log2ram`, non in `/usr/sbin/` o `/usr/bin/`.
Il pannello cercava solo nei percorsi classici → mostrava "Non installato" anche con log2ram attivo.

**Fix applicato in `webpanel.go`:**
```go
// Prima (bug):
for _, p := range []string{"/usr/sbin/log2ram", "/usr/bin/log2ram"} {

// Dopo (fix):
for _, p := range []string{"/usr/sbin/log2ram", "/usr/bin/log2ram", "/usr/local/bin/log2ram"} {
```

---

## 6. Tab Web Panel — "Log2Ram" — IMPLEMENTATO ✅

### 6.1 Posizione nel pannello

Tab tra **Crontab** e la fine della barra:
```html
<div class="tab" data-page="log2ram">Log2Ram</div>
```

### 6.2 Layout implementato

```
┌─────────────────────────────────────────────────────────┐
│  STATO LOG2RAM                                          │
│  ● Attivo / ✗ Non installato / ✗ Non attivo             │
│  RAM usata: 45 MB / 128 MB  [████████░░░░] 35%  (*)    │
│  Backup SD: /var/hdd.log — 44 MB                       │
│  Ultima sync: 06:05 oggi                               │
│  Journal: Volatile (RAM) / Persistente (SD)            │
│                                                         │
│  SE installato:   [Sync ora]  [Restart log2ram]        │
│  SE non installato: [Installa Log2Ram] (verde)         │
└─────────────────────────────────────────────────────────┘

(*) RAM usata mostrata solo quando /var/log è su tmpfs
    (log2ram attivo). Se non installato: campo vuoto/zero.

┌─────────────────────────────────────────────────────────┐
│  PERFORMANCE SD — SCRITTURE (ultimi 60 campioni)        │
│  [SD Writes MB/10s] [RAM Log MB (*)] [I/O Time %]      │
│  Grafico canvas — campionamento ogni 10s               │
└─────────────────────────────────────────────────────────┘

(*) RAM Log MB disabilitato se log2ram non è montato

┌─────────────────────────────────────────────────────────┐
│  STATISTICHE SESSIONE                                   │
│  MB scritti su SD (sessione): 45 MB                    │
│  Stima MB/giorno: 180 MB                               │
│  MB risparmiati vs baseline (4 GB/g): N/D se inattivo  │
│  Efficienza Log2Ram: N/D se inattivo                   │
│  % I/O SD attuale                                      │
│  Uptime sessione                                       │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│  LOG IN RAM (/var/log tmpfs) — collassabile             │
│  Tabella file con dimensione e data modifica            │
└─────────────────────────────────────────────────────────┘
```

### 6.3 API Backend implementate

| Endpoint | Metodo | Descrizione |
|---------|--------|-------------|
| `GET /panel/api/log2ram/status` | GET | stato, RAM usata/totale (*), backup size, ultima sync |
| `GET /panel/api/log2ram/metrics` | GET | settori scritti, % I/O, dimensione log in RAM (campione live) |
| `GET /panel/api/log2ram/metrics-history` | GET | array storico ultimi 60 campioni |
| `POST /panel/api/log2ram/sync` | POST | forza sync RAM→SD (`log2ram write`) |
| `POST /panel/api/log2ram/restart` | POST | restart servizio |
| `GET /panel/api/log2ram/files` | GET | lista file in /var/log con dimensioni |
| `POST /panel/api/log2ram/install` | POST | installazione completa (script 4 step) |

(*) `ram_used_bytes` / `ram_total_bytes` sono `0` quando `/var/log` non è su tmpfs.

### 6.4 Dati esposti dall'API `/panel/api/log2ram/status`

```json
{
  "installed": true,
  "active": true,
  "log2ram_mount": true,
  "ram_used_bytes": 47185920,
  "ram_total_bytes": 134217728,
  "ram_used_pct": 35.1,
  "backup_size_bytes": 46137344,
  "backup_path": "/var/hdd.log",
  "last_sync": "2026-05-11T06:05:00Z",
  "journal_volatile": true
}
```

Quando log2ram **non è installato**:
```json
{
  "installed": false,
  "active": false,
  "log2ram_mount": false,
  "ram_used_bytes": 0,
  "ram_total_bytes": 0,
  "ram_used_pct": 0,
  ...
}
```

### 6.5 Indicatore visivo stato

```
● verde  = Log2Ram attivo, /var/log su tmpfs, journal volatile
● giallo = Log2Ram attivo ma journal ancora persistente su SD
● rosso  = Log2Ram installato ma non attivo / mount assente
● grigio = Log2Ram non installato
```

### 6.6 Grafico — tecnica

Canvas nativo con 3 serie (toggleable):
- **Rosso** — SD Writes MB/10s
- **Verde** — RAM Log MB (disabilitato se non montato)
- **Giallo** — I/O Time %

Campionamento ogni 10 secondi, storico 60 campioni.

---

## 7. Analisi RAM post-installazione

| Componente | Utilizzo RAM |
|-----------|-------------|
| Sistema operativo | ~400 MB |
| Processi utente / desktop | ~350 MB |
| doorphoneserver | ~35 MB |
| murmurd (Mumble server) | ~50 MB |
| rsyslogd, journald, altri | ~50 MB |
| **Log2Ram (log in RAM)** | **128 MB** |
| Buffer/cache kernel | ~300 MB |
| **Totale stimato** | **~1.313 MB** |
| **RAM libera residua** | **~2.530 MB** ✅ |

Margine abbondante — nessun rischio di OOM (Out of Memory).

---

## 8. Riepilogo Comparativo

| Metrica | Senza Log2Ram | Con Log2Ram | Miglioramento |
|---------|--------------|-------------|--------------|
| Scritture SD/giorno | ~4.000 MB | ~50 MB | **-98.8%** |
| % tempo I/O SD | **76.3%** | **< 5%** | **-93%** |
| Rischio corruzione blackout | Alto | Minimo | ✅ |
| Durata SD stimata | 1–3 anni | 15–30 anni | **~10×** |
| Latenza scrittura log | ~2–10 ms | ~10 μs | **~1000×** |
| RAM aggiuntiva necessaria | — | 128 MB | su 2.695 liberi |
| Log persi in caso di crash | 0 (scrive in tempo reale) | max 6 ore | accettabile con reboot schedulati |

---

## 9. Stato Implementazione

- [x] **Analisi** scritture SD e rischio microSD
- [x] **Script installazione** `/usr/local/sbin/doorphoneserver-log2ram-install.sh`
  - [x] Step 1: journald → modalità volatile
  - [x] Step 2: rimozione `/var/log/journal/` da SD (~125 MB)
  - [x] Step 3: installazione `log2ram` dal repo azlux
  - [x] Step 4: configurazione `/etc/log2ram.conf` con `SIZE=128M`
- [x] **Sudoers** `/etc/sudoers.d/doorphoneserver-log2ram` configurato
- [x] **Tab web "Log2Ram"** nel pannello
  - [x] Handler Go: `handleLog2RamStatus`, `handleLog2RamMetrics`, `handleLog2RamSync`, `handleLog2RamRestart`, `handleLog2RamFiles`, `handleLog2RamInstall`
  - [x] Fix: `ram_used_bytes` restituisce `0` quando `/var/log` non è su tmpfs
  - [x] HTML: card stato + bottone Installa (visibile solo se non installato) + canvas grafico + tabella file collassabile
  - [x] JS: `loadLog2Ram()`, `drawL2RChart()`, polling 10s, `log2ramInstall()` con modale custom
- [x] **Step 5**: Riavvio e verifica — **COMPLETATO** (2026-05-12)
  - `systemctl is-active log2ram` → `active`
  - `findmnt /var/log` → `tmpfs` (128MB, rw)
  - Pannello mostra "Attivo — /var/log su RAM, journal volatile"
- [x] **Bug fix**: rilevamento binario `/usr/local/bin/log2ram` aggiunto (v1.7.2)
- [x] **Stabilità SD**: aggiunto `tmpfs /tmp` in `/etc/fstab` (64MB) — attivo al prossimo riavvio

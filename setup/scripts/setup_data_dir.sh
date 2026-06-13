#!/bin/bash
# Script per creare la directory dati di doorphoneserver con i permessi corretti

set -e

echo "=== Setup directory dati doorphoneserver ==="

# Crea directory
echo "Creazione /var/lib/doorphoneserver/data/..."
sudo mkdir -p /var/lib/doorphoneserver/data

# Imposta proprietario
echo "Impostazione proprietario doorphoneserver:doorphoneserver..."
sudo chown -R doorphoneserver:doorphoneserver /var/lib/doorphoneserver

# Imposta permessi
echo "Impostazione permessi 755..."
sudo chmod 755 /var/lib/doorphoneserver
sudo chmod 755 /var/lib/doorphoneserver/data

# Crea directory snapshot.
# Il servizio gira come utente 'doorphoneserver' e ffmpeg vi scrive gli snapshot
# della telecamera. Deve appartenere a doorphoneserver con permesso di scrittura,
# altrimenti ffmpeg fallisce con "Could not open file ... Input/output error".
# Percorso default (vedi <dir> in doorphoneserver.xml -> Camera.Snapshot.Dir).
SNAPSHOT_DIR=/home/doorphoneserver/snapshots
echo "Creazione $SNAPSHOT_DIR..."
sudo mkdir -p "$SNAPSHOT_DIR"
sudo chown doorphoneserver:doorphoneserver "$SNAPSHOT_DIR"
sudo chmod 775 "$SNAPSHOT_DIR"

# Crea directory soundfiles (events + audiotest).
# Il pannello legge ~/soundfiles/events e ~/soundfiles/audiotest; senza queste
# cartelle il tab Sound risponde "Cannot read events dir" (non-JSON).
SOUNDS_DIR=/home/doorphoneserver/soundfiles
echo "Creazione $SOUNDS_DIR..."
sudo mkdir -p "$SOUNDS_DIR/events" "$SOUNDS_DIR/audiotest"
# Copia i file di default dal repo (cwd = root del repo), senza sovrascrivere
if [ -d ./soundfiles ]; then
    sudo cp -rn ./soundfiles/. "$SOUNDS_DIR/" 2>/dev/null || true
fi
sudo chown -R doorphoneserver:doorphoneserver "$SOUNDS_DIR"

# Verifica
echo ""
echo "=== Verifica setup ==="
ls -la /var/lib/doorphoneserver/
ls -lad "$SNAPSHOT_DIR"

echo ""
echo "✓ Setup completato!"
echo ""
echo "Directory dati: /var/lib/doorphoneserver/data/"
echo "File che verranno salvati:"
echo "  - alarms.json"
echo "  - audio_calls_history.json"
echo ""
echo "Directory snapshot: $SNAPSHOT_DIR/"
echo "  - snapshot_YYYYMMDD_HHMMSS.jpg (catturati da ffmpeg)"

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

# Verifica
echo ""
echo "=== Verifica setup ==="
ls -la /var/lib/doorphoneserver/

echo ""
echo "✓ Setup completato!"
echo ""
echo "Directory dati: /var/lib/doorphoneserver/data/"
echo "File che verranno salvati:"
echo "  - alarms.json"
echo "  - audio_calls_history.json"

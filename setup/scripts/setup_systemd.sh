#!/bin/bash
# Installa e abilita il servizio systemd doorphoneserver.
# Eseguire come root: sudo bash setup_systemd.sh

set -e

SERVICE_SRC="/home/doorphoneserver/setup/scripts/doorphoneserver.service"
SERVICE_DST="/etc/systemd/system/doorphoneserver.service"

if [ ! -f "$SERVICE_SRC" ]; then
    echo "ERRORE: file $SERVICE_SRC non trovato"
    exit 1
fi

echo "Installazione servizio systemd..."
cp "$SERVICE_SRC" "$SERVICE_DST"
systemctl daemon-reload
systemctl enable doorphoneserver
systemctl start doorphoneserver

echo "Stato servizio:"
systemctl status doorphoneserver --no-pager

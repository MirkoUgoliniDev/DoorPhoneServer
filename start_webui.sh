#!/bin/bash
# Killa eventuali istanze precedenti su porta 8888 e rilancia la Web UI

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PORT=8888

echo "[*] Controllo processi su porta $PORT..."
PIDS=$(lsof -ti tcp:$PORT 2>/dev/null)
if [ -n "$PIDS" ]; then
    echo "[*] Kill processi: $PIDS"
    kill -9 $PIDS
    sleep 1
fi

echo "[*] Avvio Web UI..."
cd "$SCRIPT_DIR"
python3 setup/webui.py

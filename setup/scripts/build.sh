#!/bin/bash

## Per rendere lo script eseguibile: sudo chmod +x setup/scripts/build.sh
## Eseguire come utente pi dalla directory del progetto: cd /home/doorphoneserver && ./setup/scripts/build.sh

PROJECT_ROOT=/home/doorphoneserver
GO=/usr/local/go/bin/go
FINALBIN=$PROJECT_ROOT/bin/doorphoneserver

export TMPDIR=/var/tmp/go-build
mkdir -p "$TMPDIR"
mkdir -p "$PROJECT_ROOT/bin"

echo ""
echo "##########################################################"
echo ">>> Inizio della compilazione di DoorPhoneServer..."
echo "##########################################################"
echo ""

echo "[1/5] $(date '+%H:%M:%S') - Arresto servizio..."
sudo systemctl stop doorphoneserver 2>/dev/null || sudo killall -q -s 15 doorphoneserver 2>/dev/null
sleep 1

echo "[2/5] $(date '+%H:%M:%S') - Pulizia log precedenti..."
sudo rm -f /var/log/doorphoneserver.log

echo "[3/5] $(date '+%H:%M:%S') - Compilazione in corso (può richiedere 1-2 minuti)..."
echo "    → $GO build -buildvcs=false -trimpath -ldflags=\"-s -w\" -o $FINALBIN ./cmd/doorphoneserver"
echo ""

cd "$PROJECT_ROOT"
"$GO" build -v -buildvcs=false -trimpath -ldflags="-s -w" \
    -o "$FINALBIN" \
    ./cmd/doorphoneserver 2>&1
BUILD_EXIT=$?

if [ $BUILD_EXIT -eq 0 ]; then
    echo ""
    echo "    ✓ $(date '+%H:%M:%S') - Compilazione completata con successo!"

    if file "$FINALBIN" | grep -q "ELF.*executable"; then
        echo "    ✓ Binario ELF eseguibile verificato"
    else
        echo "    ✗ ERRORE: Il file compilato non è un eseguibile valido!"
        file "$FINALBIN"
        exit 1
    fi
else
    echo ""
    echo "    ✗ $(date '+%H:%M:%S') - ERRORE durante la compilazione!"
    exit 1
fi

echo "[4/5] $(date '+%H:%M:%S') - Pulizia cache di build..."
"$GO" clean -cache

echo "[5/5] $(date '+%H:%M:%S') - Verifica binario installato..."
if [ -f "$FINALBIN" ]; then
    SIZE=$(du -h "$FINALBIN" | cut -f1)
    echo "    ✓ Binario installato: $SIZE"
else
    echo "    ✗ ERRORE: Binario non trovato in $FINALBIN!"
    exit 1
fi

echo ""
echo "##########################################################"
echo "Finished building DoorPhoneServer"
echo "Binario: $FINALBIN"
echo "Config:  $PROJECT_ROOT/doorphoneserver.xml"
echo "##########################################################"

exit

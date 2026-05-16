#!/bin/bash

## Per rendere lo script eseguibile: sudo chmod +x tkbuild.sh
## Eseguire come utente pi dalla directory del progetto: cd /home/doorphoneserver && ./tkbuild.sh

PROJECT_ROOT=/home/doorphoneserver
GO=/usr/local/go/bin/go
TMPBIN=/tmp/doorphoneserver
FINALBIN=$PROJECT_ROOT/bin/doorphoneserver

export TMPDIR=/var/tmp/go-build
mkdir -p "$TMPDIR"

echo ""
echo "##########################################################"
echo ">>> Inizio della compilazione di DoorPhoneServer..."
echo "##########################################################"
echo ""

echo "[1/6] $(date '+%H:%M:%S') - Rimozione binario temporaneo precedente..."
rm -f "$TMPBIN"

echo "[2/6] $(date '+%H:%M:%S') - Pulizia log precedenti..."
sudo rm -f /var/log/doorphoneserver.log

echo "[3/6] $(date '+%H:%M:%S') - Compilazione in corso (può richiedere 1-2 minuti)..."
echo "    → $GO build -trimpath -ldflags=\"-s -w\" -o $TMPBIN $PROJECT_ROOT/cmd/doorphoneserver/main.go"
echo ""

"$GO" build -v -trimpath -ldflags="-s -w" \
    -o "$TMPBIN" \
    "$PROJECT_ROOT/cmd/doorphoneserver/main.go" 2>&1 | while read line; do
    echo "    $line"
done

BUILD_EXIT=${PIPESTATUS[0]}

if [ $BUILD_EXIT -eq 0 ]; then
    echo ""
    echo "    ✓ $(date '+%H:%M:%S') - Compilazione completata con successo!"

    if file "$TMPBIN" | grep -q "ELF.*executable"; then
        echo "    ✓ Binario ELF eseguibile verificato"
    else
        echo "    ✗ ERRORE: Il file compilato non è un eseguibile valido!"
        file "$TMPBIN"
        exit 1
    fi
else
    echo ""
    echo "    ✗ $(date '+%H:%M:%S') - ERRORE durante la compilazione!"
    exit 1
fi

echo "[4/6] $(date '+%H:%M:%S') - Pulizia cache di build..."
"$GO" clean -cache

echo "[5/6] $(date '+%H:%M:%S') - Arresto servizio e installazione binario..."
sudo systemctl stop doorphoneserver 2>/dev/null || sudo killall -q -s 15 doorphoneserver 2>/dev/null
sleep 2
sudo cp "$TMPBIN" "$FINALBIN"
sudo chmod +x "$FINALBIN"

echo "[6/6] $(date '+%H:%M:%S') - Verifica binario installato..."
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

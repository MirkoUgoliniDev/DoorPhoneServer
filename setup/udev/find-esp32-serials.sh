#!/bin/bash
set -u
shopt -s nullglob   # i glob senza match si espandono a niente, non al pattern letterale
#
# Strumento diagnostico: mostra gli ESP32-S3 attualmente collegati.
#
# NON è più necessario per configurare la regola udev: la regola
# 99-esp32.rules è generica (VID 303a) e non usa numeri seriali.
# Il bridge DoorPhoneServer identifica RFID e RELAY da solo tramite
# il protocollo GET-ROLE/HELLO.
#
# Usa questo script per verificare che i device siano visibili al sistema
# e per annotare i seriali a scopo informativo/diagnostico.

echo "Device Espressif (VID 303a) rilevati:"
echo ""

found=0
for dev in /dev/ttyACM* /dev/ttyUSB*; do
    [ -e "$dev" ] || continue
    idvendor=$(udevadm info -a -n "$dev" 2>/dev/null \
        | grep 'ATTRS{idVendor}' \
        | head -1 \
        | sed 's/.*=="\(.*\)"/\1/')
    [ "$idvendor" = "303a" ] || continue
    serial=$(udevadm info -a -n "$dev" 2>/dev/null \
        | grep 'ATTRS{serial}' \
        | grep -v '0000\|0001' \
        | head -1 \
        | sed 's/.*=="\(.*\)"/\1/')
    idproduct=$(udevadm info -a -n "$dev" 2>/dev/null \
        | grep 'ATTRS{idProduct}' \
        | head -1 \
        | sed 's/.*=="\(.*\)"/\1/')
    echo "  Device  : $dev"
    echo "  Vendor  : $idvendor"
    echo "  Product : $idproduct"
    echo "  Serial  : ${serial:-(non disponibile)}"
    echo ""
    found=$((found + 1))
done

if [ "$found" -eq 0 ]; then
    echo "  Nessun device Espressif trovato."
    echo ""
fi

echo "Note:"
echo "  - La regola /etc/udev/rules.d/99-esp32.rules si applica automaticamente"
echo "    a tutti i device con VID 303a (nessuna configurazione manuale richiesta)."
echo "  - Il bridge DoorPhoneServer invia GET-ROLE\\n a ciascuna porta ttyACM*"
echo "    e legge la risposta HELLO RFID / HELLO RELAY per assegnare i ruoli."

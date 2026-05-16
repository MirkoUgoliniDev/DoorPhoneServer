#!/bin/bash
# Aggiorna SIZE in /etc/log2ram.conf e riavvia log2ram.
# Uso: doorphoneserver-log2ram-setsize.sh <MB>
SIZE_MB="$1"
if [[ -z "$SIZE_MB" || "$SIZE_MB" -lt 64 || "$SIZE_MB" -gt 512 ]]; then
    echo "Errore: valore non valido (64-512 MB)" >&2
    exit 1
fi
sed -i "s/^SIZE=.*/SIZE=${SIZE_MB}M/" /etc/log2ram.conf
systemctl restart log2ram
echo "SIZE impostato a ${SIZE_MB}M"

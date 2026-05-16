#!/bin/bash
# Installa e configura mumble-server (Murmur) per doorphoneserver.
# Eseguire come root: sudo bash setup_mumble.sh
#
# Configurazione applicata:
#   port=64738  (default Mumble)
#   timeout=15
#   bandwidth=72000
#   serverpassword = da variabile d'ambiente MUMBLE_PASSWORD (o vuoto)

# Niente set -e: ogni sezione è indipendente, un errore non blocca le altre
echo "=== Setup mumble-server ==="

# Installa se non presente
if ! dpkg -l mumble-server &>/dev/null; then
    echo "Installazione mumble-server..."
    apt-get install -y mumble-server || { echo "  ✗ installazione mumble-server fallita"; exit 1; }
fi

# Password server: usa variabile d'ambiente (passata dal wizard),
# NON usare read interattivo (lo script gira non-interattivo via subprocess)
MUMBLE_PASSWORD="${MUMBLE_PASSWORD:-}"

# Scrivi configurazione
cat > /etc/mumble-server.ini << CONF
database=/var/lib/mumble-server/mumble-server.sqlite
logfile=/var/log/mumble-server/mumble-server.log
pidfile=/run/mumble-server/mumble-server.pid
welcometext="<br />Welcome to DoorPhoneServer"
port=64738
serverpassword=${MUMBLE_PASSWORD}
bandwidth=72000
timeout=15
CONF
[ $? -eq 0 ] && echo "  ✓ mumble-server.ini scritto" || echo "  ✗ scrittura mumble-server.ini fallita"

# Abilita e avvia
systemctl enable mumble-server 2>/dev/null && echo "  ✓ mumble-server abilitato"
systemctl restart mumble-server && echo "  ✓ mumble-server avviato" || echo "  ✗ restart mumble-server fallito"

echo "mumble-server configurato su porta 64738"
systemctl is-active mumble-server && echo "  stato: RUNNING" || echo "  stato: non attivo"

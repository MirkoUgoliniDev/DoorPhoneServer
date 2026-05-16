#!/bin/bash
# Installa e configura mumble-server (Murmur) per doorphoneserver.
# Eseguire come root: sudo bash setup_mumble.sh
#
# Configurazione applicata:
#   port=64738  (default Mumble)
#   timeout=15
#   bandwidth=72000
#   welcometext = messaggio di benvenuto
#   serverpassword = impostato da variabile o prompt interattivo

set -e

echo "=== Setup mumble-server ==="

# Installa se non presente
if ! dpkg -l mumble-server &>/dev/null; then
    echo "Installazione mumble-server..."
    apt-get install -y mumble-server
fi

# Password server — usa variabile d'ambiente o chiede
if [ -z "$MUMBLE_PASSWORD" ]; then
    read -rsp "Password server Mumble (lascia vuoto per nessuna): " MUMBLE_PASSWORD
    echo
fi

# Scrivi configurazione
cat > /etc/mumble-server.ini << CONF
database=/var/lib/mumble-server/mumble-server.sqlite
logfile=/var/log/mumble-server/mumble-server.log
pidfile=/run/mumble-server/mumble-server.pid
welcometext="<br />Welcome to this server"
port=64738
serverpassword=$MUMBLE_PASSWORD
bandwidth=72000
timeout=15
CONF

# Abilita e avvia
systemctl enable mumble-server
systemctl restart mumble-server

echo "mumble-server configurato su porta 64738"
systemctl status mumble-server --no-pager | grep Active

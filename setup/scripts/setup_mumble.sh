#!/bin/bash
# Installa e configura mumble-server (Murmur) per doorphoneserver.
# Eseguire come root: sudo bash setup_mumble.sh
#
# Configurazione applicata:
#   port=64738  (default Mumble)
#   timeout=15
#   bandwidth=72000
#   serverpassword = da MUMBLE_PASSWORD_FILE (path al file con la password)
#                    o da MUMBLE_PASSWORD (fallback legacy)
#
# Argomenti:
#   $1  path file con la password Mumble (opzionale)
#   $2  path cert.pem del server (OPZIONALE — pinning del certificato)
#   $3  path key.pem del server  (OPZIONALE — usato solo se presente anche $2)
# Se $2/$3 mancano, Murmur usa il certificato auto-generato (comportamento default).

# Niente set -e: ogni sezione è indipendente, un errore non blocca le altre
echo "=== Setup mumble-server ==="

# Installa se non presente
if ! dpkg -l mumble-server &>/dev/null; then
    echo "Installazione mumble-server..."
    apt-get install -y mumble-server || { echo "  ✗ installazione mumble-server fallita"; exit 1; }
fi

# Legge la password da file passato come $1 (evita env_reset di sudo)
# o da variabile MUMBLE_PASSWORD (fallback legacy / invocazione manuale)
if [ -n "${1:-}" ] && [ -f "$1" ]; then
    MUMBLE_PASSWORD="$(cat "$1")"
else
    MUMBLE_PASSWORD="${MUMBLE_PASSWORD:-}"
fi

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

# Certificato server fornito (OPZIONALE): $2 = cert.pem, $3 = key.pem.
# Se entrambi presenti e validi, li installa e "pinna" il certificato così i
# tablet non devono riaccettarlo dopo una migrazione. Se mancano, Murmur usa il
# certificato auto-generato (comportamento default).
CERT_SRC="${2:-}"
KEY_SRC="${3:-}"
if [ -n "$CERT_SRC" ] && [ -f "$CERT_SRC" ] && [ -n "$KEY_SRC" ] && [ -f "$KEY_SRC" ]; then
    if grep -q "BEGIN" "$CERT_SRC" && grep -q "BEGIN" "$KEY_SRC"; then
        echo "  Installazione certificato server fornito (pinning)..."
        mkdir -p /etc/mumble-server
        cp "$CERT_SRC" /etc/mumble-server/cert.pem
        cp "$KEY_SRC"  /etc/mumble-server/key.pem
        chown mumble-server:mumble-server /etc/mumble-server/cert.pem /etc/mumble-server/key.pem 2>/dev/null
        chmod 644 /etc/mumble-server/cert.pem
        chmod 600 /etc/mumble-server/key.pem
        # Aggiungi sslCert/sslKey all'ini (l'heredoc sopra non le include)
        grep -q '^sslCert=' /etc/mumble-server.ini || echo 'sslCert=/etc/mumble-server/cert.pem' >> /etc/mumble-server.ini
        grep -q '^sslKey='  /etc/mumble-server.ini || echo 'sslKey=/etc/mumble-server/key.pem'  >> /etc/mumble-server.ini
        echo "  ✓ certificato server pinned (sslCert/sslKey)"
    else
        echo "  ⚠ cert/key forniti non sembrano PEM validi — uso certificato auto-generato"
    fi
fi

# Preservazione del certificato esistente (idempotenza): se NON è stato fornito un
# cert e Murmur ne ha già uno auto-generato nel DB (re-run su un server già attivo),
# lo estraiamo e lo pinniamo. Così ri-eseguire il wizard NON cambia il certificato
# e i tablet, che lo hanno "pinnato", non perdono la fiducia. Su una prima
# installazione il DB non ha ancora un cert: si salta e Murmur ne genera uno.
MUMBLE_DB=/var/lib/mumble-server/mumble-server.sqlite
if [ -z "$CERT_SRC" ] && ! grep -q '^sslCert=' /etc/mumble-server.ini \
   && [ -f "$MUMBLE_DB" ] && command -v sqlite3 >/dev/null 2>&1; then
    EXIST_CERT="$(sqlite3 "$MUMBLE_DB" "SELECT value FROM config WHERE key='certificate';" 2>/dev/null)"
    EXIST_KEY="$(sqlite3 "$MUMBLE_DB" "SELECT value FROM config WHERE key='key';" 2>/dev/null)"
    if printf '%s' "$EXIST_CERT" | grep -q "BEGIN" && printf '%s' "$EXIST_KEY" | grep -q "BEGIN"; then
        echo "  Certificato esistente nel DB: lo preservo per non cambiarlo ai tablet"
        mkdir -p /etc/mumble-server
        printf '%s\n' "$EXIST_CERT" > /etc/mumble-server/cert.pem
        printf '%s\n' "$EXIST_KEY"  > /etc/mumble-server/key.pem
        chown mumble-server:mumble-server /etc/mumble-server/cert.pem /etc/mumble-server/key.pem 2>/dev/null
        chmod 644 /etc/mumble-server/cert.pem
        chmod 600 /etc/mumble-server/key.pem
        echo 'sslCert=/etc/mumble-server/cert.pem' >> /etc/mumble-server.ini
        echo 'sslKey=/etc/mumble-server/key.pem'  >> /etc/mumble-server.ini
        echo "  ✓ certificato esistente preservato (sslCert/sslKey)"
    fi
fi

# Abilita e avvia
systemctl enable mumble-server 2>/dev/null && echo "  ✓ mumble-server abilitato"
systemctl restart mumble-server && echo "  ✓ mumble-server avviato" || echo "  ✗ restart mumble-server fallito"

echo "mumble-server configurato su porta 64738"
systemctl is-active mumble-server && echo "  stato: RUNNING" || echo "  stato: non attivo"

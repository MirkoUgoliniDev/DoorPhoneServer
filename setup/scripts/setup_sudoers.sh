#!/bin/bash
# Installa le regole sudoers per l'utente pi, necessarie per:
#   - il build script (setup/scripts/build.sh)
#   - i bottoni VSCode (Build / Restart Service / Status)
#
# Permessi concessi senza password:
#   - rm /var/log/doorphoneserver.log  (il log è di root)
#   - systemctl stop/start/restart doorphoneserver
#   - killall doorphoneserver          (fallback nel build script)
#
# Eseguire come root: sudo bash setup/scripts/setup_sudoers.sh

set -e

SUDOERS_FILE=/etc/sudoers.d/doorphoneserver
TMP_FILE=/tmp/doorphoneserver-sudoers

cat > "$TMP_FILE" << 'SUDOERS'
# DoorPhoneServer — passwordless sudo for user pi
# Build: remove old log (owned by root when service runs as root)
pi ALL=(ALL) NOPASSWD: /usr/bin/rm -f /var/log/doorphoneserver.log
# Service management (build script + VSCode action buttons)
pi ALL=(ALL) NOPASSWD: /usr/bin/systemctl stop doorphoneserver
pi ALL=(ALL) NOPASSWD: /usr/bin/systemctl start doorphoneserver
pi ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart doorphoneserver
pi ALL=(ALL) NOPASSWD: /usr/bin/killall -q -s 15 doorphoneserver
SUDOERS

echo "Validazione sintassi sudoers..."
visudo -c -f "$TMP_FILE"

echo "Installazione $SUDOERS_FILE..."
cp "$TMP_FILE" "$SUDOERS_FILE"
chmod 440 "$SUDOERS_FILE"
rm -f "$TMP_FILE"

echo "✓ Regole sudoers installate. Il build non chiederà più la password."

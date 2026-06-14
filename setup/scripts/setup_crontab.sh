#!/bin/bash
# Installa i job crontab di sistema per doorphoneserver.
#
# Uso:
#   come root:            bash setup_crontab.sh [utente]   (default: doorphoneserver)
#   come doorphoneserver: bash setup_crontab.sh            (installa nella propria crontab)
#
# Job installati:
#   59 23 * * *  Riavvio Pi a mezzanotte meno 1
#    0  6 * * *  Riavvio Pi alle 6:00
#    5  0 * * *  Riavvio tablet dopo il riavvio notturno
#    5  6 * * *  Riavvio tablet dopo il riavvio mattutino
#    0  * * * *  Restart mumble-server ogni ora (disabilitato, abilitare se necessario)
#
# NOTA: usa "sudo systemctl reboot" — coperto dal sudoers doorphoneserver-panel
#       (/usr/bin/systemctl NOPASSWD), NON shutdown che richiederebbe sudoers separato.
set -euo pipefail

TARGET_USER="${1:-doorphoneserver}"
SCRIPTS_DIR="/home/doorphoneserver/setup/scripts"

CRON_CONTENT="$(cat << CRON
# DoorPhoneServer — crontab gestito da setup_crontab.sh
# Riavvii programmati del Raspberry Pi
59 23 * * * sudo systemctl reboot
0 6 * * * sudo systemctl reboot

# Riavvio tablet dopo ogni riavvio Pi (con ritardo per dare tempo al boot)
5 0 * * * sudo bash $SCRIPTS_DIR/restart_tablet.sh
5 6 * * * sudo bash $SCRIPTS_DIR/restart_tablet.sh

# Restart mumble-server ogni ora — abilitare se necessario
# 0 * * * * sudo systemctl restart mumble-server
CRON
)"

if [ "$(id -u)" -eq 0 ]; then
    # Eseguito come root: scrivi SEMPRE nella crontab dell'utente target,
    # mai in quella di root (è il pannello, che gira come $TARGET_USER, a
    # leggerla). Senza '-u' i job finirebbero nella crontab sbagliata e il
    # pannello mostrerebbe "Nessun job crontab trovato".
    printf '%s\n' "$CRON_CONTENT" | crontab -u "$TARGET_USER" -
    echo "Crontab installato per $TARGET_USER:"
    crontab -u "$TARGET_USER" -l
else
    # Eseguito come utente normale: installa nella propria crontab.
    printf '%s\n' "$CRON_CONTENT" | crontab -
    echo "Crontab installato per $(id -un):"
    crontab -l
fi

#!/bin/bash
# Installa i job crontab di sistema per doorphoneserver.
# Eseguire come utente doorphoneserver (o con: sudo -u doorphoneserver bash setup_crontab.sh)
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

SCRIPTS_DIR="/home/doorphoneserver/setup/scripts"

crontab - << CRON
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

echo "Crontab installato:"
crontab -l

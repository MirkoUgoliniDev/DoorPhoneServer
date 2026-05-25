#!/bin/bash
# UndoInstall.sh — Rimuove tutto ciò che il wizard DoorPhoneServer ha installato
# Lascia intatto il repository git e i tool di sistema preesistenti.

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

step() { echo -e "\n${CYAN}▶ $1${NC}"; }
ok()   { echo -e "  ${GREEN}✓ $1${NC}"; }
skip() { echo -e "  ${YELLOW}— $1 (skip)${NC}"; }

echo -e "${YELLOW}"
echo "╔══════════════════════════════════════════════════════╗"
echo "║   DoorPhoneServer — UndoInstall                     ║"
echo "║   Rimuove componenti installati dal wizard           ║"
echo "╚══════════════════════════════════════════════════════╝"
echo -e "${NC}"
echo "Il repository git in /home/doorphoneserver/ verrà mantenuto."
echo "Verranno rimossi: servizi systemd, utente, Go, log2ram,"
echo "  file generati (.env, certs, gocode, bin), pacchetti APT."
echo ""
read -p "Continuare? [s/N] " -n 1 -r; echo
[[ ! $REPLY =~ ^[SsYy]$ ]] && echo "Annullato." && exit 0

TK_USER="doorphoneserver"
HOME_DIR="/home/${TK_USER}"

# ── 1. Servizio systemd doorphoneserver ──────────────────────────────────────
step "1/10 Servizio systemd doorphoneserver"
if systemctl is-active --quiet doorphoneserver 2>/dev/null; then
    sudo systemctl stop doorphoneserver && ok "Servizio fermato"
fi
if systemctl is-enabled --quiet doorphoneserver 2>/dev/null; then
    sudo systemctl disable doorphoneserver && ok "Servizio disabilitato"
fi
sudo rm -f /etc/systemd/system/doorphoneserver.service
sudo systemctl daemon-reload && ok "daemon-reload"

# ── 2. Mumble Server ─────────────────────────────────────────────────────────
step "2/10 Mumble Server"
if systemctl is-active --quiet mumble-server 2>/dev/null; then
    sudo systemctl stop mumble-server && ok "mumble-server fermato"
fi
if systemctl is-enabled --quiet mumble-server 2>/dev/null; then
    sudo systemctl disable mumble-server && ok "mumble-server disabilitato"
fi

# ── 3. Log2Ram ───────────────────────────────────────────────────────────────
step "3/10 Log2Ram"
if command -v log2ram &>/dev/null; then
    sudo systemctl stop log2ram 2>/dev/null && ok "log2ram fermato" || true
    if dpkg -l log2ram 2>/dev/null | grep -q "^ii"; then
        sudo apt-get purge -y log2ram 2>/dev/null && ok "log2ram rimosso (dpkg)"
    else
        sudo systemctl disable log2ram 2>/dev/null || true
        sudo rm -f /usr/local/bin/log2ram /etc/log2ram.conf
        sudo rm -f /etc/systemd/system/log2ram.service
        sudo systemctl daemon-reload
        ok "log2ram rimosso (manuale)"
    fi
    sudo rm -f /etc/sudoers.d/doorphoneserver-log2ram
else
    skip "log2ram non trovato"
fi

# ── 4. Go language ───────────────────────────────────────────────────────────
step "4/10 Go language"
if [ -d /usr/local/go ]; then
    sudo rm -rf /usr/local/go && ok "Rimosso /usr/local/go"
    sudo sed -i '/\/usr\/local\/go\/bin/d' /etc/environment 2>/dev/null || true
    ok "PATH Go rimosso da /etc/environment"
else
    skip "Go non trovato in /usr/local/go"
fi

# ── 5. Utente di sistema ─────────────────────────────────────────────────────
step "5/10 Utente '${TK_USER}' (home mantenuta)"
if id "${TK_USER}" &>/dev/null; then
    sudo userdel "${TK_USER}" 2>/dev/null && ok "Utente rimosso (home mantenuta)"
else
    skip "Utente non esistente"
fi
if getent group "${TK_USER}" &>/dev/null; then
    sudo groupdel "${TK_USER}" 2>/dev/null && ok "Gruppo rimosso" || true
fi
# Ripristina ownership home al'utente pi per poter scrivere normalmente
sudo chown -R pi:pi "${HOME_DIR}" 2>/dev/null && ok "Ownership ${HOME_DIR} → pi:pi" || true

# ── 6. Sudoers ───────────────────────────────────────────────────────────────
step "6/10 File sudoers del wizard"
for f in doorphoneserver doorphoneserver-panel doorphoneserver-log2ram; do
    [ -f "/etc/sudoers.d/${f}" ] && sudo rm -f "/etc/sudoers.d/${f}" && ok "Rimosso sudoers/${f}" || true
done

# ── 7. Configurazioni di sistema ─────────────────────────────────────────────
step "7/10 Configurazioni audio/boot"
[ -f /etc/asound.conf ]        && sudo rm -f /etc/asound.conf        && ok "Rimosso /etc/asound.conf"  || true
[ -f /etc/openal/alsoft.conf ] && sudo rm -f /etc/openal/alsoft.conf && ok "Rimosso alsoft.conf"       || true

# ── 8. File generati nella home ──────────────────────────────────────────────
step "8/10 File generati in ${HOME_DIR}"
echo "  Verranno rimossi: .env, certificati TLS, gocode/, bin/, preferences/, doorphoneserver.xml"
read -p "  Confermi rimozione? [s/N] " -n 1 -r; echo
if [[ $REPLY =~ ^[SsYy]$ ]]; then
    sudo rm -f  "${HOME_DIR}/.env"
    sudo rm -f  "${HOME_DIR}/cert.pem"
    sudo rm -f  "${HOME_DIR}/nopasskey.pem"
    sudo rm -f  "${HOME_DIR}/mumble.pem"
    sudo rm -f  "${HOME_DIR}/doorphoneserver.xml"
    sudo rm -rf "${HOME_DIR}/gocode"
    sudo rm -rf "${HOME_DIR}/bin"
    sudo rm -rf "${HOME_DIR}/preferences"
    ok "File generati rimossi"
else
    skip "File generati mantenuti"
fi

# ── 9. Crontab ───────────────────────────────────────────────────────────────
step "9/10 Crontab"
crontab -l 2>/dev/null | grep -v "doorphoneserver\|restart_tablet" | crontab - 2>/dev/null \
    && ok "Voci wizard rimosse dal crontab" || skip "Nessuna voce da rimuovere"

# ── 10. Pacchetti APT ────────────────────────────────────────────────────────
step "10/10 Pacchetti APT del wizard"
echo "  (git, curl, wget, build-essential, openssl, alsa-utils, rsync NON vengono toccati)"
echo ""

WIZARD_PKGS=(
    libopenal-dev libopus-dev libasound2-dev
    mumble-server
    python3-flask python3-tk python3-dotenv
    lz4 mplayer screen ffmpeg
)

TO_REMOVE=()
for pkg in "${WIZARD_PKGS[@]}"; do
    if dpkg -l "$pkg" 2>/dev/null | grep -q "^ii"; then
        TO_REMOVE+=("$pkg")
        echo "    - $pkg"
    fi
done

if [ ${#TO_REMOVE[@]} -gt 0 ]; then
    read -p "  Rimuovere ${#TO_REMOVE[@]} pacchetti + autoremove? [s/N] " -n 1 -r; echo
    if [[ $REPLY =~ ^[SsYy]$ ]]; then
        sudo apt-get purge -y "${TO_REMOVE[@]}" 2>/dev/null || true
        sudo apt-get autoremove -y 2>/dev/null || true
        ok "Pacchetti rimossi"
    else
        skip "Pacchetti mantenuti"
    fi
else
    skip "Nessun pacchetto wizard trovato installato"
fi

# ── Fine ─────────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}╔══════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║   Undo completato!                               ║${NC}"
echo -e "${GREEN}╚══════════════════════════════════════════════════╝${NC}"
echo ""
echo "  Repository git mantenuto in: ${HOME_DIR}/"
echo "  Per una nuova installazione:"
echo "    bash ${HOME_DIR}/start_webui.sh"
echo ""

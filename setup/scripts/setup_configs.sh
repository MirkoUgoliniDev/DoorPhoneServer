#!/bin/bash
# Installa i file di configurazione di sistema per doorphoneserver.
# Eseguire come root: sudo bash setup_configs.sh
#
# Configurazioni applicate:
#   /etc/openal/alsoft.conf — OpenAL: hq-mode off, nfc on
#   /etc/modprobe.d/        — Blacklist adattatori WiFi USB concorrenti (8192cu, rtl8xxxu)
#   /boot/firmware/config.txt — Boot Pi4: BT off, headless, gpu_mem=16
#       (audio onboard LASCIATO attivo: evita la rinumerazione delle schede ALSA)
#
# NOTA: /etc/asound.conf è gestito dallo step audio del wizard (rilevamento automatico
#       scheda) e NON deve essere sovrascritto qui.

# Niente set -e: ogni sezione è indipendente, un errore non blocca le altre
echo "=== Installazione configurazioni di sistema ==="
ERRORS=0

# --- /etc/openal/alsoft.conf (solo valori non-default rilevanti) ---
echo "[1/3] /etc/openal/alsoft.conf"
mkdir -p /etc/openal
cat > /etc/openal/alsoft.conf << 'EOF'
[decoder]
hq-mode = false
distance-comp = true
nfc = true
nfc-ref-delay =
EOF
[ $? -eq 0 ] && echo "  ✓ alsoft.conf" || { echo "  ✗ alsoft.conf fallito"; ERRORS=$((ERRORS+1)); }

# --- /etc/modprobe.d/ blacklist WiFi USB ---
echo "[2/3] /etc/modprobe.d/blacklist WiFi USB"
echo "blacklist 8192cu"   > /etc/modprobe.d/blacklist-8192cu.conf
echo "blacklist rtl8xxxu" > /etc/modprobe.d/blacklist-rtl8xxxu.conf
update-initramfs -u 2>/dev/null && echo "  ✓ initramfs aggiornato" || echo "  ⚠ update-initramfs fallito (non bloccante)"

# --- /boot/firmware/config.txt (impostazioni chiave Pi4) ---
echo "[3/3] boot/config.txt"
BOOT_CFG="/boot/firmware/config.txt"
[ -f "$BOOT_CFG" ] || BOOT_CFG="/boot/config.txt"
if [ -f "$BOOT_CFG" ]; then
    cp "$BOOT_CFG" "$BOOT_CFG.bak.$(date +%Y%m%d)"
    # NB: l'audio onboard (bcm2835 + vc4-hdmi) viene lasciato ATTIVO di
    # proposito. Disabilitarlo cambierebbe la numerazione delle schede ALSA tra
    # il momento del setup e dopo il reboot (la USB si rinumera), rompendo la
    # scheda/controllo scelti dall'operatore. Con tutte le schede presenti la
    # numerazione resta stabile (WYSIWYG); asound.conf instrada comunque tutto
    # sulla scheda USB selezionata, per nome stabile. Le onboard restano inutilizzate.
    # Bluetooth off
    grep -q "dtoverlay=disable-bt"    "$BOOT_CFG" || echo "dtoverlay=disable-bt"    >> "$BOOT_CFG"
    # GPU memoria minima (headless)
    grep -q "gpu_mem="                "$BOOT_CFG" || echo "gpu_mem=16"              >> "$BOOT_CFG"
    # Video/display off — sistema headless, nessun monitor
    grep -q "camera_auto_detect"      "$BOOT_CFG" || echo "camera_auto_detect=0"   >> "$BOOT_CFG"
    grep -q "display_auto_detect"     "$BOOT_CFG" || echo "display_auto_detect=0"  >> "$BOOT_CFG"
    grep -q "hdmi_blanking"           "$BOOT_CFG" || echo "hdmi_blanking=2"        >> "$BOOT_CFG"
    grep -q "hdmi_ignore_hotplug"     "$BOOT_CFG" || echo "hdmi_ignore_hotplug=1"  >> "$BOOT_CFG"
    # Driver video vc4 lasciato ATTIVO: disabilitarlo toglierebbe le schede
    # audio vc4-hdmi, alterando la numerazione ALSA (vedi nota sopra). Riduce
    # comunque il framebuffer a 1 (headless, nessun display).
    sed -i 's/^max_framebuffers=.*/max_framebuffers=1/' "$BOOT_CFG"
    # Sopprimi avvisi undervoltage
    grep -q "avoid_warnings"          "$BOOT_CFG" || echo "avoid_warnings=1"       >> "$BOOT_CFG"
    echo "  ✓ boot/config.txt aggiornato"
else
    echo "  ⚠ boot/config.txt non trovato, skip"
    ERRORS=$((ERRORS+1))
fi

echo ""
if [ $ERRORS -eq 0 ]; then
    echo "✓ Configurazioni installate."
else
    echo "⚠ Installazione completata con $ERRORS errori (vedere sopra)."
fi
echo "ATTENZIONE: riavviare il Pi per applicare le modifiche a boot/config.txt"

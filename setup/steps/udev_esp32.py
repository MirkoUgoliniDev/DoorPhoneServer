"""Passo — Regola udev generica per tutti gli ESP32-S3 (Espressif VID 303a).

Installa una singola regola che mette nel gruppo dialout qualsiasi device
Espressif collegato via USB CDC. Il software DoorPhoneServer (bridge Go)
identifica autonomamente quale porta è RFID e quale è RELAY tramite il
protocollo GET-ROLE/HELLO — non servono né seriali specifici né symlink.
"""

import glob
from pathlib import Path
from lib.step_base import Step, Status

UDEV_RULE_PATH = Path("/etc/udev/rules.d/99-esp32.rules")

_RULE_CONTENT = """\
# /etc/udev/rules.d/99-esp32.rules
# Accesso gruppo dialout per ESP32-S3 (Espressif USB CDC, VID 303a).
# Il software DoorPhoneServer identifica automaticamente il ruolo
# di ciascun device tramite il protocollo GET-ROLE/HELLO.
SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", MODE="0660", GROUP="dialout"

# NOTA: copre solo l'USB CDC nativo dell'ESP32-S3 (/dev/ttyACM*). Per board con
# chip USB-UART esterno (CP2102 10c4, CH340 1a86, FTDI 0403) il device appare
# come /dev/ttyUSB* con VID diverso: decommenta la riga corrispondente.
#SUBSYSTEM=="tty", ATTRS{idVendor}=="10c4", MODE="0660", GROUP="dialout"
#SUBSYSTEM=="tty", ATTRS{idVendor}=="1a86", MODE="0660", GROUP="dialout"
#SUBSYSTEM=="tty", ATTRS{idVendor}=="0403", MODE="0660", GROUP="dialout"
"""


class StepUdevESP32(Step):
    def __init__(self):
        super().__init__(
            "Regola udev ESP32-S3",
            "Installa 99-esp32.rules: accesso dialout per tutti i device Espressif (VID 303a)",
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if runner.dry_run:
            runner.log(f"  [DRY-RUN] Scrive {UDEV_RULE_PATH}")
            runner.log("  [DRY-RUN] udevadm control --reload-rules")
            runner.log("  [DRY-RUN] udevadm trigger --subsystem-match=tty")
            self._set_status(Status.DONE)
            return True

        # ── Scrivi la regola generica ────────────────────────────────────────
        if not runner.write(UDEV_RULE_PATH, _RULE_CONTENT, sudo=True):
            runner.log("  Impossibile scrivere la regola udev")
            self._set_status(Status.FAILED)
            return False

        runner.run(["chmod", "644", str(UDEV_RULE_PATH)], sudo=True)

        # Reload/trigger non sono critici: se falliscono, la regola entra in
        # vigore al prossimo reboot. Segnaliamo solo un warning senza fallire.
        reload_ok, _ = runner.run(["udevadm", "control", "--reload-rules"], sudo=True)
        trigger_ok, _ = runner.run(["udevadm", "trigger", "--subsystem-match=tty"], sudo=True)
        if not (reload_ok and trigger_ok):
            runner.log("  ⚠ udevadm reload/trigger non riuscito — la regola sarà attiva dopo un reboot")

        runner.log(f"  Regola installata: {UDEV_RULE_PATH}")

        # ── Device già presenti (informativo) ────────────────────────────────
        present = sorted(glob.glob("/dev/ttyACM*"))
        if present:
            runner.log(f"  Device ttyACM* presenti: {', '.join(present)}")
            runner.log("  Il bridge Go assegnerà i ruoli RFID/RELAY automaticamente.")
        else:
            runner.log("  Nessun device ttyACM* collegato — la regola scatterà al primo collegamento.")

        self._set_status(Status.DONE)
        return True

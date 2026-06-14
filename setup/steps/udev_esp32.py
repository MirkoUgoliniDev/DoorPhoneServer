"""Passo — Regola udev per l'ESP32-S3 (symlink stabile /dev/esp32)."""

from pathlib import Path
from lib.step_base import Step, Status

# Path del device che il binario apre: usb_bridge.go -> usbSerialPath = "/dev/esp32".
# La regola udev DEVE creare proprio questo symlink, altrimenti il bridge non
# trova la seriale e l'ESP32-S3 risulta sempre disconnesso.
UDEV_RULE_PATH = Path("/etc/udev/rules.d/99-esp32.rules")

# ESP32-S3 con USB nativo (TinyUSB CDC): VID:PID = 303a:1001. Si presenta come
# /dev/ttyACM*; la regola lo aggancia per VID/PID e crea /dev/esp32 nel gruppo
# dialout (l'utente doorphoneserver ne fa parte) con permessi rw di gruppo.
UDEV_RULE_CONTENT = (
    "# /etc/udev/rules.d/99-esp32.rules\n"
    "# Symlink stabile /dev/esp32 per l'ESP32-S3 (USB nativo CDC, 303a:1001).\n"
    "# Generato dal setup DoorPhoneServer — vedi usb_bridge.go (usbSerialPath).\n"
    'SUBSYSTEM=="tty", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", '
    'SYMLINK+="esp32", MODE="0660", GROUP="dialout"\n'
)


class StepUdevESP32(Step):
    def __init__(self):
        super().__init__(
            "Regola udev ESP32-S3",
            "Installa 99-esp32.rules: symlink stabile /dev/esp32 per la seriale USB",
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if runner.dry_run:
            runner.log(f"  [DRY-RUN] scrive {UDEV_RULE_PATH}")
            runner.log("  [DRY-RUN] udevadm control --reload-rules && udevadm trigger")
            self._set_status(Status.DONE)
            return True

        runner.log(f"  Scrittura regola udev in {UDEV_RULE_PATH}...")
        if not runner.write(UDEV_RULE_PATH, UDEV_RULE_CONTENT, sudo=True):
            runner.log("  ✗ Impossibile scrivere la regola udev")
            self._set_status(Status.FAILED)
            return False
        runner.run(["chmod", "644", str(UDEV_RULE_PATH)], sudo=True)

        # Ricarica le regole e applica: se l'ESP32-S3 è già collegato, /dev/esp32
        # compare subito; altrimenti la regola scatterà al prossimo collegamento.
        runner.run(["udevadm", "control", "--reload-rules"], sudo=True)
        runner.run(["udevadm", "trigger", "--subsystem-match=tty"], sudo=True)

        if Path("/dev/esp32").exists():
            runner.log("  ✓ /dev/esp32 presente (ESP32-S3 collegato)")
        else:
            runner.log(
                "  ✓ Regola installata — /dev/esp32 comparirà al collegamento "
                "dell'ESP32-S3 (ora non rilevato)."
            )

        self._set_status(Status.DONE)
        return True

"""Passo 8 — Configura boot Raspberry Pi e configurazioni di sistema."""

from lib.step_base import Step, Status
from lib.constants import REPO_ROOT


class StepBootConfig(Step):
    def __init__(self):
        super().__init__(
            "Config Boot RPi",
            "Esegue setup_configs.sh: OpenAL, blacklist WiFi, boot/config.txt (BT off, headless)"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        script = REPO_ROOT / "setup" / "scripts" / "setup_configs.sh"
        if not script.exists():
            runner.log(f"  ✗ setup_configs.sh non trovato in {script}")
            self._set_status(Status.FAILED)
            return False

        ok, _ = runner.run(["bash", str(script)], sudo=True)
        if not ok and not runner.dry_run:
            runner.log("  ✗ setup_configs.sh fallito")
            self._set_status(Status.FAILED)
            return False

        self._set_status(Status.DONE)
        return True

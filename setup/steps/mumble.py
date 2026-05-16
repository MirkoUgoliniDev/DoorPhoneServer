"""Passo 7 — Configura e avvia Mumble Server."""

import subprocess
import time
from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import REPO_ROOT


class StepMumbleServer(Step):
    def __init__(self):
        super().__init__(
            "Mumble Server",
            "Copia mumble-server.ini e abilita il servizio Murmur"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        script = REPO_ROOT / "setup" / "scripts" / "setup_mumble.sh"
        if script.exists():
            runner.run(["bash", str(script)], sudo=True)
        else:
            runner.log("  ⚠ setup_mumble.sh non trovato, uso configurazione default")

        runner.run(["systemctl", "enable", "mumble-server"], sudo=True)
        runner.run(["systemctl", "start",  "mumble-server"], sudo=True)

        if not runner.dry_run:
            for _ in range(15):
                r = subprocess.run(
                    ["systemctl", "is-active", "--quiet", "mumble-server"]
                ).returncode == 0
                if r:
                    break
                time.sleep(1)

        self._set_status(Status.DONE)
        return True

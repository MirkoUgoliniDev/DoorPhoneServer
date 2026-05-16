"""Passo 13 — Installa code-server (opzionale)."""

from lib.step_base import Step, Status
from lib.constants import TK_USER


class StepCodeServer(Step):
    def __init__(self):
        super().__init__(
            "VSCode Server (opzionale)",
            "Installa code-server per accedere a VSCode dal browser sulla LAN",
            optional=True
        )

    def execute(self, runner, sysinfo, config):
        if not config.get("install_codeserver", False):
            runner.log("  Saltato per scelta utente")
            self._set_status(Status.SKIPPED)
            return True

        self._set_status(Status.RUNNING)

        if not runner.check_disk_space(sysinfo, needed_gb=0.6):
            self._set_status(Status.FAILED)
            return False

        runner.log("  Download e installazione code-server...")
        ok, _ = runner.shell(
            "curl -fsSL https://code-server.dev/install.sh | sh",
            sudo=True,
            retries=2
        )
        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        runner.run(
            ["systemctl", "enable", "--now", f"code-server@{TK_USER}"],
            sudo=True
        )
        self._set_status(Status.DONE)
        return True

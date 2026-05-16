"""Passo 11 — Servizio systemd doorphoneserver e crontab."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER, GOBIN, REPO_ROOT


class StepSystemdService(Step):
    def __init__(self):
        super().__init__(
            "Servizio Systemd",
            "Installa doorphoneserver.service, sudoers per il web panel e crontab"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        # --- Service file (da setup/scripts/) ---
        service_src = REPO_ROOT / "setup" / "scripts" / "doorphoneserver.service"
        dst_service  = Path("/etc/systemd/system/doorphoneserver.service")

        if not service_src.exists():
            runner.log(f"  ✗ {service_src} non trovato")
            self._set_status(Status.FAILED)
            return False

        runner.copy(service_src, dst_service, sudo=True)

        if not runner.dry_run and not (GOBIN / "doorphoneserver").exists():
            runner.log("  ⚠ Binario non trovato in GOBIN — il service potrebbe non partire")

        # --- Sudoers per web panel ---
        sudoers = Path("/etc/sudoers.d/doorphoneserver-panel")
        runner.write(sudoers, f"{TK_USER} ALL=(ALL) NOPASSWD: /usr/bin/systemctl\n", sudo=True)
        if not runner.dry_run:
            runner.run(["chmod", "440", str(sudoers)], sudo=True)
            ok, out = runner.run(["visudo", "-c", "-f", str(sudoers)], sudo=True)
            if not ok:
                runner.log(f"  ✗ sudoers non valido! Rimozione: {out}")
                runner.run(["rm", "-f", str(sudoers)], sudo=True)

        runner.run(["systemctl", "daemon-reload"], sudo=True)
        runner.run(["systemctl", "enable", "doorphoneserver"], sudo=True)

        # --- Crontab (riavvii notturni + restart tablet) ---
        cron_script = REPO_ROOT / "setup" / "scripts" / "setup_crontab.sh"
        if cron_script.exists():
            runner.run(["sudo", "-u", TK_USER, "bash", str(cron_script)])
            runner.log("  ✓ Crontab installato")
        else:
            runner.log("  ⚠ setup_crontab.sh non trovato, crontab non installato")

        self._set_status(Status.DONE)
        return True

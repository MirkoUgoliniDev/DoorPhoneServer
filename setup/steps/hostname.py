"""Passo 2 — Imposta hostname."""

from pathlib import Path
from lib.step_base import Step, Status, validate_hostname
from lib.constants import DEFAULT_HOSTNAME


class StepHostname(Step):
    def __init__(self):
        super().__init__(
            "Hostname",
            f"Imposta il nome host del Raspberry Pi (default: {DEFAULT_HOSTNAME})"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        hostname = validate_hostname(config.get("hostname", DEFAULT_HOSTNAME))
        runner.log(f"  Imposto hostname: {hostname}")

        ok, _ = runner.run(["hostnamectl", "set-hostname", hostname], sudo=True)
        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        if not runner.dry_run:
            try:
                hosts = Path("/etc/hosts").read_text()
                lines = [l for l in hosts.splitlines()
                         if not l.startswith("127.0.1.1")]
                lines.append(f"127.0.1.1\t{hostname}")
                runner.write(Path("/etc/hosts"), "\n".join(lines) + "\n", sudo=True)
            except Exception as e:
                runner.log(f"  ⚠ Impossibile aggiornare /etc/hosts: {e}")
        else:
            runner.log(f"  [DRY-RUN] write 127.0.1.1 → {hostname} in /etc/hosts")

        self._set_status(Status.DONE)
        return True

"""Passo 4 — Installa pacchetti APT."""

from lib.step_base import Step, Status
from lib.constants import APT_PACKAGES


class StepPackages(Step):
    def __init__(self):
        super().__init__(
            "Pacchetti APT",
            "apt-get update + installazione dipendenze audio, build tools, Mumble"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if not runner.check_disk_space(sysinfo, needed_gb=1.0):
            self._set_status(Status.FAILED)
            return False

        apt_env = {"DEBIAN_FRONTEND": "noninteractive"}

        runner.log("  Aggiorno lista pacchetti...")
        ok, _ = runner.run(["apt-get", "update", "-qq"], sudo=True, retries=3,
                           env=apt_env)
        if not ok and not runner.dry_run:
            runner.log("  ✗ apt-get update fallito — verifica la connessione internet")
            self._set_status(Status.FAILED)
            return False

        runner.log(f"  Installo {len(APT_PACKAGES)} pacchetti...")
        ok, _ = runner.run(
            ["apt-get", "install", "-y", "--no-install-recommends"] + APT_PACKAGES,
            sudo=True,
            retries=2,
            env=apt_env,
        )

        self._set_status(Status.DONE if ok else Status.FAILED)
        return ok

"""Passo — Scrittura file .env con le credenziali di sistema."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import REPO_ROOT


class StepEnvConfig(Step):
    def __init__(self):
        super().__init__(
            "Credenziali .env",
            "Scrive il file .env nella root del progetto"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        env_path = REPO_ROOT / ".env"
        lines = [
            "# Generato dal setup wizard DoorPhoneServer\n",
            f"MUMBLE_USERNAME={config.get('env_mumble_username', '')}\n",
            f"MUMBLE_PASSWORD={config.get('env_mumble_password', '')}\n",
            f"CAMERA_USERNAME={config.get('env_camera_username', '')}\n",
            f"CAMERA_PASSWORD={config.get('env_camera_password', '')}\n",
            f"PUSHOVER_API_TOKEN={config.get('env_pushover_token', '')}\n",
            f"PUSHOVER_USER_KEY={config.get('env_pushover_key', '')}\n",
            f"OPENROUTER_API_KEY={config.get('env_openrouter_key', '')}\n",
        ]

        if runner.dry_run:
            runner.log(f"  [DRY-RUN] scrittura {env_path}")
            self._set_status(Status.DONE)
            return True

        try:
            env_path.write_text("".join(lines))
            env_path.chmod(0o600)
            runner.log(f"  ✓ {env_path} scritto (permessi 600)")
        except Exception as e:
            runner.log(f"  ✗ Errore scrittura .env: {e}")
            self._set_status(Status.FAILED)
            return False

        self._set_status(Status.DONE)
        return True

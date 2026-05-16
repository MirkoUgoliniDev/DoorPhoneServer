"""Passo — Scrittura file .env con le credenziali di sistema."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER, TK_GROUP


class StepEnvConfig(Step):
    def __init__(self):
        super().__init__(
            "Credenziali .env",
            "Scrive il file .env in /home/doorphoneserver/ (non nel repo di setup)"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        # .env va nella home dell'utente di sistema, NON nella cartella di setup.
        # REPO_ROOT durante un fresh install punta a ~/doorphoneserver-setup/,
        # ma il binario legge .env da /home/doorphoneserver/.env
        env_path = Path(f"/home/{TK_USER}/.env")
        content = (
            "# Generato dal setup wizard DoorPhoneServer\n"
            f"MUMBLE_USERNAME={config.get('env_mumble_username', '')}\n"
            f"MUMBLE_PASSWORD={config.get('env_mumble_password', '')}\n"
            f"CAMERA_USERNAME={config.get('env_camera_username', '')}\n"
            f"CAMERA_PASSWORD={config.get('env_camera_password', '')}\n"
            f"PUSHOVER_API_TOKEN={config.get('env_pushover_token', '')}\n"
            f"PUSHOVER_USER_KEY={config.get('env_pushover_key', '')}\n"
            f"OPENROUTER_API_KEY={config.get('env_openrouter_key', '')}\n"
        )

        if runner.dry_run:
            runner.log(f"  [DRY-RUN] scrittura {env_path} (600, owner {TK_USER})")
            self._set_status(Status.DONE)
            return True

        ok = runner.write(env_path, content, sudo=True)
        if not ok:
            self._set_status(Status.FAILED)
            return False

        runner.run(["chmod", "600", str(env_path)], sudo=True)
        runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(env_path)], sudo=True)
        runner.log(f"  ✓ {env_path} scritto (600, owner {TK_USER})")

        self._set_status(Status.DONE)
        return True

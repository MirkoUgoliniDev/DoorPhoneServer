"""Passo 3 — Crea utente doorphoneserver."""

import os
import subprocess
from lib.step_base import Step, Status
from lib.constants import TK_USER, TK_GROUP, GOPATH, GOBIN, USER_GROUPS


class StepCreateUser(Step):
    def __init__(self):
        super().__init__(
            "Utente di Sistema",
            f"Crea utente '{TK_USER}' e lo aggiunge ai gruppi necessari"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        exists = subprocess.run(
            ["id", TK_USER], capture_output=True
        ).returncode == 0

        if not exists:
            ok, _ = runner.run(["useradd", "-m", "-s", "/bin/bash", TK_USER], sudo=True)
            if not ok and not runner.dry_run:
                runner.log(f"  ✗ Impossibile creare utente {TK_USER}")
                self._set_status(Status.FAILED)
                return False
        else:
            runner.log(f"  Utente {TK_USER} già presente")

        for g in USER_GROUPS:
            group_exists = subprocess.run(
                ["getent", "group", g], capture_output=True
            ).returncode == 0
            if not group_exists:
                runner.log(f"  ⚠ Gruppo '{g}' non esiste, skip")
                continue
            runner.run(["usermod", "-aG", g, TK_USER], sudo=True)

        # Aggiunge l'utente che esegue il wizard al gruppo TK_GROUP
        # così può leggere i file di gruppo (es. .env con chmod 640)
        wizard_user = subprocess.run(
            ["id", "-un"], capture_output=True, text=True
        ).stdout.strip()
        if wizard_user and wizard_user != TK_USER:
            runner.run(["usermod", "-aG", TK_GROUP, wizard_user], sudo=True)
            runner.log(f"  Aggiunto '{wizard_user}' al gruppo '{TK_GROUP}' ✓")

        # Home dir world-executable (755) per permettere ad altri utenti
        # (es. pi) di fare stat/exists su path sotto /home/doorphoneserver/
        runner.run(["chmod", "755", f"/home/{TK_USER}"], sudo=True)

        for d in [GOPATH, GOBIN, GOPATH / "src" / "github.com" / "doorphoneserver"]:
            runner.run(["mkdir", "-p", str(d)], sudo=True)
            runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(d)], sudo=True)

        self._set_status(Status.DONE)
        return True

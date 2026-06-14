"""Passo 9 — Clone repo e build binario doorphoneserver.

La home dell'utente (/home/doorphoneserver) È il repository git:
- Se .git non esiste ancora: git init + remote add + pull
- Se .git esiste già: git pull (es. re-run del wizard o macchina di sviluppo)
I file gitignored (.env, .bashrc, certificati) non vengono toccati dal pull.
"""

import os
import shlex
import subprocess
from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER, GOPATH, GOBIN, REPO_URL, REPO_BRANCH


class StepCloneAndBuild(Step):
    def __init__(self):
        super().__init__(
            "Clone & Build",
            "Clona il repository GitHub nella home e compila il binario doorphoneserver"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if not runner.check_disk_space(sysinfo, needed_gb=0.5):
            self._set_status(Status.FAILED)
            return False

        home    = Path(f"/home/{TK_USER}")
        go_bin  = "/usr/local/go/bin/go"

        build_env = {
            "GOPATH": str(GOPATH),
            "GOBIN":  str(GOBIN),
            "HOME":   str(home),
            "PATH":   f"/usr/local/go/bin:{os.environ.get('PATH', '/usr/bin:/bin')}",
            "TMPDIR": "/var/tmp",
            "GOFLAGS": "",
        }

        # --- Clone o aggiornamento repo ---
        try:
            git_dir_exists = (home / ".git").exists()
        except PermissionError:
            git_dir_exists = subprocess.run(
                ["sudo", "-n", "-u", TK_USER, "test", "-d", str(home / ".git")],
                capture_output=True
            ).returncode == 0

        if not runner.dry_run and git_dir_exists:
            runner.log("  Repo già presente in home → git pull")
            ok, _ = runner.run(
                ["git", "-C", str(home), "pull", "--ff-only"],
                user=TK_USER, env=build_env, retries=2
            )
            if not ok:
                runner.log("  ⚠ git pull fallito — uso il codice esistente")
        else:
            runner.log(f"  Inizializzazione repo in {home}...")
            # git clone non funziona in directory non-vuota (home ha già .env ecc.),
            # quindi usiamo init + remote + pull. I file gitignored non vengono toccati.
            runner.run(
                ["git", "-C", str(home), "init"],
                user=TK_USER, env=build_env
            )
            runner.run(
                ["git", "-C", str(home), "remote", "add", "origin", REPO_URL],
                user=TK_USER, env=build_env
            )
            ok, _ = runner.run(
                ["git", "-C", str(home), "pull", "origin", REPO_BRANCH],
                user=TK_USER, env=build_env,
                retries=3, retry_delay=5.0
            )
            if not ok and not runner.dry_run:
                self._set_status(Status.FAILED)
                return False

        # Verifica che il sorgente Go sia presente dopo il clone
        try:
            src_missing = not (home / "go.mod").exists()
        except PermissionError:
            src_missing = subprocess.run(
                ["sudo", "-n", "-u", TK_USER, "test", "-f", str(home / "go.mod")],
                capture_output=True
            ).returncode != 0

        if not runner.dry_run and src_missing:
            runner.log("  ✗ go.mod non trovato in home — clone fallito?")
            self._set_status(Status.FAILED)
            return False

        runner.run(["mkdir", "-p", str(GOBIN)], user=TK_USER)
        runner.run(["mkdir", "-p", str(GOPATH)], user=TK_USER)

        # --- Build ---
        runner.log("  go build... (può richiedere 5-15 minuti su Pi)")
        build_cmd = (
            f"set -e; "
            f"cd {shlex.quote(str(home))} && "
            f"{shlex.quote(go_bin)} build -v -buildvcs=false -trimpath "
            f"'-ldflags=-s -w' "
            f"-o {shlex.quote(str(GOBIN / 'doorphoneserver'))} "
            f"./cmd/doorphoneserver"
        )
        ok, _ = runner.run(
            ["bash", "-c", build_cmd],
            user=TK_USER,
            env=build_env,
            timeout=None,
        )

        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        self._set_status(Status.DONE)
        return True

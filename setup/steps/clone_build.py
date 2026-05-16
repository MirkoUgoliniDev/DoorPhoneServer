"""Passo 9 — Clone repo e build binario doorphoneserver."""

import os
import shlex
import subprocess
from lib.step_base import Step, Status
from lib.constants import TK_USER, GOPATH, GOBIN


class StepCloneAndBuild(Step):
    def __init__(self):
        super().__init__(
            "Clone & Build",
            "Clona il repository GitHub e compila il binario doorphoneserver"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if not runner.check_disk_space(sysinfo, needed_gb=0.5):
            self._set_status(Status.FAILED)
            return False

        src_dir = GOPATH / "src" / "github.com" / "doorphoneserver" / "doorphoneserver"
        go_bin  = "/usr/local/go/bin/go"

        build_env = {
            "GOPATH": str(GOPATH),
            "HOME":   f"/home/{TK_USER}",
            "PATH":   f"/usr/local/go/bin:{os.environ.get('PATH', '/usr/bin:/bin')}",
            "TMPDIR": "/var/tmp",
            "GOFLAGS": "",
        }

        try:
            _dir_exists = src_dir.exists()
        except PermissionError:
            _dir_exists = subprocess.run(
                ["sudo", "-u", TK_USER, "test", "-d", str(src_dir)],
                capture_output=True
            ).returncode == 0

        if not runner.dry_run and _dir_exists:
            runner.log("  Repo già presente → git pull")
            ok, _ = runner.run(
                ["git", "-C", str(src_dir), "pull"],
                user=TK_USER, env=build_env, retries=2
            )
            if not ok:
                runner.log("  ⚠ git pull fallito — uso il codice esistente")
        else:
            runner.log("  git clone...")
            ok, _ = runner.run(
                ["git", "clone",
                 "https://github.com/MirkoUgoliniDev/DoorPhoneServer",
                 str(src_dir)],
                user=TK_USER, env=build_env,
                retries=3, retry_delay=5.0
            )
            if not ok and not runner.dry_run:
                self._set_status(Status.FAILED)
                return False

        try:
            _src_missing = not src_dir.exists()
        except PermissionError:
            _src_missing = subprocess.run(
                ["sudo", "-u", TK_USER, "test", "-d", str(src_dir)],
                capture_output=True
            ).returncode != 0

        if not runner.dry_run and _src_missing:
            runner.log("  ✗ Directory sorgente non trovata, build impossibile")
            self._set_status(Status.FAILED)
            return False

        runner.run(["mkdir", "-p", str(GOBIN)], user=TK_USER)

        runner.log("  go build... (può richiedere 5-15 minuti su Pi)")
        build_cmd = (
            f"cd {shlex.quote(str(src_dir))} && "
            f"{shlex.quote(str(go_bin))} build -v -trimpath "
            f"'-ldflags=-s -w' "
            f"-o {shlex.quote(str(GOBIN / 'doorphoneserver'))} "
            f"./cmd/doorphoneserver"
        )
        ok, _ = runner.run(
            ["bash", "-c", build_cmd],
            user=TK_USER,
            env=build_env,
            timeout=None,  # go build può richiedere 15+ minuti su Pi 4
        )

        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        self._set_status(Status.DONE)
        return True

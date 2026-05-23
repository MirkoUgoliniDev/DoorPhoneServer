"""Passo 5 — Installa Go language."""

from pathlib import Path
from lib.step_base import Step, Status, update_etc_environment
from lib.constants import GO_VERSION


class StepGolang(Step):
    def __init__(self):
        super().__init__(
            "Go Language",
            f"Scarica e installa Go {GO_VERSION} da golang.org"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if not runner.check_disk_space(sysinfo, needed_gb=1.0):
            self._set_status(Status.FAILED)
            return False

        go_bin = Path("/usr/local/go/bin/go")

        if go_bin.exists():
            _, out = runner.run([str(go_bin), "version"])
            if GO_VERSION in out:
                runner.log(f"  Go {GO_VERSION} già installato ✓")
                self._set_status(Status.DONE)
                return True
            runner.log(f"  Versione attuale: {out.strip()} — {'(aggiornamento necessario)' if runner.dry_run else 'aggiorno'}")

        tarball = f"go{GO_VERSION}.linux-{sysinfo.go_arch}.tar.gz"
        url     = f"https://go.dev/dl/{tarball}"
        dest    = Path("/tmp") / tarball

        runner.log(f"  Download {tarball} ...")
        ok, _ = runner.run(
            ["wget", "-q", "--tries=3", "--timeout=60", "-O", str(dest), url],
            retries=3, retry_delay=5.0,
        )
        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        runner.log("  Estrazione in /usr/local ...")
        runner.run(["rm", "-rf", "/usr/local/go"], sudo=True)
        ok, _ = runner.run(
            ["tar", "-C", "/usr/local", "-xzf", str(dest)], sudo=True
        )
        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        if not runner.dry_run and dest.exists():
            try:
                dest.unlink()
            except Exception:
                pass

        update_etc_environment(runner)

        self._set_status(Status.DONE)
        return True

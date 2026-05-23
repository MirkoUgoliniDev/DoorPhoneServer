"""Passo 15 — Pulizia post-installazione."""

import os
import shutil
from pathlib import Path
from lib.step_base import Step, Status


class StepCleanup(Step):
    def __init__(self):
        super().__init__(
            "Pulizia",
            "Rimuove file temporanei e cache inutilizzate dopo l'installazione"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        # Cache Go module lasciata da operazioni manuali come utente pi
        runner_home = Path.home()
        go_cache = runner_home / "go"
        if go_cache.exists() and (go_cache / "pkg").exists():
            runner.log(f"  Rimuovo cache Go: {go_cache}")
            if not runner.dry_run:
                try:
                    shutil.rmtree(go_cache)
                    runner.log("  ✓ Cache Go rimossa")
                except Exception as e:
                    runner.log(f"  ⚠ Impossibile rimuovere {go_cache}: {e}")
        else:
            runner.log(f"  Cache Go ({go_cache}) non presente — skip")

        # Pulizia apt cache
        runner.log("  apt-get clean...")
        runner.run(["apt-get", "clean"], sudo=True)

        runner.log("")
        runner.log("  ✓ Installazione completata.")
        runner.log("  Puoi chiudere il browser e fermare il wizard con Ctrl+C.")

        self._set_status(Status.DONE)
        return True

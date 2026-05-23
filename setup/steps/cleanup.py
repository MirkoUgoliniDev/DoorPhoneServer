"""Passo 15 — Pulizia post-installazione."""

import os
import shutil
from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import REPO_ROOT


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

        # Messaggio finale: la cartella setup non può essere rimossa mentre il wizard gira
        runner.log("")
        runner.log("  ╔══════════════════════════════════════════════════════════╗")
        runner.log("  ║  PULIZIA MANUALE RICHIESTA DOPO LA CHIUSURA DEL WIZARD  ║")
        runner.log("  ╠══════════════════════════════════════════════════════════╣")
        runner.log(f"  ║  La cartella di setup non può essere rimossa ora        ║")
        runner.log(f"  ║  perché il wizard è in esecuzione al suo interno.       ║")
        runner.log("  ║                                                          ║")
        runner.log("  ║  Dopo aver chiuso il wizard, esegui:                    ║")
        runner.log("  ╚══════════════════════════════════════════════════════════╝")
        runner.log(f"    rm -rf {REPO_ROOT}")

        self._set_status(Status.DONE)
        return True

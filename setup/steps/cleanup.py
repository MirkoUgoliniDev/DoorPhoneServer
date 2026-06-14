"""Passo 15 — Pulizia post-installazione."""

import os
import shlex
import shutil
import subprocess
from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER


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

        # --- Rimozione differita del clone di setup (es. /home/pi/doorphoneserver) ---
        # È la copia temporanea da cui gira il wizard: non possiamo cancellarla mentre
        # il processo è vivo (sega il ramo su cui è seduto). Lanciamo quindi un watcher
        # distaccato che attende l'uscita del wizard (Ctrl+C) e poi la rimuove.
        # Guardie di sicurezza: deve essere davvero il clone di setup, dentro la home
        # di chi esegue, e MAI la home dell'utente di servizio (su una macchina di
        # sviluppo /home/doorphoneserver è il sistema reale e non va toccato).
        setup_root    = Path(__file__).resolve().parents[2]
        service_home  = Path(f"/home/{TK_USER}").resolve()
        runner_home_r = runner_home.resolve()

        safe_to_remove = (
            (setup_root / "setup").is_dir()
            and setup_root != service_home
            and str(setup_root).startswith(str(runner_home_r) + os.sep)
        )

        if safe_to_remove and not runner.dry_run:
            wizard_pid = os.getpid()
            watcher = (
                f"while kill -0 {wizard_pid} 2>/dev/null; do sleep 2; done; "
                f"sleep 1; rm -rf {shlex.quote(str(setup_root))}"
            )
            try:
                subprocess.Popen(
                    ["bash", "-c", watcher],
                    start_new_session=True,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
                runner.log(f"  Clone di setup {setup_root}: rimozione pianificata all'uscita del wizard")
            except Exception as e:
                safe_to_remove = False
                runner.log(f"  ⚠ Impossibile pianificare la rimozione di {setup_root}: {e}")
        elif not safe_to_remove:
            runner.log(f"  Clone di setup non auto-rimovibile ({setup_root}) — lascio invariato")

        runner.log("")
        runner.log("  ✓ Installazione completata.")
        if safe_to_remove and not runner.dry_run:
            runner.log("  Chiudi il browser e ferma il wizard con Ctrl+C:")
            runner.log(f"  alla chiusura, la copia temporanea {setup_root} verrà rimossa automaticamente.")
        else:
            runner.log("  Puoi chiudere il browser e fermare il wizard con Ctrl+C.")

        self._set_status(Status.DONE)
        return True

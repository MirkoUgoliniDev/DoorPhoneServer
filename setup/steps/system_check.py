"""Passo 1 — Controllo sistema."""

import os
import subprocess
from lib.step_base import Step, Status
from lib.constants import LOG_FILE


class StepSystemCheck(Step):
    def __init__(self):
        super().__init__(
            "Controllo Sistema",
            "Verifica modello Pi, OS, spazio disco, sudo e connessione internet"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)
        ok = True

        runner.log(f"  Modello  : {sysinfo.pi_model}")
        runner.log(f"  Arch     : {sysinfo.arch} → Go: {sysinfo.go_arch}")
        runner.log(f"  OS       : {sysinfo.codename}")
        runner.log(f"  Disco lib: {sysinfo.disk_free_gb:.1f} GB")
        runner.log(f"  RAM      : {sysinfo.ram_mb} MB")
        runner.log(f"  Boot cfg : {sysinfo.boot_config}")
        runner.log(f"  Log      : {LOG_FILE}")

        if sysinfo.disk_free_gb < 3.0:
            runner.log(
                f"  ✗ Spazio disco insufficiente ({sysinfo.disk_free_gb:.1f} GB < 3 GB richiesti)"
            )
            ok = False

        if not runner.dry_run:
            if os.geteuid() == 0:
                runner.log("  Sudo     : esecuzione diretta come root")
            else:
                # Verifica se l'utente ha sudo (con o senza password)
                r_np, _ = runner.run(["sudo", "-n", "true"])
                if r_np:
                    runner.log("  Sudo     : disponibile senza password ✓")
                else:
                    # -n fallisce se serve password, ma sudo potrebbe funzionare lo stesso
                    # Controlla appartenenza al gruppo sudo/wheel
                    groups = subprocess.run(
                        ["id", "-nG"], capture_output=True, text=True
                    ).stdout.split()
                    if any(g in groups for g in ("sudo", "wheel")):
                        runner.log("  Sudo     : disponibile (con password) ✓")
                    else:
                        runner.log(
                            "  ✗ Utente non nel gruppo sudo. "
                            "Esegui: sudo usermod -aG sudo $USER"
                        )
                        ok = False
        else:
            runner.log("  [DRY-RUN] Controllo sudo saltato")

        if not runner.dry_run:
            r, _ = runner.run(["ping", "-c", "1", "-W", "5", "8.8.8.8"], retries=1)
            if not r:
                r, _ = runner.run(["ping", "-c", "1", "-W", "5", "debian.org"], retries=1)
            runner.log("  Internet : " + ("OK ✓" if r else "✗ NON RAGGIUNGIBILE"))
            if not r:
                ok = False
        else:
            runner.log("  [DRY-RUN] Ping saltato")

        self._set_status(Status.DONE if ok else Status.FAILED)
        return ok

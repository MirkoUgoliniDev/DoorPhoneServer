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

        if os.geteuid() == 0:
            runner.log("  Sudo     : esecuzione diretta come root ✓")
        else:
            r_np, _ = runner.run(["sudo", "-n", "true"])
            if r_np:
                runner.log("  Sudo     : passwordless ✓")
            else:
                # Il wizard gira senza TTY: sudo con password non funziona mai.
                groups = subprocess.run(
                    ["id", "-nG"], capture_output=True, text=True
                ).stdout.split()
                user = subprocess.run(
                    ["whoami"], capture_output=True, text=True
                ).stdout.strip()
                if any(g in groups for g in ("sudo", "wheel")):
                    runner.log(
                        f"  ✗ Sudo richiede password — il wizard non può procedere.\n"
                        f"    Esegui da terminale, poi rilancia il wizard:\n"
                        f"    echo '{user} ALL=(ALL) NOPASSWD:ALL' | sudo tee /etc/sudoers.d/{user}-nopasswd\n"
                        f"    sudo chmod 440 /etc/sudoers.d/{user}-nopasswd"
                    )
                else:
                    runner.log(
                        f"  ✗ Utente '{user}' non nel gruppo sudo.\n"
                        f"    Esegui da terminale come root:\n"
                        f"    usermod -aG sudo {user} && "
                        f"echo '{user} ALL=(ALL) NOPASSWD:ALL' | tee /etc/sudoers.d/{user}-nopasswd"
                    )
                ok = False

        # Connettività: prima ping ICMP, poi fallback HTTPS (è ciò che usano
        # davvero apt e git). Molte reti/router bloccano l'ICMP pur avendo
        # Internet perfettamente funzionante: senza il fallback si avrebbe un
        # falso "NON RAGGIUNGIBILE".
        r, _ = runner.run(["ping", "-c", "1", "-W", "5", "8.8.8.8"], retries=1)
        if not r:
            r, _ = runner.run(["ping", "-c", "1", "-W", "5", "debian.org"], retries=1)
        if not r:
            r, _ = runner.run(
                ["curl", "-fsS", "--max-time", "8", "-o", "/dev/null", "-I", "https://deb.debian.org"],
                retries=1,
            )
        if not r:
            r, _ = runner.run(
                ["wget", "-q", "--spider", "--timeout=8", "https://github.com"],
                retries=1,
            )
        runner.log("  Internet : " + ("OK ✓" if r else "✗ NON RAGGIUNGIBILE"))
        if not r:
            ok = False

        self._set_status(Status.DONE if ok else Status.FAILED)
        return ok

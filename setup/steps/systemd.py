"""Passo 11 — Servizio systemd doorphoneserver e crontab."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER, TK_GROUP, GOBIN, REPO_ROOT


class StepSystemdService(Step):
    def __init__(self):
        super().__init__(
            "Servizio Systemd",
            "Installa doorphoneserver.service, sudoers per il web panel e crontab"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        # --- Service file (da setup/scripts/) ---
        service_src = REPO_ROOT / "setup" / "scripts" / "doorphoneserver.service"
        dst_service  = Path("/etc/systemd/system/doorphoneserver.service")

        if not service_src.exists():
            runner.log(f"  ✗ {service_src} non trovato")
            self._set_status(Status.FAILED)
            return False

        runner.copy(service_src, dst_service, sudo=True)

        if not runner.dry_run and not (GOBIN / "doorphoneserver").exists():
            runner.log("  ⚠ Binario non trovato in GOBIN — il service potrebbe non partire")

        # --- Sudoers per web panel e crontab ---
        # /usr/bin/systemctl copre: start/stop/restart/reboot (usato anche dal crontab)
        # restart_tablet.sh copre il riavvio remoto del tablet da crontab
        tablet_script = Path("/home") / TK_USER / "setup" / "scripts" / "restart_tablet.sh"
        sudoers = Path("/etc/sudoers.d/doorphoneserver-panel")
        sudoers_content = (
            f"{TK_USER} ALL=(ALL) NOPASSWD: /usr/bin/systemctl\n"
            f"{TK_USER} ALL=(ALL) NOPASSWD: /bin/bash {tablet_script}\n"
        )
        runner.write(sudoers, sudoers_content, sudo=True)
        if not runner.dry_run:
            runner.run(["chmod", "440", str(sudoers)], sudo=True)
            ok, out = runner.run(["visudo", "-c", "-f", str(sudoers)], sudo=True)
            if not ok:
                runner.log(f"  ✗ sudoers non valido! Rimozione: {out}")
                runner.run(["rm", "-f", str(sudoers)], sudo=True)

        runner.run(["systemctl", "daemon-reload"], sudo=True)
        # enable --now: abilita al boot E avvia subito, così il pannello è
        # raggiungibile sulla 8080 a fine setup senza riavvio o start manuale.
        # Il binario è già compilato (StepCloneAndBuild gira prima di questo step).
        runner.run(["systemctl", "enable", "--now", "doorphoneserver"], sudo=True)
        if not runner.dry_run:
            ok, _ = runner.run(
                ["systemctl", "is-active", "--quiet", "doorphoneserver"]
            )
            if ok:
                runner.log("  ✓ doorphoneserver avviato (attivo sulla 8080)")
            else:
                runner.log(
                    "  ✗ doorphoneserver non risulta attivo — "
                    "controlla: journalctl -u doorphoneserver -n 50"
                )

        # --- Ambiente sviluppo: GOBIN e PATH in ~/.bashrc ---
        # Permette all'utente doorphoneserver di usare `go build`, `go run` ecc.
        # direttamente dalla home (che è il repo) senza dover impostare manualmente
        # le variabili d'ambiente ad ogni sessione SSH.
        bashrc = Path(f"/home/{TK_USER}/.bashrc")
        dev_block = (
            "\n# DoorPhoneServer — Go dev environment\n"
            f"export GOBIN={GOBIN}\n"
            "export PATH=$PATH:/usr/local/go/bin:$GOBIN\n"
        )
        if not runner.dry_run:
            try:
                existing = bashrc.read_text(encoding="utf-8") if bashrc.exists() else ""
            except PermissionError:
                import subprocess as _sp
                r = _sp.run(["sudo", "-n", "cat", str(bashrc)], capture_output=True, text=True)
                existing = r.stdout if r.returncode == 0 else ""
            if "Go dev environment" not in existing:
                runner.write(bashrc, existing + dev_block, sudo=True)
                runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(bashrc)], sudo=True)
                runner.log("  ✓ GOBIN e PATH aggiunti a ~/.bashrc")
            else:
                runner.log("  ~/.bashrc già configurato per Go")
        else:
            runner.log(f"  [DRY-RUN] aggiunge Go dev env a {bashrc}")

        # --- Crontab (riavvii notturni + restart tablet) ---
        # Il demone cron non è installato di default su Debian 13: assicura che
        # sia abilitato e attivo prima di installare i job, altrimenti il comando
        # crontab fallisce e il pannello mostra "Nessun job crontab trovato".
        runner.run(["systemctl", "enable", "--now", "cron"], sudo=True)
        cron_script = REPO_ROOT / "setup" / "scripts" / "setup_crontab.sh"
        if cron_script.exists():
            # Eseguito come root passando l'utente target: lo script usa
            # `crontab -u TK_USER`, così i job finiscono nella crontab letta
            # dal pannello (che gira come TK_USER), non in quella di root.
            ok, _ = runner.run(
                ["bash", str(cron_script), TK_USER], sudo=True
            )
            if ok:
                runner.log("  ✓ Crontab installato")
            else:
                runner.log(
                    "  ✗ Crontab NON installato — verifica: "
                    f"sudo crontab -u {TK_USER} -l"
                )
        else:
            runner.log("  ⚠ setup_crontab.sh non trovato, crontab non installato")

        self._set_status(Status.DONE)
        return True

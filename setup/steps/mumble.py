"""Passo 7 — Configura e avvia Mumble Server."""

import os
import subprocess
import tempfile
import time
from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import REPO_ROOT


class StepMumbleServer(Step):
    def __init__(self):
        super().__init__(
            "Mumble Server",
            "Configura mumble-server.ini e abilita il servizio Murmur"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        script = REPO_ROOT / "setup" / "scripts" / "setup_mumble.sh"
        if script.exists():
            # Scrive MUMBLE_PASSWORD in un file temporaneo e passa il path
            # come argomento ($1) allo script — evita env_reset di sudo che
            # strippa le variabili d'ambiente custom.
            # Certificato server Mumble fornito dall'utente (OPZIONALE): solo se
            # ENTRAMBI cert e key sono presenti li scriviamo su file temporanei e
            # li passiamo come $2/$3 allo script per il "pinning". Se mancano, lo
            # script genera/usa il certificato di default (comportamento attuale).
            cert_pem = config.get("mumble_cert_pem", "") or ""
            key_pem = config.get("mumble_key_pem", "") or ""
            use_cert = bool(cert_pem.strip()) and bool(key_pem.strip())

            pwd_file = None
            cert_file = None
            key_file = None
            try:
                with tempfile.NamedTemporaryFile(
                    mode="w", suffix=".env", delete=False, dir="/tmp"
                ) as f:
                    f.write(config.get("env_mumble_password", ""))
                    pwd_file = f.name
                os.chmod(pwd_file, 0o600)

                cmd = ["bash", str(script), pwd_file]
                if use_cert:
                    with tempfile.NamedTemporaryFile(
                        mode="w", suffix=".pem", delete=False, dir="/tmp"
                    ) as fc:
                        fc.write(cert_pem)
                        cert_file = fc.name
                    with tempfile.NamedTemporaryFile(
                        mode="w", suffix=".pem", delete=False, dir="/tmp"
                    ) as fk:
                        fk.write(key_pem)
                        key_file = fk.name
                    os.chmod(cert_file, 0o600)
                    os.chmod(key_file, 0o600)
                    cmd += [cert_file, key_file]
                    runner.log("  Certificato server fornito → pinning abilitato")

                ok, _ = runner.run(cmd, sudo=True)
            finally:
                for tmp in (pwd_file, cert_file, key_file):
                    if tmp and os.path.exists(tmp):
                        try:
                            os.unlink(tmp)
                        except Exception:
                            pass
            if not ok and not runner.dry_run:
                runner.log("  ⚠ setup_mumble.sh ha avuto errori — il servizio potrebbe non partire")
        else:
            runner.log("  ⚠ setup_mumble.sh non trovato, uso configurazione default")
            runner.run(["systemctl", "enable", "mumble-server"], sudo=True)
            runner.run(["systemctl", "start",  "mumble-server"], sudo=True)

        if not runner.dry_run:
            runner.log("  Attendo avvio mumble-server...")
            for _ in range(15):
                active = subprocess.run(
                    ["systemctl", "is-active", "--quiet", "mumble-server"]
                ).returncode == 0
                if active:
                    runner.log("  ✓ mumble-server attivo")
                    break
                time.sleep(1)
            else:
                runner.log("  ⚠ mumble-server non attivo dopo 15s")

        self._set_status(Status.DONE)
        return True

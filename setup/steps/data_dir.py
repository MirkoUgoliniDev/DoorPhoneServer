"""Passo 10 — Directory preferences e certificato TLS."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER, TK_GROUP, REPO_ROOT


class StepDataDir(Step):
    def __init__(self):
        super().__init__(
            "Directory & Certificati",
            "Crea preferences/, genera certificato TLS e copia doorphoneserver.xml"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        home = Path(f"/home/{TK_USER}")

        # --- Cartella preferences/ per file JSON di configurazione ---
        prefs = home / "preferences"
        runner.run(["mkdir", "-p", str(prefs)], sudo=True)
        runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(prefs)], sudo=True)

        # Copia alarms.json e ai.json dal repo se non esistono già a destinazione
        for json_file in ("alarms.json", "ai.json"):
            src = REPO_ROOT / "preferences" / json_file
            dst = prefs / json_file
            if not runner.dry_run:
                if src.exists():
                    runner.run(["cp", "-n", str(src), str(dst)], sudo=True)
                else:
                    # Crea scheletro vuoto se il file non è nel repo
                    runner.write(dst, "{}\n", sudo=True)
                runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(dst)], sudo=True)
            else:
                runner.log(f"  [DRY-RUN] cp -n {src} → {dst}")

        # --- Certificato TLS per Mumble (in home/) ---
        runner.log("  Generazione certificato TLS...")
        cert_cmd = (
            f"openssl req -x509 -newkey rsa:4096 "
            f"-keyout {home}/nopasskey.pem "
            f"-out {home}/cert.pem "
            f"-days 1095 -nodes "
            f"-subj '/CN=doorphoneserver' 2>/dev/null && "
            f"cat {home}/nopasskey.pem {home}/cert.pem > {home}/mumble.pem"
        )
        runner.run(["bash", "-c", cert_cmd], user=TK_USER)

        # --- doorphoneserver.xml (versionato nella root del repo) ---
        xml_src = REPO_ROOT / "doorphoneserver.xml"
        xml_dst = home / "doorphoneserver.xml"
        if xml_src.exists():
            runner.log("  Copia doorphoneserver.xml in home...")
            runner.run(["cp", str(xml_src), str(xml_dst)], sudo=True)
            runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(xml_dst)], sudo=True)
        else:
            runner.log(f"  ⚠ doorphoneserver.xml non trovato in {xml_src}")

        self._set_status(Status.DONE)
        return True

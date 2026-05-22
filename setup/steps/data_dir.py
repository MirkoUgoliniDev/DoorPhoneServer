"""Passo 10 — Directory preferences e certificato TLS."""

import re
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

        # --- doorphoneserver.xml: copia dal repo e aggiorna certificate ---
        xml_src = REPO_ROOT / "doorphoneserver.xml"
        xml_dst = home / "doorphoneserver.xml"
        cert_path = str(home / "mumble.pem")

        if xml_src.exists() or runner.dry_run:
            runner.log("  Scrittura doorphoneserver.xml in home...")
            if runner.dry_run:
                runner.log(f"  [DRY-RUN] XML certificate → {cert_path}")
            else:
                content = xml_src.read_text(encoding="utf-8")
                content = re.sub(r'<certificate\s*/>', f'<certificate>{cert_path}</certificate>', content)
                content = re.sub(r'<certificate>[^<]*</certificate>', f'<certificate>{cert_path}</certificate>', content)
                runner.log(f"  ✓ XML certificate → {cert_path}")
                runner.write(xml_dst, content, sudo=True)
                runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(xml_dst)], sudo=True)
        else:
            runner.log(f"  ⚠ doorphoneserver.xml non trovato in {xml_src}")

        self._set_status(Status.DONE)
        return True

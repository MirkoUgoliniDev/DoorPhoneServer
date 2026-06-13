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

        if not runner.dry_run:
            runner.run(["chown", "-R", f"{TK_USER}:{TK_GROUP}", str(prefs)], sudo=True)

        # --- Cartella soundfiles/ (events + audiotest) ---
        # Il pannello legge ~/soundfiles/events e ~/soundfiles/audiotest accanto
        # a doorphoneserver.xml. Senza queste cartelle il tab Sound risponde
        # "Cannot read events dir" (non-JSON) e l'Audio Test resta vuoto.
        sounds_dst = home / "soundfiles"
        for sub in ("events", "audiotest"):
            runner.run(["mkdir", "-p", str(sounds_dst / sub)], sudo=True)
        sounds_src = REPO_ROOT / "soundfiles"
        if not runner.dry_run and sounds_src.exists():
            # Copia i file di default dal repo senza sovrascrivere quelli esistenti
            runner.run(
                ["bash", "-c",
                 f"cp -rn {sounds_src}/. {sounds_dst}/ 2>/dev/null || true"],
                sudo=True,
            )
        if not runner.dry_run:
            runner.run(["chown", "-R", f"{TK_USER}:{TK_GROUP}", str(sounds_dst)], sudo=True)
        else:
            runner.log(f"  [DRY-RUN] copia soundfiles/ → {sounds_dst}")

        # --- Cartella snapshots/ per le catture ffmpeg dalla telecamera ---
        # Default Camera.Snapshot.Dir = ~/snapshots. Il servizio gira come
        # doorphoneserver e deve potervi scrivere, altrimenti ffmpeg fallisce
        # con "Input/output error". Permessi 775 + owner del servizio.
        snaps_dst = home / "snapshots"
        runner.run(["mkdir", "-p", str(snaps_dst)], sudo=True)
        if not runner.dry_run:
            runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(snaps_dst)], sudo=True)
            runner.run(["chmod", "775", str(snaps_dst)], sudo=True)

        # --- Certificato TLS per Mumble (in home/) ---
        # Non rigenerare se esiste già: i tablet pinnano il certificato del client
        # e una rigenerazione su re-run del wizard causerebbe errori di autenticazione.
        mumble_pem = home / "mumble.pem"
        if runner.dry_run or not mumble_pem.exists():
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
        else:
            runner.log("  ✓ Certificato TLS già presente — mantenuto (skip rigenerazione)")

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

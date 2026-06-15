"""Passo 6 — Configurazione audio ALSA."""

import re
from pathlib import Path
from lib.step_base import Step, Status
from lib.audio_utils import best_card_pair, generate_asound_conf, get_playback_control
from lib.constants import REPO_ROOT, TK_USER, TK_GROUP


class StepAudioConfig(Step):
    def __init__(self):
        super().__init__(
            "Configurazione Audio",
            "Rileva le schede audio (ora alsa-utils è installato) e scrive asound.conf"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if config.get("play_card") is None or config.get("_audio_autodetect"):
            runner.log("  Rilevo schede audio...")
            play_card_obj, cap_card_obj = best_card_pair()
            if play_card_obj:
                runner.log(f"  Scheda OUTPUT selezionata: {play_card_obj}")
                config["play_card"] = play_card_obj.index
            if cap_card_obj:
                runner.log(f"  Scheda INPUT  selezionata: {cap_card_obj}")
                config["cap_card"] = cap_card_obj.index
            if not play_card_obj and not cap_card_obj:
                runner.log("  ⚠ Nessuna scheda audio rilevata.")
                runner.log("    Collega la scheda USB e ri-esegui la configurazione con:")
                runner.log("    python3 setup/setup_wizard.py --audio-setup")
                runner.log("    Il sistema funzionerà senza audio fino al prossimo setup.")
                self._set_status(Status.SKIPPED)
                return True

        play_card = max(0, min(9, int(config.get("play_card") or 0)))
        play_dev  = max(0, min(3, int(config.get("play_dev")  or 0)))
        cap_card  = max(0, min(9, int(config.get("cap_card")  or 0)))
        cap_dev   = max(0, min(3, int(config.get("cap_dev")   or 0)))

        runner.log(f"  Output : card {play_card}, device {play_dev}")
        runner.log(f"  Input  : card {cap_card},  device {cap_dev}")

        runner.write(
            Path("/etc/asound.conf"),
            generate_asound_conf(play_card, play_dev, cap_card, cap_dev),
            sudo=True,
        )
        runner.run(["chmod", "644", "/etc/asound.conf"], sudo=True)
        # alsoft.conf è installato dallo step Config Boot RPi (setup_configs.sh)

        # Sblocca e imposta i volumi sulla scheda scelta. Molti dongle USB (es.
        # chipset CM108) partono MUTATI via hardware: senza unmute non esce/entra
        # audio anche con la card corretta. 'alsactl store' salva lo stato così
        # resta sbloccato al reboot. I controlli inesistenti vengono ignorati.
        if not runner.dry_run:
            runner.shell(
                f'for c in Speaker PCM Master Headphone Lineout Front; do '
                f'  amixer -c {play_card} sset "$c" 90% unmute 2>/dev/null; done; '
                f'for c in Mic Capture "Mic Boost" Digital; do '
                f'  amixer -c {cap_card} sset "$c" 80% cap unmute 2>/dev/null; done; '
                f'alsactl store 2>/dev/null; true',
                sudo=True,
            )
            runner.log("  ✓ Mixer sbloccato (unmute + volumi) e salvato con alsactl store")

        # Aggiorna outputdevice nel XML sorgente (verrà copiato da StepDataDir)
        xml_src = REPO_ROOT / "doorphoneserver.xml"
        if runner.dry_run:
            ctrl = get_playback_control(play_card)
            runner.log(f"  [DRY-RUN] XML outputdevice → {ctrl or '(nessun controllo mixer rilevato)'}")
        elif xml_src.exists():
            ctrl = get_playback_control(play_card)
            if ctrl:
                # Il file di config vive nella home del servizio (REPO_ROOT ==
                # /home/doorphoneserver) ed è di proprietà di 'doorphoneserver',
                # mentre il wizard gira come 'pi'. Se il repo è stato clonato da
                # root con umask restrittiva il file resta root:root senza
                # permesso di lettura per 'pi' e read_text() qui sotto solleva
                # [Errno 13] Permission denied. Normalizzo owner+permessi
                # (idempotente) così sia il wizard sia il servizio possono
                # leggerlo/scriverlo; la successiva runner.write(sudo) li
                # preserva (cp su file esistente non cambia owner/mode).
                runner.run(["chown", f"{TK_USER}:{TK_GROUP}", str(xml_src)], sudo=True)
                runner.run(["chmod", "664", str(xml_src)], sudo=True)
                content = xml_src.read_text(encoding="utf-8")
                content = re.sub(r'<outputdevice>[^<]*</outputdevice>',
                                 f'<outputdevice>{ctrl}</outputdevice>', content)
                content = re.sub(r'<outputvolcontroldevice>[^<]*</outputvolcontroldevice>',
                                 f'<outputvolcontroldevice>{ctrl}</outputvolcontroldevice>', content)
                content = re.sub(r'<outputmutecontroldevice>[^<]*</outputmutecontroldevice>',
                                 f'<outputmutecontroldevice>{ctrl}</outputmutecontroldevice>', content)
                runner.write(xml_src, content, sudo=True)
                runner.log(f"  ✓ XML outputdevice → {ctrl}")
            else:
                runner.log("  ⚠ Controllo mixer non rilevato — outputdevice nel XML non aggiornato")

        self._set_status(Status.DONE)
        return True

"""Passo 6 — Configurazione audio ALSA."""

import re
from pathlib import Path
from lib.step_base import Step, Status
from lib.audio_utils import best_card_pair, generate_asound_conf, get_playback_control
from lib.constants import REPO_ROOT


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
                # Aggiornare outputdevice nel XML è cosmetico: NON deve mai far
                # abortire l'installazione. Il wizard gira come utente normale
                # (di norma 'pi', vedi wizard.py) che NON ha sempre sudo -n
                # disponibile, quindi non si può dipendere da sudo qui.
                # Strategia: leggi e scrivi DIRETTAMENTE (funziona quando
                # l'utente possiede o ha permesso di scrittura sul file); se la
                # scrittura diretta fallisce, prova con sudo; se tutto fallisce
                # logga un warning e prosegui.
                try:
                    content = xml_src.read_text(encoding="utf-8")
                except OSError as e:
                    runner.log(f"  ⚠ XML non leggibile ({e}) — outputdevice non aggiornato, continuo")
                    self._set_status(Status.DONE)
                    return True

                content = re.sub(r'<outputdevice>[^<]*</outputdevice>',
                                 f'<outputdevice>{ctrl}</outputdevice>', content)
                content = re.sub(r'<outputvolcontroldevice>[^<]*</outputvolcontroldevice>',
                                 f'<outputvolcontroldevice>{ctrl}</outputvolcontroldevice>', content)
                content = re.sub(r'<outputmutecontroldevice>[^<]*</outputmutecontroldevice>',
                                 f'<outputmutecontroldevice>{ctrl}</outputmutecontroldevice>', content)

                ok = runner.write(xml_src, content, sudo=False)
                if not ok:
                    ok = runner.write(xml_src, content, sudo=True)
                if ok:
                    # Il file temporaneo nasce a 0600: senza questo chmod la
                    # riscrittura lascerebbe il config a 0600 e l'utente di
                    # gruppo (pi) perderebbe la scrittura. 664 = proprietario +
                    # gruppo doorphoneserver in scrittura, lettura per tutti.
                    try:
                        xml_src.chmod(0o664)
                    except OSError:
                        pass
                    runner.log(f"  ✓ XML outputdevice → {ctrl}")
                else:
                    runner.log("  ⚠ XML outputdevice non aggiornato (permessi) — continuo comunque")
            else:
                runner.log("  ⚠ Controllo mixer non rilevato — outputdevice nel XML non aggiornato")

        self._set_status(Status.DONE)
        return True

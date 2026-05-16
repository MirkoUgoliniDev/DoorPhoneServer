"""Passo 6 — Configurazione audio ALSA."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.audio_utils import detect_audio_cards, generate_asound_conf


class StepAudioConfig(Step):
    def __init__(self):
        super().__init__(
            "Configurazione Audio",
            "Rileva le schede audio (ora alsa-utils è installato) e scrive asound.conf"
        )

    def execute(self, runner, sysinfo, config):
        self._set_status(Status.RUNNING)

        if not runner.dry_run and (
            config.get("play_card") is None or config.get("_audio_autodetect")
        ):
            runner.log("  Rilevo schede audio...")
            play_cards, cap_cards = detect_audio_cards()
            if play_cards:
                runner.log(
                    "  Schede OUTPUT rilevate: "
                    + ", ".join(str(c) for c in play_cards)
                )
                config["play_card"] = play_cards[0].index
            if cap_cards:
                runner.log(
                    "  Schede INPUT rilevate: "
                    + ", ".join(str(c) for c in cap_cards)
                )
                config["cap_card"] = cap_cards[0].index
            if not play_cards and not cap_cards:
                runner.log("  ⚠ Nessuna scheda audio rilevata.")
                runner.log("    Collega la scheda USB e ri-esegui la configurazione con:")
                runner.log("    python3 setup/setup_wizard.py --audio-setup")
                runner.log("    Il sistema funzionerà senza audio fino al prossimo setup.")
                self._set_status(Status.SKIPPED)
                return True

        play_card = max(0, min(9, int(config.get("play_card") or 1)))
        play_dev  = max(0, min(3, int(config.get("play_dev")  or 0)))
        cap_card  = max(0, min(9, int(config.get("cap_card")  or 1)))
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

        self._set_status(Status.DONE)
        return True

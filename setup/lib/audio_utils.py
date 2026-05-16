"""Rilevamento schede audio e generazione configurazione ALSA."""

import re
import shutil
import subprocess
from typing import List, Tuple


class AudioCard:
    def __init__(self, index: int, id_str: str, name: str):
        self.index  = index
        self.id_str = id_str
        self.name   = name

    def __str__(self):
        return f"card {self.index}: {self.name}"


def detect_audio_cards() -> Tuple[List[AudioCard], List[AudioCard]]:
    """
    Ritorna (playback_cards, capture_cards).
    Se aplay/arecord non sono installati restituisce liste vuote senza crash.
    """
    if not shutil.which("aplay") or not shutil.which("arecord"):
        return [], []

    def parse(output: str) -> List[AudioCard]:
        cards = []
        for line in output.splitlines():
            m = re.match(r"card\s+(\d+):\s+(\S+)\s+\[(.+?)\]", line)
            if m:
                cards.append(AudioCard(int(m.group(1)), m.group(2), m.group(3)))
        return cards

    try:
        r_play = subprocess.run(["aplay",   "-l"], capture_output=True, text=True, timeout=5)
        r_rec  = subprocess.run(["arecord", "-l"], capture_output=True, text=True, timeout=5)
        return parse(r_play.stdout), parse(r_rec.stdout)
    except Exception:
        return [], []


def validate_card_index(value, cards: List[AudioCard]) -> int:
    """Valida e normalizza l'indice scheda. Ritorna 0 su valore non valido."""
    try:
        idx = int(value)
        valid = [c.index for c in cards] if cards else list(range(10))
        return idx if idx in valid or not cards else valid[0]
    except (ValueError, TypeError):
        return 0


def generate_asound_conf(
    play_card: int, play_dev: int, cap_card: int, cap_dev: int
) -> str:
    pc = max(0, min(9, play_card))
    pd = max(0, min(3, play_dev))
    cc = max(0, min(9, cap_card))
    cd = max(0, min(3, cap_dev))

    return f"""# Generato da DoorPhoneServer Setup Wizard
pcm.dmixed {{
    type dmix
    ipc_key 1024
    ipc_key_add_uid false
    ipc_perm 0666
    slave.pcm {{
        type hw
        card {pc}
        device {pd}
        subdevice 0
        format S16_LE
        channels 2
        rate 48000
    }}
}}

pcm.dsnooped {{
    type dsnoop
    ipc_key 1025
    ipc_key_add_uid false
    ipc_perm 0666
    slave.pcm {{
        type hw
        card {cc}
        device {cd}
        subdevice 0
        format S16_LE
        channels 1
        rate 48000
    }}
}}

pcm.duplex {{
    type asym
    playback.pcm "dmixed"
    capture.pcm  "dsnooped"
}}

pcm.!default {{
    type plug
    slave.pcm "duplex"
}}

ctl.!default {{
    type hw
    card {pc}
}}
"""

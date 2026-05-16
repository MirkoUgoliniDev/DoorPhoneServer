"""Rilevamento schede audio e generazione configurazione ALSA."""

import os
import re
import shutil
import subprocess
from typing import List, Optional, Tuple


class AudioCard:
    def __init__(self, index: int, id_str: str, name: str):
        self.index  = index
        self.id_str = id_str
        self.name   = name

    def __str__(self):
        return f"card {self.index}: {self.name}"


def _is_usb_card(index: int) -> bool:
    """Controlla se la scheda è connessa via USB tramite sysfs."""
    try:
        link = os.readlink(f"/sys/class/sound/card{index}/device")
        return "usb" in link.lower()
    except OSError:
        return False


def _is_hdmi_card(name: str, id_str: str) -> bool:
    """Ritorna True se la scheda sembra essere HDMI/vc4/DisplayPort."""
    keywords = ("hdmi", "vc4", "displayport", "display")
    combined = (name + " " + id_str).lower()
    return any(k in combined for k in keywords)


def _rank_card(card: AudioCard) -> int:
    """
    Priorità: 0 = USB non-HDMI (meglio), 1 = built-in non-HDMI, 2 = HDMI.
    Usato per ordinare le schede dalla più preferita alla meno preferita.
    """
    if _is_hdmi_card(card.name, card.id_str):
        return 2
    if _is_usb_card(card.index):
        return 0
    return 1


def detect_audio_cards() -> Tuple[List[AudioCard], List[AudioCard]]:
    """
    Ritorna (playback_cards, capture_cards) ordinate per preferenza
    (USB non-HDMI prima, poi built-in, poi HDMI).
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
        return sorted(cards, key=_rank_card)

    try:
        r_play = subprocess.run(["aplay",   "-l"], capture_output=True, text=True, timeout=5)
        r_rec  = subprocess.run(["arecord", "-l"], capture_output=True, text=True, timeout=5)
        return parse(r_play.stdout), parse(r_rec.stdout)
    except Exception:
        return [], []


def best_card_pair() -> Tuple[Optional[AudioCard], Optional[AudioCard]]:
    """
    Ritorna (play_card, cap_card) preferendo schede presenti in entrambe le liste
    (full-duplex). Se non esiste una scheda full-duplex usa la prima di ciascuna lista.
    """
    play_cards, cap_cards = detect_audio_cards()
    if not play_cards and not cap_cards:
        return None, None

    cap_indices = {c.index for c in cap_cards}
    play_indices = {c.index for c in play_cards}

    # Prima scelta: scheda presente in entrambe le liste (full-duplex), la meglio ranked
    for pc in play_cards:
        if pc.index in cap_indices:
            cc = next(c for c in cap_cards if c.index == pc.index)
            return pc, cc

    # Fallback: meglio disponibile per ciascuno
    best_play = play_cards[0] if play_cards else None
    best_cap  = cap_cards[0]  if cap_cards  else None
    return best_play, best_cap


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

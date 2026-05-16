"""Classe base Step e Status."""

import re
from enum import Enum, auto
from typing import Callable, Optional
from .constants import DEFAULT_HOSTNAME


class Status(Enum):
    PENDING = auto()
    RUNNING = auto()
    DONE    = auto()
    FAILED  = auto()
    SKIPPED = auto()


STEP_ICONS = {
    Status.PENDING: "○",
    Status.RUNNING: "◎",
    Status.DONE:    "✓",
    Status.FAILED:  "✗",
    Status.SKIPPED: "⊘",
}


class Step:
    def __init__(self, name: str, description: str, optional: bool = False):
        self.name        = name
        self.description = description
        self.optional    = optional
        self.status      = Status.PENDING
        self._cb: Optional[Callable] = None

    def set_callback(self, cb: Callable):
        self._cb = cb

    def _set_status(self, s: Status):
        self.status = s
        if self._cb:
            self._cb(self, s)

    def execute(self, runner, sysinfo, config: dict) -> bool:
        raise NotImplementedError


def validate_hostname(name: str) -> str:
    """Sanitizza hostname: solo alfanumerici e trattini, max 63 char."""
    name = re.sub(r"[^a-zA-Z0-9-]", "-", name.strip()).strip("-")
    return name[:63] if name else DEFAULT_HOSTNAME


def update_etc_environment(runner, go_bin_path: str = "/usr/local/go/bin"):
    """Aggiunge go_bin_path a PATH in /etc/environment in modo robusto."""
    from pathlib import Path

    env_path = Path("/etc/environment")

    if runner.dry_run:
        runner.log(f"  [DRY-RUN] Aggiunge {go_bin_path} a PATH in /etc/environment")
        return

    try:
        try:
            content = env_path.read_text()
        except FileNotFoundError:
            content = ""

        if go_bin_path in content:
            runner.log(f"  PATH già contiene {go_bin_path}, skip")
            return

        m_quoted   = re.search(r'^PATH="([^"]+)"', content, re.MULTILINE)
        m_unquoted = re.search(r'^PATH=(\S+)',      content, re.MULTILINE)

        if m_quoted:
            new_content = content.replace(
                m_quoted.group(0),
                f'PATH="{m_quoted.group(1)}:{go_bin_path}"'
            )
        elif m_unquoted:
            new_content = content.replace(
                m_unquoted.group(0),
                f'PATH="{m_unquoted.group(1)}:{go_bin_path}"'
            )
        else:
            new_content = content.rstrip("\n") + (
                f'\nPATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:{go_bin_path}"\n'
            )

        runner.write(env_path, new_content, sudo=True)

    except Exception as e:
        runner.log(f"  ⚠ Impossibile aggiornare /etc/environment: {e}")

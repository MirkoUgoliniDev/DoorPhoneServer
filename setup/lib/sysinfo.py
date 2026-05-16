"""Rilevamento informazioni di sistema."""

import os
import re
import platform
from pathlib import Path
from .constants import GO_ARCH_MAP


class SystemInfo:
    def __init__(self):
        self.arch        = platform.machine()
        self.go_arch     = GO_ARCH_MAP.get(self.arch, "arm64")
        self.codename    = self._codename()
        self.boot_config = self._boot_config()
        self.pi_model    = self._pi_model()
        self.has_display = bool(
            os.environ.get("DISPLAY") or os.environ.get("WAYLAND_DISPLAY")
        )
        self.disk_free_gb = self._disk_free()
        self.ram_mb       = self._ram_mb()

    def _codename(self):
        try:
            txt = Path("/etc/os-release").read_text()
            m = re.search(r"VERSION_CODENAME=(\w+)", txt)
            return m.group(1) if m else "bookworm"
        except Exception:
            return "bookworm"

    def _boot_config(self):
        # Bookworm+ usa /boot/firmware/config.txt
        p = Path("/boot/firmware/config.txt")
        return p if p.exists() else Path("/boot/config.txt")

    def _pi_model(self):
        try:
            return Path("/proc/device-tree/model").read_bytes().rstrip(b"\x00").decode()
        except Exception:
            return "Dispositivo sconosciuto"

    def _disk_free(self):
        try:
            st = os.statvfs("/")
            return (st.f_bavail * st.f_frsize) / 1024 ** 3
        except Exception:
            return 0.0

    def _ram_mb(self):
        try:
            txt = Path("/proc/meminfo").read_text()
            m = re.search(r"MemTotal:\s+(\d+)", txt)
            return int(m.group(1)) // 1024 if m else 0
        except Exception:
            return 0

    def current_disk_free_gb(self) -> float:
        return self._disk_free()

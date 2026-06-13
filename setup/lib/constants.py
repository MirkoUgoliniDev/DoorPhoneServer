"""Costanti globali del wizard."""

from pathlib import Path

WIZARD_VERSION = "2.0.0"
GO_VERSION     = "1.24.4"
TK_USER        = "doorphoneserver"
TK_GROUP       = "doorphoneserver"
DEFAULT_HOSTNAME = "doorphoneserver"

REPO_ROOT  = Path(__file__).parent.parent.parent.resolve()
GOPATH     = Path(f"/home/{TK_USER}/go")
GOBIN      = Path(f"/home/{TK_USER}/bin")
LOCK_FILE  = Path("/tmp/doorphoneserver-setup.lock")
LOG_FILE   = Path("/tmp/doorphoneserver-setup.log")

GO_ARCH_MAP = {
    "aarch64": "arm64",
    "armv7l":  "armv6l",
    "armv6l":  "armv6l",
    "x86_64":  "amd64",
}

APT_PACKAGES = [
    "libopenal-dev", "libopus-dev", "libasound2-dev", "alsa-utils",
    "git", "ffmpeg", "mplayer", "screen",
    "cron",            # demone crontab: non installato di default su Debian 13 (trixie)
    "sqlite3",         # estrazione/preservazione certificato server Mumble
    "mumble-server",
    "build-essential", "curl", "wget", "openssl", "ca-certificates",
    "python3-tk", "python3-flask",
    "rsync",           # richiesto da log2ram con JOURNALD_AWARE=true
    "lz4",             # richiesto da log2ram con ZL2R=true
    "python3-dotenv",  # lettura file .env
]

# "sudo" rimosso: doorphoneserver non deve avere sudo pieno.
# L'accesso privilegiato è gestito tramite /etc/sudoers.d/doorphoneserver-*
USER_GROUPS = ["audio", "gpio", "dialout", "video"]

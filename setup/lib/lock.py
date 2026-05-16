"""Lock file per prevenire esecuzioni parallele del wizard."""

import fcntl
import os
from pathlib import Path
from .constants import LOCK_FILE


class SetupLock:
    def __init__(self):
        self._fd = None

    def acquire(self) -> bool:
        try:
            self._fd = open(LOCK_FILE, "w")
            fcntl.flock(self._fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self._fd.write(str(os.getpid()))
            self._fd.flush()
            return True
        except (IOError, OSError):
            return False

    def release(self):
        if self._fd:
            try:
                fcntl.flock(self._fd, fcntl.LOCK_UN)
                self._fd.close()
                LOCK_FILE.unlink(missing_ok=True)
            except Exception:
                pass

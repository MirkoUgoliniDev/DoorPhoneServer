"""Runner — motore di esecuzione comandi con dry-run e retry."""

import os
import re
import shlex
import shutil
import subprocess
import tempfile
import time
import logging
import threading
from pathlib import Path
from typing import Callable, Optional, Tuple

_abort_event = threading.Event()


def get_abort_event() -> threading.Event:
    return _abort_event


class Runner:
    def __init__(self, dry_run: bool = False):
        self.dry_run   = dry_run
        self._log_cb: Optional[Callable[[str], None]] = None

    def set_log_callback(self, cb: Callable[[str], None]):
        self._log_cb = cb

    def log(self, msg: str):
        logging.info(msg)
        if self._log_cb:
            self._log_cb(msg)
        else:
            print(msg)

    def run(
        self,
        cmd,
        sudo: bool = False,
        user: Optional[str] = None,
        env: Optional[dict] = None,
        cwd: Optional[str] = None,
        retries: int = 1,
        retry_delay: float = 3.0,
        timeout: Optional[int] = 1800,  # 30 min default, None = nessun limite
    ) -> Tuple[bool, str]:
        if isinstance(cmd, str):
            cmd_list = shlex.split(cmd)
        else:
            cmd_list = [str(c) for c in cmd]

        if user and os.geteuid() != 0:
            cmd_list = ["sudo", "-E", "-H", "-u", user] + cmd_list
        elif sudo and os.geteuid() != 0:
            cmd_list = ["sudo"] + cmd_list

        display = " ".join(shlex.quote(c) for c in cmd_list)

        if self.dry_run:
            self.log(f"  [DRY-RUN] {display}")
            return True, ""

        last_err = ""
        for attempt in range(1, retries + 1):
            if _abort_event.is_set():
                return False, "Installazione interrotta dall'utente"
            if attempt > 1:
                self.log(f"  Tentativo {attempt}/{retries}...")
                time.sleep(retry_delay)

            self.log(f"  $ {display}")
            try:
                run_env = os.environ.copy()
                if env:
                    run_env.update(env)
                r = subprocess.run(
                    cmd_list, capture_output=True, text=True,
                    env=run_env, cwd=cwd, timeout=timeout
                )
                if r.stdout.strip():
                    for line in r.stdout.strip().splitlines()[:10]:
                        self.log(f"    {line}")
                if r.returncode != 0:
                    last_err = r.stderr.strip()
                    self.log(f"  ERRORE (exit {r.returncode}): {last_err[:300]}")
                    continue
                return True, r.stdout
            except subprocess.TimeoutExpired:
                last_err = f"Timeout ({timeout}s) scaduto"
                self.log(f"  ERRORE: {last_err}")
            except FileNotFoundError as e:
                last_err = f"Comando non trovato: {e}"
                self.log(f"  ERRORE: {last_err}")
            except Exception as e:
                last_err = str(e)
                self.log(f"  ECCEZIONE: {last_err}")

        return False, last_err

    def shell(self, cmd: str, sudo: bool = False, env: Optional[dict] = None,
              retries: int = 1, timeout: Optional[int] = 1800) -> Tuple[bool, str]:
        if self.dry_run:
            self.log(f"  [DRY-RUN] {cmd}")
            return True, ""

        last_err = ""
        for attempt in range(1, retries + 1):
            if _abort_event.is_set():
                return False, "Interrotto"
            if attempt > 1:
                self.log(f"  Tentativo {attempt}/{retries}...")
                time.sleep(3.0)

            full = ["sudo", "bash", "-c", cmd] if (sudo and os.geteuid() != 0) else ["bash", "-c", cmd]
            self.log(f"  $ {cmd}")
            try:
                run_env = os.environ.copy()
                if env:
                    run_env.update(env)
                r = subprocess.run(full, capture_output=True, text=True,
                                  env=run_env, timeout=timeout)
                if r.stdout.strip():
                    self.log(f"    {r.stdout.strip()[:200]}")
                if r.returncode != 0:
                    last_err = r.stderr.strip()
                    self.log(f"  ERRORE: {last_err[:200]}")
                    continue
                return True, r.stdout
            except subprocess.TimeoutExpired:
                last_err = f"Timeout ({timeout}s) scaduto"
                self.log(f"  ERRORE: {last_err}")
            except Exception as e:
                last_err = str(e)
                self.log(f"  ECCEZIONE: {last_err}")

        return False, last_err

    def copy(self, src: Path, dst: Path, sudo: bool = False) -> bool:
        if self.dry_run:
            self.log(f"  [DRY-RUN] cp {src} → {dst}")
            return True
        try:
            if sudo and os.geteuid() != 0:
                ok, _ = self.run(["cp", str(src), str(dst)], sudo=True)
                return ok
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)
            self.log(f"  Copiato {src.name} → {dst}")
            return True
        except Exception as e:
            self.log(f"  ERRORE copia: {e}")
            return False

    def write(self, path: Path, content: str, sudo: bool = False) -> bool:
        if self.dry_run:
            self.log(f"  [DRY-RUN] write {path} ({len(content)} caratteri)")
            return True
        tmp = None
        try:
            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".tmp", delete=False, dir="/tmp"
            ) as f:
                f.write(content)
                tmp = Path(f.name)

            if sudo and os.geteuid() != 0:
                self.run(["mkdir", "-p", str(path.parent)], sudo=True)
                ok, _ = self.run(["cp", str(tmp), str(path)], sudo=True)
                return ok
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                shutil.move(str(tmp), path)
                tmp = None
                self.log(f"  Scritto {path}")
                return True
        except Exception as e:
            self.log(f"  ERRORE scrittura {path}: {e}")
            return False
        finally:
            if tmp and tmp.exists():
                try:
                    tmp.unlink()
                except Exception:
                    pass

    def check_disk_space(self, sysinfo, needed_gb: float) -> bool:
        free = sysinfo.current_disk_free_gb()
        if free < needed_gb:
            self.log(
                f"  ⚠ Spazio insufficiente: {free:.1f} GB liberi, "
                f"servono almeno {needed_gb:.1f} GB"
            )
            return False
        return True

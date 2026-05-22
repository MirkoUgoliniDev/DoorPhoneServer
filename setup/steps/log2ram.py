"""Passo 12 — Installa Log2Ram."""

from pathlib import Path
from lib.step_base import Step, Status
from lib.constants import TK_USER, REPO_ROOT


class StepLog2Ram(Step):
    def __init__(self):
        super().__init__(
            "Log2Ram",
            "Installa Log2Ram per proteggere la microSD dalle scritture di log",
            optional=True
        )

    def execute(self, runner, sysinfo, config):
        if not config.get("install_log2ram", True):
            runner.log("  Saltato per scelta utente")
            self._set_status(Status.SKIPPED)
            return True

        self._set_status(Status.RUNNING)

        keyring = Path("/usr/share/keyrings/azlux-archive-keyring.gpg")
        ok, _ = runner.run(
            ["wget", "-qO", str(keyring), "https://azlux.fr/repo.gpg"],
            sudo=True, retries=3, retry_delay=5.0
        )
        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        codename_map = {
            "bullseye": "bullseye",
            "bookworm": "bookworm",
            "buster":   "buster",
            "trixie":   "bookworm",
        }
        repo_codename = codename_map.get(sysinfo.codename, "bookworm")

        sources = (
            f"deb [signed-by={keyring}] "
            f"http://packages.azlux.fr/debian/ {repo_codename} main\n"
        )
        runner.write(Path("/etc/apt/sources.list.d/azlux.list"), sources, sudo=True)
        runner.run(["apt-get", "update", "-qq"], sudo=True, retries=2)
        ok, _ = runner.run(["apt-get", "install", "-y", "log2ram"], sudo=True, retries=2)
        if not ok and not runner.dry_run:
            self._set_status(Status.FAILED)
            return False

        # --- Configura /etc/log2ram.conf ---
        # Sovrascrive con valori puliti: deduplicazione JOURNALD_AWARE, SIZE esplicita
        l2r_conf = Path("/etc/log2ram.conf")
        if l2r_conf.exists() or runner.dry_run:
            try:
                existing_l2r = l2r_conf.read_text() if not runner.dry_run else ""
            except PermissionError:
                existing_l2r = ""
            l2r_settings = {
                "SIZE":          config.get("log2ram_size", "128M"),
                "PATH_DISK":     '"/var/log"',
                "JOURNALD_AWARE": "true",
                "ZL2R":          "true" if config.get("log2ram_zl2r", False) else "false",
                "COMP_ALG":      config.get("log2ram_comp_alg", "lz4"),
                "LOG_DISK_SIZE": config.get("log2ram_log_disk_size", "256M"),
            }
            l2r_lines = existing_l2r.splitlines()
            seen_keys = set()
            new_lines = []
            for line in l2r_lines:
                stripped = line.strip()
                key = stripped.split("=")[0] if "=" in stripped and not stripped.startswith("#") else None
                if key and key in l2r_settings:
                    if key not in seen_keys:
                        new_lines.append(f"{key}={l2r_settings[key]}")
                        seen_keys.add(key)
                    # duplicates silently dropped
                else:
                    new_lines.append(line)
            # append any missing keys
            for key, val in l2r_settings.items():
                if key not in seen_keys:
                    new_lines.append(f"{key}={val}")
            runner.write(l2r_conf, "\n".join(new_lines) + "\n", sudo=True)

        journald_path = Path("/etc/systemd/journald.conf")
        journald_additions = {
            "Storage": "volatile",
            "RuntimeMaxUse": "50M",
            "RuntimeMaxFileSize": "10M",
            "MaxRetentionSec": "2week",
        }
        try:
            existing = journald_path.read_text() if journald_path.exists() else "[Journal]\n"
        except PermissionError:
            existing = "[Journal]\n"

        lines = existing.splitlines()
        for key, val in journald_additions.items():
            found = False
            for i, line in enumerate(lines):
                if line.strip().startswith(f"{key}=") or line.strip().startswith(f"#{key}="):
                    lines[i] = f"{key}={val}"
                    found = True
                    break
            if not found:
                lines.append(f"{key}={val}")

        runner.write(journald_path, "\n".join(lines) + "\n", sudo=True)

        # Rimuove /var/log/journal/ se esiste (forza journal volatile)
        runner.run(["rm", "-rf", "/var/log/journal"], sudo=True)
        runner.run(["systemctl", "restart", "systemd-journald"], sudo=True)

        # Installa script setsize per il web panel
        setsize_src = REPO_ROOT / "setup" / "scripts" / "doorphoneserver-log2ram-setsize.sh"
        setsize_dst = Path("/usr/local/sbin/doorphoneserver-log2ram-setsize.sh")
        if setsize_src.exists():
            runner.copy(setsize_src, setsize_dst, sudo=True)
            runner.run(["chmod", "755", str(setsize_dst)], sudo=True)

        sudoers = Path("/etc/sudoers.d/doorphoneserver-log2ram")
        runner.write(
            sudoers,
            f"{TK_USER} ALL=(ALL) NOPASSWD: {setsize_dst}\n"
            f"{TK_USER} ALL=(ALL) NOPASSWD: /usr/bin/log2ram\n",
            sudo=True
        )
        if not runner.dry_run:
            runner.run(["chmod", "440", str(sudoers)], sudo=True)
            ok, out = runner.run(["visudo", "-c", "-f", str(sudoers)], sudo=True)
            if not ok:
                runner.log(f"  ✗ sudoers log2ram non valido: {out}")
                runner.run(["rm", "-f", str(sudoers)], sudo=True)

        # --- Abilita e avvia log2ram ---
        runner.run(["systemctl", "enable", "log2ram"], sudo=True)
        runner.run(["systemctl", "restart", "log2ram"], sudo=True)

        self._set_status(Status.DONE)
        return True

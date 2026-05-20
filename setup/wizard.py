#!/usr/bin/env python3
"""
DoorPhoneServer Setup Wizard — entry point.

Uso:
  python3 setup/wizard.py              # installazione reale
  python3 setup/wizard.py --dry-run    # simulazione (nessuna modifica)
  python3 setup/wizard.py --tui        # forza interfaccia testuale
  python3 setup/wizard.py --gui        # forza interfaccia grafica
  python3 setup/wizard.py --audio-setup
"""

import sys
import os

# Aggiunge setup/ a sys.path in modo che "lib" e "steps" siano importabili
# sia quando invocato come script (python3 setup/wizard.py)
# sia quando importato come modulo (python3 -m setup.wizard)
_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

import re
import shlex
import shutil
import signal
import argparse
import logging
import threading
import subprocess
from pathlib import Path
from typing import List, Optional

from lib.constants import (
    WIZARD_VERSION, LOG_FILE, LOCK_FILE,
    DEFAULT_HOSTNAME, TK_USER,
)
from lib.runner   import Runner, get_abort_event
from lib.sysinfo  import SystemInfo
from lib.lock     import SetupLock
from lib.step_base import Step, Status, STEP_ICONS, validate_hostname
from lib.audio_utils import detect_audio_cards, validate_card_index, generate_asound_conf
from lib.audio_utils import AudioCard
from steps import build_steps

_abort_event = get_abort_event()


# ══════════════════════════════════════════════════════════════════════════════
# LOGGING
# ══════════════════════════════════════════════════════════════════════════════

def setup_file_logging():
    if LOG_FILE.exists():
        try:
            LOG_FILE.open("a").close()
        except PermissionError:
            LOG_FILE.unlink(missing_ok=True)
    logging.basicConfig(
        filename=str(LOG_FILE),
        level=logging.DEBUG,
        format="%(asctime)s %(levelname)s %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    logging.info(f"=== DoorPhoneServer Setup Wizard v{WIZARD_VERSION} avviato ===")


# ══════════════════════════════════════════════════════════════════════════════
# GUI CUSTOMTKINTER
# ══════════════════════════════════════════════════════════════════════════════

def run_tkinter_wizard(steps: List[Step], runner: Runner, sysinfo: SystemInfo) -> bool:
    try:
        import customtkinter as ctk
        from tkinter import messagebox
    except ImportError:
        # Prova ad installare customtkinter al volo
        print("Installo customtkinter...")
        r = subprocess.run(
            [sys.executable, "-m", "pip", "install", "--quiet", "customtkinter"],
            capture_output=True
        )
        try:
            import customtkinter as ctk
            from tkinter import messagebox
        except ImportError:
            # Fallback a tkinter classico
            try:
                import tkinter as tk
                tk.Tk().destroy()
            except Exception:
                return False
            return _run_tkinter_fallback(steps, runner, sysinfo)

    play_cards_initial, cap_cards_initial = detect_audio_cards()

    config: dict = {
        "hostname":           DEFAULT_HOSTNAME,
        "play_card":          play_cards_initial[0].index if play_cards_initial else 1,
        "play_dev":           0,
        "cap_card":           cap_cards_initial[0].index  if cap_cards_initial  else 1,
        "cap_dev":            0,
        "_audio_autodetect":  not play_cards_initial,
        "install_log2ram":    True,
        "install_codeserver": False,
    }

    # Palette colori
    ACCENT    = "#89b4fa"
    SUCCESS   = "#a6e3a1"
    ERROR     = "#f38ba8"
    WARN      = "#f9e2af"
    RUNNING_C = "#89dceb"
    MUTED     = "#6c7086"

    ICON_COLOR = {
        Status.PENDING: MUTED,
        Status.RUNNING: RUNNING_C,
        Status.DONE:    SUCCESS,
        Status.FAILED:  ERROR,
        Status.SKIPPED: MUTED,
    }

    ctk.set_appearance_mode("dark")
    ctk.set_default_color_theme("blue")

    root = ctk.CTk()
    root.title(
        f"DoorPhoneServer Setup Wizard v{WIZARD_VERSION}"
        + ("  [DRY-RUN]" if runner.dry_run else "")
    )
    root.geometry("1020x740")
    root.resizable(True, True)

    def on_sigint(*_):
        _abort_event.set()
        root.quit()
    signal.signal(signal.SIGINT, on_sigint)

    # ── Layout principale ─────────────────────────────────────────────────────
    root.grid_columnconfigure(1, weight=1)
    root.grid_rowconfigure(0, weight=1)

    # ── Sidebar ───────────────────────────────────────────────────────────────
    sidebar = ctk.CTkFrame(root, width=220, corner_radius=0)
    sidebar.grid(row=0, column=0, rowspan=2, sticky="nsew")
    sidebar.grid_rowconfigure(20, weight=1)

    ctk.CTkLabel(
        sidebar,
        text="DoorPhoneServer",
        font=ctk.CTkFont(size=16, weight="bold"),
        text_color=ACCENT,
    ).grid(row=0, column=0, padx=16, pady=(20, 4), sticky="w")

    ctk.CTkLabel(
        sidebar,
        text=f"Setup Wizard v{WIZARD_VERSION}",
        font=ctk.CTkFont(size=10),
        text_color=MUTED,
    ).grid(row=1, column=0, padx=16, pady=(0, 12), sticky="w")

    ctk.CTkLabel(
        sidebar,
        text="PASSI DI INSTALLAZIONE",
        font=ctk.CTkFont(size=9, weight="bold"),
        text_color=MUTED,
    ).grid(row=2, column=0, padx=16, pady=(0, 6), sticky="w")

    step_labels: dict = {}
    for i, step in enumerate(steps):
        frm = ctk.CTkFrame(sidebar, fg_color="transparent")
        frm.grid(row=3 + i, column=0, padx=10, pady=1, sticky="ew")
        frm.grid_columnconfigure(1, weight=1)
        icon_lbl = ctk.CTkLabel(
            frm, text=STEP_ICONS[step.status], width=22,
            font=ctk.CTkFont(size=13), text_color=ICON_COLOR[step.status]
        )
        icon_lbl.grid(row=0, column=0, padx=(4, 2))
        name_lbl = ctk.CTkLabel(
            frm, text=step.name,
            font=ctk.CTkFont(size=11),
            anchor="w"
        )
        name_lbl.grid(row=0, column=1, sticky="w")
        if step.optional:
            ctk.CTkLabel(
                frm, text="opt",
                font=ctk.CTkFont(size=9), text_color=MUTED, width=24
            ).grid(row=0, column=2, padx=4)
        step_labels[i] = (icon_lbl, name_lbl)

    # Info sistema in fondo alla sidebar
    ctk.CTkLabel(
        sidebar,
        text=f"{sysinfo.pi_model}\n{sysinfo.arch} · {sysinfo.codename}\n{sysinfo.disk_free_gb:.1f} GB liberi",
        font=ctk.CTkFont(size=9),
        text_color=MUTED,
        justify="left",
    ).grid(row=21, column=0, padx=16, pady=16, sticky="sw")

    # ── Pannello destro ───────────────────────────────────────────────────────
    right = ctk.CTkFrame(root, fg_color="transparent")
    right.grid(row=0, column=1, padx=16, pady=12, sticky="nsew")
    right.grid_rowconfigure(3, weight=1)
    right.grid_columnconfigure(0, weight=1)

    # Header step corrente
    cur_step_lbl = ctk.CTkLabel(
        right, text="Configura e avvia",
        font=ctk.CTkFont(size=18, weight="bold"),
        text_color=ACCENT, anchor="w"
    )
    cur_step_lbl.grid(row=0, column=0, sticky="w", pady=(0, 2))

    cur_desc_lbl = ctk.CTkLabel(
        right,
        text="Configura le opzioni qui sotto, poi clicca Avvia Installazione",
        font=ctk.CTkFont(size=11),
        text_color=MUTED, anchor="w", wraplength=700, justify="left"
    )
    cur_desc_lbl.grid(row=1, column=0, sticky="w", pady=(0, 12))

    # ── Opzioni configurazione ────────────────────────────────────────────────
    opts = ctk.CTkFrame(right)
    opts.grid(row=2, column=0, sticky="ew", pady=(0, 10))
    opts.grid_columnconfigure(1, weight=1)
    opts.grid_columnconfigure(3, weight=1)

    # Hostname
    ctk.CTkLabel(opts, text="Hostname:", font=ctk.CTkFont(size=12)
                 ).grid(row=0, column=0, padx=(16, 8), pady=8, sticky="w")
    hostname_var = ctk.StringVar(value=DEFAULT_HOSTNAME)
    ctk.CTkEntry(opts, textvariable=hostname_var, width=220
                 ).grid(row=0, column=1, pady=8, sticky="w")
    ctk.CTkLabel(opts, text="nome rete del dispositivo",
                 font=ctk.CTkFont(size=10), text_color=MUTED
                 ).grid(row=0, column=2, columnspan=2, padx=8, sticky="w")

    # Audio OUTPUT
    ctk.CTkLabel(opts, text="Audio OUTPUT:", font=ctk.CTkFont(size=12)
                 ).grid(row=1, column=0, padx=(16, 8), pady=8, sticky="w")
    play_var = ctk.StringVar(value=str(config["play_card"]))
    if play_cards_initial:
        play_combo = ctk.CTkComboBox(
            opts, variable=play_var, width=220,
            values=[f"{c.index} — {c.name}" for c in play_cards_initial],
            state="readonly"
        )
        play_combo.set(f"{play_cards_initial[0].index} — {play_cards_initial[0].name}")
    else:
        play_combo = ctk.CTkEntry(opts, textvariable=play_var, width=80)
        ctk.CTkLabel(opts, text="auto-rilevata dopo pacchetti",
                     font=ctk.CTkFont(size=10), text_color=MUTED
                     ).grid(row=1, column=2, padx=8, sticky="w")
    play_combo.grid(row=1, column=1, pady=8, sticky="w")

    # Audio INPUT
    ctk.CTkLabel(opts, text="Audio INPUT:", font=ctk.CTkFont(size=12)
                 ).grid(row=2, column=0, padx=(16, 8), pady=8, sticky="w")
    cap_var = ctk.StringVar(value=str(config["cap_card"]))
    if cap_cards_initial:
        cap_combo = ctk.CTkComboBox(
            opts, variable=cap_var, width=220,
            values=[f"{c.index} — {c.name}" for c in cap_cards_initial],
            state="readonly"
        )
        cap_combo.set(f"{cap_cards_initial[0].index} — {cap_cards_initial[0].name}")
    else:
        cap_combo = ctk.CTkEntry(opts, textvariable=cap_var, width=80)
    cap_combo.grid(row=2, column=1, pady=8, sticky="w")

    # Switches opzioni
    log2ram_var    = ctk.BooleanVar(value=True)
    codeserver_var = ctk.BooleanVar(value=False)

    sw_frame = ctk.CTkFrame(opts, fg_color="transparent")
    sw_frame.grid(row=3, column=0, columnspan=4, padx=16, pady=(4, 12), sticky="w")

    ctk.CTkSwitch(
        sw_frame, text="Log2Ram  (protegge la microSD)",
        variable=log2ram_var, font=ctk.CTkFont(size=12)
    ).pack(side="left", padx=(0, 32))

    ctk.CTkSwitch(
        sw_frame, text="code-server  (VSCode nel browser)",
        variable=codeserver_var, font=ctk.CTkFont(size=12)
    ).pack(side="left")

    # ── Progress bar ──────────────────────────────────────────────────────────
    prog_var = ctk.DoubleVar(value=0)
    progress = ctk.CTkProgressBar(right, variable=prog_var, height=10)
    progress.grid(row=3, column=0, sticky="ew", pady=(0, 6))
    progress.set(0)

    # ── Log area ──────────────────────────────────────────────────────────────
    log_txt = ctk.CTkTextbox(
        right, font=ctk.CTkFont(family="Courier", size=10),
        state="disabled", wrap="word"
    )
    log_txt.grid(row=4, column=0, sticky="nsew", pady=(0, 8))
    right.grid_rowconfigure(4, weight=1)

    # Tag-like colori via tag_config (CTkTextbox li supporta)
    log_txt._textbox.tag_configure("dry",  foreground=WARN)
    log_txt._textbox.tag_configure("err",  foreground=ERROR)
    log_txt._textbox.tag_configure("step", foreground=ACCENT)
    log_txt._textbox.tag_configure("mut",  foreground=MUTED)

    # ── Bottoni ───────────────────────────────────────────────────────────────
    btn_row = ctk.CTkFrame(root, fg_color="transparent")
    btn_row.grid(row=1, column=1, padx=16, pady=(0, 12), sticky="ew")

    start_btn = ctk.CTkButton(
        btn_row, text="▶  Avvia Installazione",
        font=ctk.CTkFont(size=13, weight="bold"),
        height=40, corner_radius=8,
        fg_color=ACCENT, text_color="#1e1e2e",
        hover_color="#74c7ec"
    )
    start_btn.pack(side="left")

    abort_btn = ctk.CTkButton(
        btn_row, text="■  Interrompi",
        font=ctk.CTkFont(size=12),
        height=40, corner_radius=8,
        fg_color="#45475a", text_color=ERROR,
        hover_color="#585b70",
        state="disabled"
    )
    abort_btn.pack(side="left", padx=8)

    ctk.CTkButton(
        btn_row, text="Esci",
        font=ctk.CTkFont(size=12),
        height=40, corner_radius=8,
        fg_color="transparent", border_width=1,
        command=root.quit
    ).pack(side="right")

    status_lbl = ctk.CTkLabel(
        btn_row, text="", font=ctk.CTkFont(size=11), text_color=MUTED
    )
    status_lbl.pack(side="left", padx=12)

    # ── Callbacks ─────────────────────────────────────────────────────────────
    def append_log(msg: str):
        def _do():
            log_txt.configure(state="normal")
            tag = ""
            if "[DRY-RUN]" in msg:    tag = "dry"
            elif any(k in msg for k in ("ERRORE", "FAIL", "✗", "ECCEZIONE")): tag = "err"
            elif msg.startswith("►"): tag = "step"
            elif "  $" in msg:        tag = "mut"
            if tag:
                log_txt._textbox.insert("end", msg + "\n", tag)
            else:
                log_txt.insert("end", msg + "\n")
            log_txt._textbox.see("end")
            log_txt.configure(state="disabled")
        root.after(0, _do)

    runner.set_log_callback(append_log)

    def on_step_status(step_obj: Step, new_status: Status):
        idx = steps.index(step_obj)
        def _do():
            icon_lbl, _ = step_labels[idx]
            icon_lbl.configure(
                text=STEP_ICONS[new_status],
                text_color=ICON_COLOR[new_status]
            )
            cur_step_lbl.configure(text=step_obj.name)
            cur_desc_lbl.configure(text=step_obj.description)
            prog_val = (idx + (1 if new_status in (Status.DONE, Status.SKIPPED) else 0.5)) / len(steps)
            progress.set(prog_val)
        root.after(0, _do)

    for step in steps:
        step.set_callback(on_step_status)

    def ask_continue_safe(step_name: str) -> bool:
        event  = threading.Event()
        result = [False]
        def show_dialog():
            result[0] = messagebox.askyesno(
                "Errore",
                f"Passo '{step_name}' fallito.\n\nContinuare con il passo successivo?",
            )
            event.set()
        root.after(0, show_dialog)
        event.wait()
        return result[0]

    def run_all():
        def parse_card(var, cards: List[AudioCard]) -> int:
            raw = var.get().split("—")[0].strip() if "—" in var.get() else var.get()
            return validate_card_index(raw, cards)

        config.update({
            "hostname":           validate_hostname(hostname_var.get()),
            "play_card":          parse_card(play_var, play_cards_initial),
            "cap_card":           parse_card(cap_var,  cap_cards_initial),
            "install_log2ram":    log2ram_var.get(),
            "install_codeserver": codeserver_var.get(),
        })

        failed_steps = []
        try:
            for i, step in enumerate(steps):
                if _abort_event.is_set():
                    append_log("\n■ Installazione interrotta dall'utente")
                    break
                append_log(f"\n► Passo {i+1}/{len(steps)}: {step.name}")
                ok = step.execute(runner, sysinfo, config)
                if not ok:
                    failed_steps.append(step.name)
                    if not ask_continue_safe(step.name):
                        break
        except Exception as exc:
            import traceback
            logging.error(f"Eccezione nel thread installazione:\n{traceback.format_exc()}")
            append_log(f"\n✗ ERRORE INATTESO: {exc}")
            def show_crash():
                messagebox.showerror(
                    "Errore inatteso",
                    f"Il wizard ha incontrato un errore:\n\n{exc}\n\nDettagli: {LOG_FILE}"
                )
                start_btn.configure(state="normal", text="▶  Avvia Installazione")
                abort_btn.configure(state="disabled")
            root.after(0, show_crash)
            return

        def finish():
            abort_btn.configure(state="disabled")
            if _abort_event.is_set():
                status_lbl.configure(text="Interrotto")
            elif failed_steps:
                status_lbl.configure(text=f"Completato con {len(failed_steps)} errori",
                                     text_color=WARN)
                messagebox.showwarning(
                    "Completato con errori",
                    "Passi falliti:\n• " + "\n• ".join(failed_steps) +
                    f"\n\nLog completo: {LOG_FILE}"
                )
            else:
                progress.set(1.0)
                status_lbl.configure(text="✓ Completato!", text_color=SUCCESS)
                msg = (
                    f"Simulazione terminata. Nessuna modifica applicata.\n\nLog: {LOG_FILE}"
                    if runner.dry_run else
                    "DoorPhoneServer installato con successo!\n\n"
                    "Esegui: sudo reboot\n\n"
                    "Dopo il riavvio il servizio parte automaticamente.\n\n"
                    f"Log di installazione: {LOG_FILE}"
                )
                title = "DRY-RUN completato" if runner.dry_run else "Installazione completata!"
                messagebox.showinfo(title, msg)
            start_btn.configure(state="normal", text="▶  Avvia Installazione")

        root.after(0, finish)

    def on_start():
        _abort_event.clear()
        start_btn.configure(state="disabled")
        abort_btn.configure(state="normal", command=lambda: _abort_event.set())
        status_lbl.configure(text="Installazione in corso...", text_color=RUNNING_C)
        threading.Thread(target=run_all, daemon=True).start()

    start_btn.configure(command=on_start)
    try:
        root.mainloop()
    except Exception as exc:
        import traceback
        logging.error(f"Crash CTk mainloop:\n{traceback.format_exc()}")
    return True


def _run_tkinter_fallback(steps: List[Step], runner: Runner, sysinfo: SystemInfo) -> bool:
    """Fallback Tkinter classico se customtkinter non è disponibile."""
    import tkinter as tk
    from tkinter import ttk, scrolledtext, messagebox

    play_cards_initial, cap_cards_initial = detect_audio_cards()
    config: dict = {
        "hostname": DEFAULT_HOSTNAME,
        "play_card": play_cards_initial[0].index if play_cards_initial else 1,
        "play_dev": 0,
        "cap_card": cap_cards_initial[0].index if cap_cards_initial else 1,
        "cap_dev": 0,
        "_audio_autodetect": not play_cards_initial,
        "install_log2ram": True,
        "install_codeserver": False,
    }

    BG = "#1e1e2e"; SIDEBAR = "#181825"; SURFACE = "#313244"
    ACCENT = "#89b4fa"; TEXT = "#cdd6f4"; MUTED = "#6c7086"
    SUCCESS = "#a6e3a1"; ERROR = "#f38ba8"; WARN = "#f9e2af"; RUNNING_C = "#89dceb"
    ICON_COLOR = {
        Status.PENDING: MUTED, Status.RUNNING: RUNNING_C,
        Status.DONE: SUCCESS, Status.FAILED: ERROR, Status.SKIPPED: MUTED,
    }

    root = tk.Tk()
    root.title(f"DoorPhoneServer Setup Wizard v{WIZARD_VERSION}" + ("  [DRY-RUN]" if runner.dry_run else ""))
    root.geometry("990x720")
    root.configure(bg=BG)

    def on_sigint(*_):
        _abort_event.set(); root.quit()
    signal.signal(signal.SIGINT, on_sigint)

    main = tk.Frame(root, bg=BG)
    main.pack(fill="both", expand=True, padx=12, pady=10)
    hdr = tk.Frame(main, bg=BG)
    hdr.pack(fill="x", pady=(0, 8))
    tk.Label(hdr, text="DoorPhoneServer Setup Wizard", font=("Helvetica", 16, "bold"), bg=BG, fg=ACCENT).pack(side="left")
    tk.Label(hdr, text=f"{sysinfo.pi_model}  |  {sysinfo.arch}  |  {sysinfo.codename}", font=("Helvetica", 9), bg=BG, fg=MUTED).pack(side="right")
    body = tk.Frame(main, bg=BG)
    body.pack(fill="both", expand=True)

    sidebar_frame = tk.Frame(body, bg=SIDEBAR, width=220)
    sidebar_frame.pack(side="left", fill="y", padx=(0, 10))
    sidebar_frame.pack_propagate(False)
    tk.Label(sidebar_frame, text="PASSI", font=("Helvetica", 8, "bold"), bg=SIDEBAR, fg=MUTED, pady=8).pack(anchor="w", padx=10)

    step_rows: dict = {}
    for i, step in enumerate(steps):
        row = tk.Frame(sidebar_frame, bg=SIDEBAR)
        row.pack(fill="x", padx=6, pady=1)
        icon = tk.Label(row, text=STEP_ICONS[step.status], width=2, font=("Helvetica", 11), bg=SIDEBAR, fg=ICON_COLOR[step.status])
        icon.pack(side="left")
        lbl = tk.Label(row, text=step.name, font=("Helvetica", 9), bg=SIDEBAR, fg=TEXT, anchor="w")
        lbl.pack(side="left", fill="x", expand=True)
        step_rows[i] = (row, icon, lbl)

    right = tk.Frame(body, bg=BG)
    right.pack(side="left", fill="both", expand=True)
    cur_step_lbl = tk.Label(right, text="Configura e avvia", font=("Helvetica", 12, "bold"), bg=BG, fg=ACCENT)
    cur_step_lbl.pack(anchor="w")
    cur_desc_lbl = tk.Label(right, text="Configura le opzioni e clicca Avvia", font=("Helvetica", 9), bg=BG, fg=MUTED, wraplength=600, justify="left")
    cur_desc_lbl.pack(anchor="w", pady=(2, 8))

    opts = tk.Frame(right, bg=SURFACE, pady=8, padx=12)
    opts.pack(fill="x", pady=(0, 8))
    opts.columnconfigure(1, weight=1)
    hostname_var = tk.StringVar(value=DEFAULT_HOSTNAME)
    tk.Label(opts, text="Hostname:", bg=SURFACE, fg=TEXT, font=("Helvetica", 9)).grid(row=0, column=0, sticky="w", padx=(0, 8), pady=3)
    ttk.Entry(opts, textvariable=hostname_var, width=28).grid(row=0, column=1, sticky="w", pady=3)
    play_var = tk.StringVar(value=str(config["play_card"]))
    cap_var  = tk.StringVar(value=str(config["cap_card"]))
    tk.Label(opts, text="Audio OUT (card):", bg=SURFACE, fg=TEXT, font=("Helvetica", 9)).grid(row=1, column=0, sticky="w", padx=(0, 8), pady=3)
    ttk.Entry(opts, textvariable=play_var, width=8).grid(row=1, column=1, sticky="w", pady=3)
    tk.Label(opts, text="Audio IN  (card):", bg=SURFACE, fg=TEXT, font=("Helvetica", 9)).grid(row=2, column=0, sticky="w", padx=(0, 8), pady=3)
    ttk.Entry(opts, textvariable=cap_var, width=8).grid(row=2, column=1, sticky="w", pady=3)
    log2ram_var = tk.BooleanVar(value=True)
    codeserver_var = tk.BooleanVar(value=False)
    tk.Checkbutton(opts, text="Log2Ram", variable=log2ram_var, bg=SURFACE, fg=TEXT, selectcolor=SURFACE, activebackground=SURFACE).grid(row=3, column=0, sticky="w", pady=3)
    tk.Checkbutton(opts, text="code-server", variable=codeserver_var, bg=SURFACE, fg=TEXT, selectcolor=SURFACE, activebackground=SURFACE).grid(row=3, column=1, sticky="w", pady=3)

    prog_var = tk.DoubleVar(value=0)
    ttk.Progressbar(right, variable=prog_var, maximum=len(steps)).pack(fill="x", pady=(0, 6))
    log_txt = scrolledtext.ScrolledText(right, bg="#11111b", fg=TEXT, font=("Courier", 9), state="disabled", relief="flat")
    log_txt.pack(fill="both", expand=True)
    log_txt.tag_configure("dry", foreground=WARN)
    log_txt.tag_configure("err", foreground=ERROR)
    log_txt.tag_configure("step", foreground=ACCENT)
    log_txt.tag_configure("mut", foreground=MUTED)

    btn_row = tk.Frame(main, bg=BG)
    btn_row.pack(fill="x", pady=(8, 0))
    start_btn = tk.Button(btn_row, text="▶  Avvia", font=("Helvetica", 11, "bold"), bg=ACCENT, fg="#1e1e2e", relief="flat", padx=20, pady=6)
    start_btn.pack(side="left")
    abort_btn = tk.Button(btn_row, text="■  Stop", font=("Helvetica", 10), bg="#45475a", fg=ERROR, relief="flat", padx=14, pady=6, state="disabled")
    abort_btn.pack(side="left", padx=8)
    tk.Button(btn_row, text="Esci", font=("Helvetica", 10), bg=SIDEBAR, fg=TEXT, relief="flat", padx=16, pady=6, command=root.quit).pack(side="right")
    status_lbl = tk.Label(btn_row, text="", bg=BG, fg=MUTED, font=("Helvetica", 9))
    status_lbl.pack(side="left", padx=12)

    def append_log(msg: str):
        def _do():
            log_txt.configure(state="normal")
            tag = "dry" if "[DRY-RUN]" in msg else "err" if any(k in msg for k in ("ERRORE","✗","ECCEZIONE")) else "step" if msg.startswith("►") else "mut" if "  $" in msg else ""
            log_txt.insert("end", msg + "\n", tag)
            log_txt.see("end")
            log_txt.configure(state="disabled")
        root.after(0, _do)

    runner.set_log_callback(append_log)

    def on_step_status(step_obj, new_status):
        idx = steps.index(step_obj)
        def _do():
            _, icon_lbl, _ = step_rows[idx]
            icon_lbl.configure(text=STEP_ICONS[new_status], fg=ICON_COLOR[new_status])
            cur_step_lbl.configure(text=step_obj.name)
            cur_desc_lbl.configure(text=step_obj.description)
            prog_var.set(idx + (1 if new_status in (Status.DONE, Status.SKIPPED) else 0.5))
        root.after(0, _do)

    for step in steps:
        step.set_callback(on_step_status)

    def ask_continue_safe(step_name):
        event = threading.Event(); result = [False]
        def show(): result[0] = messagebox.askyesno("Errore", f"Passo '{step_name}' fallito.\nContinuare?"); event.set()
        root.after(0, show); event.wait(); return result[0]

    def run_all():
        config.update({
            "hostname": validate_hostname(hostname_var.get()),
            "play_card": validate_card_index(play_var.get(), play_cards_initial),
            "cap_card": validate_card_index(cap_var.get(), cap_cards_initial),
            "install_log2ram": log2ram_var.get(),
            "install_codeserver": codeserver_var.get(),
        })
        failed_steps = []
        try:
            for i, step in enumerate(steps):
                if _abort_event.is_set(): append_log("\n■ Interrotto"); break
                append_log(f"\n► Passo {i+1}/{len(steps)}: {step.name}")
                ok = step.execute(runner, sysinfo, config)
                if not ok:
                    failed_steps.append(step.name)
                    if not ask_continue_safe(step.name): break
        except Exception as exc:
            logging.error(f"Crash: {exc}")
            append_log(f"\n✗ ERRORE: {exc}")
            root.after(0, lambda: messagebox.showerror("Errore", str(exc)))
            root.after(0, lambda: start_btn.configure(state="normal"))
            return

        def finish():
            abort_btn.configure(state="disabled")
            if not _abort_event.is_set() and not failed_steps:
                prog_var.set(len(steps))
                status_lbl.configure(text="✓ Completato!", fg=SUCCESS)
                messagebox.showinfo("Completato!", "DoorPhoneServer installato!\n\nEsegui: sudo reboot")
            elif failed_steps:
                messagebox.showwarning("Errori", "Passi falliti:\n• " + "\n• ".join(failed_steps))
            start_btn.configure(state="normal", text="▶  Avvia")
        root.after(0, finish)

    def on_start():
        _abort_event.clear()
        start_btn.configure(state="disabled")
        abort_btn.configure(state="normal", command=lambda: _abort_event.set())
        status_lbl.configure(text="In corso...", fg=RUNNING_C)
        threading.Thread(target=run_all, daemon=True).start()

    start_btn.configure(command=on_start)
    try:
        root.mainloop()
    except Exception as exc:
        logging.error(f"Crash Tk: {exc}")
    return True


# ══════════════════════════════════════════════════════════════════════════════
# TUI WHIPTAIL
# ══════════════════════════════════════════════════════════════════════════════

def run_whiptail_wizard(steps: List[Step], runner: Runner, sysinfo: SystemInfo):
    HAS_WH = bool(shutil.which("whiptail"))

    def wh_msg(title: str, msg: str, h=10, w=72):
        if HAS_WH:
            subprocess.run(["whiptail", "--title", title, "--msgbox", msg, str(h), str(w)])
        else:
            print(f"\n[{title}]\n{msg}\n")

    def wh_yesno(title: str, msg: str, h=10, w=72) -> bool:
        if HAS_WH:
            return subprocess.run(
                ["whiptail", "--title", title, "--yesno", msg, str(h), str(w)]
            ).returncode == 0
        resp = input(f"{msg} [s/N] ").strip().lower()
        return resp in ("s", "si", "y", "yes")

    def wh_menu(title: str, msg: str, items: list, h=18, w=72, lh=8) -> Optional[str]:
        if HAS_WH:
            r = subprocess.run(
                ["whiptail", "--title", title, "--menu", msg,
                 str(h), str(w), str(lh)] + items,
                capture_output=True, text=True
            )
            return r.stderr.strip() if r.returncode == 0 else None
        print(f"\n{msg}")
        for i in range(0, len(items), 2):
            print(f"  {items[i]}: {items[i+1]}")
        return input("Scelta: ").strip() or None

    def on_sigint(*_):
        _abort_event.set()
        print("\n\n■ Installazione interrotta (Ctrl+C)")
        sys.exit(1)
    signal.signal(signal.SIGINT, on_sigint)

    mode = "[DRY-RUN] " if runner.dry_run else ""
    wh_msg(
        "DoorPhoneServer Setup Wizard",
        f"{mode}Benvenuto!\n\n"
        f"Modello : {sysinfo.pi_model}\n"
        f"OS      : {sysinfo.codename}\n"
        f"Disco   : {sysinfo.disk_free_gb:.1f} GB liberi\n"
        f"Log     : {LOG_FILE}\n\n"
        "Premi OK per continuare.",
        h=14
    )

    config: dict = {
        "hostname":   DEFAULT_HOSTNAME,
        "play_card": 1, "play_dev": 0,
        "cap_card":  1, "cap_dev":  0,
        "_audio_autodetect": True,
        "install_log2ram":    True,
        "install_codeserver": False,
    }

    # Hostname
    if HAS_WH:
        r = subprocess.run(
            ["whiptail", "--title", "Hostname", "--inputbox",
             "Nome host del dispositivo in rete:", "10", "60", DEFAULT_HOSTNAME],
            capture_output=True, text=True
        )
        if r.returncode == 0 and r.stderr.strip():
            config["hostname"] = validate_hostname(r.stderr.strip())
    else:
        val = input(f"Hostname [{DEFAULT_HOSTNAME}]: ").strip()
        config["hostname"] = validate_hostname(val) if val else DEFAULT_HOSTNAME

    play_cards, cap_cards = detect_audio_cards()

    if not play_cards and not cap_cards:
        wh_msg(
            "Audio",
            "alsa-utils non ancora installato.\n"
            "La scheda audio verrà rilevata automaticamente durante l'installazione.\n\n"
            "Se conosci il numero della scheda puoi inserirlo manualmente\n"
            "(default: 1 per scheda USB esterna).",
            h=13
        )
        if HAS_WH:
            r = subprocess.run(
                ["whiptail", "--title", "Audio OUTPUT", "--inputbox",
                 "Card audio OUTPUT (numero intero):", "8", "50", "1"],
                capture_output=True, text=True
            )
            manual = r.stderr.strip() if r.returncode == 0 else "1"
            config["play_card"] = validate_card_index(manual or "1", [])
            r = subprocess.run(
                ["whiptail", "--title", "Audio INPUT", "--inputbox",
                 "Card audio INPUT (numero intero):", "8", "50", "1"],
                capture_output=True, text=True
            )
            manual = r.stderr.strip() if r.returncode == 0 else "1"
            config["cap_card"] = validate_card_index(manual or "1", [])
        else:
            manual = input("Card audio OUTPUT [default 1]: ").strip()
            config["play_card"] = validate_card_index(manual or "1", [])
            manual = input("Card audio INPUT  [default 1]: ").strip()
            config["cap_card"]  = validate_card_index(manual or "1", [])
        config["_audio_autodetect"] = True
    else:
        if play_cards:
            items = []
            for c in play_cards:
                items += [str(c.index), c.name]
            sel = wh_menu("Audio OUTPUT", "Scheda audio OUTPUT:", items)
            if sel:
                config["play_card"] = validate_card_index(sel, play_cards)
        if cap_cards:
            items = []
            for c in cap_cards:
                items += [str(c.index), c.name]
            sel = wh_menu("Audio INPUT", "Scheda audio INPUT:", items)
            if sel:
                config["cap_card"] = validate_card_index(sel, cap_cards)
        config["_audio_autodetect"] = False

    config["install_log2ram"]    = wh_yesno("Log2Ram",       "Installare Log2Ram? (protegge la microSD)")
    config["install_codeserver"] = wh_yesno("VSCode Server", "Installare code-server (VSCode nel browser)?")

    if not wh_yesno(
        "Conferma",
        f"{mode}Avviare l'installazione?\n\n"
        f"Audio OUT: card {config['play_card']}\n"
        f"Audio IN : card {config['cap_card']}\n"
        f"Log2Ram  : {'sì' if config['install_log2ram'] else 'no'}\n"
        f"VSCode   : {'sì' if config['install_codeserver'] else 'no'}\n"
        f"Log      : {LOG_FILE}"
    ):
        print("Installazione annullata.")
        return

    runner.set_log_callback(print)

    failed_steps = []
    for i, step in enumerate(steps):
        if _abort_event.is_set():
            print("\n■ Interrotto")
            break
        pct = int(i / len(steps) * 100)
        print(f"\n[{pct:3d}%] ► Passo {i+1}/{len(steps)}: {step.name}")
        ok = step.execute(runner, sysinfo, config)
        if not ok:
            failed_steps.append(step.name)
            if not wh_yesno("Errore", f"Passo '{step.name}' fallito.\nContinuare?"):
                break

    if runner.dry_run:
        wh_msg("DRY-RUN completato",
               f"Simulazione terminata.\nNessuna modifica applicata.\nLog: {LOG_FILE}")
    elif not failed_steps:
        wh_msg("Completato!",
               f"DoorPhoneServer installato con successo!\n\nEsegui: sudo reboot\nLog: {LOG_FILE}")
    else:
        wh_msg("Completato con errori",
               "Passi falliti:\n• " + "\n• ".join(failed_steps) +
               f"\n\nLog completo: {LOG_FILE}")


# ══════════════════════════════════════════════════════════════════════════════
# MODALITÀ --audio-setup
# ══════════════════════════════════════════════════════════════════════════════

def run_audio_setup():
    setup_file_logging()
    runner  = Runner(dry_run=False)

    print("=" * 58)
    print("  DoorPhoneServer — Configurazione Audio")
    print("=" * 58)
    print()

    if not shutil.which("aplay"):
        print("✗ alsa-utils non installato. Eseguire prima il setup completo.")
        sys.exit(1)

    play_cards, cap_cards = detect_audio_cards()
    if not play_cards and not cap_cards:
        print("✗ Nessuna scheda audio rilevata. Collega la scheda USB e riprova.")
        sys.exit(1)

    print("Schede OUTPUT disponibili:")
    for c in play_cards:
        print(f"  [{c.index}] {c.name}")
    print()
    print("Schede INPUT disponibili:")
    for c in cap_cards:
        print(f"  [{c.index}] {c.name}")
    print()

    def ask_card(label: str, cards, default: int) -> int:
        if not cards:
            return default
        if len(cards) == 1:
            print(f"{label}: {cards[0].name} (card {cards[0].index}) — unica disponibile")
            return cards[0].index
        try:
            val = input(f"{label} [default {default}]: ").strip()
            return validate_card_index(val or str(default), cards)
        except (EOFError, KeyboardInterrupt):
            return default

    play_card = ask_card("Scheda OUTPUT", play_cards, play_cards[0].index if play_cards else 1)
    cap_card  = ask_card("Scheda INPUT",  cap_cards,  cap_cards[0].index  if cap_cards  else 1)

    print(f"\n  Output: card {play_card}  |  Input: card {cap_card}\n")

    runner.set_log_callback(print)
    asound = generate_asound_conf(play_card, 0, cap_card, 0)
    ok = runner.write(Path("/etc/asound.conf"), asound, sudo=True)
    if not ok:
        print("✗ Impossibile scrivere /etc/asound.conf")
        sys.exit(1)

    from lib.constants import REPO_ROOT
    src_openal = REPO_ROOT / "Configurazioni" / "etc" / "openal" / "alsoft.conf"
    if src_openal.exists():
        runner.run(["mkdir", "-p", "/etc/openal"], sudo=True)
        runner.copy(src_openal, Path("/etc/openal/alsoft.conf"), sudo=True)

    runner.run(["alsactl", "restore"], sudo=True)
    print("\n✓ Configurazione audio completata.")
    print("  Riavvia il servizio: sudo systemctl restart doorphoneserver")
    print(f"  Log: {LOG_FILE}")


# ══════════════════════════════════════════════════════════════════════════════
# PREREQUISITI & MAIN
# ══════════════════════════════════════════════════════════════════════════════

def check_prerequisites() -> SetupLock:
    if os.geteuid() == 0:
        print(
            "⚠ Stai girando come root diretto.\n"
            "  Consigliato: lancia come utente normale (il wizard usa sudo dove serve).\n"
            "  Continuo comunque..."
        )

    if sys.version_info < (3, 6):
        print(f"✗ Python 3.6+ richiesto (hai {sys.version})")
        sys.exit(1)

    lock = SetupLock()
    if not lock.acquire():
        print(
            "✗ Un'altra istanza del wizard è già in esecuzione.\n"
            f"  Se non è così, rimuovi il lock: sudo rm {LOCK_FILE}"
        )
        sys.exit(1)

    return lock


def main():
    parser = argparse.ArgumentParser(
        description="DoorPhoneServer Setup Wizard",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Esempi:\n"
            "  python3 setup/wizard.py              # installazione reale\n"
            "  python3 setup/wizard.py --dry-run    # simulazione sicura\n"
            "  python3 setup/wizard.py --tui        # forza TUI testuale\n"
            "  python3 setup/wizard.py --gui        # forza GUI grafica\n"
        )
    )
    parser.add_argument("--dry-run", action="store_true",
                        help="Simula tutto senza modificare il sistema")
    parser.add_argument("--tui", action="store_true",
                        help="Forza interfaccia testuale whiptail")
    parser.add_argument("--gui", action="store_true",
                        help="Forza interfaccia grafica Tkinter")
    parser.add_argument("--audio-setup", action="store_true",
                        help="Configura solo la scheda audio")
    parser.add_argument("--web", action="store_true",
                        help="Avvia Web UI nel browser (porta 8888)")
    parser.add_argument("--port", type=int, default=8888,
                        help="Porta Web UI (default: 8888)")
    args = parser.parse_args()

    if args.audio_setup:
        run_audio_setup()
        return

    if args.web:
        from webui import run_webui
        run_webui(port=args.port, dry_run=args.dry_run)
        return

    setup_file_logging()
    lock = check_prerequisites()

    try:
        sysinfo = SystemInfo()
        runner  = Runner(dry_run=args.dry_run)

        if args.dry_run:
            print("=" * 62)
            print("  MODALITÀ DRY-RUN")
            print("  Nessuna modifica reale verrà applicata al sistema.")
            print(f"  Log: {LOG_FILE}")
            print("=" * 62)
            print()

        steps = build_steps()

        use_gui = (sysinfo.has_display or args.gui) and not args.tui
        if use_gui:
            launched = run_tkinter_wizard(steps, runner, sysinfo)
            if not launched:
                print("Tkinter non disponibile, uso TUI...")
                run_whiptail_wizard(steps, runner, sysinfo)
        else:
            run_whiptail_wizard(steps, runner, sysinfo)

    finally:
        lock.release()
        logging.info("=== Wizard terminato ===")


if __name__ == "__main__":
    main()

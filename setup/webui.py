#!/usr/bin/env python3
"""
DoorPhoneServer Setup — Web UI
Avvia con: python3 setup/wizard.py --web
oppure:    python3 setup/webui.py
Poi apri:  http://<ip-raspberry>:8888
"""

import sys
import os
import re
import math
import json
import time
import queue
import struct
import subprocess
import threading
import logging
from pathlib import Path

# Pausa visiva (secondi) inserita tra un blocco e l'altro durante l'installazione,
# così l'utente percepisce chiaramente quale step è appena terminato e quale parte.
STEP_VISUAL_PAUSE = 0.6

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

from flask import Flask, Response, request, jsonify, render_template_string

from lib.constants import WIZARD_VERSION, DEFAULT_HOSTNAME, LOG_FILE, REPO_ROOT
from lib.runner   import Runner, get_abort_event
from lib.sysinfo  import SystemInfo
from lib.step_base import Status, STEP_ICONS, validate_hostname
from lib.audio_utils import detect_audio_cards, validate_card_index, get_card_channels
from steps import build_steps

_HELP_DIR = os.path.join(_HERE, "help")

def _load_step_help() -> dict:
    result = {}
    if not os.path.isdir(_HELP_DIR):
        return result
    for fname in os.listdir(_HELP_DIR):
        if fname.endswith(".json"):
            step_name = fname[:-5]
            try:
                with open(os.path.join(_HELP_DIR, fname), encoding="utf-8") as f:
                    result[step_name] = json.load(f)
            except Exception:
                pass
    return result

app = Flask(__name__)
app.config["SECRET_KEY"] = "tk-setup-webui"

import collections

_abort_event  = get_abort_event()
_sysinfo      = SystemInfo()
_dry_run      = False            # impostato da run_webui()
import queue as _queue_mod

_audio_proc    = None
_rec_proc      = None
_rec_buf: collections.deque = collections.deque(maxlen=200)
_rec_thread    = None
_preparing_rec = False
_REC_TMP       = "/tmp/doorphoneserver_rec_test.wav"
_REC_RAW       = "/tmp/doorphoneserver_rec_test.raw"
_play_vu_queue: collections.deque = collections.deque(maxlen=10)

# ── VU mic: singolo worker thread + broadcast a tutti i client SSE ────────────
_vu_proc      = None
_vu_subs: list = []
_vu_subs_lock = threading.Lock()
_vu_worker_thread: threading.Thread = None
_vu_card = 0
_vu_dev  = 0
_subscribers: list = []          # code SSE: lista di queue per broadcast
_state = {
    "running": False,
    "steps":   [],               # [{"name":..,"status":..,"optional":..}]
    "failed":  [],
}

# ── Pausa per input utente (es. Credenziali .env) ─────────────────────────────
_pause_event = threading.Event()
_pause_data:  dict = {}

# ── Rollback ──────────────────────────────────────────────────────────────────
_rollback_proc: subprocess.Popen = None
_rollback_subs: list = []
_rollback_lock = threading.Lock()


# ── Broadcast SSE ─────────────────────────────────────────────────────────────

def _broadcast(event: dict):
    dead = []
    for q in _subscribers:
        try:
            q.put_nowait(event)
        except queue.Full:
            dead.append(q)
    for q in dead:
        _subscribers.remove(q)


def _log_cb(msg: str):
    logging.info(msg)
    _broadcast({"type": "log", "msg": msg})


# ── HTML template ─────────────────────────────────────────────────────────────

HTML = r"""<!DOCTYPE html>
<html lang="it">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>DoorPhoneServer Setup Wizard{% if dry_run %} [DRY-RUN]{% endif %}</title>
<script src="https://cdn.tailwindcss.com"></script>
<style>
  :root { --accent:#89b4fa; --success:#a6e3a1; --error:#f38ba8;
          --warn:#f9e2af; --muted:#6c7086; --run:#89dceb; }
  body  { background:#1e1e2e; color:#cdd6f4; font-family:'Segoe UI',sans-serif; }
  .sidebar { background:#181825; min-height:100vh; }
  .card    { background:#313244; border-radius:.75rem; }
  .badge   { font-size:.65rem; padding:1px 6px; border-radius:99px; }
  .log-box { background:#11111b; font-family:'Courier New',monospace;
             font-size:.8rem; height:340px; overflow-y:auto;
             border-radius:.5rem; padding:12px; white-space:pre-wrap; }
  .log-dry  { color:var(--warn); }
  .log-err  { color:var(--error); }
  .log-step { color:var(--accent); font-weight:bold; }
  .log-muted{ color:var(--muted); }
  .step-icon{ width:22px; text-align:center; display:inline-block; }
  .step-label.clickable{ cursor:pointer; }
  .step-label.clickable:hover{ color:var(--accent) !important; text-decoration:underline; }
  .step-desc{ display:none; }
  .step-desc.visible{ display:block; }
  @keyframes sec-flash { 0%,100%{box-shadow:none} 50%{box-shadow:0 0 0 3px var(--accent)} }
  .sec-highlight{ animation:sec-flash .5s ease 2; }
  .sec-selected{ box-shadow:0 0 0 2px var(--accent); }
  .card { border: 1px solid transparent; transition:background .25s, border-color .25s; }
  @keyframes card-run-glow {
    0%,100% { box-shadow:0 0 0 1px var(--accent), 0 0 10px 0 rgba(137,180,250,.30); }
    50%     { box-shadow:0 0 0 3px var(--accent), 0 0 26px 5px rgba(137,180,250,.65); }
  }
  .card-running {
    border-color:var(--accent) !important;
    border-width:2px !important;
    background:#2a2c44 !important;
    animation:card-run-glow 1.3s ease-in-out infinite;
  }
  .card-done    { border-color:var(--success) !important; }
  .card-failed  { border-color:var(--error) !important; }
  /* Collasso dei blocchi: quando .collapsed è attivo resta solo l'header */
  .step-head { cursor:pointer; user-select:none; }
  .step-chevron { display:inline-block; transition:transform .2s; color:var(--accent); flex-shrink:0; font-size:1.4rem; line-height:1; padding:0 .25rem; }
  .step-head:hover .step-chevron { color:#fff; }
  .card.collapsed .step-chevron { transform:rotate(-90deg); }
  .card.collapsed .step-config { display:none !important; }
  .card.collapsed [id^="card-log-"] { display:none !important; }
  .progress-bar { transition:width .4s ease; }
  @keyframes badge-pulse {
    0%,100% { opacity:1; box-shadow:0 0 0 0 var(--run); }
    50%      { opacity:.75; box-shadow:0 0 0 4px transparent; }
  }
  .badge-running { animation:badge-pulse 1.2s ease-in-out infinite; }
  input[type=text],input[type=number],input[type=password],input[type=email],select {
    background:#1e1e2e !important; border:1px solid #45475a; border-radius:.4rem;
    color:#cdd6f4 !important; padding:.35rem .6rem; width:100%; box-sizing:border-box;
    display:block;
  }
  input:-webkit-autofill,input:-webkit-autofill:focus {
    -webkit-box-shadow:0 0 0 100px #1e1e2e inset !important;
    -webkit-text-fill-color:#cdd6f4 !important; }
  input:focus,select:focus { outline:none; border-color:var(--accent); }
  .btn-primary {
    background:var(--accent); color:#1e1e2e; font-weight:700;
    border-radius:.5rem; padding:.6rem 1.4rem; cursor:pointer;
    transition:opacity .2s;
  }
  .btn-primary:hover  { opacity:.85; }
  .btn-primary:disabled { opacity:.4; cursor:not-allowed; }
  .btn-danger {
    background:#45475a; color:var(--error); font-weight:600;
    border-radius:.5rem; padding:.6rem 1.2rem; cursor:pointer;
  }
  .btn-danger:disabled { opacity:.4; cursor:not-allowed; }
  .toggle { position:relative; display:inline-block; width:44px; height:24px; flex-shrink:0; }
  .toggle input { position:absolute; opacity:0; width:0; height:0; }
  .slider { position:absolute; inset:0; background:#45475a;
            border-radius:12px; transition:background .3s; cursor:pointer; overflow:hidden; }
  .slider:before { content:""; position:absolute; width:18px; height:18px;
                   left:3px; top:50%; transform:translateY(-50%);
                   background:#fff; border-radius:50%; transition:transform .3s;
                   box-shadow:0 1px 3px rgba(0,0,0,.4); }
  input:checked + .slider { background:var(--success); }
  input:checked + .slider:before { transform:translateY(-50%) translateX(20px); }
  /* Custom modal */
  .modal-overlay {
    position:fixed; inset:0; background:rgba(0,0,0,.65); backdrop-filter:blur(3px);
    display:flex; align-items:center; justify-content:center; z-index:9999;
    opacity:0; transition:opacity .2s; pointer-events:none;
  }
  .modal-overlay.open { opacity:1; pointer-events:all; }
  .modal-box {
    background:#313244; border:1px solid #45475a; border-radius:1rem;
    padding:2rem 2rem 1.5rem; max-width:420px; width:90%;
    box-shadow:0 20px 60px rgba(0,0,0,.6);
    transform:scale(.95); transition:transform .2s;
  }
  .modal-overlay.open .modal-box { transform:scale(1); }
</style>
</head>
<body class="flex">

<!-- SIDEBAR -->
<aside class="sidebar w-56 flex-shrink-0 p-4 flex flex-col">
  <div class="mb-6 flex items-center gap-3">
    <img src="/logo.svg" alt="logo" width="48" height="48" style="flex-shrink:0;border-radius:50%">
    <div>
      <div class="text-base font-bold leading-tight" style="color:var(--accent)">DoorPhoneServer</div>
      <div class="text-xs mt-0.5" style="color:var(--muted)">Setup Wizard v{{ version }}</div>
    </div>
  </div>
  <div class="text-xs font-bold mb-2 tracking-widest" style="color:var(--muted)">PASSI</div>
  <ul id="stepList" class="space-y-0 flex-1">
    {% for s in steps %}
    <li id="step-{{ loop.index0 }}" class="text-sm px-2 py-1 rounded" style="color:#cdd6f4">
      <div class="flex items-center gap-2">
        <span class="step-icon flex-shrink-0" id="icon-{{ loop.index0 }}" style="color:var(--muted)">{{ s.icon }}</span>
        <span class="step-label flex-1" data-step="{{ loop.index0 }}"
              data-desc="{{ s.description }}"
              onclick="stepLabelClick('{{ s.name }}', this)">{{ s.name }}</span>
        {% if s.optional %}<span class="badge flex-shrink-0" style="background:#45475a;color:var(--muted)">opt</span>{% endif %}
      </div>
      <div class="step-desc hidden text-xs mt-1 ml-6 leading-4" style="color:var(--muted)"></div>
    </li>
    {% endfor %}
  </ul>
  <div class="text-xs mt-6 leading-5" style="color:var(--muted)">
    {{ sysinfo.pi_model }}<br>
    {{ sysinfo.arch }} · {{ sysinfo.codename }}<br>
    {{ "%.1f"|format(sysinfo.disk_free_gb) }} GB liberi · {{ sysinfo.ram_mb }} MB RAM
  </div>
</aside>

<!-- MAIN -->
<main class="flex-1 p-6 flex flex-col gap-4 overflow-y-auto">

  <!-- DRY-RUN toggle -->
  <div id="dryRunBar" class="card px-4 py-3 flex items-center justify-between gap-4">
    <div class="flex items-center gap-3">
      <label class="toggle">
        <input type="checkbox" id="dryRunToggle" checked
               onchange="onDryRunToggle()">
        <span class="slider"></span>
      </label>
      <div>
        <span class="font-bold text-sm" id="dryRunLabel">⚠ DRY-RUN attivo</span>
        <p class="text-xs mt-0.5" id="dryRunDesc" style="color:var(--muted)">
          Simulazione — nessuna modifica al sistema
        </p>
      </div>
    </div>
    <div id="dryRunBadge" class="badge px-3 py-1 text-xs font-bold"
         style="background:#3d3000;color:var(--warn);border:1px solid var(--warn)">
      SIMULAZIONE
    </div>
  </div>

  <!-- Buttons + status -->
  <div id="btnBar" class="flex items-center gap-3 flex-wrap">
    <button id="startBtn" class="btn-primary" onclick="startInstall()">▶&nbsp; Avvia Installazione</button>
    <button id="abortBtn" class="btn-danger" disabled onclick="abortInstall()">■&nbsp; Interrompi</button>
    {% if rollback_available %}
    <button id="rollbackBtn" class="btn-danger" onclick="startRollback()"
      style="background:transparent;border:1.5px solid var(--error);color:var(--error)"
      title="Rimuove tutto ciò che il wizard ha installato">⎌&nbsp; Rollback</button>
    {% endif %}
    <span id="statusText" class="text-sm ml-4" style="color:var(--muted)"></span>
  </div>

  <!-- Rollback panel (hidden until rollback starts) -->
  <div id="rollbackPanel" style="display:none" class="card p-5 flex flex-col gap-3">
    <div class="flex items-center justify-between">
      <span id="rollbackTitle" class="font-semibold" style="color:var(--error);font-size:1rem">⎌ Rollback in corso…</span>
      <button id="rollbackAbortBtn" onclick="abortRollback()"
        style="font-size:.75rem;padding:.3rem .9rem;border-radius:.4rem;border:1px solid var(--error);background:transparent;color:var(--error);cursor:pointer">
        ■ Interrompi
      </button>
    </div>
    <div class="log-box" id="rollbackLogBox" style="height:420px;font-size:.75rem"></div>
    <div class="flex items-center justify-between">
      <div id="rollbackStatus" class="text-sm" style="color:var(--muted)"></div>
      <button id="rollbackDoneBtn" style="display:none" class="btn-primary" onclick="location.reload()">
        ▶ Ricomincia installazione
      </button>
    </div>
  </div>

  <!-- Progress bar -->
  <div id="progressBarCard" class="card p-4">
    <div class="flex justify-between text-xs mb-2" style="color:var(--muted)">
      <span id="progressLabel">In attesa</span>
      <span id="progressPct">0%</span>
    </div>
    <div class="w-full rounded-full h-2" style="background:#45475a">
      <div id="progressBar" class="progress-bar h-2 rounded-full" style="width:0%;background:var(--accent)"></div>
    </div>
  </div>

  <!-- Step cards -->
  <div id="configSection" class="flex flex-col gap-3">
    {% for s in steps %}
    <div id="step-card-{{ loop.index0 }}" class="card p-4 flex flex-col gap-0">

      <!-- Header always visible — click per collassare/espandere il blocco -->
      <div class="flex items-center gap-3 step-head" onclick="toggleStepCard({{ loop.index0 }}, event)" title="Clicca per espandere/collassare">
        <span id="icon-{{ loop.index0 }}" style="color:var(--muted);font-size:1.1rem;width:1.5rem;text-align:center;flex-shrink:0">{{ s.icon }}</span>
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="font-semibold text-sm">{{ s.name }}</span>
            {% if s.optional %}<span class="badge" style="background:#45475a;color:var(--muted)">opt</span>{% endif %}
            <span id="card-badge-{{ loop.index0 }}" class="badge text-xs" style="display:none"></span>
          </div>
          <p class="text-xs mt-0.5 truncate" style="color:var(--muted)" title="{{ s.description }}">{{ s.description }}</p>
        </div>
        {% if s.name in step_help %}
        <button data-stepname="{{ s.name }}" onclick="event.stopPropagation();openStepHelp(this.dataset.stepname)" title="Dettagli passo"
          style="flex-shrink:0;width:1.6rem;height:1.6rem;border-radius:50%;border:1.5px solid #cba6f7;background:#1e1e2e;color:#cba6f7;font-size:.72rem;font-weight:700;cursor:pointer;display:flex;align-items:center;justify-content:center;transition:background .15s,box-shadow .15s"
          onmouseover="this.style.background='#cba6f7';this.style.color='#1e1e2e';this.style.boxShadow='0 0 8px #cba6f780'"
          onmouseout="this.style.background='#1e1e2e';this.style.color='#cba6f7';this.style.boxShadow='none'">i</button>
        {% endif %}
        <span class="step-chevron">▼</span>
      </div>

      <!-- Config section (solo per step configurabili) -->
      {% if s.name == 'Controllo Sistema' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.4rem .75rem">
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">MODELLO</span>
            <p class="text-sm mt-0.5">{{ sysinfo.pi_model }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">OS</span>
            <p class="text-sm mt-0.5">{{ sysinfo.codename }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">ARCH</span>
            <p class="text-sm mt-0.5">{{ sysinfo.arch }} · Go: {{ sysinfo.go_arch }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">RAM</span>
            <p class="text-sm mt-0.5">{{ sysinfo.ram_mb }} MB</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">DISCO LIBERO</span>
            <p class="text-sm mt-0.5" style="color:{% if sysinfo.disk_free_gb < 3 %}var(--error){% else %}var(--success){% endif %}">
              {{ "%.1f"|format(sysinfo.disk_free_gb) }} GB{% if sysinfo.disk_free_gb < 3 %} ✗ &lt; 3 GB richiesti{% else %} ✓{% endif %}
            </p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">BOOT CFG</span>
            <p class="text-xs mt-0.5" style="color:var(--muted);word-break:break-all">{{ sysinfo.boot_config }}</p>
          </div>
        </div>
        <p class="text-xs mt-2" style="color:var(--muted)">Sudo e connessione internet verificati all'avvio installazione.</p>
      </div>

      {% elif s.name == 'Credenziali .env' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <!-- Mumble -->
        <div class="flex flex-col gap-2 mb-3">
          <span class="text-xs font-bold" style="color:var(--accent)">MUMBLE SERVER</span>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:.75rem">
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">Username</label>
              <input type="text" id="env_mumble_username" value="Doorpi">
            </div>
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">Password</label>
              <div style="position:relative;width:100%">
                <input type="password" id="env_mumble_password" placeholder="password server Mumble">
                <button type="button" onclick="togglePwd('env_mumble_password')" style="position:absolute;right:.5rem;top:50%;transform:translateY(-50%);color:var(--muted)">👁</button>
              </div>
            </div>
          </div>
          <div class="mt-2">
            <span class="text-xs font-semibold" style="color:var(--muted)">Certificato server (opzionale) — caricalo per non dover riconfigurare i tablet dopo la migrazione</span>
            <div style="display:grid;grid-template-columns:1fr 1fr;gap:.75rem;margin-top:.35rem">
              <div>
                <label class="block text-xs mb-1" style="color:var(--muted)">Certificato (cert.pem)</label>
                <input type="file" id="mumble_cert_file" accept=".pem,.crt,.cer" onchange="loadPem(this,'cert')">
                <div id="mumble_cert_status" class="text-xs mt-1" style="color:var(--muted)"></div>
              </div>
              <div>
                <label class="block text-xs mb-1" style="color:var(--muted)">Chiave privata (key.pem)</label>
                <input type="file" id="mumble_key_file" accept=".pem,.key" onchange="loadPem(this,'key')">
                <div id="mumble_key_status" class="text-xs mt-1" style="color:var(--muted)"></div>
              </div>
            </div>
          </div>
        </div>
        <hr style="border-color:#313244;margin-bottom:.75rem">
        <!-- Camera IP -->
        <div class="flex flex-col gap-2 mb-3">
          <span class="text-xs font-bold" style="color:var(--accent)">CAMERA IP</span>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:.75rem">
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">Username</label>
              <input type="text" id="env_camera_username" placeholder="utente camera IP">
            </div>
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">Password</label>
              <div style="position:relative;width:100%">
                <input type="password" id="env_camera_password" placeholder="password camera IP">
                <button type="button" onclick="togglePwd('env_camera_password')" style="position:absolute;right:.5rem;top:50%;transform:translateY(-50%);color:var(--muted)">👁</button>
              </div>
            </div>
          </div>
        </div>
        <hr style="border-color:#313244;margin-bottom:.75rem">
        <!-- Pushover -->
        <div class="flex flex-col gap-2 mb-3">
          <span class="text-xs font-bold" style="color:var(--accent)">PUSHOVER</span>
          <div style="display:grid;grid-template-columns:1fr 1fr;gap:.75rem">
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">API Token</label>
              <input type="text" id="env_pushover_token" placeholder="token app Pushover">
            </div>
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">User Key</label>
              <input type="text" id="env_pushover_key" placeholder="user key Pushover">
            </div>
          </div>
        </div>
        <hr style="border-color:#313244;margin-bottom:.75rem">
        <!-- OpenRouter -->
        <div class="flex flex-col gap-2 mb-3">
          <span class="text-xs font-bold" style="color:var(--accent)">OPENROUTER</span>
          <div style="position:relative;width:100%">
            <input type="password" id="env_openrouter_key" placeholder="sk-or-v1-...">
            <button type="button" onclick="togglePwd('env_openrouter_key')" style="position:absolute;right:.5rem;top:50%;transform:translateY(-50%);color:var(--muted)">👁</button>
          </div>
        </div>
        <div class="flex items-center gap-3">
          <button class="btn-primary" style="font-size:.85rem;padding:.4rem 1rem" onclick="saveEnv()">💾 Salva .env ora</button>
          <button id="resumeBtn" class="btn-primary" style="display:none;font-size:.95rem;padding:.5rem 1.4rem;background:var(--success);color:#1e1e2e" onclick="resumeInstall()">▶ Continua installazione</button>
          <span id="envSaveMsg" class="text-xs" style="color:var(--success)"></span>
        </div>
      </div>

      {% elif s.name == 'Hostname' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">HOSTNAME</label>
        <input type="text" id="hostname" value="{{ default_hostname }}" placeholder="doorphoneserver">
        <p class="text-xs mt-1" style="color:var(--muted)">Nome del dispositivo in rete</p>
      </div>

      {% elif s.name == 'Configurazione Audio' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244;display:grid;grid-template-columns:1fr 1fr;gap:.75rem;align-items:end">
        <div>
          <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">AUDIO OUTPUT (card n.)</label>
          <select id="playCard" onchange="_audioUserChose=true;_updateAudioModalCards()"><option value="0">0 — rilevamento...</option></select>
        </div>
        <div>
          <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">AUDIO INPUT (card n.)</label>
          <select id="capCard" onchange="_audioUserChose=true;_updateAudioModalCards()"><option value="0">0 — rilevamento...</option></select>
        </div>
        <div class="col-span-2 mt-1 flex gap-2 flex-wrap" style="grid-column:1/-1">
          <button onclick="refreshCards(this)" class="btn-primary" style="background:#45475a;color:#cdd6f4;font-size:.85rem;padding:.4rem 1.1rem">↺ Aggiorna schede</button>
          <button id="testAudioBtn" onclick="openAudioModal()" class="btn-primary" style="background:#cba6f7;color:#1e1e2e;font-size:.85rem;padding:.4rem 1.1rem">🔊 Test Audio &amp; Volumi</button>
          <button id="audioResumeBtn" onclick="resumeAudio()" class="btn-primary" style="display:none;font-size:.9rem;padding:.4rem 1.2rem;background:var(--success);color:#1e1e2e">▶ Prosegui</button>
        </div>
      </div>

      {% elif s.name == 'Log2Ram' %}
      <div class="step-config mt-3 pt-3 flex flex-col gap-3" style="border-top:1px solid #313244">
        <div class="flex items-center gap-3">
          <label class="toggle"><input type="checkbox" id="log2ram" checked onchange="toggleLog2RamParams()"><span class="slider"></span></label>
          <span class="text-sm">Installa Log2Ram</span>
        </div>
        <div id="log2ramParams" class="flex flex-col gap-3">
          <div>
            <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">SIZE — Dimensione RAM per i log</label>
            <input type="text" id="log2ram_size" value="128M" placeholder="es. 128M">
          </div>
          <div class="flex items-center gap-3">
            <label class="toggle"><input type="checkbox" id="log2ram_zl2r" onchange="toggleZramParams()"><span class="slider"></span></label>
            <div>
              <span class="text-sm">ZL2R — Usa zram</span>
              <span class="text-xs block" style="color:var(--muted)">Compressione log in RAM</span>
            </div>
          </div>
          <div id="zramParams" style="display:none">
            <div class="mb-2">
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">COMP_ALG</label>
              <select id="log2ram_comp_alg">
                <option value="lz4">lz4 (più veloce)</option>
                <option value="lzo">lzo</option>
                <option value="zstd">zstd (più compresso)</option>
                <option value="zlib">zlib</option>
              </select>
            </div>
            <div>
              <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">LOG_DISK_SIZE</label>
              <input type="text" id="log2ram_log_disk_size" value="256M" placeholder="es. 256M">
            </div>
          </div>
        </div>
      </div>

      {% elif s.name == 'Pacchetti APT' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div class="text-xs font-semibold mb-2" style="color:var(--muted)">{{ apt_packages|length }} PACCHETTI DA INSTALLARE</div>
        <div style="display:flex;flex-wrap:wrap;gap:.3rem">
          {% for pkg in apt_packages %}
          <span style="background:#1e1e2e;border:1px solid #45475a;border-radius:.3rem;padding:.1rem .45rem;font-size:.7rem;font-family:'Courier New',monospace;color:#cdd6f4">{{ pkg }}</span>
          {% endfor %}
        </div>
      </div>

      {% elif s.name == 'Go Language' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.4rem .75rem">
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">VERSIONE</span>
            <p class="text-sm mt-0.5">Go {{ go_version }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">ARCH TARGET</span>
            <p class="text-sm mt-0.5">linux/{{ sysinfo.go_arch }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">INSTALL PATH</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">/usr/local/go</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">TARBALL</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">go{{ go_version }}.linux-{{ sysinfo.go_arch }}.tar.gz</p>
          </div>
        </div>
      </div>

      {% elif s.name == 'Utente di Sistema' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.4rem .75rem">
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">UTENTE</span>
            <p class="text-sm mt-0.5 font-mono">{{ tk_user }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">HOME</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">/home/{{ tk_user }}/</p>
          </div>
          <div style="grid-column:1/-1">
            <span class="text-xs font-semibold" style="color:var(--muted)">GRUPPI</span>
            <div style="display:flex;flex-wrap:wrap;gap:.3rem;margin-top:.3rem">
              {% for g in user_groups %}
              <span style="background:#1e1e2e;border:1px solid #45475a;border-radius:.3rem;padding:.1rem .45rem;font-size:.75rem;font-family:'Courier New',monospace;color:#cdd6f4">{{ g }}</span>
              {% endfor %}
            </div>
          </div>
        </div>
      </div>

      {% elif s.name == 'Mumble Server' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.4rem .75rem">
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">SERVIZIO</span>
            <p class="text-sm mt-0.5">mumble-server</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">PORTA</span>
            <p class="text-sm mt-0.5">64738 TCP/UDP</p>
          </div>
          <div style="grid-column:1/-1">
            <span class="text-xs font-semibold" style="color:var(--muted)">SCRIPT</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">setup/scripts/setup_mumble.sh</p>
          </div>
        </div>
      </div>

      {% elif s.name == 'Config Boot RPi' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div class="text-xs font-semibold mb-2" style="color:var(--muted)">AZIONI DI setup_configs.sh</div>
        <div style="display:flex;flex-direction:column;gap:.3rem">
          <div class="text-xs" style="color:#cdd6f4">› Configura ALSA <span style="font-family:monospace;color:var(--muted)">(/etc/asound.conf)</span> con le schede selezionate</div>
          <div class="text-xs" style="color:#cdd6f4">› Configura OpenAL <span style="font-family:monospace;color:var(--muted)">(/etc/openal/alsoft.conf)</span></div>
          <div class="text-xs" style="color:#cdd6f4">› Blacklist moduli WiFi inutilizzati</div>
          <div class="text-xs" style="color:#cdd6f4">› Aggiorna <span style="font-family:monospace;color:var(--muted)">{{ sysinfo.boot_config }}</span> (GPU mem, audio, overlay)</div>
        </div>
      </div>

      {% elif s.name == 'Clone & Build' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.4rem .75rem">
          <div style="grid-column:1/-1">
            <span class="text-xs font-semibold" style="color:var(--muted)">REPOSITORY</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">github.com/MirkoUgoliniDev/DoorPhoneServer</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">GOPATH</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">{{ gopath }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">BINARIO OUTPUT</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">{{ gobin }}/doorphoneserver</p>
          </div>
        </div>
        <p class="text-xs mt-2" style="color:var(--muted)">⏱ go build può richiedere 5–15 min su Pi 4.</p>
      </div>

      {% elif s.name == 'Directory & Certificati' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:flex;flex-direction:column;gap:.35rem">
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">PREFERENCES</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">/home/{{ tk_user }}/preferences/</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">CERTIFICATO TLS</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">/home/{{ tk_user }}/mumble.pem &nbsp;(RSA 4096, 3 anni)</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">CONFIG MUMBLE CLIENT</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">/home/{{ tk_user }}/doorphoneserver.xml</p>
          </div>
        </div>
      </div>

      {% elif s.name == 'Servizio Systemd' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:.4rem .75rem">
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">SERVIZIO</span>
            <p class="text-sm mt-0.5">doorphoneserver.service</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">UTENTE</span>
            <p class="text-sm mt-0.5 font-mono">{{ tk_user }}</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">SUDOERS</span>
            <p class="text-xs mt-0.5 font-mono" style="color:var(--muted)">/etc/sudoers.d/doorphoneserver-panel</p>
          </div>
          <div>
            <span class="text-xs font-semibold" style="color:var(--muted)">CRONTAB</span>
            <p class="text-xs mt-0.5" style="color:var(--muted)">Riavvii notturni + restart tablet</p>
          </div>
        </div>
      </div>

      {% elif s.name == 'Pulizia' %}
      <div class="step-config mt-3 pt-3" style="border-top:1px solid #313244">
        <div style="display:flex;flex-direction:column;gap:.3rem">
          <div class="text-xs" style="color:#cdd6f4">› Cache Go <span style="font-family:monospace;color:var(--muted)">(~/go/pkg)</span> rimossa se presente</div>
          <div class="text-xs" style="color:#cdd6f4">› <span style="font-family:monospace;color:var(--muted)">apt-get clean</span> — libera cache pacchetti</div>
        </div>
      </div>

      {% endif %}

      <!-- Log area (hidden, shown during/after execution) -->
      <div id="card-log-{{ loop.index0 }}" style="display:none;margin-top:.75rem">
        <div class="log-box" id="card-logbox-{{ loop.index0 }}" style="max-height:180px;font-size:.75rem"></div>
      </div>

    </div>
    {% endfor %}
  </div>

</main>

<!-- ── STEP HELP MODAL ────────────────────────────────────────────────────── -->
<div id="stepHelpModal" class="fixed inset-0 z-50 hidden"
     style="background:rgba(0,0,0,0.75)" onclick="if(event.target===this)closeStepHelp()">
  <div class="flex items-center justify-center min-h-screen p-4">
    <div class="card w-full max-w-lg flex flex-col gap-4 p-6" style="max-height:85vh;overflow-y:auto">
      <div class="flex items-center justify-between">
        <h2 id="helpModalTitle" class="text-base font-bold" style="color:#cba6f7"></h2>
        <button onclick="closeStepHelp()" class="text-lg px-2" style="color:var(--muted)">✕</button>
      </div>
      <p id="helpModalSummary" class="text-sm" style="color:#cdd6f4;line-height:1.6"></p>
      <div id="helpModalSections" class="flex flex-col gap-3"></div>
      <p id="helpModalNote" class="text-xs rounded p-3" style="display:none;background:#1e1e2e;border:1px solid #313244;color:var(--muted);line-height:1.6"></p>
    </div>
  </div>
</div>

<!-- ── AUDIO TEST MODAL ───────────────────────────────────────────────────── -->
<div id="audioModal" class="fixed inset-0 z-50 hidden"
     style="background:rgba(0,0,0,0.75)" onclick="if(event.target===this)closeAudioModal()">
  <div class="flex items-center justify-center min-h-screen p-4">
    <div class="card w-full max-w-xl flex flex-col gap-4 p-6" style="max-height:90vh;overflow-y:auto">

      <!-- Header -->
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-bold" style="color:#cba6f7">🔊 Test Audio &amp; Volumi</h2>
        <button onclick="closeAudioModal()" class="text-lg px-2" style="color:var(--muted)">✕</button>
      </div>
      <!-- Schede attualmente selezionate (riflette i select OUTPUT/INPUT del wizard) -->
      <div id="audioModalCards" class="text-xs -mt-1 mb-1" style="color:#cdd6f4">—</div>

      <!-- TEST PLAY -->
      <div class="rounded-lg p-4" style="background:#11111b;border:1px solid #313244">
        <div class="flex items-center justify-between mb-2">
          <span class="text-sm font-bold tracking-widest" style="color:var(--muted)">TEST PLAY</span>
        </div>
        <select id="playFileSelect" class="w-full mb-2" style="font-size:.8rem">
          <option value="">— Caricamento file... —</option>
        </select>
        <div class="flex gap-2 flex-wrap">
          <button onclick="playFile()"
                  class="btn-primary" style="font-size:.8rem;padding:.35rem .9rem">
            ▶ Play
          </button>
          <button onclick="stopAudio()"
                  class="btn-danger" style="font-size:.8rem;padding:.35rem .9rem">
            ■ Stop
          </button>
        </div>
      </div>

      <!-- INPUT -->
      <div class="rounded-lg p-4" style="background:#11111b;border:1px solid #313244">
        <div class="flex items-center justify-between mb-3">
          <span class="text-sm font-bold tracking-widest" style="color:var(--muted)">INPUT</span>
        </div>
        <div class="flex gap-2 mb-3 flex-wrap items-center">
          <button id="recStartBtn" onclick="recStart()"
                  class="btn-primary" style="font-size:.8rem;padding:.35rem .9rem;background:var(--success);color:#1e1e2e">
            ⏺ Registra
          </button>
          <button id="recStopBtn" onclick="recStop()" disabled
                  class="btn-primary" style="font-size:.8rem;padding:.35rem .9rem;background:var(--error);color:#1e1e2e;opacity:.4">
            ■ Stop
          </button>
          <button id="recPlayBtn" onclick="recPlay()" disabled
                  class="btn-primary" style="font-size:.8rem;padding:.35rem .9rem;opacity:.4">
            ▶ Riproduci
          </button>
        </div>
      </div>

      <!-- VOLUMI -->
      <div class="rounded-lg p-4" style="background:#11111b;border:1px solid #313244">
        <div class="flex items-center justify-between mb-3">
          <span class="text-sm font-bold tracking-widest" style="color:var(--muted)">VOLUMI</span>
          <span id="audioPlayLabel" class="text-xs" style="color:var(--muted)"></span>
        </div>
        <div id="playVolumes" class="flex flex-col gap-3"></div>

        <!-- VU meter Speaker (attivo solo durante Test tono) -->
        <div class="mt-3">
          <div class="flex items-center gap-2 mb-1">
            <span class="text-xs w-20 text-right flex-shrink-0" style="color:var(--muted)">Speaker</span>
            <span id="playVuDb" class="text-xs font-mono ml-auto" style="color:var(--muted)">-- dB</span>
          </div>
          <div id="playVuMeter" class="flex flex-col gap-1">
            <div class="flex items-center gap-1">
              <span id="playVuLabelL" class="text-xs font-mono flex-shrink-0" style="color:var(--muted);width:1rem">L</span>
              <div class="relative flex-1 rounded overflow-hidden" style="height:10px;background:#313244">
                <div id="playVuBarL" style="height:100%;width:0%;background:linear-gradient(to right,#a6e3a1 0%,#f9e2af 65%,#f38ba8 85%);transition:width 80ms linear;border-radius:inherit"></div>
                <div id="playVuPeakL" style="position:absolute;top:0;bottom:0;width:2px;background:#f38ba8;left:0%;transition:left 80ms linear"></div>
              </div>
            </div>
            <div id="playVuRowR" class="flex items-center gap-1">
              <span class="text-xs font-mono flex-shrink-0" style="color:var(--muted);width:1rem">R</span>
              <div class="relative flex-1 rounded overflow-hidden" style="height:10px;background:#313244">
                <div id="playVuBarR" style="height:100%;width:0%;background:linear-gradient(to right,#a6e3a1 0%,#f9e2af 65%,#f38ba8 85%);transition:width 80ms linear;border-radius:inherit"></div>
                <div id="playVuPeakR" style="position:absolute;top:0;bottom:0;width:2px;background:#f38ba8;left:0%;transition:left 80ms linear"></div>
              </div>
            </div>
          </div>
          <div class="flex justify-between text-xs mt-0.5" style="color:var(--muted);padding-left:1.25rem">
            <span>-60</span><span>-40</span><span>-20</span><span>-10</span><span>0 dB</span>
          </div>
        </div>

        <!-- VU meter Microfono (sempre attivo) -->
        <div class="mt-2">
          <div class="flex items-center gap-2 mb-1">
            <span class="text-xs w-20 text-right flex-shrink-0" style="color:var(--muted)">Mic 🎤</span>
            <span id="vuDb" class="text-xs font-mono ml-auto" style="color:var(--muted)">-- dB</span>
          </div>
          <div id="vuMeter" class="flex flex-col gap-1">
            <div class="flex items-center gap-1">
              <span id="vuLabelL" class="text-xs font-mono flex-shrink-0" style="color:var(--muted);width:1rem">L</span>
              <div class="relative flex-1 rounded overflow-hidden" style="height:10px;background:#313244">
                <div id="vuBarL" style="height:100%;width:0%;background:linear-gradient(to right,#a6e3a1 0%,#f9e2af 65%,#f38ba8 85%);transition:width 80ms linear;border-radius:inherit"></div>
                <div id="vuPeakL" style="position:absolute;top:0;bottom:0;width:2px;background:#f38ba8;left:0%;transition:left 80ms linear"></div>
              </div>
            </div>
            <div id="vuRowR" class="flex items-center gap-1">
              <span class="text-xs font-mono flex-shrink-0" style="color:var(--muted);width:1rem">R</span>
              <div class="relative flex-1 rounded overflow-hidden" style="height:10px;background:#313244">
                <div id="vuBarR" style="height:100%;width:0%;background:linear-gradient(to right,#a6e3a1 0%,#f9e2af 65%,#f38ba8 85%);transition:width 80ms linear;border-radius:inherit"></div>
                <div id="vuPeakR" style="position:absolute;top:0;bottom:0;width:2px;background:#f38ba8;left:0%;transition:left 80ms linear"></div>
              </div>
            </div>
          </div>
          <div class="flex justify-between text-xs mt-0.5" style="color:var(--muted);padding-left:1.25rem">
            <span>-60</span><span>-40</span><span>-20</span><span>-10</span><span>0 dB</span>
          </div>
        </div>

        <!-- AGC -->
        <div id="agcRow" class="flex items-center gap-2 mt-3" style="display:none!important">
          <label class="toggle">
            <input type="checkbox" id="agcToggle" onchange="setAGC(this.checked)">
            <span class="slider"></span>
          </label>
          <span class="text-xs" style="color:var(--muted)">AGC (Auto Gain Control microfono)</span>
        </div>
      </div>

      <!-- Log -->
      <div id="audioLog" class="log-box" style="height:90px"></div>

      <div class="flex justify-end">
        <button onclick="closeAudioModal()"
                class="btn-danger" style="padding:.45rem 1.2rem">Chiudi</button>
      </div>
    </div>
  </div>
</div>

<script>
const N_STEPS = {{ n_steps }};
let DRY_RUN = true;  // default sicuro: l'utente deve disattivarlo per installare davvero
let currentStepIdx = -1;
let evtSource = null;

function onDryRunToggle() {
  DRY_RUN = document.getElementById('dryRunToggle').checked;
  document.getElementById('dryRunLabel').textContent =
    DRY_RUN ? '⚠ DRY-RUN attivo' : 'Installazione reale';
  document.getElementById('dryRunDesc').textContent =
    DRY_RUN ? 'Simulazione — nessuna modifica al sistema' : 'Le modifiche verranno applicate';
  const badge = document.getElementById('dryRunBadge');
  if (DRY_RUN) {
    badge.textContent = 'SIMULAZIONE';
    badge.style.cssText = 'background:#3d3000;color:var(--warn);border:1px solid var(--warn)';
  } else {
    badge.textContent = 'REALE';
    badge.style.cssText = 'background:#1a3300;color:var(--success);border:1px solid var(--success)';
  }
  updateStepLabels();
}
document.addEventListener('DOMContentLoaded', updateStepLabels);

const ICON_COLORS = {
  PENDING: 'var(--muted)',
  RUNNING: 'var(--run)',
  DONE:    'var(--success)',
  FAILED:  'var(--error)',
  SKIPPED: 'var(--muted)',
};
const ICONS = {
  PENDING: '○', RUNNING: '◎', DONE: '✓', FAILED: '✗', SKIPPED: '⊘'
};

function _resetUI(statusMsg, statusColor) {
  currentStepIdx = -1;
  document.getElementById('abortBtn').disabled = true;
  document.getElementById('startBtn').disabled = false;
  document.getElementById('startBtn').textContent = '▶  Riavvia Wizard';
  document.getElementById('configSection').style.opacity = '1';
  document.getElementById('configSection').style.pointerEvents = '';
  document.getElementById('statusText').textContent = statusMsg;
  document.getElementById('statusText').style.color = statusColor;
}

function appendCardLog(idx, msg) {
  const logDiv = document.getElementById('card-log-' + idx);
  const logBox = document.getElementById('card-logbox-' + idx);
  if (!logDiv || !logBox) return;
  logDiv.style.display = '';
  const span = document.createElement('span');
  let cls = '';
  if (msg.includes('[DRY-RUN]')) cls = 'log-dry';
  else if (/ERRORE|FAIL|✗|ECCEZIONE/.test(msg)) cls = 'log-err';
  else if (msg.startsWith('►')) cls = 'log-step';
  else if (msg.includes('  $')) cls = 'log-muted';
  if (cls) span.className = cls;
  span.textContent = msg + '\n';
  logBox.appendChild(span);
  logBox.scrollTop = logBox.scrollHeight;
}

function appendLog(msg) {
  if (currentStepIdx >= 0) appendCardLog(currentStepIdx, msg);
}

function setProgress(idx, total) {
  const pct = Math.round(idx / total * 100);
  document.getElementById('progressBar').style.width = pct + '%';
  document.getElementById('progressPct').textContent = pct + '%';
}

// Espande/collassa un blocco al click sull'header. Ignora i click sui bottoni
// interni (es. tasto "i" dei dettagli) per non interferire.
function toggleStepCard(idx, event) {
  if (event && event.target.closest('button')) return;
  const card = document.getElementById('step-card-' + idx);
  if (card) card.classList.toggle('collapsed');
}

function startInstall() {
  const cfg = {
    hostname:           document.getElementById('hostname').value,
    play_card:          document.getElementById('playCard').value,
    cap_card:           document.getElementById('capCard').value,
    install_log2ram:         document.getElementById('log2ram').checked,
    log2ram_size:            document.getElementById('log2ram_size').value || '128M',
    log2ram_zl2r:            document.getElementById('log2ram_zl2r').checked,
    log2ram_comp_alg:        document.getElementById('log2ram_comp_alg').value,
    log2ram_log_disk_size:   document.getElementById('log2ram_log_disk_size').value || '256M',
    dry_run:            DRY_RUN,
    ...getEnvFields(),
  };

  fetch('/start', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify(cfg),
  }).then(r => r.json()).then(data => {
    if (!data.ok) { alert(data.error || 'Errore avvio'); return; }

    document.getElementById('startBtn').disabled = true;
    document.getElementById('abortBtn').disabled = false;
    document.getElementById('statusText').textContent = 'Installazione in corso...';
    document.getElementById('statusText').style.color = 'var(--run)';
    document.getElementById('configSection').style.opacity = '0.5';
    document.getElementById('configSection').style.pointerEvents = 'none';

    evtSource = new EventSource('/stream');
    evtSource.onmessage = (e) => {
      const ev = JSON.parse(e.data);
      if (ev.type === 'log') {
        appendLog(ev.msg);
      } else if (ev.type === 'step') {
        // Update sidebar icon
        const icon = document.getElementById('icon-' + ev.idx);
        if (icon) {
          icon.textContent = ICONS[ev.status] || '○';
          icon.style.color = ICON_COLORS[ev.status] || 'var(--muted)';
        }
        // Update card badge
        const cardBadge = document.getElementById('card-badge-' + ev.idx);
        if (cardBadge) {
          const badgeColors = {
            RUNNING: {bg:'var(--run)',   color:'#1e1e2e'},
            DONE:    {bg:'var(--success)',color:'#1e1e2e'},
            SKIPPED: {bg:'#45475a',      color:'var(--muted)'},
            FAILED:  {bg:'var(--error)', color:'#1e1e2e'},
          };
          const bc = badgeColors[ev.status];
          if (bc) {
            cardBadge.textContent = ev.status;
            cardBadge.style.background = bc.bg;
            cardBadge.style.color = bc.color;
            cardBadge.style.display = '';
            cardBadge.classList.toggle('badge-running', ev.status === 'RUNNING');
          }
        }
        // Update card border class
        const card = document.getElementById('step-card-' + ev.idx);
        if (card) {
          card.classList.remove('card-running', 'card-done', 'card-failed');
          if (ev.status === 'RUNNING') {
            currentStepIdx = ev.idx;
            card.classList.add('card-running');
            card.classList.remove('collapsed');           // espandi il blocco attivo
            const logDiv = document.getElementById('card-log-' + ev.idx);
            if (logDiv) logDiv.style.display = '';
            card.scrollIntoView({behavior:'smooth', block:'start'});
          } else if (ev.status === 'DONE' || ev.status === 'SKIPPED') {
            card.classList.add('card-done');
            card.classList.add('collapsed');              // collassa appena completato
          } else if (ev.status === 'FAILED') {
            card.classList.add('card-failed');
            card.classList.remove('collapsed');           // tieni aperto: mostra l'errore
          }
        }
        const done = ['DONE','SKIPPED'].includes(ev.status);
        setProgress(ev.idx + (done ? 1 : 0.5), N_STEPS);
        document.getElementById('progressLabel').textContent =
          (done ? 'Completato: ' : 'In corso: ') + (ev.name || '');
      } else if (ev.type === 'done') {
        evtSource.close();
        setProgress(N_STEPS, N_STEPS);
        if (ev.failed && ev.failed.length > 0) {
          _resetUI('Completato con ' + ev.failed.length + ' errori', 'var(--warn)');
        } else {
          _resetUI('✓ Completato!', 'var(--success)');
        }
      } else if (ev.type === 'pause') {
        // Riabilita solo la sezione credenziali per l'inserimento
        document.getElementById('configSection').style.opacity = '1';
        document.getElementById('configSection').style.pointerEvents = '';
        document.getElementById('resumeBtn').style.display = '';
        document.getElementById('statusText').textContent = '⏸ Inserisci le credenziali e clicca Continua';
        document.getElementById('statusText').style.color = 'var(--warn)';
        // Scrolla alla card credenziali
        const credCard = [...document.querySelectorAll('.font-semibold.text-sm')]
          .find(el => el.textContent.trim() === 'Credenziali .env');
        if (credCard) credCard.closest('.card')?.scrollIntoView({behavior:'smooth', block:'center'});
      } else if (ev.type === 'pause_audio') {
        // Riabilita la sezione config per testare l'audio e scegliere le schede
        document.getElementById('configSection').style.opacity = '1';
        document.getElementById('configSection').style.pointerEvents = '';
        document.getElementById('audioResumeBtn').style.display = '';
        document.getElementById('statusText').textContent = '⏸ Testa l\'audio, seleziona le schede e clicca Prosegui';
        document.getElementById('statusText').style.color = 'var(--warn)';
        // alsa-utils è ora installato: rileva le schede reali
        try { refreshCards(); } catch (e) {}
        const audCard = [...document.querySelectorAll('.font-semibold.text-sm')]
          .find(el => el.textContent.trim() === 'Configurazione Audio');
        if (audCard) audCard.closest('.card')?.scrollIntoView({behavior:'smooth', block:'center'});
      } else if (ev.type === 'aborted') {
        evtSource.close();
        currentStepIdx = -1;
        _resetUI('Interrotto', 'var(--warn)');
      }
    };
    let _sseErrCount = 0;
    evtSource.onopen = () => { _sseErrCount = 0; };
    evtSource.onerror = () => {
      _sseErrCount++;
      if (_sseErrCount > 5) {
        evtSource.close();
        evtSource = null;
        _resetUI('Connessione SSE persa — ricaricare la pagina', 'var(--warn)');
      }
      // altrimenti EventSource riprova automaticamente
    };
  });
}

function resumeInstall() {
  const btn = document.getElementById('resumeBtn');
  btn.disabled = true;
  btn.textContent = '⏳ Invio...';
  fetch('/resume', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify(getEnvFields()),
  }).then(r => r.json()).then(d => {
    if (d.ok) {
      btn.style.display = 'none';
      btn.disabled = false;
      btn.textContent = '▶ Continua installazione';
      document.getElementById('configSection').style.opacity = '0.5';
      document.getElementById('configSection').style.pointerEvents = 'none';
      document.getElementById('statusText').textContent = 'Installazione in corso...';
      document.getElementById('statusText').style.color = 'var(--run)';
    }
  });
}

function resumeAudio() {
  const btn = document.getElementById('audioResumeBtn');
  btn.disabled = true;
  btn.textContent = '⏳ Invio...';
  fetch('/resume', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({
      play_card: parseInt(document.getElementById('playCard').value) || 0,
      cap_card:  parseInt(document.getElementById('capCard').value)  || 0,
    }),
  }).then(r => r.json()).then(d => {
    if (d.ok) {
      btn.style.display = 'none';
      btn.disabled = false;
      btn.textContent = '▶ Prosegui';
      document.getElementById('configSection').style.opacity = '0.5';
      document.getElementById('configSection').style.pointerEvents = 'none';
      document.getElementById('statusText').textContent = 'Installazione in corso...';
      document.getElementById('statusText').style.color = 'var(--run)';
    } else {
      btn.disabled = false;
      btn.textContent = '▶ Prosegui';
    }
  }).catch(() => { btn.disabled = false; btn.textContent = '▶ Prosegui'; });
}

function abortInstall() {
  fetch('/abort', {method:'POST'});
  document.getElementById('abortBtn').disabled = true;
  document.getElementById('statusText').textContent = 'Interruzione in corso...';
}

let _rollbackSrc = null;

const _ROLLBACK_HIDE = ['dryRunBar', 'btnBar', 'progressBarCard', 'configSection'];

function _rollbackHideAll() {
  _ROLLBACK_HIDE.forEach(id => {
    const el = document.getElementById(id);
    if (el) el.style.display = 'none';
  });
}

function startRollback() {
  const modal = document.getElementById('rollbackModal');
  modal.classList.add('open');
}

function closeRollbackModal() {
  document.getElementById('rollbackModal').classList.remove('open');
}

function _doRollback() {
  _rollbackHideAll();

  const panel   = document.getElementById('rollbackPanel');
  const logBox  = document.getElementById('rollbackLogBox');
  const status  = document.getElementById('rollbackStatus');
  const title   = document.getElementById('rollbackTitle');
  const doneBtn = document.getElementById('rollbackDoneBtn');
  const abortBtn = document.getElementById('rollbackAbortBtn');

  logBox.innerHTML = '';
  status.textContent = '';
  title.textContent = '⎌ Rollback in corso…';
  title.style.color = 'var(--error)';
  doneBtn.style.display = 'none';
  abortBtn.disabled = false;
  panel.style.display = '';
  panel.scrollIntoView({behavior:'smooth', block:'start'});

  fetch('/rollback', {method:'POST'}).then(r => r.json()).then(data => {
    if (!data.ok) {
      title.textContent = '✗ Errore avvio rollback';
      status.textContent = data.error || 'Errore sconosciuto';
      status.style.color = 'var(--error)';
      doneBtn.style.display = '';
      abortBtn.style.display = 'none';
      return;
    }

    _rollbackSrc = new EventSource('/rollback_stream');
    _rollbackSrc.onmessage = (e) => {
      const ev = JSON.parse(e.data);
      const line = ev.line || '';

      if (line.startsWith('__DONE__:')) {
        _rollbackSrc.close();
        _rollbackSrc = null;
        abortBtn.style.display = 'none';
        const rc = parseInt(line.split(':')[1], 10);
        if (rc === 0) {
          title.textContent = '✓ Rollback completato';
          title.style.color = 'var(--success)';
          status.textContent = 'Il sistema è stato ripristinato. Clicca il pulsante per ricominciare l\'installazione.';
        } else {
          title.textContent = '⚠ Rollback terminato con errori';
          title.style.color = 'var(--warn)';
          status.textContent = 'Exit code: ' + rc + '. Controlla il log sopra.';
        }
        status.style.color = rc === 0 ? 'var(--success)' : 'var(--warn)';
        doneBtn.style.display = '';
        doneBtn.scrollIntoView({behavior:'smooth', block:'nearest'});
        return;
      }

      const span = document.createElement('span');
      const stripped = line.replace(/\x1b\[[0-9;]*m/g, '');
      if (/ERRORE|FAIL|✗/.test(stripped))   span.className = 'log-err';
      else if (stripped.startsWith('▶'))    span.className = 'log-step';
      else if (/✓/.test(stripped))          span.style.color = 'var(--success)';
      else if (/—.*\(skip\)/.test(stripped)) span.className = 'log-muted';
      span.textContent = stripped + '\n';
      logBox.appendChild(span);
      logBox.scrollTop = logBox.scrollHeight;
    };
    _rollbackSrc.onerror = () => {
      if (_rollbackSrc) { _rollbackSrc.close(); _rollbackSrc = null; }
      title.textContent = '⚠ Connessione SSE persa';
      title.style.color = 'var(--warn)';
      status.textContent = 'Ricaricare la pagina per verificare lo stato.';
      status.style.color = 'var(--warn)';
      abortBtn.style.display = 'none';
      doneBtn.style.display = '';
    };
  });
}

function abortRollback() {
  fetch('/rollback_abort', {method:'POST'});
  document.getElementById('rollbackAbortBtn').disabled = true;
}

function stepLabelClick(name, labelEl) {
  if (!DRY_RUN) return;
  const liEl = labelEl.closest('li');
  const descEl = liEl.querySelector('.step-desc');

  // toggle descrizione inline
  const showing = descEl.classList.contains('visible');
  document.querySelectorAll('.step-desc.visible').forEach(d => d.classList.remove('visible'));
  if (!showing) {
    descEl.textContent = labelEl.dataset.desc;
    descEl.classList.add('visible');
  }

  // scroll alla card dello step
  const stepIdx = parseInt(labelEl.dataset.step);
  const card = document.getElementById('step-card-' + stepIdx);
  if (card) {
    // rimuovi selezione precedente
    document.querySelectorAll('.card.sec-selected').forEach(c => c.classList.remove('sec-selected'));
    card.scrollIntoView({behavior:'smooth', block:'start'});
    card.classList.remove('sec-highlight');
    void card.offsetWidth;
    card.classList.add('sec-highlight');
    // al termine del lampeggio mantieni il bordo
    setTimeout(() => {
      card.classList.remove('sec-highlight');
      card.classList.add('sec-selected');
    }, 1100);
  }
}

function updateStepLabels() {
  document.querySelectorAll('.step-label').forEach(el => {
    if (DRY_RUN) el.classList.add('clickable');
    else { el.classList.remove('clickable'); el.closest('li').querySelector('.step-desc').classList.remove('visible'); }
  });
}

function toggleLog2RamParams() {
  const on = document.getElementById('log2ram').checked;
  document.getElementById('log2ramParams').style.display = on ? '' : 'none';
}
function toggleZramParams() {
  const on = document.getElementById('log2ram_zl2r').checked;
  document.getElementById('zramParams').style.display = on ? '' : 'none';
}

function togglePwd(id) {
  const el = document.getElementById(id);
  el.type = el.type === 'password' ? 'text' : 'password';
}

// Contenuto PEM dei certificati Mumble caricati dall'utente (testo, non file binari)
window._mumblePem = {cert: '', key: ''};
function loadPem(input, kind) {
  const status = document.getElementById('mumble_' + kind + '_status');
  const f = input.files && input.files[0];
  if (!f) { window._mumblePem[kind] = ''; status.textContent = ''; return; }
  const reader = new FileReader();
  reader.onload = e => {
    const txt = e.target.result || '';
    window._mumblePem[kind] = txt;
    const ok = /-----BEGIN/.test(txt);
    status.textContent = ok ? '✓ ' + f.name : '⚠ non sembra un file PEM valido';
    status.style.color = ok ? 'var(--ok, #a6e3a1)' : 'var(--warn, #f9e2af)';
  };
  reader.readAsText(f);
}

function getEnvFields() {
  return {
    env_mumble_username: document.getElementById('env_mumble_username').value,
    env_mumble_password: document.getElementById('env_mumble_password').value,
    mumble_cert_pem:     window._mumblePem.cert,
    mumble_key_pem:      window._mumblePem.key,
    env_camera_username: document.getElementById('env_camera_username').value,
    env_camera_password: document.getElementById('env_camera_password').value,
    env_pushover_token:  document.getElementById('env_pushover_token').value,
    env_pushover_key:    document.getElementById('env_pushover_key').value,
    env_openrouter_key:  document.getElementById('env_openrouter_key').value,
  };
}

// ── Audio card refresh ────────────────────────────────────────────────────────

let _capStereo  = false;
let _playStereo = false;

function _setVuStereoMode(micStereo, spkStereo) {
  _capStereo  = micStereo;
  _playStereo = spkStereo;
  ['vuRowR', 'vuScaleR'].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.style.display = micStereo ? '' : 'none';
  });
  ['playVuRowR', 'playVuScaleR'].forEach(id => {
    const el = document.getElementById(id);
    if (el) el.style.display = spkStereo ? '' : 'none';
  });
  // Rinomina L→ "  " su mono (barra senza etichetta)
  ['vuLabelL', 'playVuLabelL'].forEach((id, i) => {
    const el = document.getElementById(id);
    if (el) el.textContent = (i === 0 ? micStereo : spkStereo) ? 'L' : '';
  });
}

function _populateCardSelect(selectId, cards, preferIndex) {
  const sel = document.getElementById(selectId);
  sel.innerHTML = '';
  if (cards.length === 0) {
    const opt = document.createElement('option');
    opt.value = '0'; opt.textContent = '(nessuna scheda rilevata)';
    sel.appendChild(opt);
    return cards;
  }
  cards.forEach(c => {
    const opt = document.createElement('option');
    opt.value = c.index;
    opt.textContent = `${c.index} — ${c.name}`;
    if (c.index === preferIndex) opt.selected = true;
    sel.appendChild(opt);
  });
  return cards;
}

// true appena l'utente sceglie a mano una scheda: da quel momento NON sovrascrivo
// più la sua scelta col default automatico.
let _audioUserChose = false;

// Default consigliato: preferisci una scheda full-duplex (presente sia in output
// che input = la USB), così OUTPUT e INPUT vanno sulla stessa scheda USB e non
// sull'audio onboard. Le liste arrivano già ordinate USB-first dal backend.
function _bestDefaultPair(playCards, capCards) {
  const capIdx = new Set(capCards.map(c => c.index));
  for (const pc of playCards) {
    if (capIdx.has(pc.index)) return { play: pc.index, cap: pc.index };
  }
  return {
    play: playCards.length ? playCards[0].index : 0,
    cap:  capCards.length  ? capCards[0].index  : 0,
  };
}

function refreshCards(btn) {
  if (btn) { btn.disabled = true; btn.textContent = '↺ …'; }
  fetch('/audio/refresh_cards')
    .then(r => r.json())
    .then(d => {
      let curPlay, curCap;
      if (_audioUserChose) {
        curPlay = parseInt(document.getElementById('playCard').value) || 0;
        curCap  = parseInt(document.getElementById('capCard').value)  || 0;
      } else {
        const best = _bestDefaultPair(d.play_cards, d.cap_cards);
        curPlay = best.play;
        curCap  = best.cap;
      }
      _populateCardSelect('playCard', d.play_cards, curPlay);
      _populateCardSelect('capCard',  d.cap_cards,  curCap);
      // Canali della scheda selezionata
      const selPlay = d.play_cards.find(c => c.index === (parseInt(document.getElementById('playCard').value)||0));
      const selCap  = d.cap_cards.find( c => c.index === (parseInt(document.getElementById('capCard').value) ||0));
      _setVuStereoMode((selCap?.channels || 1) > 1, (selPlay?.channels || 1) > 1);
      // Disabilita il test audio se nessuna scheda rilevata
      const noCards = d.play_cards.length === 0 && d.cap_cards.length === 0;
      const testBtn = document.getElementById('testAudioBtn');
      if (testBtn) {
        testBtn.disabled = noCards;
        testBtn.style.opacity = noCards ? '0.4' : '';
        testBtn.style.cursor  = noCards ? 'not-allowed' : '';
      }
      if (btn) { btn.disabled = false; btn.textContent = '↺ Aggiorna schede'; }
    })
    .catch(() => { if (btn) { btn.disabled = false; btn.textContent = '↺ Aggiorna schede'; } });
}

document.addEventListener('DOMContentLoaded', () => refreshCards(null));

// ── Audio modal ───────────────────────────────────────────────────────────────

let _vuSource   = null;   // mic
let _playVuSrc  = null;   // speaker
let _playVolPct = 100;

function _updateAudioModalCards() {
  const p = document.getElementById('playCard');
  const c = document.getElementById('capCard');
  const pt = (p && p.selectedOptions[0]) ? p.selectedOptions[0].text : '—';
  const ct = (c && c.selectedOptions[0]) ? c.selectedOptions[0].text : '—';
  const el = document.getElementById('audioModalCards');
  if (el) el.innerHTML = '🔈 Output: <b style="color:#a6e3a1">' + pt +
    '</b> &nbsp;·&nbsp; 🎤 Input: <b style="color:#a6e3a1">' + ct + '</b>';
}

function openAudioModal() {
  document.getElementById('audioModal').classList.remove('hidden');
  _updateAudioModalCards();
  loadAudioInfo();
  loadFileList();
  if (!_vuSource) toggleVU();
}

function loadFileList() {
  fetch('/audio/list_files').then(r => r.json()).then(files => {
    const sel = document.getElementById('playFileSelect');
    sel.innerHTML = files.length === 0
      ? '<option value="">— Nessun file trovato —</option>'
      : files.map(f => `<option value="${f.path}">${f.label}</option>`).join('');
  });
}
function closeAudioModal() {
  stopAudio();
  stopVU();
  document.getElementById('audioModal').classList.add('hidden');
}

// ── Step Help Modal ───────────────────────────────────────────────────────────
const STEP_HELP = {{ step_help | tojson }};
const HELP_COLORS = {
  green:  { bg: '#1c2a1e', border: '#40a02b', text: '#a6e3a1' },
  red:    { bg: '#2a1c1c', border: '#d20f39', text: '#f38ba8' },
  blue:   { bg: '#1c1e2a', border: '#1e66f5', text: '#89b4fa' },
  muted:  { bg: '#1e1e2e', border: '#45475a', text: '#9399b2' },
};

function openStepHelp(stepName) {
  const h = STEP_HELP[stepName];
  if (!h) return;
  document.getElementById('helpModalTitle').textContent = stepName;
  document.getElementById('helpModalSummary').textContent = h.summary || '';
  const sectEl = document.getElementById('helpModalSections');
  sectEl.innerHTML = '';
  (h.sections || []).forEach(sec => {
    const c = HELP_COLORS[sec.color] || HELP_COLORS.muted;
    const wrap = document.createElement('div');
    wrap.style.cssText = `background:${c.bg};border:1px solid ${c.border};border-radius:.5rem;padding:.75rem`;
    const label = document.createElement('div');
    label.style.cssText = `font-size:.65rem;font-weight:700;letter-spacing:.08em;color:${c.text};margin-bottom:.4rem`;
    label.textContent = sec.label;
    wrap.appendChild(label);
    (sec.items || []).forEach(item => {
      const row = document.createElement('div');
      row.style.cssText = `font-size:.78rem;color:#cdd6f4;padding:.15rem 0;display:flex;gap:.5rem;align-items:baseline`;
      row.innerHTML = `<span style="color:${c.text};flex-shrink:0">›</span><span>${item}</span>`;
      wrap.appendChild(row);
    });
    sectEl.appendChild(wrap);
  });
  const noteEl = document.getElementById('helpModalNote');
  if (h.note) {
    noteEl.textContent = '⚠ ' + h.note;
    noteEl.style.display = 'block';
  } else {
    noteEl.style.display = 'none';
  }
  document.getElementById('stepHelpModal').classList.remove('hidden');
}
function closeStepHelp() {
  document.getElementById('stepHelpModal').classList.add('hidden');
}

function togglePlayVU() {
  if (_playVuSrc) { stopPlayVU(); return; }
  const card = parseInt(document.getElementById('playCard').value);
  _playVuPeakL = _playVuPeakR = _playRawL = _playRawR = 0;
  _playVuSrc = new EventSource(`/audio/play_vu_stream?card=${card}&dev=0`);
  _playVuSrc.onmessage = (e) => {
    const d = JSON.parse(e.data);
    if (d.level === undefined) return;
    _playRawL = d.L !== undefined ? d.L : d.level;
    _playRawR = d.R !== undefined ? d.R : d.level;
    _updatePlayVuDisplay();
  };
  _playVuSrc.onerror = () => stopPlayVU();
}

let _vuPeakL = 0, _vuPeakR = 0, _vuPeakTimerL = null, _vuPeakTimerR = null;
let _playVuPeakL = 0, _playVuPeakR = 0, _playVuTimerL = null, _playVuTimerR = null;
let _playRawL = 0, _playRawR = 0;

function _setVuChannel(barId, peakId, level, getPeak, setPeak, getTimer, setTimer) {
  document.getElementById(barId).style.width = level + '%';
  if (level > getPeak()) {
    setPeak(level);
    document.getElementById(peakId).style.left = level + '%';
    clearTimeout(getTimer());
    setTimer(setTimeout(() => {
      setPeak(0);
      document.getElementById(peakId).style.left = '0%';
      setTimer(null);
    }, 1500));
  }
}

function _updatePlayVuDisplay() {
  const sL = Math.round(_playRawL * _playVolPct / 100);
  const sR = Math.round(_playRawR * _playVolPct / 100);
  const db = Math.round(((sL + sR) / 2 / 100 * 60) - 60);
  document.getElementById('playVuDb').textContent = db + ' dB';
  _setVuChannel('playVuBarL', 'playVuPeakL', sL,
    ()=>_playVuPeakL, v=>{ _playVuPeakL=v; }, ()=>_playVuTimerL, t=>{ _playVuTimerL=t; });
  _setVuChannel('playVuBarR', 'playVuPeakR', sR,
    ()=>_playVuPeakR, v=>{ _playVuPeakR=v; }, ()=>_playVuTimerR, t=>{ _playVuTimerR=t; });
}

function _stopPlayVuLocal() {
  if (_playVuSrc) { _playVuSrc.close(); _playVuSrc = null; }
  clearTimeout(_playVuTimerL); clearTimeout(_playVuTimerR);
  _playVuPeakL = _playVuPeakR = _playRawL = _playRawR = 0;
  ['playVuBarL','playVuBarR'].forEach(id => document.getElementById(id).style.width = '0%');
  ['playVuPeakL','playVuPeakR'].forEach(id => document.getElementById(id).style.left = '0%');
  document.getElementById('playVuDb').textContent = '-- dB';
}

function stopPlayVU() {
  _stopPlayVuLocal();
  fetch('/audio/play_stop', {method: 'POST'});
}

function toggleVU() {
  if (_vuSource) { stopVU(); return; }
  const card = parseInt(document.getElementById('capCard').value);
  _vuPeakL = _vuPeakR = 0;
  _vuSource = new EventSource(`/audio/vu_stream?card=${card}&dev=0`);
  _vuSource.onmessage = (e) => {
    const d = JSON.parse(e.data);
    const L = d.L !== undefined ? d.L : d.level;
    const R = d.R !== undefined ? d.R : d.level;
    document.getElementById('vuDb').textContent = d.db + ' dB';
    _setVuChannel('vuBarL', 'vuPeakL', L,
      ()=>_vuPeakL, v=>{ _vuPeakL=v; }, ()=>_vuPeakTimerL, t=>{ _vuPeakTimerL=t; });
    _setVuChannel('vuBarR', 'vuPeakR', R,
      ()=>_vuPeakR, v=>{ _vuPeakR=v; }, ()=>_vuPeakTimerR, t=>{ _vuPeakTimerR=t; });
  };
  let _vuRetry = 0;
  _vuSource.onerror = () => {
    _vuSource = null;
    if (_vuRetry++ < 5) setTimeout(()=>{ if(!_vuSource) toggleVU(); }, 1000);
    else _vuRetry = 0;
  };
}

function stopVU() {
  if (_vuSource) { _vuSource.close(); _vuSource = null; }
  clearTimeout(_vuPeakTimerL); clearTimeout(_vuPeakTimerR);
  _vuPeakL = _vuPeakR = 0;
  ['vuBarL','vuBarR'].forEach(id => document.getElementById(id).style.width = '0%');
  ['vuPeakL','vuPeakR'].forEach(id => document.getElementById(id).style.left = '0%');
  document.getElementById('vuDb').textContent = '-- dB';
}

function audioLog(msg, color) {
  const box = document.getElementById('audioLog');
  const s = document.createElement('span');
  s.textContent = msg + '\n';
  if (color) s.style.color = color;
  box.appendChild(s);
  box.scrollTop = box.scrollHeight;
}

function setAGC(enabled) {
  const card = parseInt(document.getElementById('capCard').value);
  fetch('/audio/agc', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({card, enabled})
  }).then(r=>r.json()).then(d=>{
    audioLog(d.ok ? `✓ AGC ${enabled ? 'attivato' : 'disattivato'}` : `✗ AGC: ${d.error}`,
             d.ok ? 'var(--success)' : 'var(--error)');
  });
}

function loadAudioInfo() {
  const playCard = parseInt(document.getElementById('playCard').value);
  const capCard  = parseInt(document.getElementById('capCard').value);
  document.getElementById('audioPlayLabel').textContent = `card ${playCard}`;
  document.getElementById('playVolumes').innerHTML = '<span style="color:var(--muted);font-size:.8rem">Caricamento...</span>';
  fetch(`/audio/info?play_card=${playCard}&cap_card=${capCard}`)
    .then(r => r.json())
    .then(data => {
      // Mostra UN'unica lista: controlli playback non duplicati da capture + capture
      const captureNames = new Set(data.cap.filter(c => c.mode === 'capture').map(c => c.name));
      const merged = [
        ...data.play.filter(c => !captureNames.has(c.name)),
        ...data.cap.filter(c => c.mode === 'capture')
      ];
      renderSliders('playVolumes', merged);
      const agcRow = document.getElementById('agcRow');
      if (data.agc !== null && data.agc !== undefined) {
        agcRow.style.removeProperty('display');
        document.getElementById('agcToggle').checked = data.agc;
      } else {
        agcRow.style.setProperty('display', 'none', 'important');
      }
    })
    .catch(() => audioLog('Errore caricamento controlli audio', 'var(--error)'));
}

const _volTimers = {};
function renderSliders(containerId, controls) {
  const div = document.getElementById(containerId);
  div.innerHTML = '';
  if (!controls || controls.length === 0) {
    div.innerHTML = '<span style="color:var(--muted);font-size:.8rem">Nessun controllo volume trovato</span>';
    return;
  }
  controls.forEach(ctrl => {
    const card = ctrl.card;
    const id = `vol_${card}_${ctrl.name.replace(/\W/g,'_')}_${ctrl.mode}`;
    const label = ctrl.mode === 'capture' ? `${ctrl.name} 🎤` : ctrl.name;
    const row = document.createElement('div');
    row.className = 'flex items-center gap-3';
    row.innerHTML = `
      <span class="text-xs w-20 text-right flex-shrink-0" style="color:var(--muted)">${label}</span>
      <input type="range" id="${id}" min="0" max="100" value="${ctrl.volume}"
             class="flex-1" style="accent-color:var(--accent)">
      <span id="${id}_val" class="text-xs w-9 text-right">${ctrl.volume}%</span>
    `;
    div.appendChild(row);
    document.getElementById(id).addEventListener('input', function() {
      const val = this.value;
      document.getElementById(`${id}_val`).textContent = val + '%';
      if ((ctrl.mode || 'playback') === 'playback') {
        _playVolPct = parseInt(val);
        _updatePlayVuDisplay();
      }
      clearTimeout(_volTimers[id]);
      _volTimers[id] = setTimeout(() => {
        fetch('/audio/set_volume', {
          method: 'POST',
          headers: {'Content-Type':'application/json'},
          body: JSON.stringify({card, control: ctrl.name, volume: parseInt(val), mode: ctrl.mode || 'playback'})
        }).then(r=>r.json()).then(d=>{
          audioLog(d.ok ? `✓ ${label}: ${val}%` : `✗ ${label}: ${d.error}`,
                   d.ok ? 'var(--success)' : 'var(--error)');
        });
      }, 300);
    });
  });
}

function playFile() {
  const sel = document.getElementById('playFileSelect');
  const path = sel.value;
  if (!path) { audioLog('✗ Seleziona un file prima', 'var(--error)'); return; }
  const playCard = parseInt(document.getElementById('playCard').value);
  _stopPlayVuLocal();
  audioLog(`▶ ${sel.options[sel.selectedIndex].text}`);
  fetch('/audio/play_stop', {method:'POST'}).then(() =>
    fetch('/audio/play_file', {
      method: 'POST', headers: {'Content-Type':'application/json'},
      body: JSON.stringify({path, play_card: playCard, play_dev: 0})
    })
  ).then(r => r.json()).then(d => {
    audioLog(d.ok ? `✓ ${d.msg}` : `✗ ${d.error}`, d.ok ? 'var(--success)' : 'var(--error)');
    if (d.ok) _startPlaybackVU();
  });
}

function _setRecBtns(recording) {
  document.getElementById('recStartBtn').disabled = recording;
  document.getElementById('recStartBtn').style.opacity = recording ? '.4' : '1';
  document.getElementById('recStopBtn').disabled = !recording;
  document.getElementById('recStopBtn').style.opacity = recording ? '1' : '.4';
}

function recStart() {
  const capCard = parseInt(document.getElementById('capCard').value);
  audioLog('⏺ Registrazione in corso...');
  fetch('/audio/rec_start', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({cap_card: capCard, cap_dev: 0})
  }).then(r=>r.json()).then(d=>{
    if (d.ok) { _setRecBtns(true); }
    else { audioLog(`✗ ${d.error}`, 'var(--error)'); }
  });
}

function recStop() {
  fetch('/audio/rec_stop', {method:'POST'})
    .then(r=>r.json()).then(d=>{
      _setRecBtns(false);
      if (d.ok) {
        audioLog('✓ Registrazione salvata', 'var(--success)');
        document.getElementById('recPlayBtn').disabled = false;
        document.getElementById('recPlayBtn').style.opacity = '1';
      } else {
        audioLog(`✗ ${d.error}`, 'var(--error)');
      }
    });
}

function recPlay() {
  const playCard = parseInt(document.getElementById('playCard').value);
  audioLog('▶ Riproduzione in corso...');
  _stopPlayVuLocal();
  fetch('/audio/play_stop', {method:'POST'}).then(() =>
    fetch('/audio/rec_play', {
      method: 'POST', headers: {'Content-Type':'application/json'},
      body: JSON.stringify({play_card: playCard, play_dev: 0})
    })
  ).then(r=>r.json()).then(d=>{
    audioLog(d.ok ? `✓ ${d.msg}` : `✗ ${d.error}`, d.ok ? 'var(--success)' : 'var(--error)');
    if (d.ok) _startPlaybackVU();
  });
}

function _startPlaybackVU() {
  if (_playVuSrc) { _playVuSrc.close(); _playVuSrc = null; }
  clearTimeout(_playVuTimerL); clearTimeout(_playVuTimerR);
  _playVuPeakL = _playVuPeakR = 0;
  ['playVuPeakL','playVuPeakR'].forEach(id => document.getElementById(id).style.left = '0%');
  _playVuSrc = new EventSource('/audio/playback_vu_stream');
  _playVuSrc.onmessage = (e) => {
    const d = JSON.parse(e.data);
    if (d.done) { stopPlayVU(); return; }
    if (d.level === undefined) return;
    _playRawL = d.L !== undefined ? d.L : d.level;
    _playRawR = d.R !== undefined ? d.R : d.level;
    _updatePlayVuDisplay();
  };
  _playVuSrc.onerror = () => stopPlayVU();
}

function stopAudio() {
  fetch('/audio/stop', {method:'POST'});
  stopPlayVU();
  audioLog('■ Stop');
}

function saveEnv() {
  const msg = document.getElementById('envSaveMsg');
  msg.textContent = 'Salvataggio...';
  msg.style.color = 'var(--muted)';
  fetch('/save_env', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify(getEnvFields())
  }).then(r=>r.json()).then(d=>{
    if(d.ok) { msg.textContent = '✓ .env salvato'; msg.style.color = 'var(--success)'; }
    else      { msg.textContent = '✗ ' + d.error;  msg.style.color = 'var(--error)'; }
  }).catch(()=>{ msg.textContent = '✗ Errore di rete'; msg.style.color='var(--error)'; });
}
</script>

<!-- Rollback confirm modal -->
<div id="rollbackModal" class="modal-overlay" onclick="if(event.target===this)closeRollbackModal()">
  <div class="modal-box flex flex-col gap-4">
    <div class="flex items-start gap-3">
      <span style="font-size:1.6rem;line-height:1">⚠</span>
      <div>
        <p class="font-bold text-base mb-1" style="color:var(--error)">Conferma Rollback</p>
        <p class="text-sm leading-6" style="color:#cdd6f4">
          Verranno rimossi dal sistema:
        </p>
        <ul class="text-sm mt-2 space-y-1" style="color:var(--muted);padding-left:1.2rem;list-style:disc">
          <li>Servizio systemd <span style="color:#cdd6f4">doorphoneserver</span></li>
          <li>Mumble server</li>
          <li>Log2Ram</li>
          <li>Go language <code style="color:var(--accent)">/usr/local/go</code></li>
          <li>Utente di sistema <code style="color:var(--accent)">doorphoneserver</code></li>
          <li>File generati <code style="color:var(--accent)">.env, certificati, bin/</code></li>
          <li>Pacchetti APT installati dal wizard</li>
        </ul>
        <p class="text-sm mt-3" style="color:var(--success)">
          ✓ Il repository git verrà mantenuto.
        </p>
      </div>
    </div>
    <div class="flex justify-end gap-3 mt-2">
      <button onclick="closeRollbackModal()"
        style="padding:.5rem 1.2rem;border-radius:.5rem;border:1px solid #45475a;background:transparent;color:#cdd6f4;cursor:pointer;font-size:.9rem">
        Annulla
      </button>
      <button onclick="closeRollbackModal(); _doRollback()"
        style="padding:.5rem 1.4rem;border-radius:.5rem;border:none;background:var(--error);color:#1e1e2e;font-weight:700;cursor:pointer;font-size:.9rem">
        ⎌ Procedi con il rollback
      </button>
    </div>
  </div>
</div>

</body>
</html>
"""


# ── Routes ────────────────────────────────────────────────────────────────────

def _check_rollback_available() -> bool:
    """Ritorna True se almeno un artefatto dell'installazione è presente sul sistema."""
    import pwd, grp
    checks = [
        os.path.isfile("/etc/systemd/system/doorphoneserver.service"),
        os.path.isdir("/usr/local/go"),
        os.path.isfile("/home/doorphoneserver/.env"),
        os.path.isfile("/home/doorphoneserver/cert.pem"),
    ]
    # controlla anche se l'utente di sistema esiste
    try:
        pwd.getpwnam("doorphoneserver")
        checks.append(True)
    except KeyError:
        pass
    return any(checks)


@app.route("/")
def index():
    from lib.constants import (
        GO_VERSION, APT_PACKAGES, TK_USER, USER_GROUPS, GOPATH, GOBIN
    )
    steps_data = build_steps()
    return render_template_string(
        HTML,
        version             = WIZARD_VERSION,
        steps               = [
            {"name": s.name, "icon": STEP_ICONS[s.status], "optional": s.optional, "description": s.description}
            for s in steps_data
        ],
        n_steps             = len(steps_data),
        sysinfo             = _sysinfo,
        default_hostname    = DEFAULT_HOSTNAME,
        dry_run             = _dry_run,
        step_help           = _load_step_help(),
        go_version          = GO_VERSION,
        apt_packages        = APT_PACKAGES,
        tk_user             = TK_USER,
        user_groups         = USER_GROUPS,
        gopath              = str(GOPATH),
        gobin               = str(GOBIN),
        rollback_available  = _check_rollback_available(),
    )


@app.route("/save_env", methods=["POST"])
def save_env():
    from lib.constants import TK_USER, TK_GROUP
    data = request.get_json(force=True)
    content = (
        "# Generato dal setup wizard DoorPhoneServer\n"
        f"MUMBLE_USERNAME={data.get('env_mumble_username','')}\n"
        f"MUMBLE_PASSWORD={data.get('env_mumble_password','')}\n"
        f"CAMERA_USERNAME={data.get('env_camera_username','')}\n"
        f"CAMERA_PASSWORD={data.get('env_camera_password','')}\n"
        f"PUSHOVER_API_TOKEN={data.get('env_pushover_token','')}\n"
        f"PUSHOVER_USER_KEY={data.get('env_pushover_key','')}\n"
        f"OPENROUTER_API_KEY={data.get('env_openrouter_key','')}\n"
    )
    env_path = Path(f"/home/{TK_USER}/.env")
    try:
        # Usa sudo -n tee per scrivere il file (di proprietà di TK_USER).
        # -n: mai prompt password (richiede NOPASSWD, vedi prerequisiti INSTALL.md).
        r = subprocess.run(
            ["sudo", "-n", "tee", str(env_path)],
            input=content, capture_output=True, text=True
        )
        if r.returncode != 0:
            return jsonify({"ok": False, "error": r.stderr.strip() or "sudo tee fallito"})
        # 660 (non 640): così l'utente pi può salvare .env da VSCode
        subprocess.run(["sudo", "-n", "chmod", "660", str(env_path)])
        subprocess.run(["sudo", "-n", "chown", f"{TK_USER}:{TK_GROUP}", str(env_path)])
        return jsonify({"ok": True})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


@app.route("/start", methods=["POST"])
def start():
    if _state["running"]:
        return jsonify({"ok": False, "error": "Installazione già in corso"})

    data = request.get_json(force=True)
    try:
        run_dry = bool(data.get("dry_run", _dry_run))
        hostname    = validate_hostname(data.get("hostname", DEFAULT_HOSTNAME))
        play_cards, cap_cards = detect_audio_cards()
        config = {
            "hostname":           hostname,
            "play_card":          validate_card_index(data.get("play_card", 1), play_cards),
            "play_dev":           0,
            "cap_card":           validate_card_index(data.get("cap_card", 1), cap_cards),
            "cap_dev":            0,
            "_audio_autodetect":  not play_cards,
            "install_log2ram":       bool(data.get("install_log2ram", True)),
            "log2ram_size":          data.get("log2ram_size", "128M"),
            "log2ram_zl2r":          bool(data.get("log2ram_zl2r", False)),
            "log2ram_comp_alg":      data.get("log2ram_comp_alg", "lz4"),
            "log2ram_log_disk_size": data.get("log2ram_log_disk_size", "256M"),
            "env_mumble_username": data.get("env_mumble_username", ""),
            "env_mumble_password": data.get("env_mumble_password", ""),
            # Certificato server Mumble fornito dall'utente (opzionale): se presente
            # viene "pinnato" così i tablet non devono riaccettarlo dopo la migrazione.
            "mumble_cert_pem":     data.get("mumble_cert_pem", ""),
            "mumble_key_pem":      data.get("mumble_key_pem", ""),
            "env_camera_username": data.get("env_camera_username", ""),
            "env_camera_password": data.get("env_camera_password", ""),
            "env_pushover_token":  data.get("env_pushover_token", ""),
            "env_pushover_key":    data.get("env_pushover_key", ""),
            "env_openrouter_key":  data.get("env_openrouter_key", ""),
        }
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})

    _abort_event.clear()
    _state["running"] = True
    _state["failed"]  = []

    runner = Runner(dry_run=run_dry)
    runner.set_log_callback(_log_cb)

    steps = build_steps()

    def on_step_status(step_obj, new_status):
        idx = steps.index(step_obj)
        _broadcast({
            "type":   "step",
            "idx":    idx,
            "status": new_status.name,
            "name":   step_obj.name,
            "desc":   step_obj.description,
        })

    for step in steps:
        step.set_callback(on_step_status)

    def run_thread():
        import traceback
        failed = []
        try:
            for i, step in enumerate(steps):
                if _abort_event.is_set():
                    _broadcast({"type": "aborted"})
                    break

                # Breve pausa visiva tra i blocchi: lascia un attimo per vedere lo
                # stato finale del blocco precedente prima che parta il successivo.
                if i > 0:
                    time.sleep(STEP_VISUAL_PAUSE)

                # Pausa per inserimento credenziali prima di scrivere .env
                if step.name == "Credenziali .env" and not runner.dry_run:
                    _pause_event.clear()
                    _broadcast({"type": "pause", "idx": i, "name": step.name})
                    _log_cb("  ⏸ In attesa delle credenziali dall'utente...")
                    while not _pause_event.wait(timeout=1.0):
                        if _abort_event.is_set():
                            break
                    if _abort_event.is_set():
                        _broadcast({"type": "aborted"})
                        break
                    config.update(_pause_data)
                    _log_cb("  ▶ Credenziali ricevute, continuo...")

                # Pausa interattiva audio: alsa-utils è ora installato, quindi
                # l'utente può testare le schede sul sistema reale e scegliere
                # quella corretta PRIMA che lo step scriva asound.conf e l'XML.
                # Evita che un autodetect sbagliato finisca in produzione.
                if step.name == "Configurazione Audio" and not runner.dry_run:
                    _pause_event.clear()
                    _broadcast({"type": "pause_audio", "idx": i, "name": step.name})
                    _log_cb("  ⏸ In attesa del test audio e della scelta scheda...")
                    while not _pause_event.wait(timeout=1.0):
                        if _abort_event.is_set():
                            break
                    if _abort_event.is_set():
                        _broadcast({"type": "aborted"})
                        break
                    if _pause_data.get("play_card") is not None:
                        config["play_card"] = int(_pause_data["play_card"])
                        config["cap_card"]  = int(_pause_data.get("cap_card", _pause_data["play_card"]))
                        config["_audio_autodetect"] = False
                        _log_cb(
                            f"  ▶ Schede confermate dall'utente: "
                            f"output card {config['play_card']}, input card {config['cap_card']}"
                        )

                _log_cb(f"\n► Passo {i+1}/{len(steps)}: {step.name}")
                try:
                    ok = step.execute(runner, _sysinfo, config)
                except Exception as exc:
                    _log_cb(f"\n✗ ECCEZIONE in '{step.name}': {exc}")
                    logging.error(traceback.format_exc())
                    ok = False
                if not ok:
                    failed.append(step.name)
                    # Un blocco CRITICO (non opzionale) fallito interrompe
                    # l'installazione: proseguire sarebbe inutile (i passi
                    # successivi fallirebbero a catena). I blocchi opzionali
                    # (es. Log2Ram) invece non bloccano.
                    if not getattr(step, "optional", False):
                        _log_cb(
                            f"\n✗ Blocco critico '{step.name}' fallito — "
                            f"installazione interrotta. Risolvi il problema e rilancia."
                        )
                        break
        except Exception as exc:
            _log_cb(f"\n✗ ERRORE INATTESO nel runner: {exc}")
            logging.error(traceback.format_exc())
        finally:
            _state["running"] = False
            _state["failed"]  = failed
            _broadcast({"type": "done", "failed": failed})

    threading.Thread(target=run_thread, daemon=True).start()
    return jsonify({"ok": True})


@app.route("/abort", methods=["POST"])
def abort():
    _abort_event.set()
    _pause_event.set()  # sblocca anche eventuale pausa
    return jsonify({"ok": True})


@app.route("/resume", methods=["POST"])
def resume():
    global _pause_data
    _pause_data = request.get_json(force=True) or {}
    _pause_event.set()
    return jsonify({"ok": True})


@app.route("/stream")
def stream():
    q = queue.Queue(maxsize=200)
    _subscribers.append(q)

    def generate():
        try:
            while True:
                try:
                    event = q.get(timeout=25)
                    yield f"data: {json.dumps(event)}\n\n"
                except queue.Empty:
                    yield ": keepalive\n\n"
        except GeneratorExit:
            pass
        finally:
            if q in _subscribers:
                _subscribers.remove(q)

    return Response(
        generate(),
        mimetype="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "X-Accel-Buffering": "no",
        }
    )


# ── Rollback endpoints ────────────────────────────────────────────────────────

def _rollback_broadcast(line: str):
    with _rollback_lock:
        dead = []
        for q in _rollback_subs:
            try:
                q.put_nowait(line)
            except Exception:
                dead.append(q)
        for q in dead:
            _rollback_subs.remove(q)


@app.route("/rollback", methods=["POST"])
def rollback_start():
    global _rollback_proc
    with _rollback_lock:
        if _rollback_proc and _rollback_proc.poll() is None:
            return jsonify({"ok": False, "error": "Rollback già in corso"})
        script = os.path.join(os.path.dirname(_HERE), "rollback.sh")
        if not os.path.isfile(script):
            return jsonify({"ok": False, "error": f"rollback.sh non trovato: {script}"})
        _rollback_proc = subprocess.Popen(
            ["bash", script, "--yes"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
        )

    def _reader():
        for line in _rollback_proc.stdout:
            _rollback_broadcast(line.rstrip("\n"))
        _rollback_proc.wait()
        rc = _rollback_proc.returncode
        _rollback_broadcast(f"__DONE__:{rc}")

    threading.Thread(target=_reader, daemon=True).start()
    return jsonify({"ok": True})


@app.route("/rollback_abort", methods=["POST"])
def rollback_abort():
    global _rollback_proc
    with _rollback_lock:
        if _rollback_proc and _rollback_proc.poll() is None:
            _rollback_proc.terminate()
    return jsonify({"ok": True})


@app.route("/rollback_stream")
def rollback_stream():
    q = queue.Queue(maxsize=500)
    with _rollback_lock:
        _rollback_subs.append(q)

    def generate():
        try:
            while True:
                try:
                    line = q.get(timeout=30)
                    yield f"data: {json.dumps({'line': line})}\n\n"
                    if line.startswith("__DONE__:"):
                        break
                except queue.Empty:
                    yield ": keepalive\n\n"
        except GeneratorExit:
            pass
        finally:
            with _rollback_lock:
                if q in _rollback_subs:
                    _rollback_subs.remove(q)

    return Response(
        generate(),
        mimetype="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


# ── Audio test helpers ────────────────────────────────────────────────────────

def _stop_audio():
    global _audio_proc
    if _audio_proc and _audio_proc.poll() is None:
        _audio_proc.terminate()
        try:
            _audio_proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            _audio_proc.kill()
    _audio_proc = None


def _stop_vu_proc():
    global _vu_proc
    if _vu_proc and _vu_proc.poll() is None:
        _vu_proc.terminate()
        try:
            _vu_proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            _vu_proc.kill()
    _vu_proc = None


def _vu_compute(raw: bytes):
    if len(raw) < 2:
        return {'level': 0, 'db': -60.0, 'L': 0, 'R': 0, 'dbL': -60.0, 'dbR': -60.0}
    n = len(raw) // 2
    samples = struct.unpack(f"{n}h", raw)

    def _chan(samps):
        rms = (sum(s * s for s in samps) / len(samps)) ** 0.5
        db  = round(20 * math.log10(rms / 32768), 1) if rms > 0 else -60.0
        return max(0, min(100, int((db + 60) / 60 * 100))), db

    if n >= 4 and n % 2 == 0:
        lL, dbL = _chan(samples[0::2])
        lR, dbR = _chan(samples[1::2])
    else:
        lL, dbL = lR, dbR = _chan(samples)

    rms   = (sum(s * s for s in samples) / len(samples)) ** 0.5
    db    = round(20 * math.log10(rms / 32768), 1) if rms > 0 else -60.0
    level = max(0, min(100, int((db + 60) / 60 * 100)))
    return {'level': level, 'db': db, 'L': lL, 'R': lR, 'dbL': dbL, 'dbR': dbR}


def _vu_publish(data: dict):
    with _vu_subs_lock:
        for q in list(_vu_subs):
            try:
                q.put_nowait(data)
            except Exception:
                pass


def _vu_worker():
    """Thread unico che legge il microfono e pubblica ai client SSE."""
    import select as _sel, time as _t
    global _vu_proc
    CHUNK = 2048
    proc = None
    while True:
        # Modalità attesa (rec_start in corso)
        if _preparing_rec:
            if proc:
                try: proc.terminate()
                except: pass
                proc = None
            _t.sleep(0.05)
            continue

        # Modalità ring-buffer (registrazione attiva)
        if _rec_proc and _rec_proc.poll() is None:
            if proc:
                try: proc.terminate()
                except: pass
                proc = None
            if _rec_buf:
                _vu_publish(_vu_compute(_rec_buf[-1]))
            _t.sleep(0.1)
            continue

        # Modalità normale: avvia/mantieni arecord
        if proc is None or proc.poll() is not None:
            if proc:
                try: proc.terminate()
                except: pass
            _t.sleep(0.15)
            if _preparing_rec:   # ricontrollo dopo il sleep: rec_start potrebbe aver appena settato il flag
                continue
            try:
                ch = get_card_channels(_vu_card, _vu_dev, stream="capture")
                proc = subprocess.Popen(
                    ["arecord", "-D", f"hw:{_vu_card},{_vu_dev}",
                     "-f", "S16_LE", "-r", "48000", "-c", str(ch)],
                    stdout=subprocess.PIPE, stderr=subprocess.DEVNULL
                )
                _vu_proc = proc
            except Exception:
                _t.sleep(0.5)
                continue

        try:
            r, _, _ = _sel.select([proc.stdout], [], [], 0.2)
            if r:
                raw = os.read(proc.stdout.fileno(), CHUNK * 2)
                if raw:
                    _vu_publish(_vu_compute(raw))
                else:
                    proc = None
            # se nessun dato, non pubblica (bar rimane all'ultimo valore)
        except OSError:
            proc = None


def _amixer_controls(card: int) -> list:
    try:
        r = subprocess.run(["amixer", "-c", str(card), "scontrols"],
                           capture_output=True, text=True, timeout=5)
        controls = []
        for line in r.stdout.splitlines():
            m = re.search(r"Simple mixer control '([^']+)'", line)
            if m:
                controls.append(m.group(1))
        return controls
    except Exception:
        return []


def _amixer_get(card: int, control: str, prefer_capture: bool = False):
    try:
        r = subprocess.run(["amixer", "-c", str(card), "sget", control],
                           capture_output=True, text=True, timeout=5)
        out = r.stdout
        if prefer_capture and "Capture" in out:
            m_vol  = re.search(r"Capture\s+\d+\s+\[(\d+)%\]", out)
            m_mute = re.search(r"Capture\s+\d+\s+\[[^\]]+\](?:\s+\[[^\]]+\])?\s+\[(on|off)\]", out)
            if m_vol:
                return {
                    "volume": int(m_vol.group(1)),
                    "muted":  m_mute.group(1) == "off" if m_mute else False,
                    "mode":   "capture",
                }
        m_vol  = re.search(r"\[(\d+)%\]", out)
        m_mute = re.search(r"\[(on|off)\]", out)
        return {
            "volume": int(m_vol.group(1)) if m_vol else None,
            "muted":  m_mute.group(1) == "off" if m_mute else False,
            "mode":   "playback",
        }
    except Exception:
        return {"volume": None, "muted": False, "mode": "playback"}


# ── Audio routes ──────────────────────────────────────────────────────────────

@app.route("/audio/refresh_cards")
def audio_refresh_cards():
    play_cards, cap_cards = detect_audio_cards()
    return jsonify({
        "play_cards": [{"index": c.index, "name": c.name,
                        "channels": get_card_channels(c.index, 0, "playback")} for c in play_cards],
        "cap_cards":  [{"index": c.index, "name": c.name,
                        "channels": get_card_channels(c.index, 0, "capture")}  for c in cap_cards],
    })


@app.route("/audio/info")
def audio_info():
    play_card = int(request.args.get("play_card", 0))
    cap_card  = int(request.args.get("cap_card",  0))
    play_ctrls = _amixer_controls(play_card)
    cap_ctrls  = _amixer_controls(cap_card) if cap_card != play_card else play_ctrls
    result = {"play": [], "cap": []}
    for c in play_ctrls:
        info = _amixer_get(play_card, c)
        if info["volume"] is not None:
            result["play"].append({"name": c, "card": play_card, **info})
    for c in cap_ctrls:
        info = _amixer_get(cap_card, c, prefer_capture=True)
        if info["volume"] is not None:
            result["cap"].append({"name": c, "card": cap_card, **info})
    try:
        r = subprocess.run(["amixer", "-c", str(cap_card), "sget", "Auto Gain Control"],
                           capture_output=True, text=True, timeout=5)
        result["agc"] = ("[on]" in r.stdout) if r.returncode == 0 else None
    except Exception:
        result["agc"] = None
    return jsonify(result)


@app.route("/audio/play_test", methods=["POST"])
def audio_play_test():
    global _audio_proc
    data = request.get_json(force=True)
    card = int(data.get("card", 0))
    dev  = int(data.get("dev",  0))
    _stop_audio()
    try:
        _audio_proc = subprocess.Popen(
            ["speaker-test", "-D", f"hw:{card},{dev}", "-t", "sine", "-f", "1000", "-l", "3", "-c", "2"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
        )
        return jsonify({"ok": True, "msg": f"Tono 1kHz su hw:{card},{dev}"})
    except FileNotFoundError:
        return jsonify({"ok": False, "error": "speaker-test non trovato (installa alsa-utils)"})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


def _stop_rec():
    global _rec_proc
    if _rec_proc and _rec_proc.poll() is None:
        _rec_proc.terminate()
        try:
            _rec_proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            _rec_proc.kill()
    _rec_proc = None


def _rec_reader_fn(proc):
    """Legge il PCM dal processo di registrazione, scrive su file e alimenta _rec_buf."""
    with open(_REC_RAW, 'wb') as f:
        while True:
            chunk = proc.stdout.read(4096)
            if not chunk:
                break
            f.write(chunk)
            _rec_buf.append(chunk)


@app.route("/audio/rec_start", methods=["POST"])
def audio_rec_start():
    global _rec_proc, _rec_thread, _preparing_rec
    data     = request.get_json(force=True)
    cap_card = int(data.get("cap_card", 0))
    cap_dev  = int(data.get("cap_dev",  0))
    _stop_rec()
    _preparing_rec = True          # blocca il VU worker dal riaprire hw
    _stop_vu_proc()
    _rec_buf.clear()
    import time; time.sleep(0.20)  # attende che il worker entri in modalità attesa
    try:
        ch = get_card_channels(cap_card, cap_dev, stream="capture")
        _rec_proc = subprocess.Popen(
            ["arecord", "-D", f"hw:{cap_card},{cap_dev}",
             "-f", "S16_LE", "-r", "48000", "-c", str(ch)],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        import time as _t; _t.sleep(0.1)
        if _rec_proc.poll() is not None:
            err = _rec_proc.stderr.read(200).decode(errors="ignore")
            _preparing_rec = False
            return jsonify({"ok": False, "error": f"arecord fallito: {err}"})
        _preparing_rec = False
        _rec_thread = threading.Thread(target=_rec_reader_fn, args=(_rec_proc,), daemon=True)
        _rec_thread.start()
        return jsonify({"ok": True})
    except Exception as e:
        _preparing_rec = False
        return jsonify({"ok": False, "error": str(e)})


@app.route("/audio/rec_stop", methods=["POST"])
def audio_rec_stop():
    global _rec_thread
    _stop_rec()
    import time, os, wave
    if _rec_thread:
        _rec_thread.join(timeout=1)
        _rec_thread = None
    if not os.path.exists(_REC_RAW) or os.path.getsize(_REC_RAW) < 2:
        return jsonify({"ok": False, "error": "File registrazione vuoto o assente"})
    try:
        with wave.open(_REC_TMP, 'wb') as wf:
            wf.setnchannels(1)
            wf.setsampwidth(2)
            wf.setframerate(48000)
            with open(_REC_RAW, 'rb') as f:
                wf.writeframes(f.read())
        return jsonify({"ok": True})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


@app.route("/audio/rec_play", methods=["POST"])
def audio_rec_play():
    global _audio_proc
    import os
    if not os.path.exists(_REC_TMP) or os.path.getsize(_REC_TMP) <= 44:
        return jsonify({"ok": False, "error": "Nessuna registrazione disponibile"})
    data      = request.get_json(force=True)
    play_card = int(data.get("play_card", 0))
    play_dev  = int(data.get("play_dev",  0))
    _stop_audio()
    _play_vu_queue.clear()
    try:
        _audio_proc = subprocess.Popen(
            ["aplay", "-D", f"plughw:{play_card},{play_dev}", "-vv", _REC_TMP],
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE
        )
        # thread che legge il livello da stderr di aplay -vv
        def _feed_play_vu(proc):
            leftover = ""
            while proc.poll() is None:
                try:
                    chunk = os.read(proc.stderr.fileno(), 512)
                    if not chunk: break
                    leftover += chunk.decode("utf-8", errors="ignore")
                    m = re.search(r"(\d{1,3})%", leftover)
                    if m:
                        _play_vu_queue.append(int(m.group(1)))
                        leftover = ""
                except OSError:
                    break
        threading.Thread(target=_feed_play_vu, args=(_audio_proc,), daemon=True).start()
        return jsonify({"ok": True, "msg": "Riproduzione in corso..."})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


@app.route("/audio/playback_vu_stream")
def audio_playback_vu_stream():
    """Legge i livelli da _play_vu_queue durante la riproduzione."""
    import time as _t
    def generate():
        while True:
            if _audio_proc is None or _audio_proc.poll() is not None:
                yield f"data: {json.dumps({'level': 0, 'L': 0, 'R': 0, 'done': True})}\n\n"
                break
            if _play_vu_queue:
                level = _play_vu_queue.popleft()
                yield f"data: {json.dumps({'level': level, 'L': level, 'R': level})}\n\n"
            else:
                yield ": ka\n\n"
            _t.sleep(0.05)
    return Response(generate(), mimetype="text/event-stream",
                    headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"})


@app.route("/audio/stop", methods=["POST"])
def audio_stop():
    _stop_audio()
    return jsonify({"ok": True})


@app.route("/audio/vu_stream")
def audio_vu_stream():
    global _vu_worker_thread, _vu_card, _vu_dev
    card = int(request.args.get("card", 0))
    dev  = int(request.args.get("dev",  0))
    _vu_card = card
    _vu_dev  = dev

    # Avvia il worker se non è già in esecuzione
    if _vu_worker_thread is None or not _vu_worker_thread.is_alive():
        _vu_worker_thread = threading.Thread(target=_vu_worker, daemon=True)
        _vu_worker_thread.start()

    my_q: _queue_mod.Queue = _queue_mod.Queue(maxsize=20)
    with _vu_subs_lock:
        _vu_subs.append(my_q)

    def generate():
        try:
            while True:
                try:
                    data = my_q.get(timeout=0.4)
                    yield f"data: {json.dumps(data)}\n\n"
                except _queue_mod.Empty:
                    yield ": ka\n\n"
        except GeneratorExit:
            pass
        finally:
            with _vu_subs_lock:
                try: _vu_subs.remove(my_q)
                except ValueError: pass

    return Response(generate(), mimetype="text/event-stream",
                    headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"})


@app.route("/audio/play_vu_stream")
def audio_play_vu_stream():
    """Genera tono 1kHz → aplay -vv, parsa il livello dal suo stderr."""
    import select
    card = int(request.args.get("card", 0))
    dev  = int(request.args.get("dev",  0))
    RATE, FREQ, BLOCK = 48000, 1000, 4800   # 100ms di campioni, stereo

    def _write_tone(stdin, stop):
        t = 0
        try:
            while not stop.is_set():
                buf = bytearray()
                for _ in range(BLOCK):
                    s = int(32767 * 0.7 * math.sin(2 * math.pi * FREQ * t / RATE))
                    buf += struct.pack("<hh", s, s)  # stereo L+R
                    t += 1
                stdin.write(bytes(buf))
                stdin.flush()
        except (BrokenPipeError, OSError, ValueError):
            pass

    def generate():
        global _audio_proc
        aplay = stop = None
        try:
            aplay = subprocess.Popen(
                ["aplay", "-D", f"plughw:{card},{dev}",
                 "-f", "S16_LE", "-r", str(RATE), "-c", "2",
                 "-t", "raw", "-vv"],
                stdin=subprocess.PIPE, stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE
            )
            _audio_proc = aplay
            stop = threading.Event()
            threading.Thread(target=_write_tone,
                             args=(aplay.stdin, stop), daemon=True).start()
            leftover = ""
            while True:
                if aplay.poll() is not None:
                    break
                r, _, _ = select.select([aplay.stderr], [], [], 0.15)
                if r:
                    raw = os.read(aplay.stderr.fileno(), 512)
                    if not raw:
                        break
                    leftover += raw.decode("utf-8", errors="ignore")
                    m = re.search(r"(\d{1,3})%", leftover)
                    if m:
                        level = min(100, int(m.group(1)))
                        leftover = ""
                        yield f"data: {json.dumps({'level': level, 'L': level, 'R': level})}\n\n"
                else:
                    yield ": ka\n\n"
        except GeneratorExit:
            pass
        finally:
            if stop:
                stop.set()
            if aplay:
                try: aplay.stdin.close()
                except OSError: pass
                if aplay.poll() is None:
                    aplay.terminate()
                    try: aplay.wait(timeout=1)
                    except subprocess.TimeoutExpired: aplay.kill()
            _audio_proc = None

    return Response(generate(), mimetype="text/event-stream",
                    headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"})


@app.route("/audio/play_stop", methods=["POST"])
def audio_play_stop():
    _stop_audio()
    return jsonify({"ok": True})


@app.route("/audio/agc", methods=["POST"])
def audio_agc():
    data  = request.get_json(force=True)
    card  = int(data.get("card", 0))
    state = "on" if data.get("enabled", False) else "off"
    try:
        r = subprocess.run(
            ["amixer", "-c", str(card), "sset", "Auto Gain Control", state],
            capture_output=True, text=True, timeout=5
        )
        if r.returncode != 0:
            return jsonify({"ok": False, "error": "AGC non disponibile su questa scheda"})
        return jsonify({"ok": True})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


@app.route("/audio/list_files")
def audio_list_files():
    import pathlib
    # Durante il setup i file di test stanno nel repo clonato (REPO_ROOT/soundfiles);
    # dopo l'installazione anche in /home/doorphoneserver/soundfiles. Cerca in
    # entrambi così il test audio funziona sia durante che dopo l'installazione.
    candidates = [
        REPO_ROOT / "soundfiles",
        pathlib.Path("/home/doorphoneserver/soundfiles"),
    ]
    exts = {".wav", ".mp3", ".ogg", ".flac"}
    result = []
    seen = set()
    for base in candidates:
        if not base.exists():
            continue
        for f in sorted(base.rglob("*")):
            if f.suffix.lower() in exts and f.is_file():
                label = str(f.relative_to(base))
                if label in seen:
                    continue
                seen.add(label)
                result.append({"path": str(f), "label": label})
    return jsonify(result)


@app.route("/audio/play_file", methods=["POST"])
def audio_play_file():
    global _audio_proc
    data      = request.get_json(force=True)
    filepath  = data.get("path", "")
    play_card = int(data.get("play_card", 0))
    play_dev  = int(data.get("play_dev",  0))
    import pathlib, shutil
    p = pathlib.Path(filepath)
    if not p.exists() or not p.is_file():
        return jsonify({"ok": False, "error": "File non trovato"})
    _stop_audio()
    _play_vu_queue.clear()
    try:
        ffmpeg_proc = subprocess.Popen(
            ["ffmpeg", "-i", str(p), "-f", "s16le", "-ar", "48000", "-ac", "2", "-"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL
        )
        _audio_proc = subprocess.Popen(
            ["aplay", "-D", f"plughw:{play_card},{play_dev}",
             "-f", "S16_LE", "-r", "48000", "-c", "2", "-vv"],
            stdin=ffmpeg_proc.stdout, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE
        )
        ffmpeg_proc.stdout.close()

        def _feed(proc):
            leftover = ""
            while proc.poll() is None:
                try:
                    chunk = os.read(proc.stderr.fileno(), 512)
                    if not chunk: break
                    leftover += chunk.decode("utf-8", errors="ignore")
                    m = re.search(r"(\d{1,3})%", leftover)
                    if m:
                        _play_vu_queue.append(int(m.group(1)))
                        leftover = ""
                except OSError:
                    break
        threading.Thread(target=_feed, args=(_audio_proc,), daemon=True).start()
        return jsonify({"ok": True, "msg": f"Riproduzione: {p.name}"})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


@app.route("/audio/set_volume", methods=["POST"])
def audio_set_volume():
    data    = request.get_json(force=True)
    card    = int(data.get("card", 0))
    control = data.get("control", "PCM")
    volume  = max(0, min(100, int(data.get("volume", 80))))
    mode    = data.get("mode", "playback")  # "playback" o "capture"
    cmd = ["amixer", "-c", str(card), "sset", control]
    if mode == "capture":
        cmd += ["capture", f"{volume}%"]
    else:
        cmd += [f"{volume}%"]
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
        return jsonify({"ok": r.returncode == 0,
                        "error": r.stderr.strip()[:100] if r.returncode != 0 else ""})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


# ── Logo ─────────────────────────────────────────────────────────────────────

@app.route("/logo.svg")
def serve_logo():
    logo_path = os.path.join(os.path.dirname(_HERE), "logo.svg")
    if os.path.exists(logo_path):
        with open(logo_path, "rb") as f:
            return f.read(), 200, {"Content-Type": "image/svg+xml", "Cache-Control": "max-age=3600"}
    return "", 404


# ── Entry point ───────────────────────────────────────────────────────────────

def run_webui(port: int = 8888, dry_run: bool = False):
    """Avvia il server Web UI. Chiamato da wizard.py --web."""
    global _dry_run
    _dry_run = dry_run

    # Flask cerca automaticamente .env nella cwd; lo disabilitiamo perché
    # /home/doorphoneserver/.env appartiene a un altro utente (PermissionError).
    os.environ["FLASK_SKIP_DOTENV"] = "1"

    from lib.constants import LOG_FILE
    if LOG_FILE.exists():
        try:
            LOG_FILE.open("a").close()
        except PermissionError:
            LOG_FILE.unlink(missing_ok=True)
    root_logger = logging.getLogger()
    if not root_logger.handlers:
        logging.basicConfig(
            filename=str(LOG_FILE),
            level=logging.DEBUG,
            format="%(asctime)s %(levelname)s %(message)s",
        )

    # Determina l'IP locale per mostrare l'URL
    import socket
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        local_ip = s.getsockname()[0]
        s.close()
    except Exception:
        local_ip = "localhost"

    print("=" * 60)
    print(f"  DoorPhoneServer Setup Wizard v{WIZARD_VERSION} — Web UI")
    if dry_run:
        print("  ⚠  MODALITÀ DRY-RUN — nessuna modifica verrà applicata")
    print(f"  Apri nel browser:")
    print(f"  ➜  http://{local_ip}:{port}")
    print(f"  ➜  http://localhost:{port}")
    print(f"  Log: {LOG_FILE}")
    print("=" * 60)
    print()

    try:
        app.run(host="0.0.0.0", port=port, threaded=True, debug=False)
    except OSError as e:
        if "Address already in use" in str(e):
            print(f"\n  ✗ Porta {port} già in uso — un'altra istanza del wizard è in esecuzione.")
            print(f"  Fermala con: kill $(lsof -ti:{port})")
        else:
            raise


if __name__ == "__main__":
    run_webui()

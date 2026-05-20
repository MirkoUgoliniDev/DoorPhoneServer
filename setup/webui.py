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
import queue
import struct
import subprocess
import threading
import logging

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

from flask import Flask, Response, request, jsonify, render_template_string

from lib.constants import WIZARD_VERSION, DEFAULT_HOSTNAME, LOG_FILE
from lib.runner   import Runner, get_abort_event
from lib.sysinfo  import SystemInfo
from lib.step_base import Status, STEP_ICONS, validate_hostname
from lib.audio_utils import detect_audio_cards, validate_card_index
from steps import build_steps

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
  .progress-bar { transition:width .4s ease; }
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
</style>
</head>
<body class="flex">

<!-- SIDEBAR -->
<aside class="sidebar w-56 flex-shrink-0 p-4 flex flex-col">
  <div class="mb-6">
    <div class="text-lg font-bold" style="color:var(--accent)">DoorPhoneServer</div>
    <div class="text-xs mt-1" style="color:var(--muted)">Setup Wizard v{{ version }}</div>
  </div>
  <div class="text-xs font-bold mb-2 tracking-widest" style="color:var(--muted)">PASSI</div>
  <ul id="stepList" class="space-y-1 flex-1">
    {% for s in steps %}
    <li id="step-{{ loop.index0 }}" class="flex items-center gap-2 text-sm px-2 py-1 rounded"
        style="color:#cdd6f4">
      <span class="step-icon" id="icon-{{ loop.index0 }}" style="color:var(--muted)">{{ s.icon }}</span>
      <span>{{ s.name }}</span>
      {% if s.optional %}<span class="badge" style="background:#45475a;color:var(--muted)">opt</span>{% endif %}
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

  <!-- Toggle DRY-RUN -->
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

  <!-- Header -->
  <div class="flex items-center justify-between">
    <div>
      <h1 id="curStepTitle" class="text-2xl font-bold" style="color:var(--accent)">
        Configura e avvia
      </h1>
      <p id="curStepDesc" class="text-sm mt-1" style="color:var(--muted)">
        Compila le opzioni e clicca Avvia Installazione
      </p>
    </div>
    <div id="statusBadge" class="hidden badge text-sm px-3 py-1"></div>
  </div>

  <!-- Config form -->
  <div id="configSection" class="flex flex-col gap-4">

    <!-- Blocco: Credenziali -->
    <div class="card p-5">
      <div class="flex items-center justify-between mb-3 cursor-pointer" onclick="toggleEnv()">
        <span class="text-sm font-bold tracking-widest" style="color:var(--muted)">🔑 CREDENZIALI (.env)</span>
        <span id="envToggleIcon" style="color:var(--muted)">▼</span>
      </div>
      <div id="envSection" class="flex flex-col gap-4">

        <!-- Mumble -->
        <div class="flex flex-col gap-2">
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
                <button type="button" onclick="togglePwd('env_mumble_password')"
                  style="position:absolute;right:.5rem;top:50%;transform:translateY(-50%);color:var(--muted)">👁</button>
              </div>
            </div>
          </div>
        </div>

        <hr style="border-color:#313244">

        <!-- Camera IP -->
        <div class="flex flex-col gap-2">
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
                <button type="button" onclick="togglePwd('env_camera_password')"
                  style="position:absolute;right:.5rem;top:50%;transform:translateY(-50%);color:var(--muted)">👁</button>
              </div>
            </div>
          </div>
        </div>

        <hr style="border-color:#313244">

        <!-- Pushover -->
        <div class="flex flex-col gap-2">
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

        <hr style="border-color:#313244">

        <!-- OpenRouter -->
        <div class="flex flex-col gap-2">
          <span class="text-xs font-bold" style="color:var(--accent)">OPENROUTER</span>
          <div style="position:relative;width:100%">
            <input type="password" id="env_openrouter_key" placeholder="sk-or-v1-...">
            <button type="button" onclick="togglePwd('env_openrouter_key')"
              style="position:absolute;right:.5rem;top:50%;transform:translateY(-50%);color:var(--muted)">👁</button>
          </div>
        </div>

        <div class="flex items-center gap-3 mt-1">
          <button class="btn-primary" style="font-size:.85rem;padding:.4rem 1rem" onclick="saveEnv()">
            💾 Salva .env ora
          </button>
          <span id="envSaveMsg" class="text-xs" style="color:var(--success)"></span>
        </div>
      </div>
    </div>

    <!-- Blocco: Sistema -->
    <div class="card p-5 flex flex-col gap-4">
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">HOSTNAME</label>
        <input type="text" id="hostname" value="{{ default_hostname }}" placeholder="doorphoneserver">
        <p class="text-xs mt-1" style="color:var(--muted)">Nome del dispositivo in rete</p>
      </div>
    </div>

    <!-- Blocco: Log2Ram -->
    <div class="card p-5 flex flex-col gap-3">
      <div class="text-xs font-semibold tracking-widest mb-1" style="color:var(--muted)">💾 LOG2RAM</div>
      <div class="flex items-center gap-3">
        <label class="toggle"><input type="checkbox" id="log2ram" checked onchange="toggleLog2RamParams()"><span class="slider"></span></label>
        <div>
          <span class="text-sm font-medium">Installa Log2Ram</span>
          <span class="text-xs block" style="color:var(--muted)">Protegge la microSD tenendo i log in RAM</span>
        </div>
      </div>
      <div id="log2ramParams" class="flex flex-col gap-3 mt-1">
        <div>
          <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">SIZE — Dimensione RAM per i log</label>
          <input type="text" id="log2ram_size" value="128M" placeholder="es. 128M">
        </div>
        <div class="flex items-center gap-3">
          <label class="toggle"><input type="checkbox" id="log2ram_zl2r" onchange="toggleZramParams()"><span class="slider"></span></label>
          <div>
            <span class="text-sm">ZL2R — Usa zram (compressione in RAM)</span>
            <span class="text-xs block" style="color:var(--muted)">Risparmia RAM comprimendo i log</span>
          </div>
        </div>
        <div id="zramParams" class="flex flex-col gap-3" style="display:none!important">
          <div>
            <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">COMP_ALG — Algoritmo compressione</label>
            <select id="log2ram_comp_alg">
              <option value="lz4">lz4 (più veloce, meno compresso)</option>
              <option value="lzo">lzo</option>
              <option value="zstd">zstd (più compresso, più CPU)</option>
              <option value="zlib">zlib (deflate)</option>
            </select>
          </div>
          <div>
            <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">LOG_DISK_SIZE — Dimensione disco zram</label>
            <input type="text" id="log2ram_log_disk_size" value="256M" placeholder="es. 256M">
          </div>
        </div>
      </div>
    </div>

    <!-- Blocco: Audio -->
    <div class="card p-5 flex flex-col gap-3">
      <div class="text-xs font-semibold tracking-widest mb-1" style="color:var(--muted)">🔊 AUDIO</div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">AUDIO OUTPUT (card n.)</label>
        {% if play_cards %}
        <select id="playCard">
          {% for c in play_cards %}<option value="{{ c.index }}">{{ c.index }} — {{ c.name }}</option>{% endfor %}
        </select>
        {% else %}
        <input type="number" id="playCard" value="1" min="0" max="9">
        <p class="text-xs mt-1" style="color:var(--muted)">Auto-rilevata dopo installazione pacchetti</p>
        {% endif %}
      </div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">AUDIO INPUT (card n.)</label>
        {% if cap_cards %}
        <select id="capCard">
          {% for c in cap_cards %}<option value="{{ c.index }}">{{ c.index }} — {{ c.name }}</option>{% endfor %}
        </select>
        {% else %}
        <input type="number" id="capCard" value="1" min="0" max="9">
        {% endif %}
      </div>
      <div>
        <button onclick="openAudioModal()"
                class="btn-primary" style="background:#cba6f7;color:#1e1e2e;font-size:.85rem;padding:.4rem 1.1rem">
          🔊 Test Audio &amp; Volumi
        </button>
      </div>
    </div>

  </div>


  <!-- Blocco: Opzioni aggiuntive -->
  <div class="card p-5 flex flex-col gap-3">
    <div class="text-xs font-semibold tracking-widest mb-1" style="color:var(--muted)">⚙️ OPZIONI AGGIUNTIVE</div>
    <div class="flex items-center gap-3">
      <label class="toggle"><input type="checkbox" id="codeserver"><span class="slider"></span></label>
      <div>
        <span class="text-sm">code-server</span>
        <span class="text-xs block" style="color:var(--muted)">VSCode nel browser (porta 8080) — opzionale</span>
      </div>
    </div>
  </div>

  <!-- Progress -->
  <div class="card p-4">
    <div class="flex justify-between text-xs mb-2" style="color:var(--muted)">
      <span id="progressLabel">In attesa</span>
      <span id="progressPct">0%</span>
    </div>
    <div class="w-full rounded-full h-2" style="background:#45475a">
      <div id="progressBar" class="progress-bar h-2 rounded-full" style="width:0%;background:var(--accent)"></div>
    </div>
  </div>

  <!-- Log -->
  <div class="card p-4 flex flex-col gap-2 flex-1">
    <div class="flex items-center justify-between">
      <span class="text-xs font-bold tracking-widest" style="color:var(--muted)">LOG</span>
      <button onclick="document.getElementById('logBox').innerHTML=''"
              class="text-xs px-2 py-0.5 rounded" style="background:#45475a;color:var(--muted)">
        Pulisci
      </button>
    </div>
    <div id="logBox" class="log-box flex-1"></div>
  </div>

  <!-- Buttons -->
  <div class="flex items-center gap-3 flex-wrap">
    <button id="startBtn" class="btn-primary" onclick="startInstall()">
      ▶&nbsp; Avvia Installazione
    </button>
    <button id="abortBtn" class="btn-danger" disabled onclick="abortInstall()">
      ■&nbsp; Interrompi
    </button>
    <span id="statusText" class="text-sm ml-4" style="color:var(--muted)"></span>
  </div>

</main>

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
          <div id="playVuMeter">
            <div class="relative w-full rounded overflow-hidden" style="height:14px;background:#313244">
              <div id="playVuBar" style="height:100%;width:0%;
                   background:linear-gradient(to right,#a6e3a1 0%,#f9e2af 65%,#f38ba8 85%);
                   transition:width 80ms linear;border-radius:inherit"></div>
              <div id="playVuPeak" style="position:absolute;top:0;bottom:0;width:2px;
                   background:#f38ba8;left:0%;transition:left 80ms linear"></div>
            </div>
            <div class="flex justify-between text-xs mt-0.5" style="color:var(--muted)">
              <span>-60</span><span>-40</span><span>-20</span><span>-10</span><span>0 dB</span>
            </div>
          </div>
        </div>

        <!-- VU meter Microfono (sempre attivo) -->
        <div class="mt-2">
          <div class="flex items-center gap-2 mb-1">
            <span class="text-xs w-20 text-right flex-shrink-0" style="color:var(--muted)">Mic 🎤</span>
            <span id="vuDb" class="text-xs font-mono ml-auto" style="color:var(--muted)">-- dB</span>
          </div>
          <div id="vuMeter">
            <div class="relative w-full rounded overflow-hidden" style="height:14px;background:#313244">
              <div id="vuBar" style="height:100%;width:0%;
                   background:linear-gradient(to right,#a6e3a1 0%,#f9e2af 65%,#f38ba8 85%);
                   transition:width 80ms linear;border-radius:inherit"></div>
              <div id="vuPeak" style="position:absolute;top:0;bottom:0;width:2px;
                   background:#f38ba8;left:0%;transition:left 80ms linear"></div>
            </div>
            <div class="flex justify-between text-xs mt-0.5" style="color:var(--muted)">
              <span>-60</span><span>-40</span><span>-20</span><span>-10</span><span>0 dB</span>
            </div>
          </div>
        </div>

        <!-- AGC -->
        <div class="flex items-center gap-2 mt-3">
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
}

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

function appendLog(msg) {
  const box = document.getElementById('logBox');
  const span = document.createElement('span');
  let cls = '';
  if (msg.includes('[DRY-RUN]')) cls = 'log-dry';
  else if (/ERRORE|FAIL|✗|ECCEZIONE/.test(msg)) cls = 'log-err';
  else if (msg.startsWith('►')) cls = 'log-step';
  else if (msg.includes('  $')) cls = 'log-muted';
  if (cls) span.className = cls;
  span.textContent = msg + '\n';
  box.appendChild(span);
  box.scrollTop = box.scrollHeight;
}

function setProgress(idx, total) {
  const pct = Math.round(idx / total * 100);
  document.getElementById('progressBar').style.width = pct + '%';
  document.getElementById('progressPct').textContent = pct + '%';
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
    install_codeserver:      document.getElementById('codeserver').checked,
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
        const icon = document.getElementById('icon-' + ev.idx);
        if (icon) {
          icon.textContent = ICONS[ev.status] || '○';
          icon.style.color = ICON_COLORS[ev.status] || 'var(--muted)';
        }
        if (ev.name) {
          document.getElementById('curStepTitle').textContent = ev.name;
        }
        if (ev.desc) {
          document.getElementById('curStepDesc').textContent = ev.desc;
        }
        const done = ['DONE','SKIPPED'].includes(ev.status);
        setProgress(ev.idx + (done ? 1 : 0.5), N_STEPS);
        document.getElementById('progressLabel').textContent =
          (done ? 'Completato: ' : 'In corso: ') + (ev.name || '');
      } else if (ev.type === 'done') {
        evtSource.close();
        document.getElementById('abortBtn').disabled = true;
        document.getElementById('startBtn').disabled = false;
        document.getElementById('startBtn').textContent = '▶  Riavvia Wizard';
        document.getElementById('configSection').style.opacity = '1';
        document.getElementById('configSection').style.pointerEvents = '';
        setProgress(N_STEPS, N_STEPS);
        if (ev.failed && ev.failed.length > 0) {
          document.getElementById('statusText').textContent =
            'Completato con ' + ev.failed.length + ' errori';
          document.getElementById('statusText').style.color = 'var(--warn)';
          appendLog('\n✗ Passi falliti: ' + ev.failed.join(', '));
        } else {
          document.getElementById('statusText').textContent = '✓ Completato!';
          document.getElementById('statusText').style.color = 'var(--success)';
          if (DRY_RUN) {
            appendLog('\n✓ DRY-RUN completato. Nessuna modifica applicata al sistema.');
          } else {
            appendLog('\n✓ DoorPhoneServer installato con successo! Esegui: sudo reboot');
          }
        }
      } else if (ev.type === 'aborted') {
        evtSource.close();
        document.getElementById('abortBtn').disabled = true;
        document.getElementById('startBtn').disabled = false;
        document.getElementById('statusText').textContent = 'Interrotto';
        document.getElementById('statusText').style.color = 'var(--warn)';
      }
    };
    evtSource.onerror = () => evtSource.close();
  });
}

function abortInstall() {
  fetch('/abort', {method:'POST'});
  document.getElementById('abortBtn').disabled = true;
  document.getElementById('statusText').textContent = 'Interruzione in corso...';
}

function toggleLog2RamParams() {
  const on = document.getElementById('log2ram').checked;
  document.getElementById('log2ramParams').style.display = on ? '' : 'none';
}
function toggleZramParams() {
  const on = document.getElementById('log2ram_zl2r').checked;
  document.getElementById('zramParams').style.display = on ? '' : 'none';
}

function toggleEnv() {
  const s = document.getElementById('envSection');
  const i = document.getElementById('envToggleIcon');
  const hidden = s.style.display === 'none';
  s.style.display = hidden ? 'grid' : 'none';
  i.textContent = hidden ? '▼' : '▶';
}

function togglePwd(id) {
  const el = document.getElementById(id);
  el.type = el.type === 'password' ? 'text' : 'password';
}

function getEnvFields() {
  return {
    env_mumble_username: document.getElementById('env_mumble_username').value,
    env_mumble_password: document.getElementById('env_mumble_password').value,
    env_camera_username: document.getElementById('env_camera_username').value,
    env_camera_password: document.getElementById('env_camera_password').value,
    env_pushover_token:  document.getElementById('env_pushover_token').value,
    env_pushover_key:    document.getElementById('env_pushover_key').value,
    env_openrouter_key:  document.getElementById('env_openrouter_key').value,
  };
}

// ── Audio modal ───────────────────────────────────────────────────────────────

let _vuSource    = null;   // mic
let _vuPeak      = 0;
let _vuPeakTimer = null;
let _playVuSrc   = null;   // speaker
let _playVuPeak  = 0;
let _playVuTimer = null;
let _playVolPct  = 100;    // valore corrente slider playback (per scalare VU)
let _playRawLevel = 0;     // ultimo livello PCM grezzo ricevuto dallo stream

function openAudioModal() {
  document.getElementById('audioModal').classList.remove('hidden');
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
  stopPlayVU();
  document.getElementById('audioModal').classList.add('hidden');
}

function togglePlayVU() {
  if (_playVuSrc) { stopPlayVU(); return; }
  const card = parseInt(document.getElementById('playCard').value);
  _playVuPeak = 0;
  _playRawLevel = 0;
  _playVuSrc = new EventSource(`/audio/play_vu_stream?card=${card}&dev=0`);
  _playVuSrc.onmessage = (e) => {
    const d = JSON.parse(e.data);
    if (d.level === undefined) return;
    _playRawLevel = d.level;
    _updatePlayVuDisplay();
  };
  _playVuSrc.onerror = () => stopPlayVU();
}

function _updatePlayVuDisplay() {
  const scaled = Math.round(_playRawLevel * _playVolPct / 100);
  document.getElementById('playVuBar').style.width = scaled + '%';
  const db = Math.round((scaled / 100 * 60) - 60);
  document.getElementById('playVuDb').textContent = db + ' dB';
  if (scaled > _playVuPeak) {
    _playVuPeak = scaled;
    document.getElementById('playVuPeak').style.left = scaled + '%';
    clearTimeout(_playVuTimer);
    _playVuTimer = setTimeout(() => {
      _playVuPeak = 0;
      document.getElementById('playVuPeak').style.left = '0%';
    }, 1500);
  }
}

function stopPlayVU() {
  if (_playVuSrc) { _playVuSrc.close(); _playVuSrc = null; }
  clearTimeout(_playVuTimer);
  _playVuPeak = 0;
  _playRawLevel = 0;
  document.getElementById('playVuBar').style.width = '0%';
  document.getElementById('playVuPeak').style.left = '0%';
  document.getElementById('playVuDb').textContent  = '-- dB';
  fetch('/audio/play_stop', {method: 'POST'});
}

function toggleVU() {
  if (_vuSource) { stopVU(); return; }
  const card = parseInt(document.getElementById('capCard').value);
  _vuPeak = 0;
  _vuSource = new EventSource(`/audio/vu_stream?card=${card}&dev=0`);
  _vuSource.onmessage = (e) => {
    const d = JSON.parse(e.data);
    document.getElementById('vuBar').style.width  = d.level + '%';
    document.getElementById('vuDb').textContent   = d.db + ' dB';
    if (d.level > _vuPeak) {
      _vuPeak = d.level;
      document.getElementById('vuPeak').style.left = d.level + '%';
      clearTimeout(_vuPeakTimer);
      _vuPeakTimer = setTimeout(() => {
        _vuPeak = 0;
        document.getElementById('vuPeak').style.left = '0%';
      }, 1500);
    }
  };
  _vuSource.onerror = () => { _vuSource = null; setTimeout(()=>{ if(!_vuSource) toggleVU(); }, 1000); };
}

function stopVU() {
  if (_vuSource) { _vuSource.close(); _vuSource = null; }
  clearTimeout(_vuPeakTimer);
  _vuPeak = 0;
  document.getElementById('vuBar').style.width = '0%';
  document.getElementById('vuPeak').style.left = '0%';
  document.getElementById('vuDb').textContent  = '-- dB';
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
      if (data.agc !== undefined)
        document.getElementById('agcToggle').checked = data.agc;
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
  stopPlayVU();
  audioLog(`▶ ${sel.options[sel.selectedIndex].text}`);
  fetch('/audio/play_file', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({path, play_card: playCard, play_dev: 0})
  }).then(r => r.json()).then(d => {
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
  stopPlayVU();
  fetch('/audio/rec_play', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({play_card: playCard, play_dev: 0})
  }).then(r=>r.json()).then(d=>{
    audioLog(d.ok ? `✓ ${d.msg}` : `✗ ${d.error}`, d.ok ? 'var(--success)' : 'var(--error)');
    if (d.ok) _startPlaybackVU();
  });
}

function _startPlaybackVU() {
  if (_playVuSrc) { _playVuSrc.close(); _playVuSrc = null; }
  _playVuSrc = new EventSource('/audio/playback_vu_stream');
  _playVuSrc.onmessage = (e) => {
    const d = JSON.parse(e.data);
    if (d.done) { stopPlayVU(); return; }
    if (d.level === undefined) return;
    _playRawLevel = d.level;
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
</body>
</html>
"""


# ── Routes ────────────────────────────────────────────────────────────────────

@app.route("/")
def index():
    steps_data = build_steps()
    play_cards, cap_cards = detect_audio_cards()
    return render_template_string(
        HTML,
        version         = WIZARD_VERSION,
        steps           = [
            {"name": s.name, "icon": STEP_ICONS[s.status], "optional": s.optional}
            for s in steps_data
        ],
        n_steps         = len(steps_data),
        sysinfo         = _sysinfo,
        default_hostname= DEFAULT_HOSTNAME,
        play_cards      = play_cards,
        cap_cards       = cap_cards,
        dry_run         = _dry_run,
    )


@app.route("/save_env", methods=["POST"])
def save_env():
    from lib.constants import REPO_ROOT
    data = request.get_json(force=True)
    lines = [
        "# Generato dal setup wizard DoorPhoneServer\n",
        f"MUMBLE_USERNAME={data.get('env_mumble_username','')}\n",
        f"MUMBLE_PASSWORD={data.get('env_mumble_password','')}\n",
        f"CAMERA_USERNAME={data.get('env_camera_username','')}\n",
        f"CAMERA_PASSWORD={data.get('env_camera_password','')}\n",
        f"PUSHOVER_API_TOKEN={data.get('env_pushover_token','')}\n",
        f"PUSHOVER_USER_KEY={data.get('env_pushover_key','')}\n",
        f"OPENROUTER_API_KEY={data.get('env_openrouter_key','')}\n",
    ]
    try:
        env_path = REPO_ROOT / ".env"
        env_path.write_text("".join(lines))
        env_path.chmod(0o600)
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
            "install_codeserver":    bool(data.get("install_codeserver", False)),
            "env_mumble_username": data.get("env_mumble_username", ""),
            "env_mumble_password": data.get("env_mumble_password", ""),
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
        failed = []
        try:
            for i, step in enumerate(steps):
                if _abort_event.is_set():
                    _broadcast({"type": "aborted"})
                    break
                _log_cb(f"\n► Passo {i+1}/{len(steps)}: {step.name}")
                ok = step.execute(runner, _sysinfo, config)
                if not ok:
                    failed.append(step.name)
        except Exception as exc:
            import traceback
            _log_cb(f"\n✗ ERRORE INATTESO: {exc}")
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
    return jsonify({"ok": True})


@app.route("/stream")
def stream():
    q = queue.Queue(maxsize=200)
    _subscribers.append(q)

    def generate():
        try:
            while True:
                event = q.get(timeout=30)
                yield f"data: {json.dumps(event)}\n\n"
        except queue.Empty:
            # keepalive
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
        return {'level': 0, 'db': -60.0}
    samples = struct.unpack(f"{len(raw)//2}h", raw)
    rms = (sum(s * s for s in samples) / len(samples)) ** 0.5
    db = round(20 * math.log10(rms / 32768), 1) if rms > 0 else -60.0
    # scala -60 dB → 0%,  0 dB → 100%
    level = max(0, min(100, int((db + 60) / 60 * 100)))
    return {'level': level, 'db': db}


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
            try:
                proc = subprocess.Popen(
                    ["arecord", "-D", f"hw:{_vu_card},{_vu_dev}",
                     "-f", "S16_LE", "-r", "48000", "-c", "1"],
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
        result["agc"] = "[on]" in r.stdout
    except Exception:
        result["agc"] = False
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
    _preparing_rec = True          # blocca il VU generator dal riaprire hw
    _stop_vu_proc()
    _rec_buf.clear()
    import time; time.sleep(0.15)  # attende che il VU generator entri in modalità attesa
    try:
        _rec_proc = subprocess.Popen(
            ["arecord", "-D", f"hw:{cap_card},{cap_dev}",
             "-f", "S16_LE", "-r", "48000", "-c", "1"],
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
                yield f"data: {json.dumps({'level': 0, 'done': True})}\n\n"
                break
            if _play_vu_queue:
                level = _play_vu_queue.popleft()
                yield f"data: {json.dumps({'level': level})}\n\n"
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
                        yield f"data: {json.dumps({'level': level})}\n\n"
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
        return jsonify({"ok": r.returncode == 0})
    except Exception as e:
        return jsonify({"ok": False, "error": str(e)})


@app.route("/audio/list_files")
def audio_list_files():
    import pathlib
    base = pathlib.Path("/home/doorphoneserver/soundfiles")
    exts = {".wav", ".mp3", ".ogg", ".flac"}
    result = []
    if base.exists():
        for f in sorted(base.rglob("*")):
            if f.suffix.lower() in exts and f.is_file():
                result.append({
                    "path": str(f),
                    "label": str(f.relative_to(base))
                })
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

    app.run(host="0.0.0.0", port=port, threaded=True, debug=False)


if __name__ == "__main__":
    run_webui()

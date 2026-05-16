#!/usr/bin/env python3
"""
DoorPhoneServer Setup — Web UI
Avvia con: python3 setup/wizard.py --web
oppure:    python3 setup/webui.py
Poi apri:  http://<ip-raspberry>:8888
"""

import sys
import os
import json
import queue
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

_abort_event  = get_abort_event()
_sysinfo      = SystemInfo()
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
<title>DoorPhoneServer Setup Wizard</title>
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
  input[type=text],input[type=number],select {
    background:#1e1e2e; border:1px solid #45475a; border-radius:.4rem;
    color:#cdd6f4; padding:.35rem .6rem; width:100%;
  }
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
  .toggle { position:relative; display:inline-block; width:44px; height:24px; }
  .toggle input { opacity:0; width:0; height:0; }
  .slider { position:absolute; inset:0; background:#45475a;
            border-radius:24px; transition:.3s; cursor:pointer; }
  .slider:before { content:""; position:absolute; width:18px; height:18px;
                   left:3px; bottom:3px; background:#cdd6f4;
                   border-radius:50%; transition:.3s; }
  input:checked + .slider { background:var(--accent); }
  input:checked + .slider:before { transform:translateX(20px); }
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
  <div id="configSection" class="card p-5 grid grid-cols-2 gap-4">
    <div>
      <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">HOSTNAME</label>
      <input type="text" id="hostname" value="{{ default_hostname }}" placeholder="doorphoneserver">
      <p class="text-xs mt-1" style="color:var(--muted)">Nome del dispositivo in rete</p>
    </div>
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
    <div class="flex flex-col gap-3 justify-center">
      <label class="flex items-center gap-3 cursor-pointer">
        <label class="toggle"><input type="checkbox" id="log2ram" checked><span class="slider"></span></label>
        <span class="text-sm">Log2Ram <span style="color:var(--muted)">(protegge microSD)</span></span>
      </label>
      <label class="flex items-center gap-3 cursor-pointer">
        <label class="toggle"><input type="checkbox" id="codeserver"><span class="slider"></span></label>
        <span class="text-sm">code-server <span style="color:var(--muted)">(VSCode nel browser)</span></span>
      </label>
    </div>
  </div>

  <!-- .env credentials -->
  <div class="card p-5">
    <div class="flex items-center justify-between mb-3 cursor-pointer" onclick="toggleEnv()">
      <span class="text-sm font-bold tracking-widest" style="color:var(--muted)">🔑 CREDENZIALI (.env)</span>
      <span id="envToggleIcon" style="color:var(--muted)">▼</span>
    </div>
    <div id="envSection" class="grid grid-cols-2 gap-4">
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">MUMBLE USERNAME</label>
        <input type="text" id="env_mumble_username" value="Doorpi">
      </div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">MUMBLE PASSWORD</label>
        <div class="relative">
          <input type="password" id="env_mumble_password" placeholder="password server Mumble">
          <button type="button" onclick="togglePwd('env_mumble_password')"
            class="absolute right-2 top-1 text-xs" style="color:var(--muted)">👁</button>
        </div>
      </div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">CAMERA USERNAME</label>
        <input type="text" id="env_camera_username" placeholder="utente camera IP">
      </div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">CAMERA PASSWORD</label>
        <div class="relative">
          <input type="password" id="env_camera_password" placeholder="password camera IP">
          <button type="button" onclick="togglePwd('env_camera_password')"
            class="absolute right-2 top-1 text-xs" style="color:var(--muted)">👁</button>
        </div>
      </div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">PUSHOVER API TOKEN</label>
        <input type="text" id="env_pushover_token" placeholder="token app Pushover">
      </div>
      <div>
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">PUSHOVER USER KEY</label>
        <input type="text" id="env_pushover_key" placeholder="user key Pushover">
      </div>
      <div class="col-span-2">
        <label class="block text-xs font-semibold mb-1" style="color:var(--muted)">OPENROUTER API KEY</label>
        <div class="relative">
          <input type="password" id="env_openrouter_key" placeholder="sk-or-v1-...">
          <button type="button" onclick="togglePwd('env_openrouter_key')"
            class="absolute right-2 top-1 text-xs" style="color:var(--muted)">👁</button>
        </div>
      </div>
      <div class="col-span-2 flex items-center gap-3 mt-1">
        <button class="btn-primary" style="font-size:.85rem;padding:.4rem 1rem" onclick="saveEnv()">
          💾 Salva .env ora
        </button>
        <span id="envSaveMsg" class="text-xs" style="color:var(--success)"></span>
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
  <div class="flex items-center gap-3">
    <button id="startBtn" class="btn-primary" onclick="startInstall()">
      ▶&nbsp; Avvia Installazione
    </button>
    <button id="abortBtn" class="btn-danger" disabled onclick="abortInstall()">
      ■&nbsp; Interrompi
    </button>
    <span id="statusText" class="text-sm ml-4" style="color:var(--muted)"></span>
  </div>

</main>

<script>
const N_STEPS = {{ n_steps }};
let evtSource = null;

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
    install_log2ram:    document.getElementById('log2ram').checked,
    install_codeserver: document.getElementById('codeserver').checked,
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
          appendLog('\n✓ DoorPhoneServer installato con successo! Esegui: sudo reboot');
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
        hostname    = validate_hostname(data.get("hostname", DEFAULT_HOSTNAME))
        play_cards, cap_cards = detect_audio_cards()
        config = {
            "hostname":           hostname,
            "play_card":          validate_card_index(data.get("play_card", 1), play_cards),
            "play_dev":           0,
            "cap_card":           validate_card_index(data.get("cap_card", 1), cap_cards),
            "cap_dev":            0,
            "_audio_autodetect":  not play_cards,
            "install_log2ram":    bool(data.get("install_log2ram", True)),
            "install_codeserver": bool(data.get("install_codeserver", False)),
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

    runner = Runner(dry_run=False)
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


# ── Entry point ───────────────────────────────────────────────────────────────

def run_webui(port: int = 8888, dry_run: bool = False):
    """Avvia il server Web UI. Chiamato da wizard.py --web."""
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
    print(f"  Apri nel browser:")
    print(f"  ➜  http://{local_ip}:{port}")
    print(f"  ➜  http://localhost:{port}")
    print(f"  Log: {LOG_FILE}")
    print("=" * 60)
    print()

    app.run(host="0.0.0.0", port=port, threaded=True, debug=False)


if __name__ == "__main__":
    run_webui()

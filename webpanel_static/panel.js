
let _audioCardPresent = true;

// --- Ace Editor (XML config) ---
let _aceEditor = null;
function getAceEditor(){
  if(_aceEditor) return _aceEditor;
  if(typeof ace === 'undefined') return null;
  _aceEditor = ace.edit('configEditor');
  _aceEditor.setTheme('ace/theme/tomorrow_night');
  _aceEditor.session.setMode('ace/mode/xml');
  _aceEditor.setOptions({
    fontSize: '13px',
    showPrintMargin: false,
    wrap: false,
    showFoldWidgets: true,
    useSoftTabs: true,
    tabSize: 2,
  });
  _aceEditor.session.setUseWorker(false);
  // Ctrl+Shift+F = fold all
  _aceEditor.commands.addCommand({
    name: 'foldAll',
    bindKey: {win:'Ctrl-Shift-F', mac:'Command-Shift-F'},
    exec: function(ed){ ed.session.foldAll(); }
  });
  return _aceEditor;
}

// --- Tabs ---
document.querySelectorAll('.tab').forEach(t=>{
  t.addEventListener('click',()=>{
    document.querySelectorAll('.tab').forEach(x=>x.classList.remove('active'));
    document.querySelectorAll('.page').forEach(x=>x.classList.remove('active'));
    t.classList.add('active');
    document.getElementById('page-'+t.dataset.page).classList.add('active');
    if(t.dataset.page==='config')loadConfig();
    if(t.dataset.page==='sounds'){loadSounds();loadVolume();}
    if(t.dataset.page==='video'){loadStreamerStatus();loadRTSPInfo();}
    if(t.dataset.page==='snapshots')loadSnapshots();
    if(t.dataset.page==='logs')loadLog();
    if(t.dataset.page==='apk')loadApkList();
    if(t.dataset.page==='users')loadMumbleUsers();
    if(t.dataset.page==='audiotest')loadAudioTestFiles();
    if(t.dataset.page==='speakingtimeline'){loadSpeakingLog();startLiveMonitor();}else{stopLiveMonitor();}
    if(t.dataset.page==='chimaai')loadORModels();
    if(t.dataset.page==='crontab')loadCron();
    if(t.dataset.page==='log2ram'){loadLog2Ram();}else{stopLog2RamPoll();}
    if(t.dataset.page==='esp32'){startESP32Poll();}else{stopESP32Poll();}
    if(t.dataset.page==='nfcwhitelist'){if(typeof _nfcPageActivated==='function')_nfcPageActivated();}
    else{if(typeof _nfcPageDeactivated==='function')_nfcPageDeactivated();}
  });
});

function toastCenter(msg,ok){
  const t=document.createElement('div');
  t.className='toast-center '+(ok?'ok':'err');
  t.textContent=msg;
  document.body.appendChild(t);
  setTimeout(()=>t.remove(),1900);
}

function fmtBytes(b){if(!b)return'0 B';const u=['B','KB','MB','GB'];let i=0;while(b>=1024&&i<3){b/=1024;i++}return b.toFixed(i?1:0)+' '+u[i]}

// --- Dashboard ---
const hist={cpu:[],mem:[],temp:[],disk:[],go:[],throttle:[]};const HIST_MAX=60;
const chartShow={cpu:true,mem:true,temp:true,disk:true,go:true,throttle:true};
function toggleSeries(key){
  chartShow[key]=!chartShow[key];
  const btn=document.getElementById('tog'+key.charAt(0).toUpperCase()+key.slice(1));
  btn.classList.toggle('on',chartShow[key]);
  drawChart();
}
function updateDashboard(){
  fetch('/panel/api/service').then(r=>r.json()).then(d=>{
    const svcDot=document.getElementById('svcDot');
    const svcSt=document.getElementById('svcStatus');
    const svcActive=d.status==='active';
    svcDot.style.background=svcActive?'var(--green)':'#e74c3c';
    svcSt.textContent=svcActive?'Running':'Stopped ('+d.status+')';
    svcSt.style.color=svcActive?'var(--green)':'#e74c3c';
    document.getElementById('svcUptime').textContent='Uptime: '+d.uptime;
    const svcBtn=document.getElementById('svcToggleBtn');
    if(svcBtn){
      svcBtn.innerHTML=svcActive?'<i class="bi bi-stop-fill"></i> Ferma':'<i class="bi bi-play-fill"></i> Avvia';
      svcBtn.className='btn-o btn-sm '+(svcActive?'btn-o-red':'btn-o-green');
      svcBtn.dataset.state=svcActive?'on':'off';
    }
    const connStr = d.connected ? ' — Connected' : ' — Disconnected';
    const buildStr = (d.build_time && d.build_time !== 'unknown') ? '   build ' + d.build_time : '';
    document.getElementById('panelVer').textContent = 'v' + d.version + connStr + buildStr;
    const hbBox=document.getElementById('hbBox');
    if(d.heartbeat_enabled){
      hbBox.style.display='inline-flex';
      const period=d.heartbeat_period_ms||1000;
      document.getElementById('hbIcon').style.animationDuration=(period/1000)+'s';
      let hbLabel='#'+d.heartbeat_count;
      if(d.heartbeat_last_time) hbLabel+=' @ '+d.heartbeat_last_time;
      document.getElementById('hbText').textContent=hbLabel;
    } else { hbBox.style.display='none'; }  }).catch(()=>{});

  fetch('/panel/api/stats').then(r=>r.json()).then(d=>{
    const temp=d.temperature||0;
    const memTotal=d.mem_total||1;
    const memFree=d.mem_free||0;
    const memUsed=memTotal-memFree;
    const memPct=((memUsed/memTotal)*100).toFixed(0);
    const diskTotal=d.disk?d.disk.total:1;
    const diskUsed=d.disk?d.disk.used:0;
    const diskPct=((diskUsed/diskTotal)*100).toFixed(0);

    document.getElementById('cpuTemp').textContent=temp.toFixed(1)+'°C';
    document.getElementById('cpuLoad').textContent=d.load||'N/A';
    document.getElementById('memUsed').textContent=fmtBytes(memUsed)+' / '+fmtBytes(memTotal)+' ('+memPct+'%)';
    document.getElementById('diskUsed').textContent=fmtBytes(diskUsed)+' / '+fmtBytes(diskTotal)+' ('+diskPct+'%)';
    document.getElementById('goRoutines').textContent=d.goroutines;
    document.getElementById('goMem').textContent=fmtBytes(d.mem_alloc);

    // CPU Frequency
    const cpuFreq=d.cpu_freq||0;
    document.getElementById('cpuFreq').textContent=cpuFreq+' MHz';
    const freqPct=Math.min(cpuFreq/1500*100,100);
    document.getElementById('gaugeCpuFreq').style.width=freqPct+'%';
    document.getElementById('gaugeCpuFreq').style.background=cpuFreq>=1400?'var(--green)':cpuFreq>=900?'var(--accent)':'var(--dim)';

    // Core Voltage
    document.getElementById('coreVolt').textContent=d.core_volt||'N/A';

    // Throttle status
    const thr=d.throttled||0;
    const thrEl=document.getElementById('throttled');
    if(thr===0){
      thrEl.innerHTML='<span style="color:var(--green)">OK</span>';
    } else {
      var flags=[];
      if(thr&0x1)flags.push('Under-voltage');
      if(thr&0x2)flags.push('Freq capped');
      if(thr&0x4)flags.push('Throttled');
      if(thr&0x8)flags.push('Soft temp limit');
      thrEl.innerHTML='<span style="color:var(--red)">'+flags.join(', ')+'</span>';
    }

    // gauge bars
    const tempPct=Math.min(temp/85*100,100);
    document.getElementById('gaugeCpuTemp').style.width=tempPct+'%';
    document.getElementById('gaugeCpuTemp').style.background=temp>70?'var(--red)':temp>55?'var(--orange)':'var(--accent)';
    document.getElementById('gaugeMem').style.width=memPct+'%';
    document.getElementById('gaugeMem').style.background=parseFloat(memPct)>85?'var(--red)':parseFloat(memPct)>70?'var(--orange)':'var(--green)';
    document.getElementById('gaugeDisk').style.width=diskPct+'%';
    document.getElementById('gaugeDisk').style.background=parseFloat(diskPct)>90?'var(--red)':parseFloat(diskPct)>75?'var(--orange)':'var(--orange)';

    const goMemMB=(d.mem_alloc||0)/(1024*1024);
    const goMemPct=Math.min(goMemMB/50*100,100);
    // Throttle: conta i bit attivi (max 4) -> percentuale 0-100
    const thrBits=[0x1,0x2,0x4,0x8].filter(b=>thr&b).length;
    const thrPct=thrBits*25;
    hist.cpu.push(d.cpu_percent||0);hist.mem.push(parseFloat(memPct));hist.temp.push(temp);hist.disk.push(parseFloat(diskPct));hist.go.push(goMemPct);hist.throttle.push(thrPct);
    if(hist.cpu.length>HIST_MAX){hist.cpu.shift();hist.mem.shift();hist.temp.shift();hist.disk.shift();hist.go.shift();hist.throttle.shift();}
    drawChart();
    // Controlla allarme RAM
    const ramAlarm=_alarmCfg&&_alarmCfg['ram_pct'];
    if(ramAlarm&&ramAlarm.enabled&&ramAlarm.threshold){
      if(parseFloat(memPct)>=ramAlarm.threshold){
        toastCenter('⚠️ RAM: utilizzo al '+memPct+'% (soglia '+ramAlarm.threshold+'%)',false);
      }
    }
  }).catch(()=>{});
}

function drawChart(){
  const c=document.getElementById('chartCanvas');const ctx=c.getContext('2d');
  const W=c.width=c.offsetWidth;const H=c.height=c.offsetHeight||280;
  ctx.clearRect(0,0,W,H);
  const pad={t:25,b:30,l:45,r:15};
  const gW=W-pad.l-pad.r;const gH=H-pad.t-pad.b;

  // grid
  ctx.strokeStyle='#334155';ctx.lineWidth=0.5;
  for(let i=0;i<=4;i++){
    const y=pad.t+gH*(i/4);
    ctx.beginPath();ctx.moveTo(pad.l,y);ctx.lineTo(W-pad.r,y);ctx.stroke();
    ctx.fillStyle='#64748b';ctx.font='10px sans-serif';ctx.textAlign='right';
    ctx.fillText((100-i*25)+'%',pad.l-6,y+3);
  }

  function drawLine(arr,color){
    if(arr.length<2)return;
    // filled area
    ctx.beginPath();ctx.fillStyle=color+'20';
    ctx.moveTo(pad.l,pad.t+gH);
    for(let i=0;i<arr.length;i++){
      const x=pad.l+(i/(HIST_MAX-1))*gW;
      const y=pad.t+gH*(1-Math.min(arr[i],100)/100);
      ctx.lineTo(x,y);
    }
    ctx.lineTo(pad.l+((arr.length-1)/(HIST_MAX-1))*gW,pad.t+gH);
    ctx.closePath();ctx.fill();
    // line
    ctx.beginPath();ctx.strokeStyle=color;ctx.lineWidth=2.5;
    for(let i=0;i<arr.length;i++){
      const x=pad.l+(i/(HIST_MAX-1))*gW;
      const y=pad.t+gH*(1-Math.min(arr[i],100)/100);
      i===0?ctx.moveTo(x,y):ctx.lineTo(x,y);
    }ctx.stroke();
    // current value dot
    if(arr.length>0){
      const lastX=pad.l+((arr.length-1)/(HIST_MAX-1))*gW;
      const lastY=pad.t+gH*(1-Math.min(arr[arr.length-1],100)/100);
      ctx.beginPath();ctx.arc(lastX,lastY,4,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();
    }
  }
  if(chartShow.cpu)drawLine(hist.cpu,'#38bdf8');
  if(chartShow.mem)drawLine(hist.mem,'#22c55e');
  if(chartShow.temp)drawLine(hist.temp,'#f97316');
  if(chartShow.disk)drawLine(hist.disk,'#a78bfa');
  if(chartShow.go)drawLine(hist.go,'#fb7185');
  if(chartShow.throttle)drawLine(hist.throttle,'#facc15');

  // legend with current values
  const series=[
    ['cpu','CPU','#38bdf8',v=>v.toFixed(1)+'%'],
    ['mem','RAM','#22c55e',v=>v.toFixed(0)+'%'],
    ['temp','Temp','#f97316',v=>v.toFixed(1)+'°C'],
    ['disk','Disk','#a78bfa',v=>v.toFixed(0)+'%'],
    ['go','Go','#fb7185',v=>(v*50/100).toFixed(1)+' MB'],
    ['throttle','Throttle','#facc15',v=>v===0?'OK':v+'%']
  ];
  let lx=pad.l;
  series.forEach(([key,name,color,fmt])=>{
    if(!chartShow[key])return;
    const arr=hist[key];const cur=arr.length?fmt(arr[arr.length-1]):'--';
    ctx.fillStyle=color;ctx.fillRect(lx,H-14,12,10);
    ctx.fillStyle='#e2e8f0';ctx.font='12px sans-serif';ctx.textAlign='left';
    const label=name+': '+cur;
    ctx.fillText(label,lx+16,H-4);lx+=Math.max(ctx.measureText(label).width+36,80);
  });
}

function serviceAction(action){
  const l={'start':'Avvia','stop':'Ferma','restart':'Riavvia'};
  showSudoModal((l[action]||action)+' DoorPhoneServer','sudo systemctl '+action+' doorphoneserver','/panel/api/service',action,updateDashboard);
}
function svcToggle(){
  const btn=document.getElementById('svcToggleBtn');
  const action=(btn&&btn.dataset.state==='on')?'stop':'start';
  const label=action==='stop'?'Ferma':'Avvia';
  confirmModal(label+' DoorPhoneServer','Confermi di <b>'+label.toLowerCase()+'</b> il servizio DoorPhoneServer?','warn',action==='stop'?'danger':'primary',label).then(ok=>{
    if(ok) serviceAction(action);
  });
}

// --- Disk Cleanup ---
function diskCleanup(){
  const outEl=document.getElementById('sudoOutput');
  document.getElementById('sudoModalTitle').textContent='Pulizia Disco';
  outEl.style.color='#e2e8f0';
  outEl.textContent='$ disk-cleanup\n\nPulizia in corso, attendere...';
  document.getElementById('sudoModal').classList.add('show');
  fetch('/panel/api/cleanup',{method:'POST'})
  .then(r=>{if(!r.ok)throw new Error('HTTP '+r.status);return r.json();})
  .then(d=>{
    const out=d.output?d.output:'Liberati: '+d.freed_mb+' MB';
    const status=d.ok?'Completato con successo':'Errore';
    outEl.textContent='$ disk-cleanup\n\n'+out+'\n\n'+status;
    outEl.style.color=d.ok?'#e2e8f0':'#ef4444';
    if(d.ok){setTimeout(updateDashboard,1000);}
  })
  .catch(e=>{
    outEl.textContent='$ disk-cleanup\n\nErrore: '+e;
    outEl.style.color='#ef4444';
  });
}

// --- Config ---
let _configLoaded = false;
function _setSaveConfigEnabled(enabled){
  const btn=document.getElementById('btnSaveConfig');
  if(btn) btn.disabled=!enabled;
}
function loadConfig(){
  _configLoaded=false;
  _setSaveConfigEnabled(false);
  document.getElementById('configMsg').textContent='Loading…';
  fetch('/panel/api/config').then(r=>r.text()).then(t=>{
    const ed=getAceEditor();
    if(ed){ ed.setValue(t,-1); }
    _configLoaded=true;
    _setSaveConfigEnabled(true);
    document.getElementById('configMsg').textContent='Loaded';
  }).catch(e=>{
    document.getElementById('configMsg').textContent='Load Error';
    toastCenter('Error loading config: '+e,false);
  });
}

function saveConfig(){
  if(!_configLoaded){
    toastCenter('Attendi il caricamento della configurazione',false);
    return;
  }
  const ed=getAceEditor();
  const body=ed ? ed.getValue() : document.getElementById('configEditor').innerText;
  // client-side XML validation
  const parser=new DOMParser();
  const doc=parser.parseFromString(body,'text/xml');
  const parseErr=doc.querySelector('parsererror');
  if(parseErr){
    const msg=parseErr.textContent.split('\n')[0];
    toastCenter('XML non valido: '+msg,false);
    document.getElementById('configMsg').textContent='XML Error';
    return;
  }
  fetch('/panel/api/config',{method:'POST',headers:{'Content-Type':'text/xml'},body:body})
  .then(r=>r.json().then(d=>({ok:r.ok,data:d})))
  .then(({ok,data})=>{
    if(!ok||!data.ok){toastCenter('Errore: '+(data.error||'Save failed'),false);document.getElementById('configMsg').textContent='Save Error';return;}
    toastCenter('Config saved! Backup: '+data.backup,true);document.getElementById('configMsg').textContent='Saved at '+new Date().toLocaleTimeString();
  })
  .catch(e=>toastCenter('Error: '+e,false));
}

// --- Sounds ---
function loadSounds(){
  fetch('/panel/api/sounds').then(r=>r.json()).then(files=>{
    const ul=document.getElementById('soundList');
    if(!files||files.length===0){ul.innerHTML='<li style="color:var(--dim)">No sound files found</li>';return;}
    ul.innerHTML=files.map(f=>'<li><div class="file-info"><span class="file-icon">'+(f.name.endsWith('.mp3')?'\uD83C\uDFB5':'\uD83D\uDD0A')+'</span><span class="file-name">'+f.name+'</span><span class="file-size">'+fmtBytes(f.size)+'</span></div><div style="display:flex;gap:4px;flex-shrink:0"><button class="btn-icon btn-o btn-o-blue" title="Play in Browser" onclick="playSound(\''+f.name+'\')"><i class="bi bi-play-fill"></i></button><button class="btn-icon btn-o btn-o-purple js-pi-audio" title="Play on Citofono" onclick="playSoundPi(\''+f.name+'\')"><i class="bi bi-bell-fill"></i></button><button class="btn-icon btn-o btn-o-red" title="Delete" onclick="deleteSound(\''+f.name+'\')"><i class="bi bi-trash3-fill"></i></button></div></li>').join('');
    applyAudioCardState();
  }).catch(e=>toastCenter('Error: '+e,false));
}

function playSound(name){
  const audio=document.getElementById('audioPlayer');
  const box=document.getElementById('audioPlayerBox');
  audio.src='/panel/api/sounds/play/'+encodeURIComponent(name);
  audio.play();
  box.style.display='block';
  document.getElementById('nowPlaying').textContent='\uD83C\uDFB5 '+name;
}
function stopAudio(){
  const audio=document.getElementById('audioPlayer');
  audio.pause();audio.src='';
  document.getElementById('audioPlayerBox').style.display='none';
}

function playSoundPi(name){
  if(!_audioCardPresent){toastCenter('Nessuna scheda audio rilevata',false);return;}
  const cmd='/usr/bin/ffplay '+name+' -autoexit -nodisp -volume 100';
  const outEl=document.getElementById('sudoOutput');
  document.getElementById('sudoModalTitle').textContent='\u25B6 Play sul Citofono: '+name;
  outEl.style.color='#e2e8f0';
  outEl.textContent='$ '+cmd+'\n\nEsecuzione in corso... (attendi fine riproduzione)';
  document.getElementById('sudoModal').classList.add('show');
  fetch('/panel/api/sounds/playpi/'+encodeURIComponent(name),{method:'POST'})
  .then(r=>{if(!r.ok)throw new Error('HTTP '+r.status);return r.json();})
  .then(d=>{
    let txt='$ '+cmd+'\n\n';
    if(d.output&&d.output.trim()) txt+=d.output.trim()+'\n\n';
    if(d.ok){
      txt+='\u2705 Riproduzione completata con successo';
      outEl.style.color='#22c55e';
    } else {
      txt+='\u274C Errore: '+(d.error||'sconosciuto');
      outEl.style.color='#ef4444';
    }
    outEl.textContent=txt;
  })
  .catch(e=>{
    outEl.textContent='$ '+cmd+'\n\n\u274C Errore di rete: '+e;
    outEl.style.color='#ef4444';
  });
}
function applyAudioCardState(){
  document.querySelectorAll('.js-pi-audio').forEach(b=>{
    b.disabled=!_audioCardPresent;
    b.title=_audioCardPresent?'Play on Citofono':'Nessuna scheda audio rilevata';
  });
}
function stopPiAudio(){
  if(!_audioCardPresent){toastCenter('Nessuna scheda audio rilevata',false);return;}
  fetch('/panel/api/sounds/stoppi',{method:'POST'})
  .then(()=>toastCenter('Stop audio citofono',true))
  .catch(e=>toastCenter('Errore: '+e,false));
}

// --- Volume Control ---
function loadVolume(){
  fetch('/panel/api/volume').then(r=>r.json()).then(d=>{
    _audioCardPresent = !d.noCard;
    applyAudioCardState();
    if(d.noCard){
      ['volOut','volIn'].forEach(id=>{const el=document.getElementById(id);if(el)el.disabled=true;});
      toastCenter('Nessuna scheda audio rilevata',false);
      return;
    }
    document.getElementById('volOut').value=d.headphone;
    document.getElementById('volOutVal').textContent=d.headphone+'%';
    document.getElementById('volIn').value=d.mic;
    document.getElementById('volInVal').textContent=d.mic+'%';
    updateMuteBadge('muteOutBadge',d.headphoneMute);
    updateMuteBadge('muteInBadge',d.micMute);
  }).catch(()=>{});
}
function updateMuteBadge(id,isMuted){
  const el=document.getElementById(id);
  if(!el)return;
  if(isMuted){el.textContent='MUTED';el.className='mute-badge mute-on';}
  else{el.textContent='UNMUTED';el.className='mute-badge mute-off';}
}
function toggleMute(control){
  if(!_audioCardPresent){toastCenter('Nessuna scheda audio rilevata',false);return;}
  fetch('/panel/api/mute',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'control='+encodeURIComponent(control)})
  .then(r=>r.json()).then(d=>{
    const badgeId=control==='Headphone'?'muteOutBadge':'muteInBadge';
    updateMuteBadge(badgeId,d.muted);
    toastCenter(control+(d.muted?' MUTED':' UNMUTED'),!d.muted);
  }).catch(e=>toastCenter('Errore mute: '+e,false));
}
function setVolume(control,val){
  if(!_audioCardPresent){toastCenter('Nessuna scheda audio rilevata',false);return;}
  const cmd='amixer sset '+control+' '+val+'%';
  document.getElementById('sudoModalTitle').textContent='Volume '+control;
  const outEl=document.getElementById('sudoOutput');
  outEl.style.color='#e2e8f0';
  outEl.textContent='$ '+cmd+'\n\nImpostazione in corso...';
  document.getElementById('sudoModal').classList.add('show');
  fetch('/panel/api/volume',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'control='+encodeURIComponent(control)+'&volume='+val})
  .then(r=>r.json())
  .then(d=>{
    const status=d.ok?'Completato con successo':'Errore';
    outEl.style.color=d.ok?'#e2e8f0':'#ef4444';
    outEl.textContent='$ '+cmd+'\n\n'+(d.output||'')+'\n\n'+status;
  })
  .catch(e=>{
    outEl.style.color='#ef4444';
    outEl.textContent='$ '+cmd+'\n\nErrore: '+e;
  });
}

// --- Video Stream ---
function rtspSnapshot(){
  const btn=event.target;btn.disabled=true;btn.textContent='Taking...';
  fetch('/panel/api/snapshots/take',{method:'POST'})
  .then(r=>{if(!r.ok)throw new Error('Failed');return r.json();})
  .then(()=>toastCenter('Snapshot taken',true))
  .catch(e=>toastCenter('Error: '+e,false))
  .finally(()=>{btn.disabled=false;btn.textContent='Snapshot';});
}

// --- RTSP Stream ---
function loadRTSPInfo(){
  fetch('/config').then(r=>r.json()).then(d=>{
    const vs=d.camera||{};
    const vid=vs.video||{};
    const el=document.getElementById('rtspInfo');
    if(vid.enabled){
      el.innerHTML='<span style="color:var(--green)">&#9679;</span> <strong>'+vid.endpoint+'</strong>';
    } else {
      el.innerHTML='<span style="color:var(--dim)">&#9679; Disabilitato o non configurato</span>';
    }
  }).catch(()=>{
    const el=document.getElementById('rtspInfo');
    if(el)el.textContent='Configurazione non disponibile';
  });
}
let _rtspPC=null;

async function connectRTSP(){
  if(_rtspPC){stopRTSP();}
  const ph=document.getElementById('rtspPlaceholder');
  const video=document.getElementById('rtspVideo');
  ph.textContent='Connessione WebRTC in corso...';
  ph.style.display='';
  video.style.display='none';
  try {
    // Rete locale: no STUN necessario
    const pc=new RTCPeerConnection({iceServers:[]});
    _rtspPC=pc;

    pc.onicecandidate=function(ev){console.log('[webrtc] ICE candidate:',ev.candidate);};
    pc.oniceconnectionstatechange=function(){console.log('[webrtc] ICE state:',pc.iceConnectionState);};
    pc.onconnectionstatechange=function(){
      console.log('[webrtc] connection state:',pc.connectionState);
      if(pc.connectionState==='failed'||pc.connectionState==='disconnected'){
        ph.textContent='Stream interrotto ('+pc.connectionState+')';ph.style.display='';video.style.display='none';
        updateSpotlightEnabled(false);_updateRtspBtn(false);
      }
      if(pc.connectionState==='connected'){
        ph.style.display='none';
        updateSpotlightEnabled(true);_updateRtspBtn(true);
      }
    };
    pc.ontrack=function(ev){
      console.log('[webrtc] ontrack:',ev.track.kind,'streams:',ev.streams.length);
      if(ev.streams&&ev.streams[0]){
        video.srcObject=ev.streams[0];
      } else {
        if(!video.srcObject)video.srcObject=new MediaStream();
        video.srcObject.addTrack(ev.track);
      }
      video.style.display='';
      ph.style.display='none';
      video.onloadedmetadata=function(){console.log('[webrtc] video metadata loaded, size:',video.videoWidth+'x'+video.videoHeight);};
      video.onplaying=function(){console.log('[webrtc] video playing');};
      video.onstalled=function(){console.log('[webrtc] video STALLED');};
      video.onerror=function(e){console.error('[webrtc] video error:',e);};
      video.play().then(function(){console.log('[webrtc] play() ok');}).catch(function(e){console.error('[webrtc] play() failed:',e);});
    };

    pc.addTransceiver('video',{direction:'recvonly'});
    const offer=await pc.createOffer();
    await pc.setLocalDescription(offer);
    console.log('[webrtc] offer created, gathering ICE...');

    await new Promise(resolve=>{
      if(pc.iceGatheringState==='complete'){resolve();return;}
      pc.addEventListener('icegatheringstatechange',()=>{if(pc.iceGatheringState==='complete')resolve();});
      setTimeout(resolve,5000);
    });
    console.log('[webrtc] ICE gathered, sending offer to server...');

    const resp=await fetch('/panel/api/webrtc/offer',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({sdp:pc.localDescription.sdp,type:pc.localDescription.type})
    });
    if(!resp.ok)throw new Error('server HTTP '+resp.status);
    const answer=await resp.json();
    console.log('[webrtc] answer received:',answer);
    if(answer.error)throw new Error(answer.error);
    await pc.setRemoteDescription(new RTCSessionDescription(answer));
    console.log('[webrtc] remote description set — waiting for connection...');
  } catch(e){
    console.error('[webrtc] error:',e);
    ph.textContent='Errore WebRTC: '+e.message;ph.style.display='';video.style.display='none';
    _rtspPC=null;_updateRtspBtn(false);
  }
}
function stopRTSP(){
  if(_rtspPC){_rtspPC.close();_rtspPC=null;}
  const video=document.getElementById('rtspVideo');
  if(video){video.srcObject=null;video.style.display='none';}
  const ph=document.getElementById('rtspPlaceholder');
  ph.style.display='';ph.textContent='Clicca Connetti per avviare lo stream RTSP';
  updateSpotlightEnabled(false);
  _updateRtspBtn(false);
}
function rtspToggle(){
  if(_rtspPC) stopRTSP(); else connectRTSP();
}
function _updateRtspBtn(connected){
  const btn=document.getElementById('rtspToggleBtn');
  if(!btn)return;
  if(connected){
    btn.textContent='Stop';
    btn.className='btn-o btn-o-orange btn-sm';
  } else {
    btn.textContent='Connetti';
    btn.className='btn-o btn-o-blue btn-sm';
  }
}
let _spotlightOn=false;
let _spotlightEnabled=false;
let _osdEnabled=true;
let _osdText='Camera';
let _osdPos='br';

function updateOSDButton(){
  const btn=document.getElementById('osdTextBtn');
  if(!btn)return;
  btn.style.background=_osdEnabled?'#2563eb':'#64748b';
  btn.title=_osdEnabled?'OSD attivo — clicca per configurare':'OSD disattivo — clicca per configurare';
}

function openOSDModal(){
  document.getElementById('osdEnabledCheck').checked=_osdEnabled;
  document.getElementById('osdTextInput').value=_osdText;
  document.querySelectorAll('input[name="osdPos"]').forEach(r=>{
    r.checked=(r.value===_osdPos);
    r.closest('.osd-pos-opt').classList.toggle('selected',r.value===_osdPos);
  });
  document.querySelectorAll('input[name="osdPos"]').forEach(r=>{
    r.onchange=function(){
      document.querySelectorAll('.osd-pos-opt').forEach(el=>el.classList.remove('selected'));
      r.closest('.osd-pos-opt').classList.add('selected');
    };
  });
  document.getElementById('osdModal').style.display='flex';
  setTimeout(()=>document.getElementById('osdTextInput').focus(),50);
}

function closeOSDModal(){
  document.getElementById('osdModal').style.display='none';
}

function applyOSDSettings(){
  const enabled=document.getElementById('osdEnabledCheck').checked;
  const text=document.getElementById('osdTextInput').value.trim()||'Camera';
  const posEl=document.querySelector('input[name="osdPos"]:checked');
  const pos=posEl?posEl.value:'br';
  const applyBtn=document.getElementById('osdApplyBtn');
  if(applyBtn){applyBtn.disabled=true;applyBtn.textContent='Invio...';}
  fetch('/panel/api/cameraosd',{
    method:'POST',
    headers:{'Content-Type':'application/x-www-form-urlencoded'},
    body:'enabled='+(enabled?'1':'0')+'&text='+encodeURIComponent(text)+'&pos='+encodeURIComponent(pos)
  })
  .then(r=>r.json())
  .then(d=>{
    if(!d.ok)throw new Error(d.error||'OSD update failed');
    _osdEnabled=enabled;
    _osdText=d.readback_text||text;
    _osdPos=pos;
    updateOSDButton();
    closeOSDModal();
    toastCenter('OSD aggiornato: '+(enabled?'ON':'OFF')+' "'+_osdText+'"',true);
  })
  .catch(e=>toastCenter('OSD error: '+e,false))
  .finally(()=>{if(applyBtn){applyBtn.disabled=false;applyBtn.textContent='Applica';}});
}

function updateSpotlightEnabled(enabled){
  _spotlightEnabled=enabled;
  const btn=document.getElementById('spotlightBtn');
  if(!enabled){
    _spotlightOn=false;
    btn.innerHTML='<i class="bi bi-lightbulb-off"></i>';
    btn.style.color='#334155';
    btn.style.filter='none';
    btn.style.opacity='0.3';
    btn.style.cursor='not-allowed';
    btn.title='Connetti la camera per usare il faro';
  } else {
    btn.style.opacity='1';
    btn.style.cursor='pointer';
    updateSpotlightIcon();
  }
}
function updateSpotlightIcon(){
  const btn=document.getElementById('spotlightBtn');
  if(_spotlightOn){
    btn.innerHTML='<i class="bi bi-lightbulb-fill"></i>';
    btn.style.filter='drop-shadow(0 0 6px #f59e0b)';
    btn.style.color='#f59e0b';
    btn.title='Faro acceso — clicca per spegnere';
  } else {
    btn.innerHTML='<i class="bi bi-lightbulb-off"></i>';
    btn.style.filter='none';
    btn.style.color='#94a3b8';
    btn.title='Faro spento — clicca per accendere';
  }
}
function toggleSpotlight(){
  if(!_spotlightEnabled)return;
  const action=_spotlightOn?'off':'on';
  const btn=document.getElementById('spotlightBtn');
  btn.style.opacity='0.5';
  fetch('/panel/api/spotlight',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'action='+action})
  .then(r=>r.json()).then(d=>{
    btn.style.opacity='1';
    if(d.ok){
      _spotlightOn=action==='on';
      updateSpotlightIcon();
    }
  }).catch(()=>{btn.style.opacity='1';});
}

function syncCameraTime(){
  const btn=document.getElementById('syncTimeBtn');
  btn.style.opacity='0.5';
  btn.style.pointerEvents='none';
  document.getElementById('sudoModalTitle').textContent='Sincronizza Orologio Camera';
  const outEl=document.getElementById('sudoOutput');
  outEl.style.color='#e2e8f0';
  outEl.textContent='Invio comando Baichuan SetGeneral...\n\nEsecuzione in corso...';
  document.getElementById('sudoModal').classList.add('show');
  fetch('/panel/api/cameratime',{method:'POST'})
  .then(r=>r.json()).then(d=>{
    btn.style.opacity='1';
    btn.style.pointerEvents='auto';
    if(d.ok){
      outEl.style.color='#22c55e';
      outEl.textContent='Orologio camera sincronizzato con successo!\n\nOra impostata: '+d.time;
    } else {
      outEl.style.color='#ef4444';
      outEl.textContent='Errore sincronizzazione orologio:\n\n'+d.error;
    }
  }).catch(e=>{
    btn.style.opacity='1';
    btn.style.pointerEvents='auto';
    outEl.style.color='#ef4444';
    outEl.textContent='Errore di connessione:\n\n'+e;
  });
}

updateOSDButton();

function probeRTSP(){
  const el=document.getElementById('rtspProbeResult');
  el.style.color='var(--dim)';
  el.textContent='Test in corso...';
  fetch('/panel/api/stream/probe').then(r=>r.json()).then(d=>{
    if(d.ok){
      el.style.color='var(--green)';
      el.textContent='OK — '+d.endpoint;
    } else {
      el.style.color='#ef4444';
      el.textContent='Errore: '+d.error;
    }
  }).catch(e=>{
    el.style.color='#ef4444';
    el.textContent='Errore: '+e;
  });
}

// --- Streamer Service ---
function loadStreamerStatus(){
  fetch('/panel/api/streamer').then(r=>r.json()).then(d=>{
    const dot=document.getElementById('streamerDot');
    const st=document.getElementById('streamerStatus');
    const en=document.getElementById('streamerEnabled');
    const up=document.getElementById('streamerUptime');
    if(!dot || !st || !en || !up) return; // Elements not found
    const active=d.status==='active';
    dot.style.background=active?'var(--green)':'#e74c3c';
    st.textContent=active?'Running':'Stopped ('+d.status+')';
    st.style.color=active?'var(--green)':'#e74c3c';
    en.textContent='('+d.enabled+')';
    up.textContent=d.uptime?'Uptime: '+d.uptime:'';
  }).catch(()=>{
    const el=document.getElementById('streamerStatus');
    if(el) el.textContent='Error';
    const dot=document.getElementById('streamerDot');
    if(dot) dot.style.background='#e74c3c';
  });
}
let _sudoRefresh=null;
function showSudoModal(title,cmd,api,action,refreshFn){
  _sudoRefresh=refreshFn;
  document.getElementById('sudoModalTitle').textContent=title;
  const outEl=document.getElementById('sudoOutput');
  outEl.style.color='#e2e8f0';
  outEl.textContent='$ '+cmd+'\n\nEsecuzione in corso...';
  document.getElementById('sudoModal').classList.add('show');
  fetch(api,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'action='+action})
  .then(r=>r.json())
  .then(d=>{
    const out=(d.output&&d.output.trim())?d.output:'(nessun output)';
    const status=d.ok?'Completato con successo':'Errore';
    outEl.textContent='$ '+cmd+'\n\n'+out+'\n\n'+status;
    if(d.ok&&_sudoRefresh){setTimeout(_sudoRefresh,2000);}
  })
  .catch(()=>{
    var _sep='\n\n';
    if(action==='restart'||action==='stop'){
      outEl.textContent='$ '+cmd+_sep+'Servizio riavviato (normale).'+_sep+'Ricarico stato...';
      if(_sudoRefresh){setTimeout(_sudoRefresh,4000);}
    }else{
      outEl.textContent='$ '+cmd+_sep+'Errore: nessuna risposta.';
    }
  });
}
function closeSudoModal(){document.getElementById('sudoModal').classList.remove('show');}
function streamerAction(action){
  const l={'start':'Avvia','stop':'Ferma','restart':'Riavvia'};
  showSudoModal((l[action]||action)+' MJPEG Streamer','sudo systemctl '+action+' mjpeg_streamer','/panel/api/streamer',action,loadStreamerStatus);
}

// --- Mumble Service ---
function loadMumbleStatus(){
  fetch('/panel/api/mumble').then(r=>r.json()).then(d=>{
    const dot=document.getElementById('mumbleDot');
    const st=document.getElementById('mumbleStatus');
    const up=document.getElementById('mumbleUptime');
    const active=d.status==='active';
    dot.style.background=active?'var(--green)':'#e74c3c';
    st.textContent=active?'Running':'Stopped ('+d.status+')';
    st.style.color=active?'var(--green)':'#e74c3c';
    up.textContent=d.uptime?'Uptime: '+d.uptime:'';
    const mumbleBtn=document.getElementById('mumbleToggleBtn');
    if(mumbleBtn){
      mumbleBtn.innerHTML=active?'<i class="bi bi-stop-fill"></i> Ferma':'<i class="bi bi-play-fill"></i> Avvia';
      mumbleBtn.className='btn-o btn-sm '+(active?'btn-o-red':'btn-o-green');
      mumbleBtn.dataset.state=active?'on':'off';
    }
  }).catch(()=>{
    document.getElementById('mumbleStatus').textContent='Error';
    document.getElementById('mumbleDot').style.background='#e74c3c';
  });
}
function mumbleAction(action){
  const l={'start':'Avvia','stop':'Ferma','restart':'Riavvia'};
  showSudoModal((l[action]||action)+' Mumble Server','sudo systemctl '+action+' mumble-server','/panel/api/mumble',action,loadMumbleStatus);
}
function mumbleToggle(){
  const btn=document.getElementById('mumbleToggleBtn');
  const action=(btn&&btn.dataset.state==='on')?'stop':'start';
  const label=action==='stop'?'Ferma':'Avvia';
  confirmModal(label+' Mumble Server','Confermi di <b>'+label.toLowerCase()+'</b> il servizio Mumble?','warn',action==='stop'?'danger':'primary',label).then(ok=>{
    if(ok) mumbleAction(action);
  });
}


// --- System Controls ---
function loadTabletStatus(){
  fetch('/panel/api/tablet')
  .then(r=>r.json()).then(d=>{
    if(!d.ok||!d.data)throw new Error('no data');
    const on=(d.data['power_tablet']===0);
    document.getElementById('tabletDot').style.background=on?'var(--green)':'var(--red)';
    document.getElementById('tabletStatus').textContent=on?'ON':'OFF';
    document.getElementById('tabletStatus').style.color=on?'var(--green)':'var(--red)';
    const btn=document.getElementById('tabletToggleBtn');
    if(btn){
      btn.textContent=on?'● ON':'○ OFF';
      btn.className='btn-o btn-sm '+(on?'btn-o-green':'btn-o-dim');
      btn.dataset.state=on?'on':'off';
    }
  }).catch(()=>{
    document.getElementById('tabletDot').style.background='var(--dim)';
    document.getElementById('tabletStatus').textContent='--';
    document.getElementById('tabletStatus').style.color='var(--dim)';
    const btn=document.getElementById('tabletToggleBtn');
    if(btn){btn.textContent='--';btn.className='btn-o btn-o-dim btn-sm';btn.dataset.state='';}
  });
}
function tabletToggle(){
  const btn=document.getElementById('tabletToggleBtn');
  if(!btn||btn.dataset.state==='')return;
  const turnOff=btn.dataset.state==='on';
  const action=turnOff?'off':'on';
  const label=turnOff?'Spegni Tablet':'Accendi Tablet';
  confirmModal(label,'Confermi di '+(turnOff?'spegnere':'accendere')+' il tablet?','warn',turnOff?'danger':'primary','Conferma')
  .then(ok=>{
    if(!ok)return;
    fetch('/panel/api/tablet',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'action='+action})
    .then(()=>loadTabletStatus())
    .catch(()=>loadTabletStatus());
  });
}
function rebootServer(){
  confirmModal(
    'Riavvio Raspberry Pi',
    'Confermi il riavvio del sistema?'
    +'<br><br><div style="margin-top:8px;padding:10px;background:rgba(239,68,68,.1);border:1px solid var(--red);border-radius:6px;font-size:13px">'
    +'⚠️ DoorPhoneServer non sarà disponibile per circa <strong>60 secondi</strong>.</div>',
    'warn','danger','Riavvia ora'
  ).then(ok=>{
    if(!ok)return;
    fetch('/?command=reboot_server').then(()=>toastCenter('Comando di riavvio inviato',true)).catch(e=>toastCenter('Errore: '+e,false));
  });
}
function tabletOn(){
  showSudoModal('Accendi Tablet','gpio write power_tablet LOW  (relay ON)','/panel/api/tablet','on',loadTabletStatus);
}
function tabletOff(){
  showSudoModal('Spegni Tablet','gpio write power_tablet HIGH  (relay OFF)','/panel/api/tablet','off',loadTabletStatus);
}

function loadPushoverUsage(){
  fetch('/panel/api/pushover-usage').then(r=>r.json()).then(d=>{
    const el=document.getElementById('pushoverRemaining');
    const gauge=document.getElementById('gaugePushover');
    const detail=document.getElementById('pushoverDetail');
    if(!d.available){
      el.textContent='N/D';
      gauge.style.width='0%';
      detail.textContent='Nessun messaggio inviato';
      return;
    }
    el.textContent=d.remaining.toLocaleString();
    // La gauge mostra i messaggi RIMANENTI (verde=tanto, rosso=poco)
    const pctRemaining=d.total>0?(d.remaining/d.total*100):0;
    gauge.style.width=pctRemaining+'%';
    if(pctRemaining>30) gauge.style.background='var(--green)';
    else if(pctRemaining>10) gauge.style.background='var(--orange)';
    else gauge.style.background='var(--red)';
    el.style.color=pctRemaining>10?'':'var(--red)';
    detail.textContent='Reset: '+d.next_reset;
    // Controlla allarme soglia
    const alarmCfg=_alarmCfg&&_alarmCfg['pushover_remaining'];
    if(alarmCfg&&alarmCfg.enabled&&alarmCfg.threshold){
      if(d.remaining<=alarmCfg.threshold){
        toastCenter('⚠️ Pushover: solo '+d.remaining+' messaggi rimanenti!',false);
      }
    }
  }).catch(()=>{});
}

function deleteSound(name){
  confirmModal('Delete File','Delete <b>'+name+'</b>? This cannot be undone.','warn','danger','Delete').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/sounds/delete',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'name='+encodeURIComponent(name)})
    .then(r=>{if(!r.ok)throw new Error('Delete failed');return r.json()})
    .then(()=>{toastCenter('Deleted '+name,true);loadSounds()}).catch(e=>toastCenter('Error: '+e,false));
  });
}

const dz=document.getElementById('dropZone');
dz.addEventListener('dragover',e=>{e.preventDefault();dz.classList.add('dragover')});
dz.addEventListener('dragleave',()=>dz.classList.remove('dragover'));
dz.addEventListener('drop',e=>{e.preventDefault();dz.classList.remove('dragover');if(e.dataTransfer.files.length)uploadFile(e.dataTransfer.files[0])});

function uploadFile(file){
  if(!file)return;
  const ext=file.name.split('.').pop().toLowerCase();
  if(ext!=='mp3'&&ext!=='wav'){toastCenter('Only .mp3 and .wav allowed',false);return;}
  if(file.size>10*1024*1024){toastCenter('File too large (max 10MB)',false);return;}
  const fd=new FormData();fd.append('file',file);
  dz.textContent='Uploading...';
  fetch('/panel/api/upload',{method:'POST',body:fd})
  .then(r=>{if(!r.ok)throw new Error('Upload failed');return r.json()})
  .then(d=>{toastCenter('Uploaded '+d.file+' ('+fmtBytes(d.size)+')',true);loadSounds();
    dz.innerHTML='Drop files here or click to browse<br><small>Max 10MB — .mp3 or .wav only</small>'})
  .catch(e=>{toastCenter('Error: '+e,false);dz.innerHTML='Drop files here or click to browse<br><small>Max 10MB — .mp3 or .wav only</small>'});
}

// --- APK Manager ---
function loadApkList(){
  fetch('/panel/api/apk/list').then(r=>r.json()).then(files=>{
    const ul=document.getElementById('apkList');
    if(!files||files.length===0){ul.innerHTML='<li style="color:var(--dim)">Nessun APK trovato</li>';return;}
    ul.innerHTML=files.map(f=>'<li><div class="file-info"><span class="file-icon">&#128241;</span><span class="file-name">'+f.name+'</span><span class="file-size">'+fmtBytes(f.size)+'</span></div><div style="display:flex;gap:4px;flex-shrink:0"><a class="btn-icon btn-o btn-o-blue" title="Download" href="/apk/'+encodeURIComponent(f.name)+'" download><i class="bi bi-download"></i></a><button class="btn-icon btn-o btn-o-red" title="Delete" onclick="deleteApk(\''+f.name+'\')"><i class="bi bi-trash3-fill"></i></button></div></li>').join('');
  }).catch(e=>toastCenter('Errore: '+e,false));
}

function deleteApk(name){
  confirmModal('Elimina APK','Eliminare <b>'+name+'</b>? Non sar&agrave; possibile annullare.','warn','danger','Elimina').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/apk/delete',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'name='+encodeURIComponent(name)})
    .then(r=>{if(!r.ok)throw new Error('Delete failed');return r.json();})
    .then(()=>{toastCenter('APK eliminato',true);loadApkList();})
    .catch(e=>toastCenter('Errore: '+e,false));
  });
}

const apkDz=document.getElementById('apkDropZone');
if(apkDz){
  apkDz.addEventListener('dragover',e=>{e.preventDefault();apkDz.classList.add('dragover')});
  apkDz.addEventListener('dragleave',()=>apkDz.classList.remove('dragover'));
  apkDz.addEventListener('drop',e=>{e.preventDefault();apkDz.classList.remove('dragover');if(e.dataTransfer.files.length)uploadApk(e.dataTransfer.files[0])});
}

function uploadApk(file){
  if(!file)return;
  const ext=file.name.split('.').pop().toLowerCase();
  if(ext!=='apk'){toastCenter('Solo file .apk consentiti',false);return;}
  if(file.size>200*1024*1024){toastCenter('File troppo grande (max 200MB)',false);return;}
  const fd=new FormData();fd.append('file',file);
  if(apkDz)apkDz.textContent='Upload in corso...';
  fetch('/panel/api/apk/upload',{method:'POST',body:fd})
  .then(r=>{if(!r.ok)return r.text().then(t=>{throw new Error(t)});return r.json();})
  .then(d=>{toastCenter('Caricato '+d.file+' ('+fmtBytes(d.size)+')',true);loadApkList();
    if(apkDz)apkDz.innerHTML='Trascina qui il file APK oppure clicca per sfogliare<br><small>Solo file .apk — max 200MB</small>';})
  .catch(e=>{toastCenter('Errore: '+e,false);if(apkDz)apkDz.innerHTML='Trascina qui il file APK oppure clicca per sfogliare<br><small>Solo file .apk — max 200MB</small>';});
}

// --- Logs ---
let logTimer=null;
function loadLog(){
  const n=document.getElementById('logLines').value;
  fetch('/panel/api/log?lines='+n).then(r=>r.text()).then(t=>{
    const el=document.getElementById('logContent');
    el.textContent=t;
    el.scrollTop=el.scrollHeight;
  }).catch(e=>{document.getElementById('logContent').textContent='Error: '+e});
  if(document.getElementById('logAuto').checked){clearTimeout(logTimer);logTimer=setTimeout(loadLog,5000);}
}
document.getElementById('logAuto').addEventListener('change',function(){
  if(this.checked)loadLog();else clearTimeout(logTimer);
});

function aiAnalyzeLog(){
  const btn = document.getElementById('aiAnalyzeBtn');
  btn.disabled = true;
  btn.textContent = '⏳ Analisi in corso...';
  document.getElementById('sudoModalTitle').textContent = '🤖 AI Analyze — Log DoorPhoneServer';
  const outEl = document.getElementById('sudoOutput');
  outEl.style.color = '#94a3b8';
  outEl.textContent = 'Invio log al modello AI... attendere (può richiedere fino a 30s)';
  document.getElementById('sudoModal').classList.add('show');
  const lines = document.getElementById('logLines').value || '100';
  fetch('/panel/api/ai/analyze', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({lines: parseInt(lines)})
  })
  .then(r => r.json())
  .then(d => {
    btn.disabled = false;
    btn.textContent = '🤖 AI Analyze';
    if(d.error){
      outEl.style.color = '#f87171';
      outEl.textContent = '❌ Errore: ' + d.error;
      return;
    }
    outEl.style.color = '#e2e8f0';
    outEl.textContent = '📋 Modello: ' + d.model + '\n\n' + d.result;
  })
  .catch(e => {
    btn.disabled = false;
    btn.textContent = '🤖 AI Analyze';
    outEl.style.color = '#f87171';
    outEl.textContent = '❌ Errore di rete: ' + e;
  });
}

function copyLog(){
  const el=document.getElementById('logContent');
  const text=el.textContent||'';
  if(!text.trim()){toastCenter('Log vuoto',false);return;}
  const range=document.createRange();
  range.selectNodeContents(el);
  const sel=window.getSelection();
  sel.removeAllRanges();
  sel.addRange(range);
  try{
    document.execCommand('copy');
    toastCenter('Log copiato negli appunti',true);
  }catch(e){
    toastCenter('Errore copia: '+e,false);
  }
  sel.removeAllRanges();
}
function clearAnalysis(){
  confirmModal('Svuota Log','Svuotare completamente il file di log?<br><br><small style="color:var(--dim)">Il log corrente verra eliminato e ripartira da zero.</small>','warn','danger','Svuota').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/log',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'action=clear'})
    .then(r=>r.json()).then(d=>{
      if(d.ok){toastCenter('Log svuotato',true);document.getElementById('logSummary').classList.remove('visible');loadLog();}
      else toastCenter('Errore: '+d.error,false);
    }).catch(e=>toastCenter('Errore: '+e,false));
  });
}

// --- Snapshots ---
let currentFloor='';
function loadSnapshots(floor){
  if(floor!==undefined)currentFloor=floor;
  document.querySelectorAll('.floor-btn').forEach(b=>b.classList.toggle('active',b.dataset.floor===currentFloor));
  const delBtn=document.getElementById('btnDeleteAll');
  if(delBtn)delBtn.innerHTML='<i class="bi bi-trash3-fill"></i> Elimina '+(currentFloor?currentFloor:'Tutte');
  const apiUrl='/panel/api/snapshots'+(currentFloor?'?floor='+currentFloor:'');
  fetch(apiUrl).then(r=>r.json()).then(files=>{
    const g=document.getElementById('snapGallery');
    const c=document.getElementById('snapCount');
    if(delBtn){delBtn.disabled=!files||files.length===0;}
    if(!files||files.length===0){g.innerHTML='<div style="color:var(--dim)">No snapshots found</div>';c.textContent='';return;}
    c.textContent=files.length+' snapshot'+(files.length>1?'s':'');
    g.innerHTML=files.map(f=>{
      const imgUrl='/panel/api/snapshots/view/'+encodeURIComponent(f.name);
      return '<div style="background:var(--bg);border:1px solid var(--border);border-radius:8px;overflow:hidden">'
        +'<img src="'+imgUrl+'" style="width:100%;height:150px;object-fit:cover;cursor:pointer" onclick="openLightbox(\''+imgUrl+'\')">' 
        +'<div style="padding:8px;font-size:12px">'
        +'<div style="font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">'+f.name+'</div>'
        +'<div style="color:var(--dim)">'+f.mod_time+'</div>'
        +'<div style="display:flex;gap:6px;margin-top:6px">'
        +'<a href="'+imgUrl+'" download="'+f.name+'" class="btn-icon btn-o btn-o-blue" style="text-decoration:none" title="Save"><i class="bi bi-download"></i></a>'
        +'<button class="btn-icon btn-o btn-o-red" style="font-size:14px" title="Delete" onclick="deleteSnapshot(\''+f.name+'\')"><i class="bi bi-trash3-fill"></i></button>'
        +'</div></div></div>';
    }).join('');
  }).catch(e=>toastCenter('Error: '+e,false));
}

function takeSnapshotFromPanel(){
  const btn=event.target;btn.disabled=true;btn.textContent='Taking...';
  fetch('/panel/api/snapshots/take',{method:'POST'})
  .then(r=>{if(!r.ok)throw new Error('Failed');return r.json();})
  .then(()=>{toastCenter('Snapshot taken',true);loadSnapshots();})
  .catch(e=>toastCenter('Error: '+e,false))
  .finally(()=>{btn.disabled=false;btn.textContent='📸 Take Snapshot';});
}

function deleteSnapshot(name){
  confirmModal('Delete Snapshot','Delete <b>'+name+'</b>? This cannot be undone.','warn','danger','Delete').then(ok=>{
    if(!ok)return;
    const overlay=document.getElementById('confirmModal');
    const icon=document.getElementById('modalIcon');
    const body=document.getElementById('modalBody');
    const okBtn=document.getElementById('modalOk');
    const cancelBtn=document.getElementById('modalCancel');
    body.innerHTML='<div style="text-align:center;padding:12px">Eliminazione in corso\u2026</div>';
    okBtn.style.display='none';
    cancelBtn.style.display='none';
    overlay.classList.add('show');
    fetch('/panel/api/snapshots/delete',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'name='+encodeURIComponent(name)})
    .then(r=>{if(!r.ok)throw new Error('Delete failed');return r.json();})
    .then(()=>{
      icon.className='modal-icon info';icon.textContent='\u2705';
      document.getElementById('modalTitle').textContent='Eliminata';
      body.innerHTML='<b>'+name+'</b> eliminata con successo.';
      cancelBtn.textContent='Chiudi';cancelBtn.style.display='';
      cancelBtn.onclick=()=>{overlay.classList.remove('show');};
      loadSnapshots();
    })
    .catch(e=>{
      icon.className='modal-icon warn';icon.textContent='\u274C';
      document.getElementById('modalTitle').textContent='Errore';
      body.innerHTML='Errore durante l\u2019eliminazione:<br>'+e;
      cancelBtn.textContent='Chiudi';cancelBtn.style.display='';
      cancelBtn.onclick=()=>{overlay.classList.remove('show');};
    });
  });
}
function deleteAllSnapshots(){
  const label=currentFloor?'piano <b>'+currentFloor+'</b>':'<b>tutti i piani</b>';
  confirmModal('Elimina Tutte le Snapshot',
    'Eliminare tutte le immagini di '+label+'?'
    +'<br><br><div style="margin-top:4px;padding:10px;background:rgba(239,68,68,.1);border:1px solid var(--red);border-radius:6px;font-size:13px">'
    +'\u26A0\uFE0F Questa operazione non pu\u00F2 essere annullata.</div>',
    'warn','danger','Elimina Tutte'
  ).then(ok=>{
    if(!ok)return;
    const body=currentFloor?'floor='+encodeURIComponent(currentFloor):'';
    fetch('/panel/api/snapshots/deleteall',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:body})
    .then(r=>{if(!r.ok)throw new Error('Delete failed');return r.json();})
    .then(d=>{toastCenter('Eliminate '+d.deleted+' immagini',true);loadSnapshots();})
    .catch(e=>toastCenter('Errore: '+e,false));
  });
}

function openLightbox(url){
  const lb=document.getElementById('snapLightbox');
  document.getElementById('snapLightboxImg').src=url;
  lb.style.display='flex';
}
function closeLightbox(){
  document.getElementById('snapLightbox').style.display='none';
}

// --- Confirm Modal ---
function confirmModal(title,body,iconType,btnType,btnLabel){
  return new Promise(resolve=>{
    const overlay=document.getElementById('confirmModal');
    const icon=document.getElementById('modalIcon');
    const okBtn=document.getElementById('modalOk');
    const cancelBtn=document.getElementById('modalCancel');
    // Reset state from previous usage
    okBtn.style.display='';
    cancelBtn.style.display='';
    cancelBtn.textContent='Cancel';
    document.getElementById('modalTitle').textContent=title;
    document.getElementById('modalBody').innerHTML=body;
    icon.className='modal-icon '+(iconType||'warn');
    icon.textContent=iconType==='info'?'ℹ':'⚠';
    okBtn.className='modal-btn '+(btnType||'danger');
    okBtn.textContent=btnLabel||'Confirm';
    overlay.classList.add('show');
    function cleanup(){overlay.classList.remove('show');okBtn.onclick=null;cancelBtn.onclick=null;}
    okBtn.onclick=()=>{cleanup();resolve(true);};
    cancelBtn.onclick=()=>{cleanup();resolve(false);};
    overlay.onclick=e=>{if(e.target===overlay){cleanup();resolve(false);}};
  });
}

// --- Mumble Users ---
function deviceLabelFromName(name){
  // Estrae label device da username: PIANO1→P1, PIANO2→P2, Doorpi→DP, ecc.
  const m=name.match(/^([A-Za-z]+?)(\d+)$/);
  if(m){
    // Prima lettera maiuscola della parte testuale + numero
    return m[1].charAt(0).toUpperCase()+m[2];
  }
  // Nessun numero: iniziali (es. Doorpi→DP, doorphoneserver→D)
  const words=name.trim().split(/\s+/);
  if(words.length>=2) return words.map(w=>w.charAt(0).toUpperCase()).join('');
  return name.substring(0,2).toUpperCase();
}

function loadMumbleUsers(){
  fetch('/panel/api/mumbleusers?_='+Date.now(),{cache:'no-store'})
    .then(r=>r.json())
    .then(users=>{
      const tbody=document.getElementById('usersTableBody');
      const countEl=document.getElementById('userCount');
      if(!tbody)return;
      countEl.textContent=users.length+(users.length===1?' utente connesso':' utenti connessi');
      if(users.length===0){
        tbody.innerHTML='<tr><td colspan="4" style="padding:12px;color:var(--dim)">Nessun utente connesso</td></tr>';
        return;
      }
      users.sort((a,b)=>{
        if(a.is_self&&!b.is_self)return -1;
        if(!a.is_self&&b.is_self)return 1;
        return a.name.localeCompare(b.name);
      });
      tbody.innerHTML=users.map(u=>{
        const badges=[];
        const micIcon=(color,tip)=>`<i class="bi bi-mic-mute-fill" title="${tip}" style="color:${color};font-size:16px"></i>`;
        const earIcon=(color,tip)=>`<span title="${tip}" style="position:relative;display:inline-block;font-size:16px;line-height:1"><i class="bi bi-ear-fill" style="color:${color}"></i><svg style="position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none" viewBox="0 0 16 16"><line x1="2" y1="14" x2="14" y2="2" stroke="${color}" stroke-width="2.5"/></svg></span>`;
        if(u.muted)badges.push(micIcon('#ef4444','Silenziato dal server'));
        if(u.deafened)badges.push(earIcon('#f97316','Sordo (server)'));
        if(u.suppressed)badges.push(micIcon('#f59e0b','Soppresso dal canale'));
        if(u.self_muted)badges.push(micIcon('#6b7280','Microfono disattivato'));
        if(u.self_deafened)badges.push(earIcon('#6b7280','Audio disattivato'));
        if(u.priority_speaker)badges.push('<span style="background:#a855f7;color:#fff;border-radius:4px;padding:1px 5px;font-size:11px">Priority</span>');
        if(u.recording)badges.push('<span style="background:#ec4899;color:#fff;border-radius:4px;padding:1px 5px;font-size:11px">REC</span>');
        const isSelf=u.is_self;
        const devLabel=deviceLabelFromName(u.name);
        const connAt=u.connected_at||'—';
        // Ring button: map device label to ring&PN query param
        const ringCmdMap={'P1':'ring&P1','P2':'ring&P2','P3':'ring&P3','P4':'ring&P4'};
        const ringCmd=ringCmdMap[devLabel];
        const ringBtn=ringCmd
          ? `<button onclick="ringDevice('${ringCmd}')" title="Suona campanello ${devLabel}"
               style="background:none;border:1px solid var(--accent);border-radius:5px;padding:3px 8px;cursor:pointer;color:var(--accent);font-size:15px;line-height:1">
               <i class="bi bi-bell"></i>
             </button>`
          : '<span style="color:var(--dim)">—</span>';
        return `<tr style="border-bottom:1px solid var(--border);${isSelf?'background:rgba(56,189,248,0.06)':''}"
          title="${isSelf?'Questo dispositivo':''}"> 
          <td style="padding:6px 10px;font-weight:${isSelf?'700':'400'};color:${isSelf?'var(--accent)':'var(--text)'}">${u.name}${isSelf?' ★':''}</td>
          <td style="padding:6px 10px">${badges.length?badges.join(' '):'<span style="color:var(--dim)">—</span>'}</td>
          <td style="padding:6px 10px;font-family:monospace;font-size:12px;color:var(--dim)">${connAt}</td>
          <td style="padding:6px 10px">${ringBtn}</td>
        </tr>`;
      }).join('');
    })
    .catch(()=>{
      const tbody=document.getElementById('usersTableBody');
      if(tbody)tbody.innerHTML='<tr><td colspan="4" style="padding:12px;color:var(--red)">Errore caricamento</td></tr>';
    });
}

function ringDevice(cmd){
  const label = cmd.split('&')[1] || cmd;
  confirmModal('Suona campanello','Vuoi suonare il campanello di <b>'+label+'</b>?','warn','primary','Suona').then(ok=>{
    if(!ok) return;
    fetch('/?command='+cmd)
      .then(r=>{
        if(r.ok) toastCenter('Campanello inviato!',true);
        else toastCenter('Errore invio campanello',false);
      })
      .catch(()=>toastCenter('Errore connessione',false));
  });
}
let _usersRefreshTimer=null;
setInterval(()=>{
  if(document.getElementById('usersAutoRefresh')&&document.getElementById('usersAutoRefresh').checked){
    const activePage=document.querySelector('.page.active');
    if(activePage&&activePage.id==='page-users')loadMumbleUsers();
  }
},5000);

// --- Audio Test ---
function loadAudioTestFiles(){
  fetch('/panel/api/audiotest').then(r=>r.json()).then(files=>{
    const ul=document.getElementById('atFileList');
    if(!files||files.length===0){ul.innerHTML='<li style="color:var(--dim)">Nessun file .wav o .mp3 trovato</li>';return;}
    ul.innerHTML=files.map(f=>'<li><div class="file-info"><span class="file-icon">'+(f.name.toLowerCase().endsWith('.mp3')?'\uD83C\uDFB5':'&#128266;')+'</span><span class="file-name">'+f.name+'</span><span class="file-size">'+fmtBytes(f.size)+'</span></div><div style="display:flex;gap:4px;flex-shrink:0"><button class="btn-icon btn-o btn-o-blue" title="Play in Browser" onclick="atPlayBrowser(\''+f.name+'\')"><i class="bi bi-play-fill"></i></button><button class="btn-icon btn-o btn-o-purple" title="Invia su Mumble (Test)" onclick="atRunTest(\''+f.name+'\')"><i class="bi bi-broadcast"></i></button><button class="btn-icon btn-o btn-o-red" title="Elimina" onclick="atDeleteFile(\''+f.name+'\')"><i class="bi bi-trash3-fill"></i></button></div></li>').join('');
  }).catch(e=>toastCenter('Errore: '+e,false));
}

function uploadAudioTest(file){
  if(!file)return;
  const ext=file.name.split('.').pop().toLowerCase();
  if(ext!=='wav'&&ext!=='mp3'){toastCenter('Solo file .wav e .mp3 ammessi',false);return;}
  if(file.size>10*1024*1024){toastCenter('File troppo grande (max 10MB)',false);return;}
  const fd=new FormData();fd.append('file',file);
  const dz=document.getElementById('atDropZone');
  dz.textContent='Uploading...';
  fetch('/panel/api/audiotest/upload',{method:'POST',body:fd})
  .then(r=>{if(!r.ok)throw new Error('Upload failed');return r.json();})
  .then(d=>{toastCenter('Caricato: '+d.file+' ('+fmtBytes(d.size)+')',true);loadAudioTestFiles();
    dz.innerHTML='Drop files here or click to browse<br><small>Max 10MB — .wav o .mp3</small>';})
  .catch(e=>{toastCenter('Errore: '+e,false);dz.innerHTML='Drop files here or click to browse<br><small>Max 10MB — .wav o .mp3</small>';});
}

function atPlayBrowser(name){
  const player=document.getElementById('atAudioPlayer');
  const box=document.getElementById('atPlayerBox');
  document.getElementById('atNowPlaying').textContent=name;
  player.src='/panel/api/audiotest/play/'+encodeURIComponent(name);
  box.style.display='block';
  player.play();
}

function atStopAudio(){
  const player=document.getElementById('atAudioPlayer');
  player.pause();player.src='';
  document.getElementById('atPlayerBox').style.display='none';
}

function atRunTest(name){
  confirmModal('Avvia Test Audio','Inviare <b>'+name+'</b> nel canale Mumble come test?','info','primary','Avvia').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/audiotest/run',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'name='+encodeURIComponent(name)})
    .then(r=>{if(!r.ok)throw new Error('Errore server');return r.json();})
    .then(()=>toastCenter('Test avviato: '+name,true))
    .catch(e=>toastCenter('Errore: '+e,false));
  });
}

function atDeleteFile(name){
  confirmModal('Elimina File','Eliminare <b>'+name+'</b>?','warn','danger','Elimina').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/audiotest/delete',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'name='+encodeURIComponent(name)})
    .then(r=>{if(!r.ok)throw new Error('Delete failed');return r.json();})
    .then(()=>{toastCenter('Eliminato: '+name,true);loadAudioTestFiles();})
    .catch(e=>toastCenter('Errore: '+e,false));
  });
}

// Setup drag&drop audiotest
(function(){
  const dz=document.getElementById('atDropZone');
  if(!dz)return;
  dz.addEventListener('dragover',e=>{e.preventDefault();dz.classList.add('dragover');});
  dz.addEventListener('dragleave',()=>dz.classList.remove('dragover'));
  dz.addEventListener('drop',e=>{e.preventDefault();dz.classList.remove('dragover');if(e.dataTransfer.files.length)uploadAudioTest(e.dataTransfer.files[0]);});
})();

// --- Live Monitor (TX Doorpi + RX chi parla) ---
let liveMonitorInterval=null;
function startLiveMonitor(){
  if(liveMonitorInterval)return;
  liveMonitorInterval=setInterval(pollLiveMonitor,300);
}
function stopLiveMonitor(){
  if(liveMonitorInterval){clearInterval(liveMonitorInterval);liveMonitorInterval=null;}
  const rxDot=document.getElementById('rxDot');
  const rxFrom=document.getElementById('rxFrom');
  const rxBar=document.getElementById('rxBar');
  const rxLvl=document.getElementById('rxLevel');
  const txDot=document.getElementById('txDot');
  const txBar=document.getElementById('txBar');
  const txLvl=document.getElementById('txLevel');
  if(rxDot)rxDot.style.background='#374151';
  if(rxFrom){rxFrom.innerHTML='<i class="bi bi-volume-up-fill"></i> Altoparlante';rxFrom.style.color='var(--text-muted)';}
  if(rxBar)rxBar.style.width='0%';
  if(rxLvl)rxLvl.textContent='—';
  if(txDot)txDot.style.background='#374151';
  if(txBar)txBar.style.width='0%';
  if(txLvl)txLvl.textContent='—';
}
function pollLiveMonitor(){
  fetch('/panel/api/audiotest/rxstatus')
    .then(r=>r.json())
    .then(d=>{
      const dot=document.getElementById('rxDot');
      const from=document.getElementById('rxFrom');
      const bar=document.getElementById('rxBar');
      const lvl=document.getElementById('rxLevel');
      if(!dot)return;
      if(d.receiving){
        dot.style.background='#22c55e';
        from.textContent=d.from;
        from.style.color='#22c55e';
        bar.style.width=d.level+'%';
        lvl.textContent=d.level;
      } else {
        dot.style.background='#374151';
        from.innerHTML=d.from?d.from:'<i class="bi bi-volume-up-fill"></i> Altoparlante';
        from.style.color='var(--text-muted)';
        bar.style.width='0%';
        lvl.textContent='—';
      }
    })
    .catch(()=>{});
  fetch('/panel/api/audiotest/txstatus')
    .then(r=>r.json())
    .then(d=>{
      const dot=document.getElementById('txDot');
      const bar=document.getElementById('txBar');
      const lvl=document.getElementById('txLevel');
      if(!dot)return;
      if(d.transmitting){
        dot.style.background='#38bdf8';
        bar.style.width=d.level+'%';
        lvl.textContent=d.level;
      } else {
        dot.style.background='#374151';
        bar.style.width='0%';
        lvl.textContent='—';
      }
    })
    .catch(()=>{});
}

// --- Alarm Modal ---
let _alarmCfg={};
let _alarmKey='';

function loadAlarmConfig(){
  fetch('/panel/api/alarms').then(r=>r.json()).then(cfg=>{
    _alarmCfg=cfg;
    updateAlarmBells();
  }).catch(()=>{});
}

function updateAlarmBells(){
  const map={load_avg:'alarmBtnLoad',disk_pct:'alarmBtnDisk',throttle:'alarmBtnThrottle',cpu_temp:'alarmBtnTemp',pushover_remaining:'alarmBtnPushover',ram_pct:'alarmBtnRam'};
  for(const[key,btnId] of Object.entries(map)){
    const btn=document.getElementById(btnId);
    if(!btn)continue;
    const active=_alarmCfg[key]&&_alarmCfg[key].enabled;
    btn.style.opacity=active?'1':'0.3';
    btn.title=(active?'Allarme attivo — ':'Allarme disattivo — ')+'clicca per configurare';
  }
}

function openAlarmModal(key){
  _alarmKey=key;
  const titles={load_avg:'Allarme Load Average',disk_pct:'Allarme Disco',throttle:'Allarme Throttle',cpu_temp:'Allarme Temperatura CPU',pushover_remaining:'Allarme Messaggi Pushover',ram_pct:'Allarme RAM'};
  const hints={
    load_avg:'Invia notifica se il Load Average (1 min) supera la soglia.',
    disk_pct:'Invia notifica se il disco supera la percentuale impostata.',
    throttle:'Invia notifica quando il Raspberry Pi va in throttling.',
    cpu_temp:'Invia notifica se la temperatura CPU supera la soglia (°C).',
    pushover_remaining:'Invia notifica quando i messaggi Pushover rimanenti scendono sotto la soglia.',
    ram_pct:'Invia notifica se la RAM utilizzata supera la percentuale impostata.'
  };
  const cur=(_alarmCfg&&_alarmCfg[key])||{};
  document.getElementById('alarmModalTitle').textContent=titles[key]||key;
  document.getElementById('alarmEnabled').checked=!!cur.enabled;
  document.getElementById('alarmHint').textContent=hints[key]||'';
  const thrRow=document.getElementById('alarmThresholdRow');
  if(key==='throttle'){
    thrRow.style.display='none';
  } else {
    thrRow.style.display='';
    const lbls={load_avg:'Soglia Load Average (es. 3.0)',disk_pct:'Soglia disco % (es. 80)',cpu_temp:'Soglia temperatura °C (es. 70)',pushover_remaining:'Messaggi rimanenti minimi (es. 500)',ram_pct:'Soglia RAM % (es. 80)'};
    document.getElementById('alarmThresholdLabel').textContent=lbls[key]||'Soglia';
    document.getElementById('alarmThreshold').value=cur.threshold||'';
  }
  document.getElementById('alarmModal').classList.add('show');
}

function closeAlarmModal(){
  document.getElementById('alarmModal').classList.remove('show');
}

function saveAlarm(){
  if(!_alarmKey){toastCenter('Chiave allarme non valida',false);return;}
  if(!_alarmCfg)_alarmCfg={};
  _alarmCfg[_alarmKey]={enabled:document.getElementById('alarmEnabled').checked};
  if(_alarmKey!=='throttle'){
    const v=parseFloat(document.getElementById('alarmThreshold').value);
    if(isNaN(v)||v<=0){toastCenter('Soglia non valida',false);return;}
    _alarmCfg[_alarmKey].threshold=v;
  }
  fetch('/panel/api/alarms',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(_alarmCfg)})
    .then(r=>r.json()).then(d=>{
      if(d.ok){toastCenter('Allarme salvato',true);updateAlarmBells();}
      else toastCenter('Errore salvataggio: '+(d.error||''),false);
    }).catch(e=>toastCenter('Errore: '+e,false));
  closeAlarmModal();
}

// --- Chima AI / OpenRouter ---
let _orModels = [];
let _orSort = {key:'name', asc:true};
let _orSelectedId = '';

function updateORSelectedBar(id, name){
  _orSelectedId = id;
  const bar = document.getElementById('orSelectedBar');
  if(!id){
    bar.style.display='none';
    return;
  }
  bar.style.display='flex';
  document.getElementById('orSelectedName').textContent = name || id;
  document.getElementById('orSelectedId').textContent = id;
  // evidenzia la riga selezionata
  document.querySelectorAll('#orTableBody tr').forEach(tr=>{
    tr.style.outline = tr.dataset.modelId===id ? '2px solid #2563eb' : '';
  });
}

function selectORModel(id, name){
  fetch('/panel/api/openrouter/selected',{
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({id,name})
  }).then(r=>r.json()).then(d=>{
    if(d.ok){ updateORSelectedBar(id,name); toastCenter('Modello selezionato',true); }
    else toastCenter('Errore: '+(d.error||''),false);
  }).catch(e=>toastCenter('Errore: '+e,false));
}

function clearORSelected(){
  fetch('/panel/api/openrouter/selected',{
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({id:'',name:''})
  }).then(r=>r.json()).then(d=>{
    if(d.ok){ updateORSelectedBar('',''); toastCenter('Selezione rimossa',true); }
  }).catch(e=>toastCenter('Errore: '+e,false));
}

function loadORSelected(){
  fetch('/panel/api/openrouter/selected')
    .then(r=>r.json())
    .then(d=>updateORSelectedBar(d.id||'', d.name||''))
    .catch(()=>{});
}

function fmtPrice(raw){
  const n = parseFloat(raw);
  if(isNaN(n) || n === 0) return '<span style="color:var(--green);font-weight:600">FREE</span>';
  // OpenRouter returns price per token; multiply by 1M for $/M
  const perM = n * 1_000_000;
  return '$' + (perM < 0.01 ? perM.toFixed(4) : perM.toFixed(3));
}

function fmtCtx(n){
  if(!n) return '—';
  return n >= 1000 ? (n/1000).toFixed(0)+'k' : String(n);
}

function renderORTable(models){
  const tbody = document.getElementById('orTableBody');
  if(!models.length){
    tbody.innerHTML='<tr><td colspan="6" style="padding:16px;color:var(--dim)">Nessun modello trovato.</td></tr>';
    return;
  }
  tbody.innerHTML = models.map(m=>{
    const pPrompt = fmtPrice(m.pricing?.prompt);
    const pCompl  = fmtPrice(m.pricing?.completion);
    const isFree  = parseFloat(m.pricing?.prompt||'1')===0 && parseFloat(m.pricing?.completion||'1')===0;
    const isSel   = m.id === _orSelectedId;
    const rowBg   = isSel ? 'background:rgba(37,99,235,0.12);outline:2px solid #2563eb;' : (isFree ? 'background:rgba(34,197,94,0.06)' : '');
    const btnStyle = isSel
      ? 'background:#2563eb;color:#fff;border:none;border-radius:5px;padding:3px 10px;font-size:12px;cursor:default'
      : 'background:#1e293b;color:#94a3b8;border:1px solid var(--border);border-radius:5px;padding:3px 10px;font-size:12px;cursor:pointer';
    const btnLabel = isSel ? '<i class="bi bi-check2"></i> Selezionato' : 'Usa';
    const btnClick = isSel ? '' : `onclick="selectORModel('${m.id.replace(/'/g,"\\'")}','${(m.name||m.id).replace(/'/g,"\\'")}')"`;
    return `<tr data-model-id="${m.id}" style="border-bottom:1px solid var(--border);${rowBg}">
      <td style="padding:7px 10px;font-weight:500">${m.name||m.id}</td>
      <td style="padding:7px 10px;color:var(--dim);font-size:11px;font-family:monospace">${m.id}</td>
      <td style="padding:7px 10px;text-align:right">${pPrompt}</td>
      <td style="padding:7px 10px;text-align:right">${pCompl}</td>
      <td style="padding:7px 10px;text-align:right;color:var(--dim)">${fmtCtx(m.context_length)}</td>
      <td style="padding:7px 10px;text-align:center"><button style="${btnStyle}" ${btnClick}>${btnLabel}</button></td>
    </tr>`;
  }).join('');
}

function filterORModels(){
  const q = (document.getElementById('orSearch').value||'').toLowerCase();
  const freeOnly = document.getElementById('orFreeOnly').checked;
  let list = _orModels.filter(m=>{
    const match = !q || (m.name||'').toLowerCase().includes(q) || m.id.toLowerCase().includes(q);
    const free  = !freeOnly || (parseFloat(m.pricing?.prompt||'1')===0 && parseFloat(m.pricing?.completion||'1')===0);
    return match && free;
  });
  list = sortOR(list);
  document.getElementById('orCount').textContent = list.length + ' modelli';
  renderORTable(list);
}

function sortOR(list){
  const k = _orSort.key;
  return [...list].sort((a,b)=>{
    let va, vb;
    if(k==='prompt'||k==='completion'){
      va = parseFloat(a.pricing?.[k]||'0');
      vb = parseFloat(b.pricing?.[k]||'0');
    } else if(k==='context'){
      va = a.context_length||0;
      vb = b.context_length||0;
    } else if(k==='id'){
      va = a.id; vb = b.id;
    } else {
      va = (a.name||a.id).toLowerCase();
      vb = (b.name||b.id).toLowerCase();
    }
    if(va<vb) return _orSort.asc?-1:1;
    if(va>vb) return _orSort.asc?1:-1;
    return 0;
  });
}

function sortORModels(key){
  if(_orSort.key===key) _orSort.asc=!_orSort.asc; else {_orSort.key=key;_orSort.asc=true;}
  ['Name','Id','Prompt','Completion','Context'].forEach(x=>{
    const el=document.getElementById('orSort'+x);
    if(el) el.textContent='';
  });
  const colId = 'orSort'+key.charAt(0).toUpperCase()+key.slice(1);
  const el = document.getElementById(colId);
  if(el) el.textContent = _orSort.asc ? ' ▲' : ' ▼';
  filterORModels();
}

function loadORModels(){
  document.getElementById('orLoading').style.display='block';
  document.getElementById('orTableWrap').style.display='none';
  document.getElementById('orError').style.display='none';
  // carica selezione e modelli in parallelo
  Promise.all([
    fetch('/panel/api/openrouter/selected').then(r=>r.json()).catch(()=>({id:'',name:''})),
    fetch('/panel/api/openrouter/models').then(r=>r.json())
  ]).then(([sel, d])=>{
    _orSelectedId = sel.id || '';
    updateORSelectedBar(sel.id||'', sel.name||'');
    if(d.error){throw new Error(d.error);}
    _orModels = (d.data||[]).sort((a,b)=>(a.name||a.id).localeCompare(b.name||b.id));
    document.getElementById('orLoading').style.display='none';
    document.getElementById('orTableWrap').style.display='block';
    filterORModels();
  }).catch(e=>{
    document.getElementById('orLoading').style.display='none';
    const err=document.getElementById('orError');
    err.style.display='block';
    err.textContent='Errore: '+e.message;
  });
}

// --- Live Clock ---
function updateLiveClock(){
  const el=document.getElementById('liveClock');
  if(!el) return;
  const now=new Date();
  const d=String(now.getDate()).padStart(2,'0');
  const m=String(now.getMonth()+1).padStart(2,'0');
  const y=now.getFullYear();
  const hh=String(now.getHours()).padStart(2,'0');
  const mm=String(now.getMinutes()).padStart(2,'0');
  const ss=String(now.getSeconds()).padStart(2,'0');
  el.textContent=d+'/'+m+'/'+y+'  '+hh+':'+mm+':'+ss;
}
updateLiveClock();
setInterval(updateLiveClock,1000);

// --- Sysinfo ---
function loadSysInfo(){
  fetch('/panel/api/sysinfo').then(r=>r.json()).then(d=>{
    const s=(id,v)=>{const el=document.getElementById(id);if(el)el.textContent=v||'--';};
    s('si-os',       d.os);
    s('si-kernel',   d.kernel);
    s('si-cpumodel', d.cpu_model);
    s('si-cores',    d.cpu_cores+' core · '+d.arch);
    s('si-hostname', d.hostname);
    s('si-appver',   'v'+d.app_version);
    s('si-gover',    d.go_version);
    s('si-uptime',   d.uptime_sys);
    s('si-boot',     d.boot_time);
  }).catch(()=>{});
}

// --- Init ---
fetch('/panel/api/features').then(r=>r.json()).then(f=>{
  if(!f.esp32){
    ['esp32','nfcwhitelist'].forEach(page=>{
      const tab=document.querySelector('.tab[data-page="'+page+'"]');
      if(tab) tab.style.display='none';
    });
  }
}).catch(()=>{});

updateDashboard();
setInterval(updateDashboard,3000);
loadAlarmConfig();
loadStreamerStatus();
setInterval(loadStreamerStatus,10000);
loadMumbleStatus();
setInterval(loadMumbleStatus,10000);
loadTabletStatus();
setInterval(loadTabletStatus,8000);
loadSysInfo();
setInterval(loadSysInfo,60000);

loadPushoverUsage();
setInterval(loadPushoverUsage,60000);
loadSounds();
loadLog();


// --- Speaking Timeline ---
function groupSpeakingEntries(entries){
  const chron = entries.slice().reverse();
  const calls = [];
  let current = null;

  for(const e of chron){
    if(e.type === 'open'){
      current = { openTime: new Date(e.at), closeTime: null, closed: false, speaks: [] };
      calls.push(current);
    } else if(e.type === 'close'){
      if(current){ current.closeTime = new Date(e.at); current.closed = true; }
    } else if(e.type === 'speak'){
      if(!current){
        current = { openTime: new Date(e.at), closeTime: null, closed: false, speaks: [] };
        calls.push(current);
      }
      current.speaks.push(e);
    }
  }
  return calls.reverse();
}

function renderSpeakingCall(call){
  const openStr = call.openTime.toLocaleString('it-IT',{
    day:'2-digit',month:'2-digit',year:'numeric',
    hour:'2-digit',minute:'2-digit',second:'2-digit'
  });

  const durSec = call.closeTime
    ? Math.round((call.closeTime - call.openTime) / 1000)
    : Math.round((Date.now() - call.openTime) / 1000);
  const durStr = durSec >= 60 ? `${Math.floor(durSec/60)}m ${durSec%60}s` : `${durSec}s`;

  const seen = new Set();
  const participants = [];
  for(const e of call.speaks){ if(!seen.has(e.who)){ seen.add(e.who); participants.push(e.who); } }

  const statusColor = call.closed ? 'var(--red,#ef4444)' : 'var(--green,#22c55e)';
  const statusIcon  = call.closed ? 'bi-telephone-x-fill' : 'bi-telephone-fill';
  const statusLabel = call.closed ? 'Chiusa' : 'In corso';

  const rows = [
    `<div style="display:flex;align-items:center;gap:10px;padding:6px 12px;background:rgba(34,197,94,0.08);border-left:3px solid var(--green,#22c55e)">
      <span style="color:var(--dim);font-size:12px;min-width:70px">${call.openTime.toLocaleTimeString('it-IT',{hour:'2-digit',minute:'2-digit',second:'2-digit'})}</span>
      <i class="bi bi-telephone-fill" style="color:var(--green,#22c55e);font-size:12px"></i>
      <span style="font-weight:600;color:var(--green,#22c55e)">Chiamata aperta</span>
    </div>`,
    ...call.speaks.map((e,i)=>{
      const ts = new Date(e.at).toLocaleTimeString('it-IT',{hour:'2-digit',minute:'2-digit',second:'2-digit'});
      return `<div style="display:flex;align-items:center;gap:10px;padding:5px 12px;background:${i%2===0?'rgba(255,255,255,0.02)':'transparent'}">
        <span style="color:var(--dim);font-size:12px;min-width:70px">${ts}</span>
        <i class="bi bi-mic-fill" style="color:var(--blue,#3b82f6);font-size:12px"></i>
        <span style="font-weight:600">${e.who}</span>
      </div>`;
    }),
    call.closed
      ? `<div style="display:flex;align-items:center;gap:10px;padding:6px 12px;background:rgba(239,68,68,0.08);border-left:3px solid var(--red,#ef4444)">
           <span style="color:var(--dim);font-size:12px;min-width:70px">${call.closeTime.toLocaleTimeString('it-IT',{hour:'2-digit',minute:'2-digit',second:'2-digit'})}</span>
           <i class="bi bi-telephone-x-fill" style="color:var(--red,#ef4444);font-size:12px"></i>
           <span style="font-weight:600;color:var(--red,#ef4444)">Chiamata chiusa</span>
           <span style="margin-left:auto;font-size:12px;color:var(--dim)">${durStr}</span>
         </div>`
      : `<div style="display:flex;align-items:center;gap:10px;padding:6px 12px;background:rgba(34,197,94,0.05);border-left:3px solid var(--green,#22c55e)">
           <span style="color:var(--dim);font-size:12px;min-width:70px">—</span>
           <i class="bi bi-circle-fill" style="color:var(--green,#22c55e);font-size:8px;animation:pulse 1.5s infinite"></i>
           <span style="font-weight:600;color:var(--green,#22c55e)">In corso</span>
           <span style="margin-left:auto;font-size:12px;color:var(--dim)">${durStr}</span>
         </div>`
  ].join('');

  const header = `<div style="display:flex;align-items:center;gap:10px;padding:10px 14px;background:var(--card-bg);cursor:pointer;user-select:none"
       onclick="this.nextElementSibling.style.display=this.nextElementSibling.style.display==='none'?'block':'none'">
    <i class="bi ${statusIcon}" style="color:${statusColor}"></i>
    <span style="font-size:13px">${openStr}</span>
    <span style="font-size:12px;color:var(--dim)">·</span>
    <span style="font-size:12px;color:var(--dim)">${call.speaks.length} turni</span>
    <span style="font-size:12px;color:var(--dim)">·</span>
    <span style="font-size:12px;color:${statusColor}">${statusLabel}</span>
    <span style="margin-left:auto;font-size:12px;color:var(--dim)">${participants.join(' ↔ ')}</span>
    <i class="bi bi-chevron-down" style="color:var(--dim);font-size:11px;margin-left:4px"></i>
  </div>`;

  return `<div style="border:1px solid var(--border);border-radius:8px;margin-bottom:12px;overflow:hidden">
    ${header}
    <div style="display:none">${rows}</div>
  </div>`;
}

function clearSpeakingLog(){
  confirmModal('Cancella Log','Cancellare tutti gli eventi del parlato?<br><small style="color:var(--dim)">L\'operazione non può essere annullata.</small>','warn','danger','Cancella').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/speaking-log/clear',{method:'POST'})
      .then(r=>r.json())
      .then(d=>{if(d.ok)loadSpeakingLog();})
      .catch(()=>toastCenter('Errore cancellazione',false));
  });
}

function loadSpeakingLog(){
  const loading = document.getElementById('speakingLogLoading');
  const empty   = document.getElementById('speakingLogEmpty');
  const list    = document.getElementById('speakingLogList');
  if(!loading) return;
  loading.style.display = 'block';
  empty.style.display   = 'none';
  list.style.display    = 'none';

  fetch('/panel/api/speaking-log')
    .then(r=>r.json())
    .then(entries=>{
      loading.style.display = 'none';
      if(!entries || entries.length === 0){
        empty.style.display = 'block';
        return;
      }
      const calls = groupSpeakingEntries(entries);
      if(calls.length === 0){ empty.style.display = 'block'; return; }
      list.style.display = 'block';
      list.innerHTML = calls.map(c=>renderSpeakingCall(c)).join('');
    })
    .catch(()=>{
      if(loading) loading.textContent = 'Errore nel caricamento';
    });
}

// --- Crontab Manager ---
function loadCron(){
  const loading=document.getElementById('cronLoading');
  const empty=document.getElementById('cronEmpty');
  const wrap=document.getElementById('cronTableWrap');
  const tbody=document.getElementById('cronTableBody');
  loading.style.display='';empty.style.display='none';wrap.style.display='none';
  fetch('/panel/api/cron').then(r=>r.json()).then(jobs=>{
    loading.style.display='none';
    if(!Array.isArray(jobs)||jobs.length===0){empty.style.display='';return;}
    wrap.style.display='';
    tbody.innerHTML=jobs.map(j=>{
      const schedEsc=j.schedule.replace(/"/g,'&quot;');
      const cmdEsc=j.command.replace(/</g,'&lt;').replace(/>/g,'&gt;');
      return `<tr style="border-bottom:1px solid var(--border)">
        <td style="padding:8px 6px">
          <label class="cron-toggle" title="${j.enabled?'Disabilita':'Abilita'}">
            <input type="checkbox" ${j.enabled?'checked':''} onchange="cronToggle(${j.index},this)">
            <span class="cron-slider"></span>
          </label>
        </td>
        <td style="padding:8px 6px;font-family:monospace;color:var(--accent);white-space:nowrap">${schedEsc}</td>
        <td style="padding:8px 6px;font-family:monospace;word-break:break-all">${cmdEsc}</td>
        <td style="padding:8px 6px;text-align:center">
          <button class="btn-o btn-o-red btn-sm" onclick="cronDelete(${j.index})" title="Elimina"><i class="bi bi-trash3"></i></button>
        </td>
      </tr>`;
    }).join('');
  }).catch(e=>toastCenter('Errore crontab: '+e,false));
}

function cronToggle(index,cb){
  const fd=new FormData();fd.append('action','toggle');fd.append('index',index);
  fetch('/panel/api/cron',{method:'POST',body:fd})
    .then(r=>r.json()).then(d=>{
      if(d.ok)toastCenter('Job aggiornato',true);
      else toastCenter('Errore: '+(d.error||'?'),false);
      loadCron();
    }).catch(e=>{toastCenter('Errore: '+e,false);if(cb)cb.checked=!cb.checked;});
}

function cronDelete(index){
  if(!confirm('Eliminare questo job crontab?'))return;
  const fd=new FormData();fd.append('action','delete');fd.append('index',index);
  fetch('/panel/api/cron',{method:'POST',body:fd})
    .then(r=>r.json()).then(d=>{
      if(d.ok){toastCenter('Job eliminato',true);loadCron();}
      else toastCenter('Errore: '+(d.error||'?'),false);
    }).catch(e=>toastCenter('Errore: '+e,false));
}

function cronAdd(){
  const sched=document.getElementById('cronNewSchedule').value.trim();
  const cmd=document.getElementById('cronNewCommand').value.trim();
  if(!sched||!cmd){toastCenter('Inserire schedule e comando',false);return;}
  const fd=new FormData();fd.append('action','add');fd.append('schedule',sched);fd.append('command',cmd);
  fetch('/panel/api/cron',{method:'POST',body:fd})
    .then(r=>r.json()).then(d=>{
      if(d.ok){
        toastCenter('Job aggiunto',true);
        document.getElementById('cronNewSchedule').value='';
        document.getElementById('cronNewCommand').value='';
        loadCron();
      } else toastCenter('Errore: '+(d.error||'?'),false);
    }).catch(e=>toastCenter('Errore: '+e,false));
}

// =============================================================================
// Log2Ram
// =============================================================================
const l2rHist={sdWrites:[],ramSize:[],ioPct:[]};
const l2rHistMax=60;
const l2rShow={sdWrites:true,ramSize:true,ioPct:true};
let l2rPollTimer=null;
let l2rIsActive=false; // true quando log2ram è installato, attivo e /var/log su tmpfs

function loadLog2Ram(){
  fetch('/panel/api/log2ram/status').then(r=>r.json()).then(renderL2RStatus).catch(()=>{});
  fetch('/panel/api/log2ram/files').then(r=>r.json()).then(renderL2RFiles).catch(()=>{});
  fetch('/panel/api/log2ram/config').then(r=>r.json()).then(d=>{
    const inp=document.getElementById('l2rSizeInput');
    if(inp&&d.size_mb)inp.value=d.size_mb;
  }).catch(()=>{});
  updateL2RMetrics();
  if(!l2rPollTimer)l2rPollTimer=setInterval(updateL2RMetrics,10000);
}

function stopLog2RamPoll(){
  if(l2rPollTimer){clearInterval(l2rPollTimer);l2rPollTimer=null;}
}

function renderL2RStatus(d){
  const dot=document.getElementById('l2rDot');
  const txt=document.getElementById('l2rStatusText');
  let color,label;
  if(!d.installed){color='#6b7280';label='Non installato';}
  else if(!d.active){color='#ef4444';label='Non attivo';}
  else if(!d.log2ram_mount){color='#ef4444';label='Montaggio /var/log su tmpfs assente';}
  else if(!d.journal_volatile){color='#eab308';label='Attivo — journal ancora persistente su SD';}
  else{color='#22c55e';label='Attivo — /var/log su RAM, journal volatile';}
  dot.style.background=color;
  txt.textContent=label;
  l2rIsActive = !!(d.installed && d.active && d.log2ram_mount);

  // mostra bottone Installa solo se non installato, nasconde sync/restart
  const btnInstall=document.getElementById('l2rBtnInstall');
  const btnSync=document.getElementById('l2rBtnSync');
  const btnRestart=document.getElementById('l2rBtnRestart');
  const sizeRow=document.getElementById('l2rSizeRow');
  if(btnInstall){
    btnInstall.style.display=d.installed?'none':'';
    if(btnSync)btnSync.style.display=d.installed?'':'none';
    if(btnRestart)btnRestart.style.display=d.installed?'':'none';
    if(sizeRow)sizeRow.style.display=d.installed?'flex':'none';
  }

  const ramUsed=d.ram_used_bytes?(d.ram_used_bytes/1048576).toFixed(1)+' MB':'—';
  const ramTotal=d.ram_total_bytes?(d.ram_total_bytes/1048576).toFixed(0)+' MB':'—';
  const ramPct=(d.ram_used_pct||0).toFixed(1);
  const barColor=(d.ram_used_pct||0)>80?'#ef4444':'#22c55e';
  const backupSize=d.backup_size_bytes?(d.backup_size_bytes/1048576).toFixed(1)+' MB':'—';
  const lastSync=d.last_sync?new Date(d.last_sync).toLocaleString('it-IT'):'—';
  const journal=d.journal_volatile?'Volatile (RAM)':'<span style="color:#eab308">Persistente (SD)</span>';

  // RAM Log MB ha senso solo con /var/log su tmpfs
  const ramBtn=document.getElementById('l2rTogRam');
  if(ramBtn){
    if(d.log2ram_mount){
      ramBtn.disabled=false;ramBtn.style.opacity='';ramBtn.title='';
    } else {
      l2rShow.ramSize=false;
      ramBtn.disabled=true;ramBtn.style.opacity='0.3';ramBtn.title='Disponibile solo con log2ram attivo';
    }
  }

  document.getElementById('l2rInfoGrid').innerHTML=`
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(34,197,94,.12);color:${barColor}"><i class="bi bi-memory"></i></div>
      <div class="sysinfo-chip-body">
        <span class="sysinfo-chip-label">RAM usata</span>
        <span class="sysinfo-chip-value">${ramUsed} / ${ramTotal} (${ramPct}%)</span>
        <div style="height:4px;background:#334155;border-radius:2px;margin-top:6px"><div style="height:100%;width:${Math.min(d.ram_used_pct||0,100)}%;background:${barColor};border-radius:2px;transition:width .5s"></div></div>
      </div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(56,189,248,.12);color:#38bdf8"><i class="bi bi-hdd-fill"></i></div>
      <div class="sysinfo-chip-body">
        <span class="sysinfo-chip-label">Backup SD</span>
        <span class="sysinfo-chip-value">${d.backup_path||'/var/hdd.log'}</span>
        <span class="sysinfo-chip-label" style="margin-top:3px">${backupSize}</span>
      </div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(167,139,250,.12);color:#a78bfa"><i class="bi bi-clock-fill"></i></div>
      <div class="sysinfo-chip-body">
        <span class="sysinfo-chip-label">Ultima sync</span>
        <span class="sysinfo-chip-value">${lastSync}</span>
      </div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(251,191,36,.12);color:#fbbf24"><i class="bi bi-journal-text"></i></div>
      <div class="sysinfo-chip-body">
        <span class="sysinfo-chip-label">Journal</span>
        <span class="sysinfo-chip-value">${journal}</span>
      </div>
    </div>`;
}

function updateL2RMetrics(){
  fetch('/panel/api/log2ram/metrics-history').then(r=>r.json()).then(arr=>{
    if(!Array.isArray(arr)||arr.length<2)return;
    l2rHist.sdWrites.length=0;l2rHist.ramSize.length=0;l2rHist.ioPct.length=0;
    for(let i=1;i<arr.length;i++){
      const delta=arr[i].sd_write_mb-arr[i-1].sd_write_mb;
      l2rHist.sdWrites.push(Math.max(0,delta));
      l2rHist.ramSize.push(arr[i].log_ram_mb||0);
      l2rHist.ioPct.push(arr[i].sd_io_pct||0);
    }
    drawL2RChart();
    renderL2RStats(arr[arr.length-1], l2rHist.sdWrites);
  }).catch(()=>{});
}

function renderL2RStats(m, recentDeltas){
  const grid=document.getElementById('l2rStatsGrid');
  if(!grid)return;
  const uptimeH=(m.uptime_sec/3600).toFixed(1);
  // Usa il rate recente (ultimi campioni del grafico) per la stima giornaliera,
  // evitando che la burst di scrittura al boot distorca la media.
  // Ogni campione è un delta di 10s; la media dei campioni recenti × 8640 = MB/giorno.
  let mbPerDay='—';
  if(recentDeltas && recentDeltas.length>=2){
    const window=recentDeltas.slice(-12); // ultimi ~2 minuti (12 campioni × 10s)
    const avgPer10s=window.reduce((a,b)=>a+b,0)/window.length;
    mbPerDay=(avgPer10s*8640).toFixed(0);
  } else if(m.uptime_sec>0){
    mbPerDay=((m.sd_write_mb/m.uptime_sec)*86400).toFixed(0);
  }
  const baseline=4000;
  const saved=baseline-parseFloat(mbPerDay||0);
  // Efficienza e risparmio sono significativi solo se log2ram è attivo e montato
  const effStr=l2rIsActive&&saved>0?(saved/baseline*100).toFixed(1)+'%':'N/D';
  const savedStr=l2rIsActive&&saved>0?saved.toFixed(0)+' MB':'N/D';
  const ioColor=m.sd_io_pct>30?'#ef4444':m.sd_io_pct>10?'#eab308':'#22c55e';
  grid.innerHTML=`
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(239,68,68,.12);color:#ef4444"><i class="bi bi-sd-card-fill"></i></div>
      <div class="sysinfo-chip-body"><span class="sysinfo-chip-label">MB scritti su SD (sessione)</span><span class="sysinfo-chip-value">${m.sd_write_mb.toFixed(1)} MB</span></div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(249,115,22,.12);color:#f97316"><i class="bi bi-graph-up-arrow"></i></div>
      <div class="sysinfo-chip-body"><span class="sysinfo-chip-label">Stima scritture giornaliere</span><span class="sysinfo-chip-value">${mbPerDay} MB/giorno</span></div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(34,197,94,.12);color:#22c55e"><i class="bi bi-piggy-bank-fill"></i></div>
      <div class="sysinfo-chip-body"><span class="sysinfo-chip-label">MB risparmiati vs baseline (4 GB/g)</span><span class="sysinfo-chip-value">${savedStr}</span></div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(56,189,248,.12);color:#38bdf8"><i class="bi bi-speedometer2"></i></div>
      <div class="sysinfo-chip-body"><span class="sysinfo-chip-label">Efficienza Log2Ram</span><span class="sysinfo-chip-value">${effStr}</span></div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(${ioColor==='#ef4444'?'239,68,68':ioColor==='#eab308'?'234,179,8':'34,197,94'},.12);color:${ioColor}"><i class="bi bi-activity"></i></div>
      <div class="sysinfo-chip-body"><span class="sysinfo-chip-label">I/O SD attuale</span><span class="sysinfo-chip-value" style="color:${ioColor}">${m.sd_io_pct.toFixed(1)}%</span></div>
    </div>
    <div class="sysinfo-chip">
      <div class="sysinfo-chip-icon" style="background:rgba(167,139,250,.12);color:#a78bfa"><i class="bi bi-stopwatch-fill"></i></div>
      <div class="sysinfo-chip-body"><span class="sysinfo-chip-label">Uptime sessione</span><span class="sysinfo-chip-value">${uptimeH} ore</span></div>
    </div>`;
}

function drawL2RChart(){
  const c=document.getElementById('l2rCanvas');
  if(!c)return;
  const ctx=c.getContext('2d');
  const W=c.width=c.offsetWidth;const H=c.height=c.offsetHeight||220;
  ctx.clearRect(0,0,W,H);
  const pad={t:20,b:25,l:45,r:15};
  const gW=W-pad.l-pad.r;const gH=H-pad.t-pad.b;

  // griglia
  ctx.strokeStyle='#334155';ctx.lineWidth=0.5;
  for(let i=0;i<=4;i++){
    const y=pad.t+gH*(i/4);
    ctx.beginPath();ctx.moveTo(pad.l,y);ctx.lineTo(W-pad.r,y);ctx.stroke();
    ctx.fillStyle='#64748b';ctx.font='10px sans-serif';ctx.textAlign='right';
    ctx.fillText((100-i*25)+'%',pad.l-6,y+3);
  }

  const maxSd=Math.max(1,...(l2rHist.sdWrites.length?l2rHist.sdWrites:[1]));
  const maxRam=Math.max(1,...(l2rHist.ramSize.length?l2rHist.ramSize:[1]));

  function drawL2RLine(arr,color,norm){
    if(!arr.length)return;
    ctx.beginPath();
    arr.forEach((v,i)=>{
      const x=pad.l+gW*(i/(l2rHistMax-1));
      const y=pad.t+gH*(1-Math.min(v/norm,1));
      i===0?ctx.moveTo(x,y):ctx.lineTo(x,y);
    });
    ctx.strokeStyle=color;ctx.lineWidth=2.5;ctx.stroke();
    const lx=pad.l+gW*((arr.length-1)/(l2rHistMax-1));
    const ly=pad.t+gH*(1-Math.min(arr[arr.length-1]/norm,1));
    ctx.lineTo(lx,pad.t+gH);ctx.lineTo(pad.l,pad.t+gH);ctx.closePath();
    ctx.fillStyle=color+'33';ctx.fill();
    ctx.beginPath();ctx.arc(lx,ly,3.5,0,Math.PI*2);ctx.fillStyle=color;ctx.fill();
  }

  if(l2rShow.sdWrites)drawL2RLine(l2rHist.sdWrites,'#ef4444',maxSd);
  if(l2rShow.ramSize) drawL2RLine(l2rHist.ramSize,'#22c55e',maxRam);
  if(l2rShow.ioPct)   drawL2RLine(l2rHist.ioPct,'#eab308',100);

  // legenda
  const series=[
    {k:'sdWrites',c:'#ef4444',lbl:'SD Writes '+(l2rHist.sdWrites.length?l2rHist.sdWrites[l2rHist.sdWrites.length-1].toFixed(2):'—')+' MB/10s'},
    {k:'ramSize', c:'#22c55e',lbl:'RAM Log '+(l2rHist.ramSize.length?l2rHist.ramSize[l2rHist.ramSize.length-1].toFixed(1):'—')+' MB'},
    {k:'ioPct',   c:'#eab308',lbl:'I/O SD '+(l2rHist.ioPct.length?l2rHist.ioPct[l2rHist.ioPct.length-1].toFixed(1):'—')+'%'}
  ];
  let lx=pad.l;
  series.forEach(s=>{
    ctx.fillStyle=l2rShow[s.k]?s.c:'#475569';
    ctx.fillRect(lx,6,12,8);
    ctx.fillStyle='#94a3b8';ctx.font='10px sans-serif';ctx.textAlign='left';
    ctx.fillText(s.lbl,lx+15,14);
    lx+=ctx.measureText(s.lbl).width+30;
  });
}

function l2rToggle(key,btn){
  if(btn.disabled)return;
  l2rShow[key]=!l2rShow[key];
  btn.style.opacity=l2rShow[key]?'1':'0.4';
  drawL2RChart();
}

function renderL2RFiles(files){
  const tbody=document.getElementById('l2rFilesBody');
  if(!tbody)return;
  if(!files||!files.length){
    tbody.innerHTML='<tr><td colspan="3" style="padding:10px 8px;color:var(--dim);text-align:center">Nessun file</td></tr>';
    return;
  }
  tbody.innerHTML=files.map(f=>`
    <tr style="border-bottom:1px solid var(--border)">
      <td style="padding:5px 8px;font-family:monospace;font-size:12px">${f.name}</td>
      <td style="padding:5px 8px;text-align:right;white-space:nowrap">${l2rFmtBytes(f.size)}</td>
      <td style="padding:5px 8px;text-align:right;white-space:nowrap;color:var(--dim)">${f.modified}</td>
    </tr>`).join('');
}

function l2rFmtBytes(b){
  if(b<1024)return b+' B';
  if(b<1048576)return (b/1024).toFixed(1)+' KB';
  return (b/1048576).toFixed(1)+' MB';
}

function l2rFilesToggle(hdr){
  const wrap=document.getElementById('l2rFilesWrap');
  const chev=document.getElementById('l2rFilesChevron');
  const open=wrap.style.display==='none';
  wrap.style.display=open?'block':'none';
  chev.style.transform=open?'rotate(180deg)':'';
}

function log2ramSaveSize(){
  const inp=document.getElementById('l2rSizeInput');
  const mb=parseInt(inp?inp.value:0,10);
  if(!mb||mb<64||mb>512){toastCenter('Valore non valido (64–512 MB)',false);return;}
  fetch('/panel/api/log2ram/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({size_mb:mb})})
    .then(r=>r.json())
    .then(d=>{
      if(d.ok){toastCenter('SIZE aggiornato a '+mb+'M — riavvio necessario per applicare',true);}
      else{toastCenter('Errore: '+(d.error||'?'),false);}
    }).catch(()=>toastCenter('Errore di rete',false));
}

function log2ramSync(){
  fetch('/panel/api/log2ram/sync',{method:'POST'})
    .then(r=>r.json())
    .then(d=>toastCenter(d.ok?'Sync completata':'Errore: '+(d.error||'?'),d.ok))
    .catch(e=>toastCenter('Errore: '+e,false));
}

function log2ramRestart(){
  confirmModal('Restart Log2Ram','Riavviare il servizio log2ram?','warn','danger','Riavvia').then(ok=>{
    if(!ok)return;
    fetch('/panel/api/log2ram/restart',{method:'POST'})
      .then(r=>r.json())
      .then(d=>toastCenter(d.ok?'log2ram riavviato':'Errore: '+(d.error||'?'),d.ok))
      .catch(e=>toastCenter('Errore: '+e,false));
  });
}

function log2ramInstall(){
  const body=`<div style="line-height:1.7">
    Verranno eseguite le seguenti operazioni:<br>
    <ol style="margin:8px 0 0 16px;padding:0">
      <li>journald → modalità volatile</li>
      <li>Rimozione journal SD (~125 MB)</li>
      <li>Installazione repo azlux + log2ram</li>
      <li>Configurazione <code>/etc/log2ram.conf</code></li>
    </ol>
    <div style="margin-top:10px;color:var(--dim);font-size:12px">Riavvio necessario al termine.</div>
  </div>`;
  confirmModal('Installa Log2Ram',body,'warn','primary','Installa').then(ok=>{
    if(!ok)return;
    const btn=document.getElementById('l2rBtnInstall');
    if(btn){btn.disabled=true;btn.innerHTML='<span style="opacity:.7">Installazione in corso...</span>';}
    fetch('/panel/api/log2ram/install',{method:'POST'})
      .then(r=>r.json())
      .then(d=>{
        if(!d.ok){
          if(btn){btn.disabled=false;btn.innerHTML='<i class="bi bi-download"></i> Installa Log2Ram';}
          toastCenter('Errore: '+(d.error||'?'),false);
          return;
        }
        // job avviato in background — poll ogni 2s
        const poll=setInterval(()=>{
          fetch('/panel/api/log2ram/install/status')
            .then(r=>r.json())
            .then(s=>{
              if(!s.done)return;
              clearInterval(poll);
              if(btn){btn.disabled=false;btn.innerHTML='<i class="bi bi-download"></i> Installa Log2Ram';}
              if(s.success){
                toastCenter('Log2Ram installato! Riavvia il sistema per attivarlo.',true);
                setTimeout(loadLog2Ram,1000);
              }else{
                toastCenter('Errore installazione. Controlla i log.',false);
              }
            })
            .catch(()=>{});
        },2000);
      })
      .catch(()=>{
        if(btn){btn.disabled=false;btn.innerHTML='<i class="bi bi-download"></i> Installa Log2Ram';}
        toastCenter('Errore di rete durante installazione',false);
      });
  });
}

// --- ESP32-S3 ---
let esp32PollTimer=null;

function startESP32Poll(){
  pollESP32();
  if(!esp32PollTimer)esp32PollTimer=setInterval(pollESP32,2000);
}
function stopESP32Poll(){
  if(esp32PollTimer){clearInterval(esp32PollTimer);esp32PollTimer=null;}
}

function pollESP32(){
  fetch('/panel/api/esp32/status').then(r=>r.json()).then(d=>{
    const dot=document.getElementById('esp32Dot');
    const lbl=document.getElementById('esp32Status');
    if(dot&&lbl){
      dot.style.background=d.connected?'#22c55e':'#ef4444';
      lbl.style.color=d.connected?'#22c55e':'#ef4444';
      lbl.textContent=d.connected?'Connesso':'Disconnesso';
    }
    // mostra i log card solo se connesso
    const logDisplay=d.connected?'block':'none';
    ['esp32LogCard','nfcLogCard'].forEach(id=>{
      const el=document.getElementById(id);
      if(el)el.style.display=logDisplay;
    });
    // disabilita i controlli se non connesso
    const fan=document.getElementById('fanSlider');
    const fanBtn=document.querySelector('button[onclick="esp32FanSet()"]');
    const door=document.getElementById('btnDoor');
    [fan,fanBtn,door].forEach(el=>{if(el){el.disabled=!d.connected;el.style.opacity=d.connected?'1':'0.4';}});
    const pins=d.pins||{};
    const ringFlash=d.ring_flash||{};
    const now=Date.now();
    function setLed(id,pin){
      const el=document.getElementById(id);
      if(!el)return;
      const pressed=pins[pin]===0; // active-low: 0 = premuto
      const ringing=ringFlash[pin]&&(now-ringFlash[pin])<3000;
      el.style.background=(pressed||ringing)?'#22c55e':'var(--dim)';
      el.style.boxShadow=(pressed||ringing)?'0 0 8px #22c55e':'none';
    }
    setLed('ledP1','p1');
    setLed('ledP2','p2');
    setLed('ledP3','p3');
    setTabletSwitch(d.tablet_on||false);
    const fanPct=d.fan_pct||0;
    const slider=document.getElementById('fanSlider');
    const fanVal=document.getElementById('fanVal');
    if(slider&&fanVal&&parseInt(slider.value,10)!==fanPct){
      slider.value=fanPct;
      fanVal.textContent=fanPct+'%';
    }
    const usbLines=d.usb_log||[];
    const area=document.getElementById('esp32CardLog');
    if(area){
      if(usbLines.length>0){
        area.value=usbLines.slice().reverse().join('\n');
      }
    }
    const floors=d.floors||{};
    ['P1','P2','P3'].forEach(p=>{
      const slots=floors[p.toLowerCase()]||[];
      [1,2,3,4].forEach((s,si)=>{
        const inp=document.getElementById('floor'+p+'-'+s);
        if(inp&&document.activeElement!==inp)inp.value=slots[si]||'';
      });
    });
    if(d.connected)pollKeyStatus();
  }).catch(()=>{});
}

function esp32FanSet(){
  const slider=document.getElementById('fanSlider');
  if(!slider)return;
  const duty=parseInt(slider.value,10);
  fetch('/panel/api/esp32/fan',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'duty='+duty})
    .then(r=>r.json())
    .then(d=>toastCenter(d.ok?'Ventola impostata a '+duty+'%':(d.error||'Errore invio comando'),d.ok))
    .catch(()=>toastCenter('Errore di rete',false));
}

function setTabletSwitch(on){
  const track=document.getElementById('tabletToggleTrack');
  const thumb=document.getElementById('tabletToggleThumb');
  const lbl=document.getElementById('tabletToggleLabel');
  if(!track||!thumb||!lbl)return;
  track.style.background=on?'#22c55e':'var(--dim)';
  thumb.style.left=on?'22px':'2px';
  lbl.textContent=on?'Acceso':'Spento';
  lbl.style.color=on?'#22c55e':'var(--dim)';
}

function esp32TabletToggle(){
  const track=document.getElementById('tabletToggleTrack');
  if(!track)return;
  const currentlyOn=track.style.background==='rgb(34, 197, 94)';
  const newState=currentlyOn?'off':'on';
  fetch('/panel/api/esp32/tablet',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:'state='+newState})
    .then(r=>r.json())
    .then(d=>{
      if(d.ok){
        setTabletSwitch(newState==='on');
        toastCenter('Tablet '+(newState==='on'?'acceso':'spento'),true);
      } else {
        toastCenter(d.error||'Errore invio comando',false);
      }
    })
    .catch(()=>toastCenter('Errore di rete',false));
}

function esp32ClearCardLog(){
  fetch('/panel/api/esp32/usblog',{method:'POST'})
    .then(r=>r.json())
    .then(d=>{
      const area=document.getElementById('esp32CardLog');
      if(area)area.value='';
      toastCenter('Log USB svuotato',true);
    })
    .catch(()=>toastCenter('Errore di rete',false));
}

function esp32FloorSet(floor){
  const params=new URLSearchParams({floor});
  [1,2,3,4].forEach(s=>{
    const inp=document.getElementById('floor'+floor+'-'+s);
    params.set('t'+s, inp?inp.value.slice(0,20):'');
  });
  fetch('/panel/api/esp32/floors',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:params.toString()})
    .then(r=>r.json())
    .then(d=>toastCenter(d.ok?'Piano '+floor+' aggiornato':(d.error||'Errore invio comando'),d.ok))
    .catch(()=>toastCenter('Errore di rete',false));
}

function esp32Door(){
  const btn=document.getElementById('btnDoor');
  if(!btn||btn.disabled)return;
  // effetto fisico: premi giù, tieni 180ms, rilascia
  btn.classList.add('pressing');
  btn.disabled=true;
  setTimeout(()=>{
    btn.classList.remove('pressing');
    setTimeout(()=>{btn.disabled=false;},600);
  },180);
  fetch('/panel/api/esp32/door',{method:'POST'})
    .then(r=>r.json())
    .then(d=>toastCenter(d.ok?'Portone aperto':(d.error||'Errore invio comando'),d.ok))
    .catch(()=>toastCenter('Errore di rete',false));
}

function updateKeyUI(d){
  const dot=document.getElementById('keyStatusDot');
  const lbl=document.getElementById('keyStatusLabel');
  const fp=document.getElementById('keyFP');
  const btnGen=document.getElementById('btnKeyGen');
  const btnForce=document.getElementById('btnKeyGenForce');
  const btnForceHint=document.getElementById('btnKeyGenForceHint');
  const banner=document.getElementById('keyReEnrollBanner');
  if(!dot)return;
  if(d.present){
    dot.style.background='var(--green)';
    lbl.textContent='Presente';
    lbl.style.color='var(--green)';
    fp.textContent=(d.fp||'—').toUpperCase();
    if(btnGen)btnGen.style.display='none';
    if(btnForce)btnForce.style.display='';
    if(btnForceHint)btnForceHint.style.display='';
  } else {
    dot.style.background='var(--red)';
    lbl.textContent='Assente';
    lbl.style.color='var(--red)';
    fp.textContent='—';
    if(btnGen)btnGen.style.display='';
    if(btnForce)btnForce.style.display='none';
    if(btnForceHint)btnForceHint.style.display='none';
  }
  if(banner)banner.style.display=d.re_enroll_needed?'flex':'none';
}

function pollKeyStatus(){
  fetch('/panel/api/esp32/key-status')
    .then(r=>r.json())
    .then(d=>{if(d.ok)updateKeyUI(d);})
    .catch(()=>{});
}

function esp32KeyGen(force){
  const doGen=()=>{
    fetch('/panel/api/esp32/key-gen',{
      method:'POST',
      headers:{'Content-Type':'application/x-www-form-urlencoded'},
      body:'force='+(force?'true':'false')
    })
    .then(r=>r.json())
    .then(d=>{
      if(d.ok){
        toastCenter('Chiave '+(force?'rigenerata':'generata')+' — FP: '+(d.fp||'').toUpperCase(),true);
        pollKeyStatus();
      } else {
        toastCenter(d.error||'Errore generazione chiave',false);
      }
    })
    .catch(()=>toastCenter('Errore di rete',false));
  };
  if(force){
    confirmModal(
      'Rigenera chiave AES',
      'Questa operazione genera una nuova chiave e <strong>invalida tutte le tessere</strong> già registrate. Dovrai re-enrollare ogni tessera.',
      'danger','danger','Rigenera'
    ).then(ok=>{if(ok)doGen();}).catch(()=>{});
  } else {
    doGen();
  }
}

function esp32KeyGenForce(){esp32KeyGen(true);}

function esp32KeyAckReEnroll(){
  confirmModal(
    'Conferma re-enroll completato',
    'Hai ri-enrollato <strong>tutte</strong> le tessere con la nuova chiave?',
    'warn','primary','Sì, conferma'
  ).then(ok=>{
    if(!ok)return;
    fetch('/panel/api/esp32/key-reenroll-ack',{method:'POST'})
      .then(r=>r.json())
      .then(d=>{
        if(d.ok){
          toastCenter('Re-enroll confermato',true);
          pollKeyStatus();
        } else {
          toastCenter(d.error||'Errore',false);
        }
      })
      .catch(()=>toastCenter('Errore di rete',false));
  }).catch(()=>{});
}

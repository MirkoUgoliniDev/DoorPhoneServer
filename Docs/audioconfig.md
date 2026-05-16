# Audio Configuration — DoorPhoneServer (Doorpi)

## Hardware

| Componente | Dettaglio |
|---|---|
| Scheda audio | C-Media USB Headphone Set (USB Audio) |
| Vendor/Product | `0d8c:000c` C-Media Electronics, Inc. |
| USB bus | Bus 001 Device 003, full-speed, usb-0000:01:00.0-1.2 |
| ALSA card | **card 1** (`Set`) — unica scheda audio presente |
| ALSA device | `hw:1,0` / `plughw:1,0` |
| Card 0 | Non esiste (BCM2835 headphones/HDMI disabilitati nel kernel) |
| Speaker | Collegato all'uscita jack della scheda USB |

### Kernel parameters (audio-related)
```
snd_bcm2835.enable_headphones=0
snd_bcm2835.enable_hdmi=0
```

## ALSA — `/etc/asound.conf`

```
defaults.pcm.card 1
defaults.ctl.card 1

pcm.dmixer {
    type dmix
    ipc_key 1024
    ipc_perm 0666
    slave {
        pcm { type hw; card 1; device 0 }
        rate 48000
        channels 2
    }
}

pcm.dsnooper {
    type dsnoop
    ipc_key 1025
    ipc_perm 0666
    slave {
        pcm { type hw; card 1; device 0 }
        rate 48000
        channels 1
    }
}

pcm.!default {
    type asym
    playback.pcm "plug:dmixer"
    capture.pcm "plug:dsnooper"
}

ctl.!default {
    type hw
    card 1
}
```

**Note:**
- `dmix` permette playback condiviso (più processi contemporaneamente)
- `dsnoop` permette capture condiviso
- **OpenAL bypassa dmix** aprendo il device hw direttamente (esclusivo)
- Nessun `~/.asoundrc` presente

## OpenAL — `/etc/openal/alsoft.conf`

- Tutte le opzioni commentate (valori di default)
- OpenAL usa ALSA backend, device `default` → risolto via `asound.conf` a card 1
- `openal.OpenDevice("")` in `stream.go` apre il device di default

## Mixer Controls (card 1)

| Control | Tipo | Range | Stato normale |
|---|---|---|---|
| `Headphone` | pvolume + pswitch | 0–151 | 151 [100%] [on] |
| `Mic` | pvolume + cvolume + pswitch + cswitch | Playback 0–32, Capture 0–16 | Playback [off], Capture [on] |
| `Auto Gain Control` | pswitch | on/off | [on] |

### Comandi mixer frequenti
```bash
# Stato mixer
amixer -c 1 scontents

# Volume headphone
amixer -c 1 sset 'Headphone' 100%

# Mute/unmute headphone
amixer -c 1 sset 'Headphone' mute
amixer -c 1 sset 'Headphone' unmute

# Volume/mute mic
amixer -c 1 sset 'Mic' 90%
amixer -c 1 sset 'Mic' capture on
```

## Catena Audio in DoorPhoneServer

### Voice playback (Mumble → speaker)
```
Mumble server → gumble → OnAudioStream() → OpenAL source.Play()
    → OpenAL default device ("") → ALSA hw:1,0 (esclusivo, bypassa dmix)
```

### Voice capture (mic → Mumble)
```
ALSA hw:1,0 → OpenAL CaptureOpenDevice("") → gumble → Mumble server
```

### Sound events (ring, TTS, ecc.)
```
localMediaPlayer() → ffplay → ALSA default → dmix → hw:1,0
```
**Nota:** ffplay usa il device ALSA default (che passa per dmix), ma se OpenAL
tiene il device aperto in modo esclusivo, ffplay non può accedere a hw:1,0.
In `handleSoundPlayPi()` (webpanel.go) si usa `amixer -c 1` (hardcoded card 1).

### Volume control (volume-go library)
```
volume.GetVolume("Headphone")  → amixer -M get Headphone
volume.SetVolume("Headphone")  → amixer set Headphone NN%
volume.Mute("Headphone")      → amixer set Headphone mute
volume.Unmute("Headphone")    → amixer set Headphone unmute
```
**Nota:** `volume-go` non usa flag `-c`, si affida a `defaults.ctl.card 1` in asound.conf.

## XML Config (doorphoneserver.xml)

```xml
<outputdevice>Headphone</outputdevice>
<outputvolcontroldevice>Headphone</outputvolcontroldevice>
<outputmutecontroldevice>Headphone</outputmutecontroldevice>
<simplexwithmute>true</simplexwithmute>
```

- `OutputDevice`: nome del mixer ALSA per mute/unmute
- `SimplexWithMute=true`: muta speaker durante trasmissione (TX), unmute al rilascio

## Flusso Mute/Unmute

### All'avvio (`ClientStart` in client.go)
1. `volume.Unmute("Headphone")` → speaker unmuted
2. `Connect()` → connessione al server Mumble
3. `OpenStream()` → OpenAL apre device hw:1,0

### Simplex mode (durante TX)
- `TransmitStart()` → `volume.Mute("Headphone")` (speaker off durante TX)
- `TransmitStop()` → `volume.Unmute("Headphone")` (speaker on dopo TX)

### GPIO buttons (P1/P2/P3)
- Pressione → `cmdMuteUnmute("unmute")` → play ring → send message

### Comandi remoti via Mumble text message
- `cmd-accept-call` → `cmdStartTransmitting()` + `cmdMuteUnmute("unmute")`
- `cmd-close-call` → `cmdMuteUnmute("mute")` + 200ms + `cmdMuteUnmute("unmute")`
- `cmd-unlock` → `cmdUnlockDoor()` + `cmdMuteUnmute("mute")` + 200ms + `cmdMuteUnmute("unmute")`

## Problemi Noti e Fix

### Speaker muted permanentemente (fix: ba9d0e8)
**Causa:** `cmd-close-call` da piano1/piano2/piano3 chiamava `cmdMuteUnmute("mute")`
senza mai fare unmute. 3 messaggi in rapida successione → speaker permanentemente off.
**Fix:** Aggiunto `time.Sleep(200ms)` + `cmdMuteUnmute("unmute")` dopo ogni mute
in `close-call` e `unlock` (custom_func.go).

### ALSA card errata in handleSoundPlayPi (fix: c127c5d)
**Causa:** `amixer -c 0` usato per unmute, ma card 0 non esiste.
**Fix:** Cambiato a `amixer -c 1`.

### Headphone Playback Switch off
**Causa:** Il mixer `Headphone` ha un playback switch separato dal volume.
Se `[off]`, nessun suono anche con volume al 100%.
**Verifica:** `amixer -c 1 sget 'Headphone'` → deve mostrare `[on]`.

### OpenAL blocca il device esclusivamente
OpenAL apre `hw:1,0` in modo esclusivo. Nessun altro processo (aplay, ffplay)
può accedere al device mentre doorphoneserver è in esecuzione, a meno che non passi
per dmix. Ma dmix richiede che anche OpenAL usi dmix (non lo fa).

## Test Rapidi

```bash
# Verifica scheda visibile
aplay -l

# Verifica mixer
amixer -c 1 sget 'Headphone'

# Test audio diretto (con doorphoneserver fermo!)
sudo systemctl stop doorphoneserver
speaker-test -D plughw:1,0 -t sine -f 1000 -c 1 -l 1
aplay -D plughw:1,0 soundfiles/events/door_ring.wav

# Test con doorphoneserver attivo (deve passare per default/dmix)
ffplay soundfiles/events/door_ring.wav -autoexit -nodisp -volume 100

# Stato PCM (verifica corruzione)
cat /proc/asound/card1/pcm0p/sub0/status

# Chi usa il device audio
sudo lsof /dev/snd/*
```

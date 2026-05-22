# Audio — Fix e note tecniche

## Schede testate

| Scheda | Playback | Capture | Note |
|--------|----------|---------|------|
| Waveshare USB to Audio (CM108) | 2ch stereo | 2ch stereo | `USB PnP Audio Device` |
| C-Media USB PnP Sound Device   | 2ch stereo | 1ch mono   | `USB PnP Sound Device` |

---

## Problema 1 — Channels count non available (arecord)

**Errore:** `arecord: set_params:1398: Channels count non available`

**Causa:** il codice usava `-c 1` hardcoded, ma entrambe le schede testate accettano
solo S16_LE (il chip CM108 richiede stereo anche in capture).

**Fix:** `get_card_channels(card, dev, stream)` legge i canali da `/proc/asound/cardN/streamM`
senza aprire il device. Viene chiamata prima di ogni `arecord`/`aplay`.

```python
# setup/lib/audio_utils.py
def get_card_channels(card, dev=0, stream="capture") -> int:
    # Legge /proc/asound/cardN/stream0..3, sezione "Capture:" o "Playback:"
    # Ritorna 1 come fallback sicuro
```

**Attenzione:** NON usare `arecord --dump-hw-params /dev/null` come fallback —
`/dev/null` è il file di OUTPUT della registrazione, arecord registra all'infinito
e tiene il device occupato.

---

## Problema 2 — Device or resource busy (rec_start)

**Errore:** `arecord: main:850: audio open error: Device or resource busy`

**Causa:** race condition tra `_vu_worker` e `rec_start`:
1. `rec_start` setta `_preparing_rec = True` e killa `_vu_proc`
2. Il worker è nel sleep di 0.15s prima del `Popen` (già passato il check `_preparing_rec`)
3. Il worker si sveglia e avvia un nuovo `arecord` PRIMA che `rec_start` parta
4. `rec_start` trova il device occupato

**Fix:** aggiunto check `_preparing_rec` **dopo** il sleep nel worker, subito prima del `Popen`:

```python
# setup/webui.py — _vu_worker()
_t.sleep(0.15)
if _preparing_rec:   # ricontrollo dopo il sleep
    continue
proc = subprocess.Popen(["arecord", ...])
```

---

## Problema 3 — VU meter mono/stereo dinamico

**Comportamento atteso:**
- Scheda stereo (es. CM108): VU Speaker **L+R**, VU Mic **L+R**
- Scheda mista (es. C-Media): VU Speaker **L+R** (playback stereo), VU Mic **singola barra** (capture mono)
- Scheda mono: entrambi **singola barra**

**Come funziona:**
- `/audio/refresh_cards` ritorna `channels` per ogni scheda
- `refreshCards()` JS chiama `_setVuStereoMode(micStereo, spkStereo)`
- La riga R (`vuRowR`, `playVuRowR`) viene mostrata/nascosta in base ai canali reali

**Nota hardware:** è normale che una scheda "mono" abbia playback stereo —
l'hardware USB audio economico ha spesso uscita FL+FR ma microfono MONO.

---

## Problema 4 — AGC non disponibile

**Errore:** `✗ AGC: undefined`

**Causa:** il chip CM108 non espone il controllo `Auto Gain Control` in ALSA.
`amixer sget "Auto Gain Control"` ritorna exit code 1.

**Fix:** `/audio/info` ritorna `agc: null` se il controllo non esiste.
Il toggle AGC nel modale viene nascosto (`display:none`) quando `agc === null`.

---

## asound.conf — canali dinamici

`generate_asound_conf()` chiama `get_card_channels()` per determinare i canali
di `dmix` (playback) e `dsnoop` (capture). Non più hardcoded a 1 o 2.

```
pcm.dsnooped {
    slave.pcm {
        channels N   ← rilevato da /proc/asound
    }
}
```

---

## Rilevamento canali — perché /proc/asound

`/proc/asound/cardN/streamM` è leggibile anche quando il device è già aperto
da un altro processo (es. il VU worker). Non richiede `open()` del device ALSA.

Struttura del file:
```
Playback:
  Interface 1
    Channels: 2       ← canali hardware reali
    Channel map: FL FR

Capture:
  Interface 2
    Channels: 1       ← MONO
    Channel map: MONO
```

# Sistema di Monitoraggio Canale Audio Bidirezionale

## Panoramica

Il sistema di monitoraggio del canale audio è stato implementato per verificare che quando c'è una chiamata (postino che suona P1/P2/P3), l'inquilino risponda e ci sia effettivamente un canale audio funzionante tra il client Android e il Raspberry Pi.

Il sistema include:
- **Monitoraggio in tempo reale** con logging dettagliato
- **Storico chiamate** con statistiche complete
- **Interfaccia web** con visualizzazione interattiva delle chiamate
- **Auto-refresh** ogni 10 secondi nel pannello web

## Funzionalità

### 1. Monitoraggio RX (Ricezione da Client Android)

Quando un client Android (inquilino) parla, il sistema monitora:

- **Client attivo**: Nome dell'utente che sta trasmettendo
- **Pacchetti ricevuti**: Conteggio dei pacchetti audio ricevuti
- **Bitrate**: Velocità di trasmissione in kbps
- **Livello audio**: Percentuale del volume audio (0-100%)
- **Durata**: Tempo totale della trasmissione

**Log di esempio:**
```
info: [AUDIO-RX] Sessione avviata - Client: Android_P1
info: [AUDIO-RX] Client: Android_P1 | Pacchetti: 50 | Bitrate: 48.2 kbps | Livello: 65% | Durata: 2s
info: [AUDIO-RX] Sessione terminata - Client: Android_P1 | Pacchetti: 150 | Bytes: 14400 | Bitrate: 48.0 kbps | Durata: 6s
```

### 2. Monitoraggio TX (Trasmissione verso Client Android)

Quando il Raspberry trasmette verso i client Android, il sistema monitora:

- **Pacchetti trasmessi**: Conteggio dei pacchetti audio inviati
- **Pacchetti dropped**: Pacchetti persi durante la trasmissione
- **Packet loss**: Percentuale di perdita pacchetti
- **Bitrate**: Velocità di trasmissione in kbps
- **Durata**: Tempo totale della trasmissione

**Log di esempio:**
```
info: [AUDIO-TX] Trasmissione avviata verso client Android
info: [AUDIO-TX] Pacchetti: 50 | Dropped: 0 (0.0%) | Bitrate: 48.0 kbps | Durata: 2s
info: [AUDIO-TX] Trasmissione terminata | Pacchetti: 200 | Bytes: 19200 | Dropped: 2 (1.0%) | Bitrate: 48.0 kbps | Durata: 8s
```

### 3. Riepilogo Canale Audio

Alla fine di ogni trasmissione, viene generato un riepilogo completo:

```
info: [AUDIO-CHANNEL] Status: DUPLEX (RX+TX)
info: [AUDIO-CHANNEL] RX: Client=Android_P1 Packets=150 Bitrate=48.0 kbps Level=65% Duration=6s
info: [AUDIO-CHANNEL] TX: Packets=200 Dropped=2 (1.0%) Bitrate=48.0 kbps Duration=8s
```

## Stati del Canale

Il canale audio può trovarsi in uno dei seguenti stati:

- **IDLE**: Nessuna attività audio
- **RX-ONLY**: Solo ricezione (client Android sta parlando)
- **TX-ONLY**: Solo trasmissione (Raspberry sta trasmettendo)
- **DUPLEX (RX+TX)**: Comunicazione bidirezionale attiva

## Ottimizzazioni Prestazioni

Il sistema è ottimizzato per non appesantire il Raspberry:

1. **Logging periodico**: Log ogni 50 pacchetti invece che ad ogni pacchetto
2. **Operazioni leggere**: Solo incrementi di contatori e timestamp
3. **Lock brevi**: Mutex utilizzati solo per aggiornamenti rapidi
4. **Calcoli semplici**: Media mobile con formula leggera

## API HTTP

È disponibile un endpoint HTTP per verificare lo stato del canale audio in tempo reale:

```bash
curl "http://raspberry-ip:8080/?command=audiochannelstatus"
```

Questo comando logga lo stato corrente del canale audio con tutte le statistiche disponibili.

## Integrazione nel Flusso di Lavoro

### Scenario: Postino suona al piano P1

1. **Postino preme il pulsante P1**
   - Sistema suona il campanello
   - Log: `info: Ring Piano 1 activated`

2. **Inquilino risponde dal client Android**
   - Sistema avvia monitoraggio RX
   - Log: `info: [AUDIO-RX] Sessione avviata - Client: Android_P1`
   - Log periodico ogni 50 pacchetti con statistiche

3. **Raspberry trasmette verso l'inquilino**
   - Sistema avvia monitoraggio TX
   - Log: `info: [AUDIO-TX] Trasmissione avviata verso client Android`
   - Log periodico ogni 50 pacchetti con statistiche

4. **Fine conversazione**
   - Log: `info: [AUDIO-RX] Sessione terminata - Client: Android_P1 | ...statistiche...`
   - Log: `info: [AUDIO-TX] Trasmissione terminata | ...statistiche...`
   - Log: `info: [AUDIO-CHANNEL] Status: IDLE`

## Verifica Funzionalità

Per verificare che il canale audio sia funzionante:

1. **Controllare i log durante una chiamata**:
   ```bash
   tail -f /var/log/doorphoneserver/doorphoneserver.log | grep AUDIO
   ```

2. **Verificare presenza di entrambi RX e TX**:
   - Se vedi solo `[AUDIO-RX]`: il Raspberry riceve ma non trasmette
   - Se vedi solo `[AUDIO-TX]`: il Raspberry trasmette ma non riceve
   - Se vedi entrambi: canale bidirezionale funzionante ✓

3. **Controllare packet loss**:
   - Packet loss < 5%: Ottimo
   - Packet loss 5-10%: Accettabile
   - Packet loss > 10%: Problemi di rete

4. **Verificare bitrate**:
   - Bitrate normale: ~48 kbps (codec Opus)
   - Bitrate troppo basso: possibili problemi di banda

## File Modificati

- **stream.go**: Aggiunta struttura `AudioChannelStats` e integrazione nel flusso audio
- **clientcommands.go**: Logging stato canale in `TransmitStart` e `TransmitStop`
- **httpapi.go**: Nuovo comando API `audiochannelstatus`

## Esempio di Log Completo di una Chiamata

```
info: Ring Piano 1 activated
info: [AUDIO-RX] Sessione avviata - Client: Android_P1
info: [AUDIO-TX] Trasmissione avviata verso client Android
info: [AUDIO-CHANNEL] Trasmissione avviata - Status: TX-ONLY
info: [AUDIO-RX] Client: Android_P1 | Pacchetti: 50 | Bitrate: 48.2 kbps | Livello: 65% | Durata: 2s
info: [AUDIO-TX] Pacchetti: 50 | Dropped: 0 (0.0%) | Bitrate: 48.0 kbps | Durata: 2s
info: [AUDIO-RX] Client: Android_P1 | Pacchetti: 100 | Bitrate: 48.1 kbps | Livello: 68% | Durata: 4s
info: [AUDIO-TX] Pacchetti: 100 | Dropped: 1 (1.0%) | Bitrate: 48.0 kbps | Durata: 4s
info: [AUDIO-RX] Sessione terminata - Client: Android_P1 | Pacchetti: 150 | Bytes: 14400 | Bitrate: 48.0 kbps | Durata: 6s
info: [AUDIO-TX] Trasmissione terminata | Pacchetti: 200 | Bytes: 19200 | Dropped: 2 (1.0%) | Bitrate: 48.0 kbps | Durata: 8s
info: [AUDIO-CHANNEL] Status: IDLE
info: [AUDIO-CHANNEL] RX: Client=Android_P1 Packets=150 Bitrate=48.0 kbps Level=68% Duration=6s
info: [AUDIO-CHANNEL] TX: Packets=200 Dropped=2 (1.0%) Bitrate=48.0 kbps Duration=8s
```

## Conclusione

Questo sistema fornisce visibilità completa sul canale audio bidirezionale, permettendo di verificare in tempo reale che la comunicazione tra il postino (tramite Raspberry) e l'inquilino (tramite client Android) sia effettivamente funzionante.

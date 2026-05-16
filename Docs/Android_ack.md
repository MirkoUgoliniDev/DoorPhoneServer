# Implementazione ACK lato Android

## Contesto

Il Raspberry Pi (doorphoneserver) ora risponde con messaggi di ACK dopo aver elaborato
i comandi `cmd-accept-call` e `cmd-close-call` inviati dall'app Android.

Senza gestione degli ACK lato Android, l'app continua a re-inviare i comandi
dopo un timeout, generando duplicati che il Raspberry ignora con un warning.

---

## Protocollo attuale (prima delle modifiche Android)

```
Android                          Raspberry
   |                                 |
   |──── cmd-accept-call ──────────→ |  elabora, avvia TX
   |                                 |──── cmd-ack-accept-call ──→ (ignorato dall'app)
   |                                 |
   |──── cmd-close-call ───────────→ |  elabora, ferma TX
   |                                 |──── cmd-ack-close-call ───→ (ignorato dall'app)
   |                                 |
   | [timeout ~14s]                  |
   |──── cmd-close-call ───────────→ |  IGNORATO (nessuna sessione attiva)
```

## Protocollo target (dopo le modifiche Android)

```
Android                          Raspberry
   |                                 |
   |──── cmd-accept-call ──────────→ |  elabora, avvia TX
   |   [attende ACK, max 5s]         |
   |←─── cmd-ack-accept-call ──────  |
   |   [cancella retry timer]        |
   |                                 |
   |──── cmd-close-call ───────────→ |  elabora, ferma TX
   |   [attende ACK, max 5s]         |
   |←─── cmd-ack-close-call ───────  |
   |   [cancella retry timer]        |
```

---

## Messaggi Mumble coinvolti

Tutti i messaggi viaggiano come **Mumble Text Message** diretti all'utente
(non broadcast al canale). Il prefisso `cmd-` è aggiunto automaticamente
da `SendMessageToUser()` lato Raspberry.

| Direzione          | Messaggio Mumble        | Significato                        |
|--------------------|-------------------------|------------------------------------|
| Android → Raspberry | `cmd-accept-call`      | Utente ha accettato la chiamata    |
| Android → Raspberry | `cmd-close-call`       | Utente ha chiuso la chiamata       |
| Raspberry → Android | `cmd-ack-accept-call`  | TX avviato, sessione audio attiva  |
| Raspberry → Android | `cmd-ack-close-call`   | TX fermato, sessione chiusa        |

---

## Cosa implementare lato Android

### 1. Ricezione messaggi di testo Mumble

Verificare che il listener dei messaggi di testo Mumble sia attivo e che
intercetti i messaggi diretti all'utente (non solo i messaggi di canale).

Il messaggio ricevuto avrà il formato esatto:
```
cmd-ack-accept-call
cmd-ack-close-call
```

Nel handler dei messaggi in arrivo aggiungere:

```kotlin
fun onTextMessageReceived(message: String) {
    when (message.trim()) {
        "cmd-ack-accept-call" -> handleAckAcceptCall()
        "cmd-ack-close-call"  -> handleAckCloseCall()
    }
}
```

---

### 2. Gestione ACK per accept-call

#### Comportamento attuale (da modificare):
- L'app invia `cmd-accept-call`
- NON aspetta conferma
- Potrebbe re-inviare per sicurezza

#### Comportamento target:

```kotlin
// Variabili di stato
private var acceptCallAckReceived = false
private var acceptCallRetryJob: Job? = null

fun sendAcceptCall() {
    acceptCallAckReceived = false
    sendMumbleMessage("cmd-accept-call")

    // Avvia timer di retry
    acceptCallRetryJob = scope.launch {
        delay(5000) // aspetta 5 secondi
        if (!acceptCallAckReceived) {
            // ACK non ricevuto: logga l'errore, NON re-inviare il comando
            // (il Raspberry potrebbe aver già elaborato e il retry causerebbe problemi)
            Log.w("CALL", "ACK accept-call non ricevuto entro 5s")
            // Opzionale: mostrare UI warning
        }
    }
}

fun handleAckAcceptCall() {
    acceptCallAckReceived = true
    acceptCallRetryJob?.cancel()
    acceptCallRetryJob = null
    Log.i("CALL", "ACK accept-call ricevuto - sessione confermata")
    // Opzionale: aggiornare UI per confermare che l'audio è attivo
}
```

---

### 3. Gestione ACK per close-call

Questo è il caso più critico perché genera i duplicati osservati nei log.

#### Comportamento attuale (da modificare):
- L'app invia `cmd-close-call`
- Dopo ~14 secondi re-invia `cmd-close-call` (presumibilmente timeout interno)

#### Comportamento target:

```kotlin
// Variabili di stato
private var closeCallAckReceived = false
private var closeCallRetryJob: Job? = null

fun sendCloseCall() {
    closeCallAckReceived = false
    sendMumbleMessage("cmd-close-call")

    // Avvia timer di attesa ACK
    closeCallRetryJob = scope.launch {
        delay(5000) // aspetta 5 secondi
        if (!closeCallAckReceived) {
            Log.w("CALL", "ACK close-call non ricevuto entro 5s - la chiamata potrebbe essere già chiusa")
            // NON re-inviare: il Raspberry ignora i duplicati quando non c'è sessione attiva
            // Considerare: chiudere la UI della chiamata comunque lato Android
            forceCloseCallUI()
        }
    }
}

fun handleAckCloseCall() {
    closeCallAckReceived = true
    closeCallRetryJob?.cancel()
    closeCallRetryJob = null
    Log.i("CALL", "ACK close-call ricevuto - sessione chiusa confermata")
    // Chiudere la UI della chiamata
    closeCallUI()
}
```

---

### 4. Reset dello stato alla disconnessione Mumble

Se la connessione Mumble cade mentre si aspetta un ACK, cancellare
tutti i timer pendenti e resettare lo stato:

```kotlin
fun onMumbleDisconnected() {
    acceptCallAckReceived = false
    closeCallAckReceived = false
    acceptCallRetryJob?.cancel()
    closeCallRetryJob?.cancel()
    acceptCallRetryJob = null
    closeCallRetryJob = null
}
```

---

### 5. Gestione del comando cmd-ring in arrivo

Verificare anche la corretta ricezione del `cmd-ring` dal Raspberry,
che viene inviato quando il postino preme il campanello.

Il messaggio ricevuto sarà:
```
cmd-ring
```

Questo non ha ACK — è fire-and-forget dal Raspberry. L'app deve:
1. Mostrare la notifica/schermata di chiamata in arrivo
2. Offrire i pulsanti "Accetta" (→ invia `cmd-accept-call`) e "Rifiuta" (→ invia `cmd-close-call`)

---

## Timeout consigliati

| Evento                     | Timeout attesa ACK | Azione se timeout         |
|----------------------------|--------------------|---------------------------|
| Attesa `ack-accept-call`   | 5 secondi          | Log warning, no retry     |
| Attesa `ack-close-call`    | 5 secondi          | Chiudi UI chiamata anyway |

**Non fare retry dei comandi** — il Raspberry ha la protezione `activeCallUser`
che ignora i duplicati fuori sessione. Un retry potrebbe interferire con una
sessione successiva se arriva troppo tardi.

---

## Verifica nei log Raspberry

Dopo l'implementazione, i log Raspberry NON dovranno più mostrare:

```
[ warn ] [CALL] Comando 'close-call' da 'p4' ignorato
[ warn ] [CALL]   Motivo: nessuna sessione attiva
```

Il flusso corretto sarà:
```
[info] CMD RECEIVED:close-call
[info] [CALL] Android client closed call - stopping TX
[info] [AUDIO-TX] Trasmissione terminata | ...
[info] [CALL] Call session closed
[info] [CALL] ACK sent to p4 (ack-close-call)
```
— e nient'altro da p4 per quella chiamata.

---

## File Raspberry di riferimento

| File | Funzione |
|------|----------|
| `custom_func.go` → `execute_command()` | Gestione comandi in arrivo e invio ACK |
| `clientcommands.go` → `SendMessageToUser()` | Invio messaggio Mumble diretto |
| `Docs/call_protocol.md` | Documentazione completa del protocollo |

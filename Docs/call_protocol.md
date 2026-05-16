# Protocollo Comandi Chiamata (Raspberry ↔ Android)

## Panoramica

La comunicazione tra il Raspberry Pi e i client Android avviene tramite messaggi di testo Mumble con prefisso `cmd-`. Il Raspberry riceve comandi dal tablet e risponde con messaggi di ACK per confermare l'avvenuta elaborazione.

---

## Flusso Completo di una Chiamata

```
Raspberry                          Android (es. P4)
    |                                    |
    |── cmd-ring ─────────────────────→  |   Raspberry avvia il ring
    |                                    |   (utente preme tasto ring)
    |                                    |
    |  ←──────────────── cmd-accept-call |   Utente accetta sul tablet
    |── cmd-ack-accept-call ──────────→  |   Raspberry conferma ricezione
    |                                    |
    |   [TX audio Raspberry → Android]   |
    |   [RX audio Android → Raspberry]   |
    |                                    |
    |  ←──────────────── cmd-close-call  |   Utente chiude sul tablet
    |── cmd-ack-close-call ───────────→  |   Raspberry conferma ricezione
    |                                    |
```

---

## Comandi (Android → Raspberry)

| Comando           | Descrizione                                      |
|-------------------|--------------------------------------------------|
| `cmd-ring`        | (non usato in ricezione) inviato dal Raspberry   |
| `cmd-accept-call` | L'utente Android ha accettato la chiamata        |
| `cmd-close-call`  | L'utente Android ha chiuso la chiamata           |
| `cmd-unlock`      | Richiesta sblocco porta                          |
| `cmd-temp`        | Richiesta temperatura CPU Raspberry              |

## ACK (Raspberry → Android)

| ACK                    | In risposta a      | Descrizione                              |
|------------------------|--------------------|------------------------------------------|
| `cmd-ack-accept-call`  | `cmd-accept-call`  | TX avviato, sessione audio attiva        |
| `cmd-ack-close-call`   | `cmd-close-call`   | TX fermato, sessione audio chiusa        |

---

## Motivazione degli ACK

Prima dell'introduzione degli ACK, il Raspberry elaborava i comandi senza rispondere. Questo causava un comportamento di retry indesiderato lato Android: non ricevendo conferma, il tablet reinviava `cmd-close-call` più volte (tipicamente dopo 4s e 25s), generando warning nei log:

```
[WARN] [CALL] Command 'close-call' from unauthorized user 'p4' (expected '') - ignored
```

Con gli ACK, il client Android può interrompere i retry non appena riceve la conferma.

---

## Autorizzazione Comandi

Solo l'utente che ha ricevuto il `cmd-ring` può inviare `cmd-accept-call` e `cmd-close-call`. Questo è garantito dalla variabile `activeCallUser` in `custom_func.go`:

- Viene impostata al momento dell'invio del ring (`activeCallUser = piano`)
- Viene azzerata dopo la chiusura della chiamata (`activeCallUser = ""`)
- Comandi da utenti non autorizzati vengono ignorati con un warning

---

## Implementazione

- **File:** `custom_func.go` — funzione `execute_command`
- **Invio ACK:** tramite `b.SendMessageToSession(senderSession, "ack-accept-call")` / `b.SendMessageToSession(senderSession, "ack-close-call")`
- **Prefisso automatico:** `SendMessageToSession` aggiunge `cmd-` al messaggio, quindi il tablet riceve `cmd-ack-accept-call` e `cmd-ack-close-call`
- **Targeting:** gli ACK vengono inviati direttamente alla **session ID** del mittente (non per nome utente), garantendo la consegna indipendentemente dal canale
- **Ordine `close-call`:** l'ACK viene inviato **prima** di chiudere la sessione TX, in modo che Android lo riceva mentre la connessione è ancora attiva

## Note Tecniche

- La session ID del mittente viene estratta da `e.Sender.Session` in `OnTextMessage` e passata a `execute_command` come parametro `senderSession uint32`
- `SendMessageToSession` (in `clientcommands.go`) cerca l'utente per session ID nell'elenco `b.Client.Users` e chiama `usr.Send()` — questo corrisponde al targeting `session[]` del protocollo Mumble (TextMessage diretto, non broadcast di canale)
- Il targeting per nome (`SendMessageToUser`) era soggetto a mismatch case-insensitive (`P4` vs `p4`) che causava la mancata consegna degli ACK

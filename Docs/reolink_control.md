# Reolink Camera Control — Documentazione Completa

## Camera Testata
- **Modello**: Reolink LUMUS Series E430 (wifi_solo_ipc)
- **Risoluzione**: 2560×1440
- **IP**: 192.168.1.124
- **Credenziali**: doorpiuser / G5mhReC2 (ruolo admin)

## Porte Aperte
| Porta | Protocollo | Note |
|-------|-----------|------|
| 554   | RTSP      | Streaming video |
| 8000  | ONVIF     | Autenticazione OK, ma NON controlla il faretto bianco |
| 9000  | Baichuan  | Protocollo binario proprietario Reolink — **FUNZIONA per il faretto** |
| 80/443 | HTTP     | **CHIUSE** — l'API HTTP Reolink non è raggiungibile |

---

## Metodi Tentati (in ordine)

### 1. API HTTP Reolink (FALLITO)
Le porte 80/443 sono chiuse su questa camera. I comandi standard:
- `SetWhiteLed` → connection refused
- `SetLighting` → connection refused

```
POST http://<IP>/cgi-bin/api.cgi?user=<user>&password=<pass>
[{"cmd":"SetWhiteLed","param":{"WhiteLed":{"channel":0,"state":1,"bright":100,"mode":0}}}]
```

### 2. ONVIF (PARZIALE — non controlla il faretto)
L'autenticazione ONVIF funziona (porta 8000), ma la E430 non supporta il controllo della luce bianca via ONVIF.

Metodi provati:
- **PTZ SendAuxiliaryCommand** (`tt:WhiteLight|On`) → SOAP fault
- **Imaging SetImagingSettings IrCutFilter** → cambia il filtro IR, NON il faretto
- **Imaging SupplementaryLight** → non supportato

**Autenticazione ONVIF**: WS-Security UsernameToken con PasswordDigest
```
PasswordDigest = Base64(SHA1(nonce + created + password))
```

### 3. Protocollo Baichuan (FUNZIONA!)
Protocollo binario proprietario Reolink sulla porta TCP 9000.

---

## Protocollo Baichuan — Dettagli Tecnici

### Fonti di Riferimento
- https://github.com/xannor/reolink_baichuan (Python, header big-endian — NON funziona su E430)
- https://github.com/QuantumEntangledAndy/neolink (Rust — la fonte corretta)

### Header (Little-Endian)
```
Offset  Dim  Campo           Formato
0       4    magic           uint32 LE = 0x0ABCDEF0
4       4    msg_id          uint32 LE
8       4    body_len        uint32 LE
12      1    channel_id      uint8 (sempre 0)
13      1    stream_type     uint8 (sempre 0)
14      2    msg_num         uint16 LE
16      2    response_code   uint16 LE
18      2    class           uint16 LE
```
**Totale header base**: 20 byte

Per `class = 0x6414` o `class = 0x0000` si aggiungono 4 byte:
```
20      4    payload_offset  uint32 LE
```
**Totale header esteso**: 24 byte

### Classi
| Valore | Nome | Header | Uso |
|--------|------|--------|-----|
| 0x6514 | Legacy | 20B | LoginUpgrade |
| 0x6614 | Modern (no offset) | 20B | Risposte nonce |
| 0x6414 | Modern Binary | 24B | Login, comandi post-login |
| 0x0000 | Modern Binary Alt | 24B | Alcune risposte |

### Message ID
| ID  | Comando |
|-----|---------|
| 1   | Login |
| 288 | FloodlightManual (accendi/spegni faretto) |
| 290 | FloodlightTasksWrite |
| 291 | FloodlightStatusList |
| 438 | FloodlightTasksRead |

### Response Code (nei header)
| Valore | Significato |
|--------|-------------|
| 0xDC12 | LoginUpgrade request |
| 0xDD12 | FullAes encryption negotiated |
| 0x00C8 (200) | Successo |

---

## Crittografia

### BCEncrypt (XOR) — usata durante il login
Chiave ciclica di 8 byte:
```
XML_KEY = [0x1F, 0x2D, 0x3C, 0x4B, 0x5A, 0x69, 0x78, 0xFF]
```

Formula per ogni byte `i` con offset:
```
key_idx = (i + (offset % 8)) % 8
output[i] = input[i] XOR XML_KEY[key_idx] XOR (offset & 0xFF)
```

L'offset è sempre 0 per il login.

### FullAes (AES-128-CFB) — usata dopo il login
- **Algoritmo**: AES-128-CFB con segment_size=128
- **IV**: `0123456789abcdef` (16 byte ASCII)
- **Derivazione chiave**:
  ```
  key = MD5("{nonce}-{password}").hexdigest().UPPER()[0:16]
  ```
  Esempio: nonce=`abc123`, password=`mypass` → MD5(`abc123-mypass`) → uppercase hex → primi 16 caratteri

### MD5 per login
- Hash MD5 in esadecimale **MAIUSCOLO**
- Troncato a **31 caratteri** (si scarta l'ultimo carattere hex)
- `userName = MD5(username + nonce).upper()[:31]`
- `password = MD5(password + nonce).upper()[:31]`

---

## Flusso Completo di Login + Floodlight

### Step 1: LoginUpgrade
```
→ Header: msg_id=1, body_len=0, response_code=0xDC12, class=0x6514
```
La camera risponde con:
```
← Header: class=0x6614, response_code=0xDD12, body_len=158
← Body: BCEncrypt-encrypted XML con nonce
```

Corpo decriptato (XOR):
```xml
<Encryption version="1.1">
  <type>md5</type>
  <nonce>69e5140c-qU0VmXiLt3vDcBh9dAgY</nonce>
</Encryption>
```

### Step 2: Modern Login
```
→ Header: msg_id=1, body_len=N, response_code=0, class=0x6414, payload_offset=0
→ Body: BCEncrypt-encrypted XML
```

XML login (prima della cifratura):
```xml
<?xml version="1.0" encoding="UTF-8" ?>
<body>
<LoginUser version="1.1">
<userName>MD5(username+nonce).UPPER()[:31]</userName>
<password>MD5(password+nonce).UPPER()[:31]</password>
<userVer>1</userVer>
</LoginUser>
<LoginNet version="1.1">
<type>LAN</type>
<udpPort>0</udpPort>
</LoginNet>
</body>
```

Risposta:
```
← Header: response_code=200 (0x00C8), body con DeviceInfo XML
```

### Step 3: FloodlightManual (dopo login)
Da qui in poi si usa **AES-128-CFB** (non più BCEncrypt).

Genera chiave AES:
```
aes_key = MD5("{nonce}-{password}").hexdigest().upper()[:16]
```

Il messaggio ha **due parti**, entrambe cifrate AES separatamente:

**Extension XML**:
```xml
<?xml version="1.0" encoding="UTF-8" ?>
<Extension version="1.1">
<channelId>0</channelId>
</Extension>
```

**Payload XML**:
```xml
<?xml version="1.0" encoding="UTF-8" ?>
<body>
<FloodlightManual version="1">
<channelId>0</channelId>
<status>1</status>       <!-- 1=ON, 0=OFF -->
<duration>60</duration>   <!-- secondi, 0 per OFF -->
</FloodlightManual>
</body>
```

Header:
```
→ msg_id=288, body_len=len(encExt)+len(encPayload), msg_num=1,
  response_code=0, class=0x6414, payload_offset=len(encExt)
→ Body: encExt + encPayload
```

Risposta successo:
```
← response_code=200
```

---

## Implementazione Go

Il file `baichuan.go` nel progetto doorphoneserver implementa tutto in Go puro:
- `bcXOR()` — BCEncrypt XOR
- `bcMD5Upper31()` — MD5 maiuscolo troncato a 31 char
- `bcMakeAESKey()` — derivazione chiave AES
- `bcAESEncrypt()` — AES-128-CFB encrypt
- `bcMakeHeader()` — costruisce header Baichuan 20/24 byte
- `bcRecvMessage()` — legge un messaggio completo
- `BaichuanSetFloodlight(cameraIP, username, password, state, duration)` — funzione pubblica

Nessuna dipendenza esterna — usa solo `crypto/aes`, `crypto/cipher`, `crypto/md5` della standard library Go.

### Integrazione in webpanel.go
Nel `handleSpotlight()` il flusso è:
1. **Method 1**: HTTP SetWhiteLed → fallisce (porta 80 chiusa)
2. **Method 2**: HTTP SetLighting → fallisce
3. **Method 3**: ONVIF → fallisce (non supporta faretto bianco)
4. **Method 4**: **Baichuan** → **SUCCESSO**

---

## Note e Insidie

1. **Header big-endian vs little-endian**: il repo xannor usa big-endian (`!IIIIBBH`) che NON funziona sulla E430. Il formato corretto è **little-endian** come nel repo neolink.

2. **response_code nel LoginUpgrade**: solo `0xDC12` funziona. Valori come `0x0000`, `0xDC00`, `0xDC01` non producono risposte utili.

3. **BCEncrypt durante il login**: anche se la camera negozia FullAes (0xDD12), il messaggio di login stesso è sempre cifrato con BCEncrypt (XOR). Solo i comandi successivi usano AES.

4. **payload_offset**: per class 0x6414, il campo payload_offset indica dove finisce l'Extension e inizia il Payload nel body. Entrambi sono cifrati AES separatamente.

5. **MD5 troncato**: il login usa 31 caratteri hex (non 32). Si scarta l'ultimo carattere.

6. **Timeout**: la camera risponde velocemente (~100ms). Un timeout di 10-15 secondi è più che sufficiente.

7. **Connessione per comando**: ogni operazione apre una nuova connessione TCP, fa login, invia il comando, e chiude. Non serve mantenere la connessione aperta.

8. **msg_id nella risposta**: la camera può rispondere con un msg_id diverso (es. 78 invece di 288). Il campo chiave è `response_code=200`.

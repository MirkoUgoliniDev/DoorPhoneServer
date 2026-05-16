// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"regexp"
	"strings"
	"time"
)

const (
	bcMagic             = 0x0abcdef0
	bcMsgLogin          = 1
	bcMsgGetOSDName     = 44
	bcMsgSetOSDName     = 45
	bcMsgFloodlight     = 288
	bcMsgSetGeneral     = 105
	bcClassLegacy       = 0x6514
	bcClassModernBinary = 0x6414
	bcLoginUpgradeRC    = 0xDC12
	bcHeaderSize        = 20
)

var bcXORKey = []byte{0x1F, 0x2D, 0x3C, 0x4B, 0x5A, 0x69, 0x78, 0xFF}

// BaichuanDebug abilita i log dettagliati del protocollo Baichuan.
// Viene settato da Config.Global.Software.Camera.Debug all'avvio.
var BaichuanDebug bool

// bcXOR performs BCEncrypt XOR with cycling key and offset
func bcXOR(data []byte, offset int) []byte {
	out := make([]byte, len(data))
	for i := range data {
		keyIdx := (i + (offset % 8)) % 8
		out[i] = data[i] ^ bcXORKey[keyIdx] ^ byte(offset&0xFF)
	}
	return out
}

// bcMD5Upper31 returns upper-case MD5 hex truncated to 31 chars
func bcMD5Upper31(s string) string {
	h := md5.Sum([]byte(s))
	return strings.ToUpper(fmt.Sprintf("%x", h))[:31]
}

// bcMakeAESKey derives AES-128 key from nonce and password
func bcMakeAESKey(nonce, password string) []byte {
	phrase := fmt.Sprintf("%s-%s", nonce, password)
	h := md5.Sum([]byte(phrase))
	hex := strings.ToUpper(fmt.Sprintf("%x", h)) + "\x00"
	return []byte(hex[:16])
}

// bcXMLEscape esegue l'escape dei caratteri speciali XML in una stringa.
// @param s stringa da escappare
// @return stringa con i caratteri speciali XML sostituiti con le entità corrispondenti
func bcXMLEscape(s string) string {
	repl := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return repl.Replace(s)
}

// bcAESEncrypt encrypts data with AES-128-CFB
func bcAESEncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	iv := []byte("0123456789abcdef")
	ct := make([]byte, len(plaintext))
	stream := cipher.NewCFBEncrypter(block, iv) //nolint:staticcheck // Baichuan protocol requires AES-128-CFB
	stream.XORKeyStream(ct, plaintext)
	return ct, nil
}

// bcAESDecrypt decifra dati con AES-128-CFB.
// @param key chiave AES a 16 byte
// @param ciphertext dati cifrati da decifrare
// @return testo in chiaro decifrato o errore
func bcAESDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	iv := []byte("0123456789abcdef")
	pt := make([]byte, len(ciphertext))
	stream := cipher.NewCFBDecrypter(block, iv) //nolint:staticcheck // Baichuan protocol requires AES-128-CFB
	stream.XORKeyStream(pt, ciphertext)
	return pt, nil
}

// bcMakeHeader builds a Baichuan protocol header (20 or 24 bytes)
func bcMakeHeader(msgID, bodyLen uint32, msgNum, responseCode, class uint16, payloadOffset *uint32) []byte {
	buf := make([]byte, bcHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], bcMagic)
	binary.LittleEndian.PutUint32(buf[4:8], msgID)
	binary.LittleEndian.PutUint32(buf[8:12], bodyLen)
	buf[12] = 0 // channel_id
	buf[13] = 0 // stream_type
	binary.LittleEndian.PutUint16(buf[14:16], msgNum)
	binary.LittleEndian.PutUint16(buf[16:18], responseCode)
	binary.LittleEndian.PutUint16(buf[18:20], class)
	if payloadOffset != nil {
		extra := make([]byte, 4)
		binary.LittleEndian.PutUint32(extra, *payloadOffset)
		buf = append(buf, extra...)
	}
	return buf
}

// bcRecvExact reads exactly n bytes from conn
func bcRecvExact(conn net.Conn, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(conn, buf)
	return buf, err
}

// bcRecvMessage reads one Baichuan message (header + body)
func bcRecvMessage(conn net.Conn) (msgID uint32, responseCode, class uint16, body []byte, err error) {
	msgID, responseCode, class, body, _, err = bcRecvMessageWithOffset(conn)
	return msgID, responseCode, class, body, err
}

// bcRecvMessageWithOffset legge un messaggio Baichuan con informazioni sull'offset del payload.
// @param conn connessione TCP verso la telecamera
// @return msgID, responseCode, class, body, payloadOffset e un eventuale errore
func bcRecvMessageWithOffset(conn net.Conn) (msgID uint32, responseCode, class uint16, body []byte, payloadOffset uint32, err error) {
	hdr, err := bcRecvExact(conn, bcHeaderSize)
	if err != nil {
		return 0, 0, 0, nil, 0, fmt.Errorf("read header: %w", err)
	}
	msgID = binary.LittleEndian.Uint32(hdr[4:8])
	bodyLen := binary.LittleEndian.Uint32(hdr[8:12])
	responseCode = binary.LittleEndian.Uint16(hdr[16:18])
	class = binary.LittleEndian.Uint16(hdr[18:20])

	if class == bcClassModernBinary || class == 0x0000 {
		po, poErr := bcRecvExact(conn, 4)
		if poErr != nil {
			return 0, 0, 0, nil, 0, fmt.Errorf("read payload_offset: %w", poErr)
		}
		payloadOffset = binary.LittleEndian.Uint32(po)
	}

	if bodyLen > 0 {
		body, err = bcRecvExact(conn, int(bodyLen))
		if err != nil {
			return 0, 0, 0, nil, payloadOffset, fmt.Errorf("read body: %w", err)
		}
	}
	return msgID, responseCode, class, body, payloadOffset, nil
}

// bcRecvUntilMessageIDs legge messaggi dalla connessione finché non riceve uno con ID atteso.
// @param conn connessione TCP verso la telecamera
// @param allowed mappa degli ID messaggio accettati
// @param maxRead numero massimo di messaggi da leggere prima di restituire errore
// @return msgID, responseCode, class, body, payloadOffset e un eventuale errore
func bcRecvUntilMessageIDs(conn net.Conn, allowed map[uint32]bool, maxRead int) (msgID uint32, responseCode, class uint16, body []byte, payloadOffset uint32, err error) {
	if maxRead <= 0 {
		maxRead = 1
	}
	for i := 0; i < maxRead; i++ {
		msgID, responseCode, class, body, payloadOffset, err = bcRecvMessageWithOffset(conn)
		if err != nil {
			return 0, 0, 0, nil, 0, err
		}
		if allowed[msgID] {
			return msgID, responseCode, class, body, payloadOffset, nil
		}
		log.Printf("warn: Baichuan skipping unexpected msgID=%d while waiting for %v", msgID, allowed)
	}
	return 0, 0, 0, nil, 0, fmt.Errorf("expected Baichuan response message not received")
}

// bcSendEncryptedModern invia un messaggio Baichuan in formato moderno cifrato con AES.
// @param conn connessione TCP verso la telecamera
// @param msgID identificatore del tipo di messaggio
// @param msgNum numero progressivo del messaggio
// @param aesKey chiave AES per la cifratura
// @param payloadXML payload XML da cifrare e inviare
// @return response code e un eventuale errore
func bcSendEncryptedModern(conn net.Conn, msgID uint32, msgNum uint16, aesKey []byte, payloadXML string) (uint16, error) {
	extXML := `<?xml version="1.0" encoding="UTF-8" ?>
<Extension version="1.1">
<channelId>0</channelId>
</Extension>
`

	encExt, err := bcAESEncrypt(aesKey, []byte(extXML))
	if err != nil {
		return 0, fmt.Errorf("encrypt extension: %w", err)
	}
	encPayload, err := bcAESEncrypt(aesKey, []byte(payloadXML))
	if err != nil {
		return 0, fmt.Errorf("encrypt payload: %w", err)
	}

	totalBody := append(encExt, encPayload...)
	poVal := uint32(len(encExt))
	hdr := bcMakeHeader(msgID, uint32(len(totalBody)), msgNum, 0, bcClassModernBinary, &poVal)
	if _, err := conn.Write(append(hdr, totalBody...)); err != nil {
		return 0, fmt.Errorf("send message %d: %w", msgID, err)
	}

	_, rc, _, _, _, err := bcRecvUntilMessageIDs(conn, map[uint32]bool{msgID: true}, 6)
	if err != nil {
		return 0, fmt.Errorf("recv message %d response: %w", msgID, err)
	}
	return rc, nil
}

// BaichuanSetFloodlight connects to a Reolink camera via Baichuan protocol (port 9000)
// and sends a FloodlightManual command. state: 1=on, 0=off. duration in seconds (used when on).
func BaichuanSetFloodlight(cameraIP, username, password string, state int, duration int) error {
	addr := fmt.Sprintf("%s:9000", cameraIP)
	if BaichuanDebug {
		log.Printf("info: Baichuan connecting to %s\n", addr)
	}

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		log.Printf("warn: failed to set deadline: %v\n", err)
	}

	// Step 1: LoginUpgrade — request encryption negotiation
	po := uint32(0)
	hdr := bcMakeHeader(bcMsgLogin, 0, 0, bcLoginUpgradeRC, bcClassLegacy, nil)
	if _, err := conn.Write(hdr); err != nil {
		return fmt.Errorf("send LoginUpgrade: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan sent LoginUpgrade\n")
	}

	// Read nonce response
	_, rc1, _, body1, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv nonce: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan nonce response: rc=0x%04X bodyLen=%d\n", rc1, len(body1))
	}

	nonceXML := string(bcXOR(body1, 0))
	re := regexp.MustCompile(`<nonce>([^<]+)</nonce>`)
	m := re.FindStringSubmatch(nonceXML)
	if len(m) < 2 {
		return fmt.Errorf("nonce not found in response: %s", nonceXML)
	}
	nonce := m[1]
	if BaichuanDebug {
		log.Printf("info: Baichuan nonce: %s\n", nonce)
	}

	// Step 2: Modern login with MD5(user+nonce) / MD5(pass+nonce)
	loginXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<LoginUser version="1.1">
<userName>%s</userName>
<password>%s</password>
<userVer>1</userVer>
</LoginUser>
<LoginNet version="1.1">
<type>LAN</type>
<udpPort>0</udpPort>
</LoginNet>
</body>
`, bcMD5Upper31(username+nonce), bcMD5Upper31(password+nonce))

	encLogin := bcXOR([]byte(loginXML), 0)
	hdr2 := bcMakeHeader(bcMsgLogin, uint32(len(encLogin)), 0, 0, bcClassModernBinary, &po)
	if _, err := conn.Write(append(hdr2, encLogin...)); err != nil {
		return fmt.Errorf("send login: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan sent modern login\n")
	}

	_, rc2, _, _, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv login response: %w", err)
	}
	if rc2 != 200 {
		return fmt.Errorf("login failed: response_code=%d", rc2)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan login OK (code=200)\n")
	}

	// Step 3: Send FloodlightManual
	aesKey := bcMakeAESKey(nonce, password)

	extXML := `<?xml version="1.0" encoding="UTF-8" ?>
<Extension version="1.1">
<channelId>0</channelId>
</Extension>
`
	payloadXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<FloodlightManual version="1">
<channelId>0</channelId>
<status>%d</status>
<duration>%d</duration>
</FloodlightManual>
</body>
`, state, duration)

	encExt, err := bcAESEncrypt(aesKey, []byte(extXML))
	if err != nil {
		return fmt.Errorf("encrypt extension: %w", err)
	}
	encPayload, err := bcAESEncrypt(aesKey, []byte(payloadXML))
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	totalBody := append(encExt, encPayload...)
	poVal := uint32(len(encExt))
	hdr3 := bcMakeHeader(bcMsgFloodlight, uint32(len(totalBody)), 1, 0, bcClassModernBinary, &poVal)
	if _, err := conn.Write(append(hdr3, totalBody...)); err != nil {
		return fmt.Errorf("send floodlight: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan sent FloodlightManual state=%d duration=%d\n", state, duration)
	}

	_, rc3, _, _, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv floodlight response: %w", err)
	}
	if rc3 != 200 {
		return fmt.Errorf("floodlight command failed: response_code=%d", rc3)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan FloodlightManual SUCCESS (code=200)\n")
	}
	return nil
}

// BaichuanSetTime connects to a Reolink camera via Baichuan protocol (port 9000)
// and sets the camera clock to the current system time.
func BaichuanSetTime(cameraIP, username, password string) error {
	addr := fmt.Sprintf("%s:9000", cameraIP)
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime connecting to %s\n", addr)
	}

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		log.Printf("warn: failed to set deadline: %v\n", err)
	}

	// Step 1: LoginUpgrade
	hdr := bcMakeHeader(bcMsgLogin, 0, 0, bcLoginUpgradeRC, bcClassLegacy, nil)
	if _, err := conn.Write(hdr); err != nil {
		return fmt.Errorf("send LoginUpgrade: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime sent LoginUpgrade\n")
	}

	_, rc1, _, body1, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv nonce: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime nonce response: rc=0x%04X bodyLen=%d\n", rc1, len(body1))
	}

	nonceXML := string(bcXOR(body1, 0))
	re := regexp.MustCompile(`<nonce>([^<]+)</nonce>`)
	m := re.FindStringSubmatch(nonceXML)
	if len(m) < 2 {
		return fmt.Errorf("nonce not found in response: %s", nonceXML)
	}
	nonce := m[1]
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime nonce: %s\n", nonce)
	}

	// Step 2: Modern login
	loginXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<LoginUser version="1.1">
<userName>%s</userName>
<password>%s</password>
<userVer>1</userVer>
</LoginUser>
<LoginNet version="1.1">
<type>LAN</type>
<udpPort>0</udpPort>
</LoginNet>
</body>
`, bcMD5Upper31(username+nonce), bcMD5Upper31(password+nonce))

	po := uint32(0)
	encLogin := bcXOR([]byte(loginXML), 0)
	hdr2 := bcMakeHeader(bcMsgLogin, uint32(len(encLogin)), 0, 0, bcClassModernBinary, &po)
	if _, err := conn.Write(append(hdr2, encLogin...)); err != nil {
		return fmt.Errorf("send login: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime sent modern login\n")
	}

	_, rc2, _, _, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv login response: %w", err)
	}
	if rc2 != 200 {
		return fmt.Errorf("login failed: response_code=%d", rc2)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime login OK (code=200)\n")
	}

	// Step 3: Send SetGeneral (msg ID 105) with current system time
	now := time.Now()
	_, tzOffset := now.Zone()
	// Reolink uses negative of UTC offset in seconds
	reolinkTZ := -tzOffset

	aesKey := bcMakeAESKey(nonce, password)

	extXML := `<?xml version="1.0" encoding="UTF-8" ?>
<Extension version="1.1">
<channelId>0</channelId>
</Extension>
`
	payloadXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<SystemGeneral version="1.1">
<timeFormat>0</timeFormat>
<timeZone>%d</timeZone>
<year>%d</year>
<month>%d</month>
<day>%d</day>
<hour>%d</hour>
<minute>%d</minute>
<second>%d</second>
</SystemGeneral>
</body>
`, reolinkTZ, now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second())

	encExt, err := bcAESEncrypt(aesKey, []byte(extXML))
	if err != nil {
		return fmt.Errorf("encrypt extension: %w", err)
	}
	encPayload, err := bcAESEncrypt(aesKey, []byte(payloadXML))
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	totalBody := append(encExt, encPayload...)
	poVal := uint32(len(encExt))
	hdr3 := bcMakeHeader(bcMsgSetGeneral, uint32(len(totalBody)), 1, 0, bcClassModernBinary, &poVal)
	if _, err := conn.Write(append(hdr3, totalBody...)); err != nil {
		return fmt.Errorf("send SetGeneral: %w", err)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan sent SetGeneral time=%04d-%02d-%02d %02d:%02d:%02d tz=%d\n",
			now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second(), reolinkTZ)
	}

	_, rc3, _, _, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv SetGeneral response: %w", err)
	}
	if rc3 != 200 {
		return fmt.Errorf("SetGeneral command failed: response_code=%d", rc3)
	}
	if BaichuanDebug {
		log.Printf("info: Baichuan SetTime SUCCESS (code=200)\n")
	}
	return nil
}

// BaichuanSetOSDText enables/disables OSD channel name and updates its text (top-left label on most Reolink UIs).
func BaichuanSetOSDText(cameraIP, username, password string, enabled bool, text, pos string) error {
	addr := fmt.Sprintf("%s:9000", cameraIP)
	if BaichuanDebug {
		log.Printf("info: Baichuan SetOSD connecting to %s\n", addr)
	}

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		log.Printf("warn: failed to set deadline: %v\n", err)
	}

	// Step 1: LoginUpgrade
	hdr := bcMakeHeader(bcMsgLogin, 0, 0, bcLoginUpgradeRC, bcClassLegacy, nil)
	if _, err := conn.Write(hdr); err != nil {
		return fmt.Errorf("send LoginUpgrade: %w", err)
	}

	_, _, _, body1, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv nonce: %w", err)
	}

	nonceXML := string(bcXOR(body1, 0))
	re := regexp.MustCompile(`<nonce>([^<]+)</nonce>`)
	m := re.FindStringSubmatch(nonceXML)
	if len(m) < 2 {
		return fmt.Errorf("nonce not found in response: %s", nonceXML)
	}
	nonce := m[1]

	// Step 2: Modern login
	loginXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<LoginUser version="1.1">
<userName>%s</userName>
<password>%s</password>
<userVer>1</userVer>
</LoginUser>
<LoginNet version="1.1">
<type>LAN</type>
<udpPort>0</udpPort>
</LoginNet>
</body>
`, bcMD5Upper31(username+nonce), bcMD5Upper31(password+nonce))

	po := uint32(0)
	encLogin := bcXOR([]byte(loginXML), 0)
	hdr2 := bcMakeHeader(bcMsgLogin, uint32(len(encLogin)), 0, 0, bcClassModernBinary, &po)
	if _, err := conn.Write(append(hdr2, encLogin...)); err != nil {
		return fmt.Errorf("send login: %w", err)
	}

	_, rc2, _, _, err := bcRecvMessage(conn)
	if err != nil {
		return fmt.Errorf("recv login response: %w", err)
	}
	if rc2 != 200 {
		return fmt.Errorf("login failed: response_code=%d", rc2)
	}

	// Step 3: Send OsdChannelName write (msg ID 45)
	aesKey := bcMakeAESKey(nonce, password)

	flag := 0
	if enabled {
		flag = 1
	}
	name := strings.TrimSpace(text)
	if name == "" {
		name = "Camera"
	}
	if len(name) > 48 {
		name = name[:48]
	}
	name = bcXMLEscape(name)

	// Map position to coordinates (normalized 0-65536 range)
	var osdX, osdY int
	switch strings.ToLower(strings.TrimSpace(pos)) {
	case "tl":
		osdX, osdY = 1, 1
	case "tr":
		osdX, osdY = 65534, 1
	case "bl":
		osdX, osdY = 1, 65534
	default: // br
		osdX, osdY = 65534, 65534
	}

	payloadOSDOnly := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<OsdChannelName version="1.1">
<channelId>0</channelId>
<name>%s</name>
<enable>%d</enable>
<topLeftX>%d</topLeftX>
<topLeftY>%d</topLeftY>
<enWatermark>0</enWatermark>
<enBgcolor>0</enBgcolor>
</OsdChannelName>
</body>
`, name, flag, osdX, osdY)
	payloadOSDWithDatetime := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<OsdChannelName version="1.1">
<channelId>0</channelId>
<name>%s</name>
<enable>%d</enable>
<topLeftX>%d</topLeftX>
<topLeftY>%d</topLeftY>
<enWatermark>0</enWatermark>
<enBgcolor>0</enBgcolor>
</OsdChannelName>
<OsdDatetime version="1.1">
<channelId>0</channelId>
<enable>1</enable>
<topLeftX>65537</topLeftX>
<topLeftY>1</topLeftY>
<width>0</width>
<height>0</height>
<language>English</language>
</OsdDatetime>
</body>
`, name, flag, osdX, osdY)

	rc3, err := bcSendEncryptedModern(conn, bcMsgSetOSDName, 1, aesKey, payloadOSDOnly)
	if err != nil {
		return fmt.Errorf("OsdChannelName write failed: %w", err)
	}
	if rc3 == 400 {
		log.Printf("warn: OsdChannelName write returned 400 with OSD-only payload, retrying with OsdDatetime block")
		rc3, err = bcSendEncryptedModern(conn, bcMsgSetOSDName, 2, aesKey, payloadOSDWithDatetime)
		if err != nil {
			return fmt.Errorf("OsdChannelName write retry failed: %w", err)
		}
	}
	if rc3 != 200 {
		return fmt.Errorf("OsdChannelName write failed: response_code=%d", rc3)
	}

	state, getErr := BaichuanGetOSDText(cameraIP, username, password)
	if getErr != nil {
		log.Printf("warn: Baichuan SetOSD applied, but readback failed: %v\n", getErr)
		log.Printf("info: Baichuan SetOSD SUCCESS (enabled=%v text=%q)\n", enabled, text)
		return nil
	}

	log.Printf("info: Baichuan SetOSD SUCCESS requested(enabled=%v text=%q) readback(enabled=%v text=%q)\n",
		enabled, text, state.Enabled, state.Text)
	return nil
}

// BaichuanOSDState rappresenta lo stato corrente del testo OSD (On-Screen Display) della telecamera.
type BaichuanOSDState struct {
	// Enabled indica se il nome del canale OSD è attivo
	Enabled bool
	// Text è il testo attualmente visualizzato come nome del canale OSD
	Text string
}

// BaichuanGetOSDText legge lo stato attuale del testo OSD da una telecamera Reolink.
// @param cameraIP indirizzo IP della telecamera
// @param username nome utente per l'autenticazione
// @param password password per l'autenticazione
// @return struttura BaichuanOSDState con lo stato OSD corrente, o errore
func BaichuanGetOSDText(cameraIP, username, password string) (*BaichuanOSDState, error) {
	addr := fmt.Sprintf("%s:9000", cameraIP)

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		log.Printf("warn: failed to set deadline: %v\n", err)
	}

	hdr := bcMakeHeader(bcMsgLogin, 0, 0, bcLoginUpgradeRC, bcClassLegacy, nil)
	if _, err := conn.Write(hdr); err != nil {
		return nil, fmt.Errorf("send LoginUpgrade: %w", err)
	}

	_, _, _, body1, err := bcRecvMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("recv nonce: %w", err)
	}

	nonceXML := string(bcXOR(body1, 0))
	re := regexp.MustCompile(`<nonce>([^<]+)</nonce>`)
	m := re.FindStringSubmatch(nonceXML)
	if len(m) < 2 {
		return nil, fmt.Errorf("nonce not found in response: %s", nonceXML)
	}
	nonce := m[1]

	loginXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<body>
<LoginUser version="1.1">
<userName>%s</userName>
<password>%s</password>
<userVer>1</userVer>
</LoginUser>
<LoginNet version="1.1">
<type>LAN</type>
<udpPort>0</udpPort>
</LoginNet>
</body>
`, bcMD5Upper31(username+nonce), bcMD5Upper31(password+nonce))

	po := uint32(0)
	encLogin := bcXOR([]byte(loginXML), 0)
	hdr2 := bcMakeHeader(bcMsgLogin, uint32(len(encLogin)), 0, 0, bcClassModernBinary, &po)
	if _, err := conn.Write(append(hdr2, encLogin...)); err != nil {
		return nil, fmt.Errorf("send login: %w", err)
	}

	_, rc2, _, _, err := bcRecvMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("recv login response: %w", err)
	}
	if rc2 != 200 {
		return nil, fmt.Errorf("login failed: response_code=%d", rc2)
	}

	aesKey := bcMakeAESKey(nonce, password)

	extXML := `<?xml version="1.0" encoding="UTF-8" ?>
<Extension version="1.1">
<channelId>0</channelId>
</Extension>
`
	encExt, err := bcAESEncrypt(aesKey, []byte(extXML))
	if err != nil {
		return nil, fmt.Errorf("encrypt extension: %w", err)
	}

	poVal := uint32(len(encExt))
	hdr3 := bcMakeHeader(bcMsgGetOSDName, uint32(len(encExt)), 1, 0, bcClassModernBinary, &poVal)
	if _, err := conn.Write(append(hdr3, encExt...)); err != nil {
		return nil, fmt.Errorf("send OsdChannelName get: %w", err)
	}

	msgID, rc3, class3, body3, payloadOffset, err := bcRecvUntilMessageIDs(conn, map[uint32]bool{bcMsgGetOSDName: true}, 6)
	if err != nil {
		return nil, fmt.Errorf("recv OsdChannelName get response: %w", err)
	}
	if rc3 != 200 {
		return nil, fmt.Errorf("OsdChannelName get failed: response_code=%d", rc3)
	}
	_ = msgID

	var payload []byte
	if class3 == bcClassModernBinary || class3 == 0x0000 {
		if payloadOffset > 0 && int(payloadOffset) <= len(body3) {
			payload = body3[payloadOffset:]
		} else {
			payload = body3
		}
	} else {
		payload = body3
	}

	if len(payload) == 0 {
		return nil, fmt.Errorf("empty OSD payload in response")
	}

	plain, err := bcAESDecrypt(aesKey, payload)
	if err != nil {
		return nil, fmt.Errorf("decrypt OSD payload: %w", err)
	}
	xmlBody := string(plain)

	nameRe := regexp.MustCompile(`(?s)<OsdChannelName[^>]*>.*?<name>(.*?)</name>`)
	enRe := regexp.MustCompile(`(?s)<OsdChannelName[^>]*>.*?<enable>(\d+)</enable>`)

	nameMatch := nameRe.FindStringSubmatch(xmlBody)
	enMatch := enRe.FindStringSubmatch(xmlBody)
	if len(nameMatch) < 2 || len(enMatch) < 2 {
		return nil, fmt.Errorf("cannot parse OSD readback payload")
	}

	return &BaichuanOSDState{
		Enabled: strings.TrimSpace(enMatch[1]) == "1",
		Text:    strings.TrimSpace(html.UnescapeString(nameMatch[1])),
	}, nil
}

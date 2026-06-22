package doorphoneserver

import (
	"bufio"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// discoveryMu serializza le sessioni di probing tra i due bridge
// evitando che entrambi aprano lo stesso port simultaneamente.
var discoveryMu sync.Mutex

// portsInUse traccia le porte già in uso da un bridge connesso. Il probing
// dell'altro bridge le salta: su Linux le seriali non sono esclusive quando
// il processo gira come root (TIOCEXCL viene bypassato), quindi senza questo
// guard il probe aprirebbe la porta attiva dell'altro ESP rubandone i byte.
var (
	portsInUseMu sync.Mutex
	portsInUse   = make(map[string]bool)
)

func markPortInUse(path string) {
	portsInUseMu.Lock()
	portsInUse[path] = true
	portsInUseMu.Unlock()
}

func unmarkPortInUse(path string) {
	portsInUseMu.Lock()
	delete(portsInUse, path)
	portsInUseMu.Unlock()
}

func isPortInUse(path string) bool {
	portsInUseMu.Lock()
	defer portsInUseMu.Unlock()
	return portsInUse[path]
}

// probeRole apre path, invia GET-ROLE\n e legge righe finché riceve
// "HELLO <ROLE>" entro timeout. Chiude la porta prima di ritornare.
// Ritorna il ruolo (es. "RFID" o "RELAY") o errore.
func probeRole(path string, timeout time.Duration) (string, error) {
	port, err := serial.Open(path, &serial.Mode{
		BaudRate: usbBaudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return "", err
	}
	defer port.Close()

	// SetReadTimeout fa ritornare Read con (0,nil) allo scadere; bufio.Scanner
	// termina dopo maxConsecutiveEmptyReads, quindi il loop non blocca all'infinito.
	if err := port.SetReadTimeout(timeout); err != nil {
		return "", err
	}
	if _, err := port.Write([]byte("GET-ROLE\n")); err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(port)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "HELLO ") {
			role := strings.TrimSpace(strings.TrimPrefix(line, "HELLO "))
			return strings.ToUpper(role), nil
		}
	}
	return "", fmt.Errorf("nessuna risposta HELLO da %s", path)
}

// detectAllInOne proba le porte candidate (senza riservarle) per una finestra
// temporale e ritorna true se almeno un device risponde "HELLO ALL". Gira a
// startup, prima che parta qualunque connectLoop: probeRole apre e chiude la
// porta da solo e non marca nulla in uso, quindi non c'è race con i bridge.
func detectAllInOne(window time.Duration) bool {
	deadline := time.Now().Add(window)
	for {
		for _, path := range scanCandidatePorts() {
			role, err := probeRole(path, usbProbeTimeout)
			if err == nil && role == "ALL" {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		// Breve pausa prima del prossimo giro: dà tempo all'enumerazione USB
		// di esporre il device se il boot non è ancora completo.
		time.Sleep(usbRetryDelay)
	}
}

// scanCandidatePorts ritorna i path /dev/ttyACM* e /dev/ttyUSB* disponibili, ordinati.
func scanCandidatePorts() []string {
	var ports []string
	for _, pattern := range []string{"/dev/ttyACM*", "/dev/ttyUSB*"} {
		matches, _ := filepath.Glob(pattern)
		ports = append(ports, matches...)
	}
	sort.Strings(ports)
	return ports
}

// findPortForRole cerca tra i device seriali USB non già in uso quello che
// risponde con HELLO <role>. La porta trovata viene riservata (markPortInUse)
// sotto discoveryMu prima del ritorno, così l'altro bridge non la riapre nel
// proprio probe. Ritorna il path trovato o "" se nessun device corrisponde.
// Il chiamante DEVE chiamare unmarkPortInUse(path) quando la sessione termina
// o se l'apertura definitiva fallisce.
func findPortForRole(role string, timeout time.Duration) string {
	discoveryMu.Lock()
	defer discoveryMu.Unlock()

	for _, path := range scanCandidatePorts() {
		if isPortInUse(path) {
			continue
		}
		found, err := probeRole(path, timeout)
		if err != nil || found != role {
			continue
		}
		markPortInUse(path)
		return path
	}
	return ""
}

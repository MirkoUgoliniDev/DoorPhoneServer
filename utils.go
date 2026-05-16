// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"net"
	"os"
	"strings"

	"github.com/kennygrant/sanitize"
)

/*
func reset() {
	term.Sync()
}
*/

// esc esegue l'escape HTML di una stringa rimuovendo tag e caratteri speciali.
// @param str stringa da sanitizzare
// @return stringa con HTML escapato
func esc(str string) string {
	return sanitize.HTML(str)
}

// cleanstring pulisce una stringa rimuovendo caratteri non validi per i nomi.
// @param str stringa da pulire
// @return stringa sanitizzata adatta per nomi file e utenti
func cleanstring(str string) string {
	return sanitize.Name(str)
}

/*
func localAddresses() {
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("error: localAddresses %v", err)
		return
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()

		if err != nil {
			log.Printf("error: localAddresses %v", err)
			continue
		}

		for _, a := range addrs {
			if i.Name != "lo" {
				log.Printf("info: %v %v", i.Name, a)
			}
		}
	}
}


func (b *DoorPhoneServer) pingconnectedserver() {
	resp, err := gumble.Ping(b.Address, time.Second*1, time.Second*5)

	if err != nil {
		log.Printf("error: Ping Error %s", err)
		return
	}

	major, minor, patch := resp.Version.SemanticVersion()

	log.Printf("info: Server Address:         %s", resp.Address)
	log.Printf("info: Current Channel:        %s", b.Client.Self.Channel.Name)
	log.Printf("info: Server Ping:            %d", resp.Ping)
	log.Printf("info: Server Version:         %d.%d.%d", major, minor, patch)
	log.Printf("info: Server Users:           %d/%d", resp.ConnectedUsers, resp.MaximumUsers)
	log.Printf("info: Server Maximum Bitrate: %d", resp.MaximumBitrate)
}


func zipit(source, target string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	info, err := os.Stat(source)
	if err != nil {
		return nil
	}

	var baseDir string
	if info.IsDir() {
		baseDir = filepath.Base(source)
	}

	filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		if baseDir != "" {
			header.Name = filepath.Join(baseDir, strings.TrimPrefix(path, source))
		}

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	})

	return err
}


func createDirIfNotExist(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0777)
		if err != nil {
			panic(err)
		}
	}
}

func cleardir(dir string) {
	// The target directory.
	//directory := CamImageSavePath	// path must end on "/"... fix for no "/"?
	directory := dir + "/" // path with "/"
	// Open the directory and read all its files.
	dirRead, err := os.Open(directory)
	if err != nil {
		log.Printf("error: cannot open directory %s: %v", directory, err)
		return
	}
	defer dirRead.Close()
	dirFiles, _ := dirRead.Readdir(0)
	// Loop over the directory's files.
	for index := range dirFiles {
		fileHere := dirFiles[index]
		// Get name of file and its full path.
		nameHere := fileHere.Name()
		fullPath := directory + nameHere
		// Remove the files.
		os.Remove(fullPath)
		log.Println("info: Removed file", fullPath)

	}
}

func dirIsEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		log.Println("debug: Dir is Not Empty")
		return false, err // Not Empty
	}
	defer f.Close()

	_, err = f.Readdirnames(1) // Or f.Readdir(1)  // empty
	if err == io.EOF {
		log.Println("debug: Dir is Empty")
		return true, nil
	}
	return false, err // Either not empty or error, suits both cases
}

func isCommandAvailable(name string) bool {
	cmd := exec.Command("/bin/sh", "-c", "command -v "+name)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func check(err error) {
	if err != nil {
		FatalCleanUp(err.Error())
	}
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	//d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	//s := m / time.Second
	return fmt.Sprintf("%02d:%02d", h, m) // show sec’s also?
}

func before(value string, a string) string { // used for sox time
	// Get substring before a string.
	pos := strings.Index(value, a)
	if pos == -1 {
		return ""
	}
	return value[0:pos]
}


func getOutboundIP() string {
	consensus := externalip.DefaultConsensus(nil, nil)
	ip, err := consensus.ExternalIP()
	if err == nil {
		return ip.String()
	}
	return "Could Not Get Public WAN IP"
}

*/

// FileExists verifica se un file esiste e non è una directory.
// @param filepath percorso del file da verificare
// @return true se il file esiste, false altrimenti
func FileExists(filepath string) bool {

	fileinfo, err := os.Stat(filepath)

	if os.IsNotExist(err) {
		return false
	}

	return !fileinfo.IsDir()
}

// getMacAddr restituisce la lista degli indirizzi MAC di tutte le interfacce di rete.
// @return slice di stringhe con gli indirizzi MAC o errore se le interfacce non sono leggibili
func getMacAddr() ([]string, error) {
	ifas, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var as []string
	for _, ifa := range ifas {
		a := ifa.HardwareAddr.String()
		if a != "" {
			as = append(as, a)
		}
	}
	return as, nil
}

// IsAudioCardPresent verifica se è presente almeno una scheda audio nel sistema
// leggendo /proc/asound/cards. Restituisce false (con warning nel log) se non ne trova.
func IsAudioCardPresent() bool {
	data, err := os.ReadFile("/proc/asound/cards")
	if err != nil || strings.Contains(string(data), "no soundcards") {
		return false
	}
	return true
}

// checkRegex verifica se una stringa corrisponde a un'espressione regolare.

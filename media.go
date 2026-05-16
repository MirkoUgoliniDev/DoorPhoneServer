// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strconv"
)

// localMediaPlayer riproduce un file audio localmente tramite ffplay.
// @param fileNameWithPath percorso completo del file audio da riprodurre
// @param playbackvolume volume di riproduzione (0-100)
// @param blocking se true attende il completamento della riproduzione prima di tornare
// @param duration durata massima di riproduzione in secondi (0 = fino alla fine del file)
// @param loop numero di ripetizioni (massimo 3 per evitare loop infiniti)
func localMediaPlayer(fileNameWithPath string, playbackvolume int, blocking bool, duration float32, loop int) {

	if loop == 0 || loop > 3 {
		log.Println("warn: Infinite Loop or more than 3 loops not allowed")
		return
	}

	CmdArguments := []string{fileNameWithPath, "-volume", strconv.Itoa(playbackvolume), "-autoexit", "-loop", strconv.Itoa(loop), "-autoexit", "-nodisp"}

	if duration > 0 {
		CmdArguments = []string{fileNameWithPath, "-volume", strconv.Itoa(playbackvolume), "-autoexit", "-t", fmt.Sprintf("%.1f", duration), "-loop", strconv.Itoa(loop), "-autoexit", "-nodisp"}
	}

	cmd := exec.Command("/usr/bin/ffplay", CmdArguments...)

	WaitForFFPlay := make(chan struct{})

	go func() {

		if err := cmd.Run(); err != nil {
			log.Printf("error: failed executing cmd.Run: %v\n", err)
		}

		if blocking {
			WaitForFFPlay <- struct{}{} // signal that the routine has completed
		}

	}()

	if blocking {
		<-WaitForFFPlay
	}
}

// PlayTone genera e riproduce un tono sinusoidale tramite ffplay.
// @param toneFreq frequenza del tono in Hz
// @param toneDuration durata del tono in secondi
// @param destination destinazione di riproduzione ("local" è l'unica supportata)
// @param withRXLED se true abilita il LED RX durante la riproduzione (non implementato)
func (b *DoorPhoneServer) PlayTone(toneFreq int, toneDuration float32, destination string, withRXLED bool) {
	if destination == "local" {
		cmdArguments := []string{"-f", "lavfi", "-i", "sine=frequency=" + strconv.Itoa(toneFreq) + ":duration=" + fmt.Sprintf("%f", toneDuration), "-autoexit", "-nodisp"}
		cmd := exec.Command("/usr/bin/ffplay", cmdArguments...)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if err != nil {
			log.Println("error: ffplay error ", err)
			return
		}
		log.Printf("info: Played Tone at Frequency %v Hz With Duration of %v Seconds\n", toneFreq, toneDuration)
	}
}


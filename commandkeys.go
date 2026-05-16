// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"fmt"
	"log"
	"time"

	"github.com/MirkoUgoliniDev/volume-go"
)

// cmdMuteUnmute gestisce il comando di mute/unmute del dispositivo audio di uscita.
// @param subCommand può essere "toggle", "mute" o "unmute"
func (b *DoorPhoneServer) cmdMuteUnmute(subCommand string) {

	log.Printf("debug: F3 pressed %v Speaker Requested\n", subCommand)

	//TODO: DA VERIFICARE

	OrigMuted, err := volume.GetMuted(Config.Global.Software.Settings.OutputMuteControlDevice)

	if err != nil {
		log.Println("error: Unable to get current Muted/Unmuted State ", err)
		if subCommand == "toggle" {
			return
		}
	} else {
		if OrigMuted {
			log.Println("debug: Originally Device is Muted")
		} else {
			log.Println("debug: Originally Device is Unmuted")
		}
	}

	if subCommand == "toggle" {

		if OrigMuted {
			err := volume.Unmute(Config.Global.Software.Settings.OutputMuteControlDevice)
			if err != nil {
				log.Println("error: Unmuting Failed", err)
				return
			}
			TTSEvent("unmutespeaker")
			log.Println("info: Output Device Unmuted")
			return
		} else {
			TTSEvent("mutespeaker")
			err = volume.Mute(Config.Global.Software.Settings.OutputMuteControlDevice)
			if err != nil {
				log.Println("error: Muting Failed", err)
				return
			}
			log.Println("info: Output Device Muted")
			return
		}

	}

	if subCommand == "mute" {
		TTSEvent("mutespeaker")
		err = volume.Mute(Config.Global.Software.Settings.OutputMuteControlDevice)
		if err != nil {
			log.Println("error: Muting Failed ", err)
			return
		}
		log.Println("info: Output Device Muted")
	}

	//force unmute
	if subCommand == "unmute" {
		err := volume.Unmute(Config.Global.Software.Settings.OutputMuteControlDevice)
		TTSEvent("unmutespeaker")
		if err != nil {
			log.Println("error: Unmute Failed ", err)
			return
		}
		log.Println("info: Output Device Unmuted")
		return
	}

}


// cmdStartTransmitting avvia la modalità trasmissione PTT se non già attiva.
func (b *DoorPhoneServer) cmdStartTransmitting() {
	log.Println("debug: F8 pressed TX Mode Requested (Start Transmitting)")
	log.Println("info: Start Transmitting")
	TTSEvent("starttransmitting")
	if !b.IsTransmitting.Load() {
		time.Sleep(100 * time.Millisecond)
		b.TransmitStart()
	} else {
		log.Println("error: Already in Transmitting Mode")
	}
}


// cmdPlayback attiva o disattiva lo streaming audio nel canale Mumble corrente.
// Se la trasmissione PTT è attiva, la interrompe prima di avviare lo streaming.
func (b *DoorPhoneServer) cmdPlayback() {
	log.Println("debug: F11 pressed Start/Stop Stream Stream into Current Channel Requested")
	log.Println("info: Stream into Current Channel")
	TTSEvent("playstream")

	if b.IsTransmitting.Load() {
		log.Println("alert: doorphoneserver was already transmitting will now stop transmitting and start the stream")
		b.TransmitStop(false)
	}

	IsPlayStream.Store(!IsPlayStream.Load())
	NowStreaming.Store(IsPlayStream.Load())

	if IsPlayStream.Load() && Config.Global.Software.Settings.StreamSendMessage {
		b.SendMessage(fmt.Sprintf("%s Streaming", b.Username), false)
	}

}

/*
func (b *DoorPhoneServer) cmdQuitDoorPhoneServer() {
	log.Println("debug: Ctrl-C Terminate Program Requested")
	duration := time.Since(StartTime)
	log.Printf("info: DoorPhoneServer Now Running For %v \n", secondsToHuman(int(duration.Seconds())))
	TTSEvent("quitdoorphoneserver")
	CleanUp()
}

func (b *DoorPhoneServer) cmdDebugStacktrace() {
	buf := make([]byte, 1<<16)
	stackSize := runtime.Stack(buf, true)
	var debug bytes.Buffer
	debug.WriteString(string(buf[0:stackSize]))
	scanner := bufio.NewScanner(&debug)
	var line int = 1
	log.Println("debug: Pressed Ctrl-D")
	log.Println("info: Stack Dump Requested")
	for scanner.Scan() {
		log.Printf("debug: line: %d %s\n", line, scanner.Text())
		line++
	}
	goStreamStats()
}
*/

/*
func (b *DoorPhoneServer) cmdPlayRepeaterTone() {
	log.Println("debug: Ctrl-G Pressed")
	log.Println("info: Play Repeater Tone on Speaker and Simulate RX Signal")
	if Config.Global.Software.Sounds.RepeaterTone.Enabled {
		b.PlayTone(Config.Global.Software.Sounds.RepeaterTone.ToneFrequencyHz, Config.Global.Software.Sounds.RepeaterTone.ToneDurationSec, "local", true)
	} else {
		log.Println("warn: Repeater Tone Disabled by Config")
	}
}


func (b *DoorPhoneServer) cmdLiveReload() {
	log.Println("debug: Ctrl-B Pressed")
	log.Println("info: XML Config Live Reload")
	err := readxmlconfig(ConfigXMLFile, true)
	if err != nil {
		message := err.Error()
		FatalCleanUp(message)
	}
}

func cmdSanityCheck() {
	log.Println("debug: Ctrl-H Pressed")
	log.Println("info: XML Sanity Checker")
	CheckConfigSanity(false)
}
*/

// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"path/filepath"
)

// TTSEvent riproduce il file audio associato a un evento TTS configurato nell'XML.
// @param name nome dell'evento TTS (es. "starttransmitting", "ring", "quitdoorphoneserver")
func TTSEvent(name string) {
	if !Config.Global.Software.TTS.Enabled {
		return
	}

	for _, tts := range Config.Global.Software.TTS.Sound {

		if tts.Action == name {
			if tts.Enabled {
				file := tts.File
				if !filepath.IsAbs(file) {
					file = filepath.Join(filepath.Dir(ConfigXMLFile), file)
				}
				localMediaPlayer(file, Config.Global.Software.TTS.Volumelevel, tts.Blocking, 0, 1)
				return
			}
		}
	}
}

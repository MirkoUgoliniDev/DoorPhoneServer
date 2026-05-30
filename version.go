// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

const (
	// doorphoneserverVersion è la versione corrente del software doorphoneserver
	doorphoneserverVersion string = "3.0.0"
)

// BuildTime viene iniettato al compile time via -ldflags.
// Esempio: go build -ldflags "-X 'doorphoneserver.BuildTime=2026-05-30 16:33'"
var BuildTime = "unknown"

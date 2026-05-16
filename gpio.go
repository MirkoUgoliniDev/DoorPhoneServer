// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stianeikeland/go-rpio/v4"
	"github.com/MirkoUgoliniDev/gpio"
)

// gpioDebounceDelay è il ritardo di debounce applicato alla lettura dei pin di ingresso GPIO
const gpioDebounceDelay = 150 * time.Millisecond

// gpioSysfsOffset è l'offset da aggiungere ai numeri BCM quando si usa l'interfaccia sysfs.
// Su kernel 6.x (Raspberry Pi OS Bookworm) il gpiochip BCM ha base 512 anziché 0:
// es. BCM 26 → sysfs 538. Inizializzato da init() prima di qualsiasi chiamata GPIO.
var gpioSysfsOffset uint

func init() {
	gpioSysfsOffset = detectGPIOSysfsBase()
	log.Printf("info: GPIO sysfs base offset: %d", gpioSysfsOffset)
}

// detectGPIOSysfsBase rileva il base offset del controller GPIO BCM leggendo
// /sys/class/gpio/gpiochipXXX/label. Restituisce 0 se non riesce a rilevarlo
// (comportamento compatibile con kernel precedenti dove la base era 0).
func detectGPIOSysfsBase() uint {
	entries, err := os.ReadDir("/sys/class/gpio")
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "gpiochip") {
			continue
		}
		chipPath := "/sys/class/gpio/" + e.Name()
		label, err := os.ReadFile(chipPath + "/label")
		if err != nil {
			continue
		}
		// pinctrl-bcm2711 (Pi 4), pinctrl-bcm2835 (Pi 3/Zero)
		if strings.HasPrefix(strings.TrimSpace(string(label)), "pinctrl-bcm") {
			base, err := os.ReadFile(chipPath + "/base")
			if err != nil {
				continue
			}
			n, err := strconv.ParseUint(strings.TrimSpace(string(base)), 10, 32)
			if err != nil {
				continue
			}
			return uint(n)
		}
	}
	return 0
}

// pianoButton raggruppa lo stato di un singolo pulsante campanello per piano.
type pianoButton struct {
	used  bool
	pin   gpio.Pin
	pinNo uint
	state uint
	name  string // es. "P1", "P2", "P3"
}

// pianoButtons è la lista dei pulsanti piano configurabili via GPIO.
var pianoButtons = []*pianoButton{
	{name: "P1"},
	{name: "P2"},
	{name: "P3"},
}

// initGPIO inizializza il sottosistema GPIO, configura i pin di ingresso e avvia
// le goroutine di monitoraggio per i pulsanti dei campanelli dei piani.
func (b *DoorPhoneServer) initGPIO() {

	if err := rpio.Open(); err != nil {
		log.Println("error: GPIO Error, ", err)
		b.GPIOEnabled = false
		return
	}

	b.GPIOEnabled = true

	//handle inputs on RPI GPIO
	for _, io := range Config.Global.Hardware.IO.Pins.Pin {

		//log.Printf("debug: GPIO Setup Input Device[%v]-Name[%v]-PinNo[%v]-Direction[%v]-Enabled[%v]", io.Device, io.Name, io.PinNo, io.Direction, io.Enabled)

		if io.Enabled && io.Direction == "input" && io.Type == "gpio" {

			for _, pb := range pianoButtons {
				if io.Name == strings.ToLower(pb.name) && io.PinNo > 0 {
					log.Printf("debug: GPIO Setup Input Device %v Name %v PinNo %v", io.Device, io.Name, io.PinNo)
					p := rpio.Pin(io.PinNo)
					p.PullUp()
					pb.used = true
					pb.pinNo = io.PinNo
					break
				}
			}

		}

	}

	// rpio.Close() rimossa: chiudeva il driver GPIO prima di gpio.NewInput()
	// rpio.Close() verrà chiamata nel cleanup dell'applicazione

	for _, pb := range pianoButtons {
		if pb.used {
			pb.pin = gpio.NewInput(pb.pinNo + gpioSysfsOffset)
			go b.runPianoButtonLoop(pb)
		}
	}

}

// runPianoButtonLoop è la goroutine di monitoraggio per un pulsante piano GPIO.
func (b *DoorPhoneServer) runPianoButtonLoop(pb *pianoButton) {
	for {
		select {
		case <-GetGlobalContext().Done():
			return
		default:
		}
		if IsConnected.Load() {
			currentState, err := pb.pin.Read()
			time.Sleep(gpioDebounceDelay)
			if currentState != pb.state && err == nil {
				pb.state = currentState
				if pb.state == 1 {
					log.Printf("debug: %s Button is released", pb.name)
				} else {
					log.Printf("debug: %s Button is pressed", pb.name)
					b.cmdRingPiano(pb.name)
				}
			}
		} else {
			select {
			case <-GetGlobalContext().Done():
				return
			case <-time.After(1 * time.Second):
			}
		}
	}
}

// GPIOOutPin controlla un singolo pin GPIO di uscita per nome.
// @param name nome del pin GPIO come definito nella configurazione XML
// @param command comando da eseguire: "on", "off" o "pulse"
func GPIOOutPin(name string, command string) {

	for _, io := range Config.Global.Hardware.IO.Pins.Pin {

		if io.Enabled && io.Direction == "output" && io.Name == name {

			if command == "on" {
				if io.Log {
					log.Printf("debug: Turning On %v at pin %v Output GPIO\n", io.Name, io.PinNo)
				}
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, true)
			}

			if command == "off" {
				if io.Log {
					log.Printf("debug: Turning Off %v at pin %v Output GPIO\n", io.Name, io.PinNo)
				}
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, false)
			}

			if command == "pulse" {
				if io.Log {
					log.Printf("debug: Pulsing %v at pin %v Output GPIO\n", io.Name, io.PinNo)
				}
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, false)
				time.Sleep(Config.Global.Hardware.IO.Pulse.Leading * time.Millisecond)
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, true)
				time.Sleep(Config.Global.Hardware.IO.Pulse.Pulse * time.Millisecond)
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, false)
				time.Sleep(Config.Global.Hardware.IO.Pulse.Trailing * time.Millisecond)
			}

		}
	}
}

// GPIOOutAll controlla tutti i pin GPIO di uscita del dispositivo specificato.
// @param name nome del dispositivo GPIO (es. "led/relay")
// @param command comando da eseguire su tutti i pin: "on" o "off"
func GPIOOutAll(name string, command string) {

	for _, io := range Config.Global.Hardware.IO.Pins.Pin {
		if io.Enabled && io.Direction == "output" && io.Device == "led/relay" {
			if command == "on" {
				log.Printf("debug: Turning On %v Output GPIO\n", io.Name)
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, true)
			}
			if command == "off" {
				log.Printf("debug: Turning Off %v Output GPIO\n", io.Name)
				gpio.NewOutput(io.PinNo+gpioSysfsOffset, false)
			}
		}
	}
}

// GetGPIOState legge lo stato corrente di un pin GPIO per numero.
// @param pinNumber numero BCM del pin GPIO da leggere
// @return 0 se basso, 1 se alto, -1 in caso di errore; errore se non leggibile
func GetGPIOState(pinNumber int) (int, error) {
	var resultStatus int

	if err := rpio.Open(); err != nil {
		return -1, fmt.Errorf("failed to open rpio: %v", err)
	}

	defer rpio.Close()

	pin := rpio.Pin(pinNumber)
	pin.Output()

	pinstatus := pin.Read()

	switch pinstatus {
	case rpio.Low:
		resultStatus = 0
	case rpio.High:
		resultStatus = 1
	default:
		return -1, fmt.Errorf("unknown pin state")
	}

	return resultStatus, nil

}

// SetGPIOState imposta lo stato di un pin GPIO e restituisce lo stato risultante come JSON.
// @param device nome del dispositivo GPIO come definito nella configurazione XML
// @param status stato da impostare: "on" o "off"
// @return stringa JSON con il nome del device e lo stato risultante, o errore
func SetGPIOState(device string, status string) (string, error) {

	var resultStatus int

	GPIOOutPin(device, status)

	err := rpio.Open()
	if err != nil {
		fmt.Println(err)
		return "", fmt.Errorf("impossibile aprire pin")
	}
	defer rpio.Close()

	gpioNumber := GetGPIO(device)

	pin := rpio.Pin(gpioNumber)

	pin.Output()
	pinstatus := pin.Read()

	switch pinstatus {
	case rpio.Low:
		resultStatus = 0
	case rpio.High:
		resultStatus = 1
	default:
		return "", fmt.Errorf("unknown pin state")
	}

	deviceStatus := map[string]int{
		device: resultStatus,
	}

	data, err := json.Marshal(deviceStatus)
	if err != nil {
		return "", fmt.Errorf("error marshalling response to JSON: %w", err)
	}

	return string(data), nil

}

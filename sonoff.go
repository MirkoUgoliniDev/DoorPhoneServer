// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TasmotaStatus rappresenta la risposta JSON dello stato di un dispositivo con firmware Tasmota.
type TasmotaStatus struct {
	Status struct {
		Module       int
		DeviceName   string
		FriendlyName []string
		Topic        string
		ButtonTopic  string
		Power        string
		PowerOnState int
		LedState     int
		LedMask      string
		SaveData     int
		SaveState    int
		SwitchTopic  string
		SwitchMode   []int
		ButtonRetain int
		SwitchRetain int
		SensorRetain int
		PowerRetain  int
		InfoRetain   int
		StateRetain  int
		StatusRetain int
	} `json:"Status"`
}

// SonOffStatus rappresenta la risposta JSON dello stato di un dispositivo SonOff con protocollo zeroconf.
type SonOffStatus struct {
	Seq   uint32
	Error int
	Data  struct {
		Switch         string
		Startup        string
		Pulse          string
		PulseWidth     int
		Ssid           string
		OtaUnlock      bool
		FwVersion      string
		Deviceid       string
		Bssid          string
		SignalStrength int
	} `json:"data"`
}

// Sonoff rappresenta una collezione di dispositivi SonOff/Tasmota configurati.
type Sonoff struct {
	// Devices è la lista dei dispositivi SonOff gestiti
	Devices []Device
}

// Device rappresenta un singolo dispositivo SonOff/Tasmota con la sua configurazione.
type Device struct {
	Name    string
	Type    string
	URL     string
	Enabled bool
	Log     bool
	Desc    string
	Status  string
}

//var configPath = ""
//var ctx context.Context

// GetSonOffUrl restituisce l'URL del dispositivo SonOff con il nome specificato.
// @param name nome del dispositivo SonOff come definito nella configurazione XML
// @return URL del dispositivo, o stringa vuota se non trovato o SonOff disabilitato
func GetSonOffUrl(name string) string {
	var url string
	if !Config.Global.Hardware.IO.Sonoff.Enabled {
		url = ""
	}
	for _, sonoff := range Config.Global.Hardware.IO.Sonoff.Device {
		if sonoff.Name == name {
			url = sonoff.Url
		}
	}
	return url
}

// GetSonOffLOG restituisce se il logging dettagliato è abilitato per il dispositivo specificato.
// @param name nome del dispositivo SonOff come definito nella configurazione XML
// @return true se il logging è abilitato, false altrimenti
func GetSonOffLOG(name string) bool {
	var flag bool
	if Config.Global.Hardware.IO.Sonoff.Enabled {
		for _, sonoff := range Config.Global.Hardware.IO.Sonoff.Device {
			if sonoff.Name == name {
				flag = sonoff.Log
			}
		}
	}
	return flag
}

// GetSonOffType restituisce il tipo del dispositivo SonOff (es. "tasmota", "sonoff").
// @param name nome del dispositivo come definito nella configurazione XML
// @return tipo del dispositivo, o stringa vuota se non trovato o SonOff disabilitato
func GetSonOffType(name string) string {
	if !Config.Global.Hardware.IO.Sonoff.Enabled {
		return ""
	}
	for _, sonoff := range Config.Global.Hardware.IO.Sonoff.Device {
		if sonoff.Name == name {
			return sonoff.Type
		}
	}
	return ""
}

// Control_SonOff_Device imposta lo stato di un dispositivo SonOff/Tasmota tramite HTTP.
// @param device nome del dispositivo da controllare
// @param status stato da impostare: "on" o "off"
// @return stringa JSON con esito dell'operazione o errore
func Control_SonOff_Device(device string, status string) (string, error) {

	result, err := Tasmota_Set_Status(device, status)
	return result, err
}

// Get_SonOff_DeviceStatus legge lo stato corrente di un dispositivo SonOff/Tasmota.
// @param device nome del dispositivo come definito nella configurazione XML
// @return 1 se acceso, 0 se spento, o errore se non raggiungibile
func Get_SonOff_DeviceStatus(device string) (int, error) {
	var status int
	var err error
	status, err = Tasmota_Get_Status(device)
	return status, err
}

// SonOff_Set_Status imposta lo stato di un dispositivo SonOff originale tramite protocollo zeroconf.
// @param device nome del dispositivo come definito nella configurazione XML
// @param status stato da impostare: "on" o "off"
func SonOff_Set_Status(device string, status string) {
	var IP string
	var url string
	var Log bool
	var err error

	Log = GetSonOffLOG(device)
	IP = GetSonOffUrl(device)

	url = IP + "/zeroconf/switch"

	if Log {
		log.Printf("ENDPOINT: %#v\n", url)
	}

	type Message struct {
		Data struct{ Switch string }
	}

	c := new(Message)
	c.Data.Switch = status

	u, err := json.Marshal(c)

	if err != nil {
		log.Printf("error: SonOff JSON marshal failed: %v\n", err)
		return
	}

	if Log {
		log.Printf("%#v\n", string(u))
	}

	var jsonData = []byte(string(u))
	responseBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequest("POST", url, responseBody)
	if err != nil {
		log.Printf("error: SonOff HTTP request creation failed: %v\n", err)
		return
	}

	req.Header.Set("user-agent", "insomnia/2022.4.2")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("accept", "*/*")
	req.Header.Set("content-length", strconv.Itoa(responseBody.Len()))

	client := &http.Client{}

	resp, err := client.Do(req)

	if err != nil {
		log.Printf("error: SonOff HTTP request failed: %v\n", err)
		return
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error: SonOff reading response body failed: %v\n", err)
		return
	}

	if Log {
		log.Printf("Response: %#v\n", string(body))
	}

}

// SonOff_Get_Status legge lo stato di un dispositivo SonOff originale tramite protocollo zeroconf.
// @param device nome del dispositivo come definito nella configurazione XML
// @return 1 se acceso, 0 se spento, o errore se non raggiungibile
func SonOff_Get_Status(device string) (int, error) {
	var Log bool
	var err error
	var IP string
	var url string

	Log = GetSonOffLOG(device)
	IP = GetSonOffUrl(device)

	url = IP + "/zeroconf/info"

	if Log {
		log.Printf("ENDPOINT: %#v\n", url)
	}

	type Message struct {
		Deviceid string
		Data     struct{}
	}

	c := Message{Deviceid: device}

	jsonData, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("error marshalling JSON: %v", err)
	}

	responseBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequest("POST", url, responseBody)
	if err != nil {
		return 0, fmt.Errorf("error creating HTTP request: %v", err)
	}

	req.Header.Set("user-agent", "insomnia/2022.4.2")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("accept", "*/*")
	req.Header.Set("content-length", strconv.Itoa(responseBody.Len()))

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, fmt.Errorf("error performing HTTP request: %v", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("error reading response body: %v", err)
	}

	var sonoffStatus SonOffStatus
	err = json.Unmarshal(body, &sonoffStatus)
	if err != nil {
		return 0, fmt.Errorf("error unmarshalling JSON response: %v", err)
	}

	status := 0
	if sonoffStatus.Data.Switch == "on" {
		status = 1
	}

	if Log {
		log.Printf("Switch: %#v\n", sonoffStatus.Data.Switch)
		log.Printf("Status: %#v\n", status)
	}

	return status, nil
}

// Tasmota_Set_Status imposta lo stato di un dispositivo con firmware Tasmota tramite HTTP GET.
// Ritenta fino a 4 volte in caso di risposta errata, con pausa di 2 secondi tra i tentativi.
// @param device nome del dispositivo come definito nella configurazione XML
// @param status stato da impostare: "on" o "off"
// @return stringa JSON con esito dell'operazione o errore
func Tasmota_Set_Status(device string, status string) (string, error) {
	if device == "" || (status != "on" && status != "off") {
		return "", fmt.Errorf("invalid device or status: device=%s, status=%s", device, status)
	}

	logEnabled := GetSonOffLOG(device)
	deviceIP := GetSonOffUrl(device)

	if deviceIP == "" {
		return "", fmt.Errorf("device %s not found or disabled", device)
	}

	url := fmt.Sprintf("%s/cm?cmnd=power%%20%s", deviceIP, strings.ToUpper(status))
	expectedResponse := fmt.Sprintf(`{"POWER":"%s"}`, strings.ToUpper(status))

	if logEnabled {
		log.Printf("Sending request to Tasmota device: %s", url)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for attempts := 0; attempts < 4; attempts++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", fmt.Errorf("error creating request: %w", err)
		}

		req.Header.Set("user-agent", "insomnia/2022.4.2")
		req.Header.Set("Content-Type", "text/html; charset=UTF-8")
		req.Header.Set("accept", "*/*")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error sending request: %v", err)
			if attempts < 3 {
				time.Sleep(2 * time.Second)
				continue
			}
			return "", fmt.Errorf("error sending request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("error reading response body: %w", err)
		}

		if logEnabled {
			log.Printf("Response received: %s", body)
		}

		if string(body) == expectedResponse {
			return fmt.Sprintf(`{"device":"%s","status":"%s"}`, device, status), nil
		}

		log.Printf("Unexpected response, retrying... (%d/3)", attempts+1)
		time.Sleep(2 * time.Second)
	}

	return "", fmt.Errorf("failed to set status for device %s after retries", device)
}

// Tasmota_Get_Status legge lo stato di un dispositivo con firmware Tasmota tramite HTTP GET.
// @param device nome del dispositivo come definito nella configurazione XML
// @return 1 se acceso, 0 se spento, o errore se non raggiungibile
func Tasmota_Get_Status(device string) (int, error) {
	var Log bool
	var IP string
	var url string

	Log = GetSonOffLOG(device)
	IP = GetSonOffUrl(device)

	url = IP + "/cm?cmnd=status"

	if Log {
		log.Printf("ENDPOINT: %#v\n", url)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("error: Failed to retrieve Tasmota status: %v\n", err)
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("error: Unexpected status code: %d\n", resp.StatusCode)
		return 0, errors.New("unexpected status code")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error: Failed to read Tasmota status response: %v\n", err)
		return 0, err
	}

	var tasmotaStatus TasmotaStatus
	err = json.Unmarshal(body, &tasmotaStatus)
	if err != nil {
		log.Printf("error: Failed to unmarshal Tasmota status JSON: %v\n", err)
		return 0, err
	}

	power := 0
	if strings.ToUpper(tasmotaStatus.Status.Power) == "ON" || tasmotaStatus.Status.Power == "1" {
		power = 1
	}

	if Log {
		log.Printf("Power status: %s (converted to %d)\n", tasmotaStatus.Status.Power, power)
	}

	return power, nil

}

// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"crypto/tls"
	"log"
	"strconv"
	"strings"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"
)

// mqttsubscribe si connette al broker MQTT configurato e si iscrive al topic specificato.
// Supporta riconnessione automatica e rimane attivo fino alla cancellazione del contesto globale.
func (b *DoorPhoneServer) mqttsubscribe() {

	log.Printf("info: MQTT Subscription Information")
	log.Printf("info: MQTT Broker      : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTBroker)
	log.Printf("debug: MQTT clientid    : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTId)
	log.Printf("debug: MQTT user        : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTUser)
	log.Printf("debug: MQTT password    : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTPassword)
	log.Printf("info: Subscribed topic : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTTopic)

	connOpts := MQTT.NewClientOptions().AddBroker(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTBroker).SetClientID(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTId).SetCleanSession(true)
	connOpts.SetAutoReconnect(true)
	connOpts.SetMaxReconnectInterval(2 * time.Minute)
	connOpts.SetConnectionLostHandler(func(c MQTT.Client, err error) {
		log.Printf("alert: MQTT connection lost: %v. Auto-reconnecting...", err)
	})
	if Config.Global.Software.RemoteControl.MQTT.Settings.MQTTUser != "" {
		connOpts.SetUsername(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTUser)
		if Config.Global.Software.RemoteControl.MQTT.Settings.MQTTPassword != "" {
			connOpts.SetPassword(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTPassword)
		}
	}
	tlsConfig := &tls.Config{InsecureSkipVerify: true, ClientAuth: tls.NoClientCert}
	connOpts.SetTLSConfig(tlsConfig)

	connOpts.OnConnect = func(c MQTT.Client) {
		if token := c.Subscribe(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTTopic, byte(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTQos), b.onMessageReceived); token.Wait() && token.Error() != nil {
			log.Printf("error: MQTT subscription failed: %v", token.Error())
			return
		}
		log.Println("info: MQTT subscription successful")
	}

	client := MQTT.NewClient(connOpts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		log.Printf("error: MQTT connection failed: %v", token.Error())
		return
	}
	log.Printf("info: Connected to     : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTBroker)

	<-GetGlobalContext().Done()
	client.Disconnect(250)
	log.Println("info: MQTT client disconnected")
}

// onMessageReceived è il callback invocato quando viene ricevuto un messaggio MQTT.
// Analizza il payload, verifica che il comando sia definito nella configurazione e lo esegue.
// @param client client MQTT che ha ricevuto il messaggio
// @param message messaggio MQTT ricevuto con topic e payload
func (b *DoorPhoneServer) onMessageReceived(client MQTT.Client, message MQTT.Message) {

	var (
		CommandDefined bool
		PayLoad        string
	)

	funcs := map[string]interface{}{
		"relay": relay}

	PayLoad = strings.ToLower(string(message.Payload()))
	log.Printf("info: Received MQTT message on topic: %s Payload: %s\n", message.Topic(), PayLoad)

	for _, mqttcommand := range Config.Global.Software.RemoteControl.MQTT.Commands.Command {
		if strings.Contains(PayLoad, strings.ToLower(mqttcommand.Action)) {
			CommandDefined = true
		}
	}

	if !CommandDefined {
		log.Printf("error: MQTT Command %v Not Defined\n", PayLoad)
		return
	}

	Command := strings.Split(strings.ToLower(PayLoad), ":")

	for _, mqttcommand := range Config.Global.Software.RemoteControl.MQTT.Commands.Command {
		if strings.Contains(Command[0], mqttcommand.Action) {
			if mqttcommand.Enabled {
				var Err error
				switch Command[0] {
				case "relay":
					if len(Command) == 4 && Command[2] == "pulse" {
					_, Err = b.Call(funcs, mqttcommand.Action, "pulse", Command[1])
					} else if len(Command) == 3 {
						if Command[2] == "on" {
							_, Err = b.Call(funcs, mqttcommand.Action, "on", Command[1])
						}
						if Command[2] == "off" {
							_, Err = b.Call(funcs, mqttcommand.Action, "off", Command[1])
						}
					} else {
						log.Println("error: Malformed MQTT Command")
					}

				}

				if Err == nil {
					log.Printf("MQTT Command %v Processed", Command)
				} else {
					log.Printf("error: MQTT Command %v Failed", Command)
				}
			}
		}
	}
}


// relay controlla un relè GPIO tramite il suo numero identificativo.
// Accetta solo i relè 1 o 2, numeri fuori range vengono ignorati.
// @param command azione da eseguire: "on", "off" o "pulse"
// @param no numero del relè come stringa ("1" o "2")
func relay(command string, no string) {
	number := no
	checkno, err := strconv.Atoi(no)
	if err != nil || checkno == 0 || checkno > 2 {
		return
	}
	switch command {
	case "pulse":
		GPIOOutPin("relay"+number, "pulse")
	case "on":
		GPIOOutPin("relay"+number, "on")
	case "off":
		GPIOOutPin("relay"+number, "off")
	}
}

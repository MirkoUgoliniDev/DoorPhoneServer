// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/comail/colog"
	"github.com/MirkoUgoliniDev/gumble/gumble"
	"github.com/MirkoUgoliniDev/gumble/gumbleffmpeg"
	"golang.org/x/sys/unix"
)

// readDotEnvKey legge il valore di una singola chiave dal file .env al momento della chiamata,
// senza usare os.Getenv, così i cambiamenti al file hanno effetto immediato senza riavvio.
func readDotEnvKey(key string) string {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(ConfigXMLFile), ".env"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) == key {
			return strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		}
	}
	return ""
}

// loadDotEnv carica le variabili dal file .env nella stessa directory del file XML
// e le imposta come variabili d'ambiente del processo. Valori già presenti non vengono sovrascritti.
func loadDotEnv(dir string) {
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// applyEnvOverrides sovrascrive i campi sensibili della config con i valori dal .env se presenti.
func applyEnvOverrides() {
	if v := os.Getenv("MUMBLE_USERNAME"); v != "" {
		for i := range Config.Accounts.Account {
			Config.Accounts.Account[i].UserName = v
		}
	}
	if v := os.Getenv("MUMBLE_PASSWORD"); v != "" {
		for i := range Config.Accounts.Account {
			Config.Accounts.Account[i].Password = v
		}
	}
	if v := os.Getenv("CAMERA_USERNAME"); v != "" {
		Config.Global.Software.Camera.Username = v
	}
	if v := os.Getenv("CAMERA_PASSWORD"); v != "" {
		Config.Global.Software.Camera.Password = v
	}
	if v := os.Getenv("PUSHOVER_API_TOKEN"); v != "" {
		Config.Global.Software.PUSHOVER.APIToken = v
	}
	if v := os.Getenv("PUSHOVER_USER_KEY"); v != "" {
		Config.Global.Software.PUSHOVER.UserKey = v
	}
}

// ConfigStruct rappresenta la struttura completa della configurazione XML di doorphoneserver,
// inclusi account Mumble, impostazioni software, hardware GPIO, MQTT, HTTP API e TTS.
type ConfigStruct struct {
	XMLName  xml.Name `xml:"document"`
	Accounts struct {
		Account []struct {
			Name          string `xml:"name,attr"`
			Default       bool   `xml:"default,attr"`
			ServerAndPort string `xml:"serverandport"`
			UserName      string `xml:"-"`
			Password      string `xml:"-"`
			Insecure      bool   `xml:"insecure"`
			Register      bool   `xml:"register"`
			Certificate   string `xml:"certificate"`
			Channel       string `xml:"channel"`
			Ident         string `xml:"ident"`
		} `xml:"account"`
	} `xml:"accounts"`
	Global struct {
		Software struct {
			Settings struct {
				SingleInstance          bool          `xml:"singleinstance"`
				OutputDevice            string        `xml:"outputdevice"`
				OutputVolControlDevice  string        `xml:"outputvolcontroldevice"`
				OutputMuteControlDevice string        `xml:"outputmutecontroldevice"`
				LogFilenameAndPath      string        `xml:"logfilenameandpath"`
				Logging                 string        `xml:"logging"`
				Loglevel                string        `xml:"loglevel"`
				LogMaxSizeMB            int           `xml:"logmaxsizemb"`
				LogRetentionDays        int           `xml:"logretentiondays"`
				CancellableStream       bool          `xml:"cancellablestream"`
				StreamOnStart           bool          `xml:"streamonstart"`
				StreamOnStartAfter      time.Duration `xml:"streamonstartafter"`
				StreamSendMessage       bool          `xml:"streamsendmessage"`
				TXOnStart               bool          `xml:"txonstart"`
				TXOnStartAfter          time.Duration `xml:"txonstartafter"`
				RepeatTXTimes           int           `xml:"repeattxtimes"`
				RepeatTXDelay           time.Duration `xml:"repeattxdelay"`
				SimplexWithMute         bool          `xml:"simplexwithmute"`
			} `xml:"settings"`
			TTS struct {
				Enabled     bool   `xml:"enabled,attr"`
				Volumelevel int    `xml:"volumelevel"`
				Language    string `xml:"language,attr"`
				Sound       []struct {
					Action   string `xml:"action,attr"`
					File     string `xml:"file,attr"`
					Blocking bool   `xml:"blocking,attr"`
					Enabled  bool   `xml:"enabled,attr"`
				} `xml:"sound"`
			} `xml:"tts"`
			PUSHOVER struct {
				Enabled  bool   `xml:"enabled,attr"`
				APIToken string `xml:"-"`
				UserKey  string `xml:"-"`
				Log      bool   `xml:"log,attr"`
			} `xml:"pushover"`
			Camera struct {
				Debug bool `xml:"debug,attr"`
				Video struct {
					Enabled  bool   `xml:"enabled,attr"`
					Endpoint string `xml:"endpoint"`
				} `xml:"video"`
				Snapshot struct {
					Enabled       bool   `xml:"enabled,attr"`
					Method        string `xml:"method"`
					Endpoint      string `xml:"endpoint"`
					Dir           string `xml:"dir"`
					MaxSnapshots  int    `xml:"maxsnapshots"`
					RetentionDays int    `xml:"retentiondays"`
				} `xml:"snapshot"`
				Username  string `xml:"-"`
				Password  string `xml:"-"`
				OnvifPort int    `xml:"onvifport"`
			} `xml:"camera"`


			RemoteControl struct {
				XMLName xml.Name `xml:"remotecontrol"`
				HTTP    struct {
					Enabled    bool   `xml:"enabled,attr"`
					ListenPort string `xml:"listenport,attr"`
					Command    []struct {
						Action        string `xml:"action,attr"`
						Funcname      string `xml:"funcname,attr"`
						Funcparamname string `xml:"funcparamname,attr"`
						Message       string `xml:"message,attr"`
						Enabled       bool   `xml:"enabled,attr"`
					} `xml:"command"`
				} `xml:"http"`

				MQTT struct {
					Enabled  bool `xml:"enabled,attr"`
					Settings struct {
						MQTTEnabled             bool   `xml:"enabled,attr"`
						MQTTTopic               string `xml:"mqtttopic"`
						MQTTBroker              string `xml:"mqttbroker"`
						MQTTPassword            string `xml:"mqttpassword"`
						MQTTUser                string `xml:"mqttuser"`
						MQTTId                  string `xml:"mqttid"`
						MQTTQos                 int    `xml:"qos"`
					} `xml:"settings"`
					Commands struct {
						Command []struct {
							Action  string `xml:"action,attr"`
							Message string `xml:"message,attr"`
							Enabled bool   `xml:"enabled,attr"`
						} `xml:"command"`
					} `xml:"commands"`
				} `xml:"mqtt"`
			}


			PrintVariables struct {
				PrintAccount        bool `xml:"printaccount"`
				PrintSystemSettings bool `xml:"printsystemsettings"`
				PrintProvisioning   bool `xml:"printprovisioning"`
				PrintTTS            bool `xml:"printtts"`
				PrintSounds         bool `xml:"printsounds"`
				PrintTxTimeout      bool `xml:"printtxtimeout"`
				PrintHTTPAPI        bool `xml:"printhttpapi"`
				PrintMQTT           bool `xml:"printmqtt"`
				PrintHardware       bool `xml:"printhardware"`
				PrintPins           bool `xml:"printpins"`
				PrintPulse          bool `xml:"printpulse"`
				PrintHeartBeat      bool `xml:"printheartbeat"`
				PrintComment        bool `xml:"printcomment"`
				PrintAudioRecord    bool `xml:"printaudiorecord"`
				PrintKeyboardMap    bool `xml:"printkeyboardmap"`
				PrintMultimedia     bool `xml:"printmultimedia"`
			} `xml:"printvariables"`
			Tablet struct {
				Enabled bool `xml:"enabled,attr"`
				P1      struct {
					KioskMode             bool `xml:"kioskmode,attr"`
					HideStatusBar         bool `xml:"hide_status_bar,attr"`
					MicLevel              int  `xml:"miclevel,attr"`
					SpeakerLevel          int  `xml:"speakerlevel,attr"`
					ScreenBrightnessLevel int  `xml:"screenbrightnesslevel,attr"`
				} `xml:"p1"`
				P2 struct {
					KioskMode             bool `xml:"kioskmode,attr"`
					HideStatusBar         bool `xml:"hide_status_bar,attr"`
					MicLevel              int  `xml:"miclevel,attr"`
					SpeakerLevel          int  `xml:"speakerlevel,attr"`
					ScreenBrightnessLevel int  `xml:"screenbrightnesslevel,attr"`
				} `xml:"p2"`
				P3 struct {
					KioskMode             bool `xml:"kioskmode,attr"`
					HideStatusBar         bool `xml:"hide_status_bar,attr"`
					MicLevel              int  `xml:"miclevel,attr"`
					SpeakerLevel          int  `xml:"speakerlevel,attr"`
					ScreenBrightnessLevel int  `xml:"screenbrightnesslevel,attr"`
				} `xml:"p3"`
				P4 struct {
					KioskMode             bool `xml:"kioskmode,attr"`
					HideStatusBar         bool `xml:"hide_status_bar,attr"`
					MicLevel              int  `xml:"miclevel,attr"`
					SpeakerLevel          int  `xml:"speakerlevel,attr"`
					ScreenBrightnessLevel int  `xml:"screenbrightnesslevel,attr"`
				} `xml:"p4"`
			} `xml:"tablet"`
		} `xml:"software"`
		Hardware struct {
			VoiceActivityTimermsecs time.Duration `xml:"voiceactivitytimermsecs"`
			IO                      struct {
				Pins struct {
					Pin []struct {
						Direction string `xml:"direction,attr"`
						Device    string `xml:"device,attr"`
						Name      string `xml:"name,attr"`
						PinNo     uint   `xml:"pinno,attr"`
						Type      string `xml:"type,attr"`
						ID        int    `xml:"chipid,attr"`
						Enabled   bool   `xml:"enabled,attr"`
						Log       bool   `xml:"log,attr"`
					} `xml:"pin"`
				} `xml:"pins"`
				Pulse struct {
					Leading  time.Duration `xml:"leadingmsecs,attr"`
					Pulse    time.Duration `xml:"pulsemsecs,attr"`
					Trailing time.Duration `xml:"trailingmsecs,attr"`
				} `xml:"pulse"`

				Sonoff struct {
					Enabled bool `xml:"enabled,attr"`
					Device  []struct {
						Name    string `xml:"name,attr"`
						Type    string `xml:"type,attr"`
						Url     string `xml:"url,attr"`
						Enabled bool   `xml:"enabled,attr"`
						Log     bool   `xml:"log,attr"`
						Desc    string `xml:"desc,attr"`
						Status  string `xml:"status,attr"`
					} `xml:"device"`
				} `xml:"sonoff"`
			} `xml:"io"`
			HeartBeat struct {
				Enabled     bool   `xml:"enabled,attr"`
				LEDPin      string `xml:"heartbeatledpin"`
				Periodmsecs int    `xml:"periodmsecs"`
				LEDOnmsecs  int    `xml:"ledonmsecs"`
				LEDOffmsecs int    `xml:"ledoffmsecs"`
			} `xml:"heartbeat"`

		} `xml:"hardware"`
	} `xml:"global"`
}

// VTStruct rappresenta la struttura per la gestione delle voice trigger (ID vocali)
// con utenti e canali associati per il controllo basato su riconoscimento vocale.
type VTStruct struct {
	ID []struct {
		Value uint32
		Users struct {
			User []string
		}
		Channels struct {
			Channel []struct {
				Name      string
				Recursive bool
				Links     bool
				Group     string
			}
		}
	}
}

// TTYKBStruct rappresenta un mapping di tasto tastiera a comando doorphoneserver,
// utilizzato per il controllo dell'applicazione tramite input da terminale TTY.
type TTYKBStruct struct {
	Enabled    bool
	KeyLabel   uint32
	Command    string
	ParamName  string
	ParamValue string
}


// InputEventSoundFileStruct associa un evento di input a un file audio da riprodurre,
// con il flag di abilitazione per attivare o disattivare il suono dell'evento.
type InputEventSoundFileStruct struct {
	Event   string
	File    string
	Enabled bool
}

// streamTrackerStruct tiene traccia di un flusso audio Mumble attivo per un utente specifico,
// con il canale per ricevere i pacchetti audio in ingresso.
// Cancel permette di terminare la goroutine associata allo stream.
// CreatedAt registra il timestamp di creazione per debug e cleanup.
type streamTrackerStruct struct {
	UserID      uint32
	UserName    string
	UserSession uint32
	C           <-chan *gumble.AudioPacket
	Cancel      context.CancelFunc // Funzione per terminare la goroutine
	CreatedAt   time.Time          // Timestamp creazione stream
}

// talkingStruct rappresenta lo stato di trasmissione vocale corrente,
// indicando se qualcuno sta parlando e il nome dell'utente che parla.
type talkingStruct struct {
	IsTalking  bool
	WhoTalking string
}

// Generic Global Config Variables
// Config contiene la configurazione globale caricata dal file XML.
var Config ConfigStruct

// ConfigXMLFile è il percorso assoluto del file di configurazione XML attivo.
var ConfigXMLFile string

// Generic Global State Variables
// Variabili atomiche per lo stato globale del sistema, accessibili in modo thread-safe.
// KillHeartBeat segnala l'arresto del LED heartbeat.
// IsPlayStream indica se è in corso la riproduzione di uno stream audio.
// IsConnected indica se il client è connesso al server Mumble.
// Streaming indica se lo streaming audio locale è attivo.
// HTTPServRunning indica se il server HTTP API è in esecuzione.
// NowStreaming indica se lo streaming verso il canale Mumble è attivo.
// InStreamTalking indica se un utente sta parlando durante lo streaming.
// InStreamSource indica se la sorgente dello stream è attiva.
// HeartBeatCount contiene il contatore dei battiti del LED heartbeat.
// HeartBeatLastTime contiene il timestamp Unix dell'ultimo heartbeat.
var (
	KillHeartBeat        atomic.Bool
	IsPlayStream         atomic.Bool
	IsConnected          atomic.Bool
	Streaming            atomic.Bool
	HTTPServRunning      atomic.Bool
	NowStreaming         atomic.Bool
	InStreamTalking      atomic.Bool
	InStreamSource       atomic.Bool
	MumbleServiceStopped atomic.Bool // set when user intentionally stops mumble-server from web panel
	HeartBeatCount       atomic.Int64
	HeartBeatLastTime    atomic.Int64
)

// Generic Global Counter Variables
// AccountCount è il numero di account Mumble abilitati (default) trovati nel config.
// ConnectAttempts è il numero totale di tentativi di connessione effettuati.
// AccountIndex è l'indice dell'account Mumble attualmente in uso.
// GenericCounter è un contatore generico di uso generale.
// ChannelIndex è l'indice del canale Mumble correntemente selezionato.
var (
	AccountCount    int
	ConnectAttempts int
	AccountIndex    int
	GenericCounter  int
	ChannelIndex    int
)

// Generic Global Timer Variables
// StartTime è il timestamp di avvio dell'applicazione.
// LastTime è il timestamp Unix dell'ultimo evento rilevante.
// TalkedTicker è il ticker usato per il polling dello stato di trasmissione vocale (ogni 200ms).
// Talking è il canale usato per notificare i cambiamenti di stato di chi parla.
var (
	StartTime    = time.Now()
	LastTime     = now.Unix()
	TalkedTicker = time.NewTicker(time.Millisecond * 200)
	Talking      = make(chan talkingStruct, 10)
)

// TTYKeyMap mappa i tasti della tastiera TTY ai comandi doorphoneserver configurati.
var (
	TTYKeyMap = make(map[rune]TTYKBStruct)
)

// Mumble Account Settings Global Variables
// Slice parallele contenenti i parametri di connessione per ogni account Mumble abilitato.
// Default, Name, Server, Username, Password, Insecure, Register, Certificate, Channel, Ident
// sono popolate da readxmlconfig() leggendo gli account con Default=true dal file XML.
// VT contiene le strutture voice trigger per ogni account.
// Accounts è il numero totale di account caricati.
var (
	Default     []bool
	Name        []string
	Server      []string
	Username    []string
	Password    []string
	Insecure    []bool
	Register    []bool
	Certificate []string
	Channel     []string
	Ident       []string
	VT          []VTStruct
	Accounts    int
)

// Generic Local Variables
// pstream è lo stream audio gumbleffmpeg correntemente attivo per la trasmissione nel canale Mumble.
// pstreamMu protegge pstream da accessi concorrenti (su ARM 32-bit i puntatori non sono atomici).
// LastSpeaker è il nome dell'ultimo utente che ha parlato nel canale.
var (
	pstream     *gumbleffmpeg.Stream
	pstreamMu   sync.Mutex
	LastSpeaker string = ""
)

// StreamTracker mappa gli UserID Mumble alle relative strutture di tracciamento dello stream audio.
var StreamTracker = map[uint32]streamTrackerStruct{}

// StreamTrackerMu è il mutex per l'accesso thread-safe a StreamTracker.
var StreamTrackerMu sync.RWMutex

// readxmlconfig legge e analizza il file di configurazione XML di doorphoneserver.
// Alla prima lettura (reloadxml=false) popola Config e le slice degli account Mumble.
// Con reloadxml=true aggiorna solo i parametri modificabili a caldo senza riavvio.
// Chiama CheckConfigSanity per validare la configurazione dopo il parsing.
// @param file percorso del file XML di configurazione da leggere
// @param reloadxml true per aggiornamento a caldo dei parametri modificabili, false per caricamento iniziale
// @return errore se il file non può essere aperto o il parsing XML fallisce
func readxmlconfig(file string, reloadxml bool) error {
	var ReConfig ConfigStruct

	xmlFile, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("error opening file %s: %v", file, err)
	}
	log.Println("info: Successfully Read file " + filepath.Base(file))
	defer xmlFile.Close()

	if !reloadxml {
		if abs, err := filepath.Abs(file); err == nil {
			ConfigXMLFile = abs
		} else {
			ConfigXMLFile = file
		}
	}

	byteValue, _ := io.ReadAll(xmlFile)

	if !reloadxml {
		err = xml.Unmarshal(byteValue, &Config)
		if err != nil {
			return fmt.Errorf("error unmarshalling file %s: %v", filepath.Base(file), err)
		}
		loadDotEnv(filepath.Dir(ConfigXMLFile))
		applyEnvOverrides()
		BaichuanDebug = Config.Global.Software.Camera.Debug
	} else {
		err = xml.Unmarshal(byteValue, &ReConfig)
		if err != nil {
			return fmt.Errorf("error unmarshalling file %s: %v", filepath.Base(file), err)
		}
	}

	CheckConfigSanity(reloadxml)

	if !reloadxml {
		for _, account := range Config.Accounts.Account {
			if account.Default {
				Name = append(Name, account.Name)
				Server = append(Server, account.ServerAndPort)
				Username = append(Username, account.UserName)
				Password = append(Password, account.Password)
				Insecure = append(Insecure, account.Insecure)
				Register = append(Register, account.Register)
				Certificate = append(Certificate, account.Certificate)
				Channel = append(Channel, account.Channel)
				Ident = append(Ident, account.Ident)
				AccountCount++
			}
		}
	}

	exec, err := os.Executable()

	if err != nil {
		exec = "./doorphoneserver" //Hardcode our default name
	}

	// Set our default config file path (for autoprovision)
	defaultConfPath, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		FatalCleanUp("Unable to get path for config file " + err.Error())
	}

	// Set our default logging path
	//This section is pretty unix specific.. sorry if you like windows support.
	defaultLogPath := "/tmp/" + filepath.Base(exec) + ".log" // Safe assumption as it should be writable for everyone
	// First see if we can write in our CWD and use it over /tmp
	cwd, err := os.Getwd()
	if err == nil {
		cwd, err := filepath.Abs(cwd)
		if err == nil {
			if unix.Access(cwd, unix.W_OK) == nil {
				defaultLogPath = cwd + "/" + filepath.Base(exec) + ".log"
			}
		}
	}

	// Next try a file in our config path and favor it over CWD
	if unix.Access(defaultConfPath, unix.W_OK) == nil {
		defaultLogPath = defaultConfPath + "/" + filepath.Base(exec) + ".log"
	}

	// Last, see if the system doorphoneserver log exists and is writeable and do that over CWD, HOME and /tmp
	if _, err := os.Stat("/var/log/" + filepath.Base(exec) + ".log"); err == nil {
		f, err := os.OpenFile("/var/log/"+filepath.Base(exec)+".log", os.O_WRONLY, 0664)
		if err == nil {
			defaultLogPath = "/var/log/" + filepath.Base(exec) + ".log"
		}
		f.Close()
	}


	if len(Config.Global.Software.Settings.OutputVolControlDevice) == 0 {
		Config.Global.Software.Settings.OutputVolControlDevice = Config.Global.Software.Settings.OutputDevice
	}
	if len(Config.Global.Software.Settings.OutputMuteControlDevice) == 0 {
		Config.Global.Software.Settings.OutputMuteControlDevice = Config.Global.Software.Settings.OutputDevice
	}

	if strings.ToLower(Config.Global.Software.Settings.Logging) != "screen" && Config.Global.Software.Settings.LogFilenameAndPath == "" {
		Config.Global.Software.Settings.LogFilenameAndPath = defaultLogPath
	}

	if Config.Global.Software.Settings.LogMaxSizeMB <= 0 {
		Config.Global.Software.Settings.LogMaxSizeMB = 5
	}
	if Config.Global.Software.Settings.LogRetentionDays <= 0 {
		Config.Global.Software.Settings.LogRetentionDays = 7
	}

	if !reloadxml {
		if Config.Global.Hardware.VoiceActivityTimermsecs == 0 {
			Config.Global.Hardware.VoiceActivityTimermsecs = 200
		}
	}

	log.Println("info: Successfully loaded XML configuration file into memory")

	// Add Allowed Mutable Settings For doorphoneserver upon live reloadxml config to the list below omit all other variables
	if reloadxml {

		if Config.Global.Software.Settings.Loglevel != ReConfig.Global.Software.Settings.Loglevel {
			Config.Global.Software.Settings.Loglevel = ReConfig.Global.Software.Settings.Loglevel
			switch Config.Global.Software.Settings.Loglevel {
			case "trace":
				colog.SetMinLevel(colog.LTrace)
				log.Println("info: Loglevel Set to Trace")
			case "debug":
				colog.SetMinLevel(colog.LDebug)
				log.Println("info: Loglevel Set to Debug")
			case "info":
				colog.SetMinLevel(colog.LInfo)
				log.Println("info: Loglevel Set to Info")
			case "warning":
				colog.SetMinLevel(colog.LWarning)
				log.Println("info: Loglevel Set to Warning")
			case "error":
				colog.SetMinLevel(colog.LError)
				log.Println("info: Loglevel Set to Error")
			case "alert":
				colog.SetMinLevel(colog.LAlert)
				log.Println("info: Loglevel Set to Alert")
			default:
				colog.SetMinLevel(colog.LInfo)
				log.Println("info: Default Loglevel unset in XML config automatically loglevel to Info")
			}
		}

		Config.Global.Software.Settings.CancellableStream = ReConfig.Global.Software.Settings.CancellableStream
		Config.Global.Software.Settings.StreamSendMessage = ReConfig.Global.Software.Settings.StreamSendMessage
		Config.Global.Software.Settings.RepeatTXTimes = ReConfig.Global.Software.Settings.RepeatTXTimes
		Config.Global.Software.Settings.RepeatTXDelay = ReConfig.Global.Software.Settings.RepeatTXDelay
		Config.Global.Software.Settings.SimplexWithMute = ReConfig.Global.Software.Settings.SimplexWithMute
		Config.Global.Software.TTS = ReConfig.Global.Software.TTS
Config.Global.Software.RemoteControl.HTTP.Enabled = ReConfig.Global.Software.RemoteControl.HTTP.Enabled
		Config.Global.Software.RemoteControl.HTTP.Command = ReConfig.Global.Software.RemoteControl.HTTP.Command
		Config.Global.Software.RemoteControl.MQTT.Commands.Command = ReConfig.Global.Software.RemoteControl.MQTT.Commands.Command


		Config.Global.Hardware.IO.Sonoff.Enabled = ReConfig.Global.Hardware.IO.Sonoff.Enabled
		Config.Global.Hardware.IO.Sonoff.Device = ReConfig.Global.Hardware.IO.Sonoff.Device

		Config.Global.Software.PrintVariables = ReConfig.Global.Software.PrintVariables
	}
	return nil
}

// CheckConfigSanity verifica la coerenza e la correttezza logica della configurazione XML caricata.
// Controlla account, file audio, pin GPIO, impostazioni MQTT e altri parametri critici.
// In caso di errori gravi (alert) termina l'applicazione tramite FatalCleanUp.
// In caso di avvisi non critici (warn) registra i messaggi e disabilita le sezioni errate.
// @param reloadxml true se la verifica avviene durante un reload a caldo della configurazione
func CheckConfigSanity(reloadxml bool) {

	Warnings := 0
	Alerts := 0

	log.Println("info: Starting XML Configuration Sanity and Logical Checks")

	Counter := 0
	for _, account := range Config.Accounts.Account {
		if account.Default {
			if len(account.Name) == 0 {
				log.Print("warn: Config Error [Section Accounts] Account Name Not Defined for Enabled Account")
			}
			if len(account.ServerAndPort) == 0 {
				log.Print("alert: Config Error [Section Accounts] Account Server And Port Not Defined for Enabled Account")
			}

			if len(account.Certificate) > 0 && !FileExists(account.Certificate) {
				log.Print("warn: Config Error [Section Accounts] Certificate Enabled but Not Found")
			}
			Counter++
		}
	}

	if Counter == 0 {
		log.Print("alert: Config Error [Section Accounts] No Default/Enabled Accounts Found in Config")
		Alerts++
	}


	for index, gpio := range Config.Global.Hardware.IO.Pins.Pin {
		if gpio.Enabled {

			if !(gpio.Direction == "input" || gpio.Direction == "output") {
				log.Printf("warn: Config Error [Section GPIO] Enabled GPIO Name %v Pin Number %v Direction %v Misconfiguired\n", gpio.Name, gpio.PinNo, gpio.Direction)
				Config.Global.Hardware.IO.Pins.Pin[index].Enabled = false
				Warnings++
			}

			if (gpio.Direction == "input") && !(gpio.Device == "pushbutton" || gpio.Device == "toggleswitch" || gpio.Device == "rotaryencoder") {
				log.Printf("warn: Config Error [Section GPIO] Enabled Input GPIO Name %v Pin Number %v Name Mis-Configured\n", gpio.Name, gpio.PinNo)
				Config.Global.Hardware.IO.Pins.Pin[index].Enabled = false
				Warnings++
			}

			if (gpio.Direction == "output") && !(gpio.Device == "led/relay") {
				log.Printf("warn: Config Error [Section GPIO] Enabled Output GPIO Name %v Pin Number %v Name Mis-Configured\n", gpio.Name, gpio.PinNo)
				Config.Global.Hardware.IO.Pins.Pin[index].Enabled = false
				Warnings++
			}

			if !(gpio.Name == "heartbeat" || gpio.Name == "p1" || gpio.Name == "p2" || gpio.Name == "p3" || gpio.Name == "unlockdoor" || gpio.Name == "insidelight" || gpio.Name == "outsidelight" || gpio.Name == "power_tablet" || gpio.Name == "on_off") {
				log.Printf("warn: Config Error [Section GPIO] Enabled GPIO Name %v Pin Number %v Invalid Name\n", gpio.Name, gpio.PinNo)
				Config.Global.Hardware.IO.Pins.Pin[index].Enabled = false
				Warnings++
			}

			if gpio.PinNo > 30 {
				log.Printf("warn: Config Error [Section GPIO] Enabled GPIO Name %v Pin Number %v Invalid GPIO Number\n", gpio.Name, gpio.PinNo)
				Config.Global.Hardware.IO.Pins.Pin[index].Enabled = false
				Warnings++
			}


			if gpio.Name == "heartbeat" {
				if Config.Global.Hardware.HeartBeat.Periodmsecs < 100 || Config.Global.Hardware.HeartBeat.LEDOnmsecs < 100 || Config.Global.Hardware.HeartBeat.LEDOffmsecs < 100 {
					if gpio.PinNo == 0 {
						log.Printf("warn: Config Error [Section GPIO] Name %v Invalid GPIO Pin %v Value\n", gpio.Name, gpio.PinNo)
						Config.Global.Hardware.IO.Pins.Pin[index].Enabled = false
						Warnings++
					}
				}
			}

		}
	}

	if Config.Global.Software.RemoteControl.MQTT.Enabled {

		if len(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTTopic) == 0 {
			log.Println("warn: Config Error [Section MQTT] Enabled MQTT With Empty Topic")
			Config.Global.Software.RemoteControl.MQTT.Enabled = false
			Warnings++
		}
		if len(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTBroker) == 0 {
			log.Println("warn: Config Error [Section MQTT] Enabled MQTT With Empty Broker")
			Config.Global.Software.RemoteControl.MQTT.Enabled = false
			Warnings++
		}
		if len(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTPassword) == 0 {
			log.Println("warn: Config Error [Section MQTT] Enabled MQTT With Empty MQTTPassword")
			Config.Global.Software.RemoteControl.MQTT.Enabled = false
			Warnings++
		}
		if len(Config.Global.Software.RemoteControl.MQTT.Settings.MQTTId) == 0 {
			log.Println("warn: Config Error [Section MQTT] Enabled MQTT With Empty MQTTID")
			Config.Global.Software.RemoteControl.MQTT.Enabled = false
			Warnings++
		}

	}

	if Warnings+Alerts > 0 {
		if Alerts > 0 {
			FatalCleanUp("alert: Fatal Errors Found In doorphoneserver.xml config file please fix errors, doorphoneserver stopping now!")
		}

		if Warnings > 0 {
			log.Println("warn: Non-Critical Errors Found In doorphoneserver.xml config file please fix errors or doorphoneserver may not behave as expected")
		}
	} else {
		log.Println("info: Finished XML Configuration Sanity and Logical Checks Without Any Alerts/Errors/Warnings")
	}

}

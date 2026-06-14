// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/allan-simon/go-singleinstance"
	"github.com/comail/colog"
	"github.com/MirkoUgoliniDev/gumble/gumble"
	"github.com/MirkoUgoliniDev/gumble/gumbleffmpeg"
	"github.com/MirkoUgoliniDev/gumble/gumbleutil"
	_ "github.com/MirkoUgoliniDev/gumble/opus"
	"github.com/MirkoUgoliniDev/volume-go"
)

var (
	//currentChannelID     uint32
	// prevChannelID memorizza l'ID del canale precedentemente selezionato
	prevChannelID uint32
	// prevParticipantCount conta il numero di partecipanti nel canale alla rilevazione precedente
	prevParticipantCount int = 0
	//prevButtonPress      string = "none"
	// maxchannelid contiene il valore massimo di ID canale trovato durante la scansione
	maxchannelid uint32
	// tmessage è un buffer temporaneo per i messaggi di testo
	tmessage string
	// isrepeattx controlla se la trasmissione ripetuta è abilitata
	isrepeattx atomic.Bool
)

// init inizializza lo stato predefinito delle variabili del pacchetto al caricamento.
func init() {
	isrepeattx.Store(true)
}

// DoorPhoneServer rappresenta un'istanza del client PTT connessa a un server Mumble.
// Contiene la configurazione, il client gumble attivo e lo stato della trasmissione.
type DoorPhoneServer struct {
	Config          *gumble.Config
	Client          *gumble.Client
	Name            string
	Address         string
	Username        string
	Ident           string
	TLSConfig       tls.Config
	ConnectAttempts uint
	Stream          *Stream
	ChannelName     string
	IsTransmitting  atomic.Bool
	IsPlayStream    bool
	GPIOEnabled     bool
	USBBridge       *USBBridge
	NFCWhitelist    *NFCWhitelistManager
}

// ChannelsListStruct contiene le informazioni di un canale Mumble per la visualizzazione in lista.
type ChannelsListStruct struct {
	chanID     uint32
	chanName   string
	chanParent *gumble.Channel
	chanUsers  int
}

// Init inizializza e avvia il client doorphoneserver leggendo la configurazione XML.
// Configura il logging, la connessione al server Mumble, MQTT e il server HTTP.
// @param file percorso del file di configurazione XML
// @param ServerIndex indice dell'account server da utilizzare
func Init(file string, ServerIndex string) {

	colog.Register()

	err := readxmlconfig(file, false)
	if err != nil {
		FatalCleanUp(err.Error())
	}

	if Config.Global.Software.Settings.LogFilenameAndPath == "" {
		FatalCleanUp("LogFilenameAndPath is not configured in XML")
	}

	logWriter, err := NewRotatingLogWriter(
		Config.Global.Software.Settings.LogFilenameAndPath,
		Config.Global.Software.Settings.LogMaxSizeMB,
		Config.Global.Software.Settings.LogRetentionDays,
	)
	if err != nil {
		FatalCleanUp("Cannot open log file: " + err.Error())
	}
	defer logWriter.Close()
	colog.SetOutput(logWriter)

	log.Printf("info: Log rotation enabled: max %d MB, retention %d days",
		Config.Global.Software.Settings.LogMaxSizeMB,
		Config.Global.Software.Settings.LogRetentionDays)

	if Config.Global.Software.Settings.SingleInstance {
		lockFile, err := singleinstance.CreateLockFile("doorphoneserver.lock")
		if err != nil {
			log.Println("error: Another Instance of doorphoneserver is already running!!, Killing this Instance")
			time.Sleep(5 * time.Second)
			TTSEvent("quitdoorphoneserver")
			CleanUp()
		}
		defer lockFile.Close()
	}

	if Config.Global.Software.Settings.Logging == "fileonly" {
		colog.SetFlags(log.Ldate | log.Ltime)
	}

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

	AccountIndex, _ = strconv.Atoi(ServerIndex)

	b := DoorPhoneServer{
		Config:      gumble.NewConfig(),
		Name:        Name[AccountIndex],
		Address:     Server[AccountIndex],
		Username:    Username[AccountIndex],
		Ident:       Ident[AccountIndex],
		ChannelName: Channel[AccountIndex],
	}

	ctx, cancel := context.WithCancel(context.Background())
	SetGlobalContext(ctx, cancel)

	b.NFCWhitelist = NewNFCWhitelistManager()

	if ioUseESP32() {
		usbBridge := NewUSBBridge(ctx)
		b.USBBridge = usbBridge
		ioUSB = NewGPIOUsb(usbBridge, &b)
		go ioUSB.Run(ctx)
		go NewSmartcard(usbBridge).Run(ctx)
		usbBridge.SetNFCManager(b.NFCWhitelist)

		// Sync NFC whitelist all'avvio: attende la connessione USB poi confronta NVS ↔ JSON
		// e riconcilia la chiave AES DESFire (EnsureKey).
		go func() {
			time.Sleep(8 * time.Second)
			tags, err := usbBridge.SendTagList(5 * time.Second)
			if err != nil {
				log.Printf("[NFC] sync avvio fallita: %v", err)
			} else {
				result := b.NFCWhitelist.SyncFromESP32(tags)
				log.Printf("[NFC] sync avvio: esp32=%v json=%v in_sync=%v",
					result["esp32_count"], result["json_count"], result["in_sync"])
			}
			fp, err := usbBridge.EnsureKey()
			if err != nil {
				log.Printf("[NFC] EnsureKey fallita: %v", err)
				return
			}
			log.Printf("[NFC] chiave AES pronta, FP=%s re_enroll=%v", fp, IsReEnrollNeeded())
		}()
	} else {
		log.Printf("info: IO backend = RPi — ESP32/USB/NFC disabilitati")
	}

	if Config.Global.Software.RemoteControl.MQTT.Enabled {
		log.Printf("info: Attempting to Contact MQTT Server")
		log.Printf("info: MQTT Broker      : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTBroker)
		log.Printf("info: Subscribed topic : %s\n", Config.Global.Software.RemoteControl.MQTT.Settings.MQTTTopic)
		go b.mqttsubscribe()
	} else {
		log.Printf("info: MQTT Server Subscription Disabled in Config")
	}

	MACName := ""

	if len(b.Username) == 0 {
		macaddress, err := getMacAddr()
		if err != nil {
			log.Println("error: Could Not Get Network Interface MAC Address")
		} else {
			for _, a := range macaddress {
				tmacname := a
				MACName = strings.Replace(tmacname, ":", "", -1)
			}
		}
		if len(MACName) == 0 {
			buf := make([]byte, 6)
			_, err := rand.Read(buf)
			if err != nil {
				FatalCleanUp("Cannot Generate Random Number Error " + err.Error())
			}
			buf[0] |= 2
			b.Config.Username = fmt.Sprintf("doorphoneserver-%02x%02x%02x%02x%02x%02x", buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])
		} else {
			b.Config.Username = fmt.Sprintf("doorphoneserver-%v", MACName)
		}
	} else {
		b.Config.Username = Username[AccountIndex]
	}

	log.Printf("info: Connecting to Server %v Identified As %v With Username %v\n", Config.Accounts.Account[AccountIndex].ServerAndPort, Config.Accounts.Account[AccountIndex].Name, b.Config.Username)
	b.Config.Password = Password[AccountIndex]

	if Insecure[AccountIndex] {
		b.TLSConfig.InsecureSkipVerify = true
	}
	if Certificate[AccountIndex] != "" {
		cert, err := tls.LoadX509KeyPair(Certificate[AccountIndex], Certificate[AccountIndex])
		if err != nil {
			FatalCleanUp("Certificate Error " + err.Error())
		}
		b.TLSConfig.Certificates = append(b.TLSConfig.Certificates, cert)
	}

	log.Println("info: Attempting to start HTTP server on port", Config.Global.Software.RemoteControl.HTTP.ListenPort)

	var httpServer *http.Server

	if Config.Global.Software.RemoteControl.HTTP.Enabled && !HTTPServRunning.Load() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", b.httpAPI)
		b.RegisterWebPanelRoutes(mux)
		httpServer = &http.Server{
			Addr:         ":" + Config.Global.Software.RemoteControl.HTTP.ListenPort,
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		go func() {
			log.Printf("info: HTTP API Server started on port %s", Config.Global.Software.RemoteControl.HTTP.ListenPort)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				FatalCleanUp("Problem Starting HTTP API Server " + err.Error())
			}
		}()
	}

	// Avvia monitor cleanup goroutine stale
	StartStreamCleanupMonitor()

	// Avvia monitoraggio sistema ogni 1 minuto
	StartSystemMonitoring(1 * time.Minute)

	// Sincronizza stato software power tablet con lo stato GPIO fisico
	InitPowerTabletState()

	// Log summary metriche ogni 10 minuti
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-GetGlobalContext().Done():
				return
			case <-ticker.C:
				LogMetricsSummary()
			}
		}
	}()

	b.ClientStart()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-sigs
	log.Println("info: Shutdown signal received, cleaning up...")

	CancelGlobalContext()

	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("error: HTTP server shutdown error: %v", err)
		} else {
			log.Println("info: HTTP server stopped gracefully")
		}
	}

	CleanUp()
	os.Exit(0)
}

// ClientStart avvia il client Mumble, configura GPIO, heartbeat e gestisce la ricezione audio.
// Viene chiamato dopo l'inizializzazione da Init per avviare la connessione principale.
func (b *DoorPhoneServer) ClientStart() {
	log.Println("info: Logging active for file:", Config.Global.Software.Settings.LogFilenameAndPath)

	if !ioUseESP32() {
		GPIOOutAll("led/relay", "off")
	}

	colog.SetFlags(log.Ldate | log.Ltime)

	b.Config.Attach(gumbleutil.AutoBitrate)

	b.Config.Attach(b)

	log.Printf("info: [%d] Default Mumble Accounts Found in XML config\n", AccountCount)

	b.initGPIO()

	if !IsAudioCardPresent() {
		log.Println("warn: No audio card detected — skipping unmute")
	} else if err := volume.Unmute(Config.Global.Software.Settings.OutputDevice); err != nil {
		log.Println("error: Unable to Unmute ", err)
	} else {
		log.Println("debug: Speaker UnMuted Before Connect to Server")
	}

	TTSEvent("doorphoneserverloaded")

	b.Connect()

	pstreamMu.Lock()
	pstream = gumbleffmpeg.New(b.Client, gumbleffmpeg.SourceFile(""), 0)
	pstreamMu.Unlock()


	if Register[AccountIndex] && !b.Client.Self.IsRegistered() {
		b.Client.Self.Register()
		log.Println("alert: Client Is Now Registered")
	} else {
		log.Println("info: Client Is Already Registered")
	}

	if Config.Global.Software.Settings.StreamOnStart {
		time.Sleep(Config.Global.Software.Settings.StreamOnStartAfter * time.Second)
		b.cmdPlayback()
	}

	if Config.Global.Software.Settings.TXOnStart {
		time.Sleep(Config.Global.Software.Settings.TXOnStartAfter * time.Second)
		b.cmdStartTransmitting()
	}

	go func() {
		var RXLEDStatus bool
		for {
			select {
			case <-GetGlobalContext().Done():
				return
			case v := <-Talking:
				if LastSpeaker != v.WhoTalking {
					LastSpeaker = v.WhoTalking
				}

				if !RXLEDStatus {
					log.Println("info: Speaking->", v.WhoTalking)
					RXLEDStatus = true
				}

				if v.IsTalking {
					GlobalSpeakingLog.Add(v.WhoTalking)
				}
			case <-TalkedTicker.C:
				if RXLEDStatus {
					RXLEDStatus = false
					TalkedTicker.Stop()
					// Non chiudere la sessione se c'è una chiamata attiva:
					// la sessione viene chiusa esplicitamente da cmd-close-call
					activeCallMu.Lock()
					noActiveCall := activeCallUser == ""
					activeCallMu.Unlock()
					if noActiveCall {
						GlobalSpeakingLog.Close()
					}
				}
			}
		}
	}()

}

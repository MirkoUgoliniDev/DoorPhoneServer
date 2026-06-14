// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"log"
	"net"
	"os"
	"time"

	"github.com/MirkoUgoliniDev/gumble/gumble"
	"github.com/MirkoUgoliniDev/gumble/gumbleffmpeg"
	"github.com/MirkoUgoliniDev/volume-go"
)

// FatalCleanUp registra un errore critico e termina il programma.
// @param message messaggio di errore da registrare nel log prima della terminazione
func FatalCleanUp(message string) {
	// Logga l'errore critico
	log.Println("Fatal error:", message)
	// Aggiungi ulteriori informazioni sull'errore
	log.Println("DoorPhoneServer is terminating due to an unrecoverable error.")
	// Termina con un errore
	os.Exit(1)
}

// CleanUp spegne tutte le periferiche GPIO e termina il programma in modo controllato.
func CleanUp() {
	// Spegne i relè solo con backend RPi. Con ESP32 lo stato dei relè è gestito
	// dalla scheda e non va azzerato allo shutdown del servizio (spegnerebbe il
	// tablet ad ogni riavvio del doorphoneserver), coerentemente con ClientStart.
	if !ioUseESP32() {
		GPIOOutAll("led/relay", "off")
	}
	// Registrare il messaggio di terminazione
	log.Println("SIGHUP Termination of Program Requested by User...shutting down doorphoneserver")
	// Terminare il programma
	os.Exit(0)
}

// Connect stabilisce la connessione al server Mumble configurato.
// In caso di errore, avvia il processo di riconnessione automatica.
func (b *DoorPhoneServer) Connect() {
	IsConnected.Store(false)
	IsPlayStream.Store(false)
	NowStreaming.Store(false)

	var err error

	_, err = gumble.DialWithDialer(new(net.Dialer), b.Address, b.Config, &b.TLSConfig)

	if err != nil {
		log.Printf("error: Connection Error %v  connecting to %v failed, attempting again...", err, b.Address)
		log.Println("debug: In the Connect Function & Trying With Username ", Username)
		b.ReConnect()
	} else {
		b.OpenStream()
	}
}

// ReConnect tenta di riconnettersi al server Mumble fino a 3 volte.
// Se i tentativi sono esauriti, chiama FatalCleanUp.
func (b *DoorPhoneServer) ReConnect() {
	IsConnected.Store(false)
	IsPlayStream.Store(false)
	NowStreaming.Store(false)

	if b.Client != nil {
		log.Println("info: Attempting Reconnection With Server")
		b.Client.Disconnect() // ignora l'errore: il server potrebbe averci già disconnessi
	}

	if ConnectAttempts < 3 {
		ConnectAttempts++
		b.Connect()
	} else {
		ConnectAttempts = 0
		log.Println("alert: Unable to connect to Mumble server after 3 attempts. Web panel still running — retry from dashboard.")
	}
}

// TransmitStart avvia la trasmissione PTT sul canale Mumble corrente.
// Gestisce il mute dell'altoparlante in modalità simplex e interrompe lo streaming in corso.
func (b *DoorPhoneServer) TransmitStart() {
	if !IsConnected.Load() {
		return
	}

	LastSpeaker = ""

	//TODO: DA VERIFICA
	if Config.Global.Software.Settings.SimplexWithMute {
		err := volume.Mute(Config.Global.Software.Settings.OutputDevice)
		if err != nil {
			log.Println("error: Unable to Mute ", err)
		} else {
			log.Println("info: Speaker Muted ")
		}
	}

	if IsPlayStream.Load() {
		IsPlayStream.Store(false)
		NowStreaming.Store(false)
		time.Sleep(100 * time.Millisecond)

	}

	b.IsTransmitting.Store(true)

	pstreamMu.Lock()
	p := pstream
	pstreamMu.Unlock()
	if p != nil && p.State() == gumbleffmpeg.StatePlaying {
		if err := p.Stop(); err != nil {
			log.Printf("error: failed to stop Stream: %v\n", err)
		}
	}

	if err := b.StartSource(); err != nil {
		log.Printf("error: failed to start Source: %v\n", err)
	}
	
	// Log stato canale audio dopo avvio trasmissione
	log.Printf("info: [AUDIO-CHANNEL] Trasmissione avviata - Status: %s", AudioChannelMonitor.GetChannelStatus())
}

// TransmitStop interrompe la trasmissione PTT in corso e ripristina l'audio in uscita.
// @param withBeep se true riproduce un segnale acustico di fine trasmissione (non implementato)
func (b *DoorPhoneServer) TransmitStop(withBeep bool) {
	if !IsConnected.Load() {
		return
	}

	b.IsTransmitting.Store(false)

	if err := b.StopSource(); err != nil {
		log.Printf("error: failed to stop Source: %v\n", err)
	}
	
	// Log riepilogo canale audio dopo fine trasmissione
	AudioChannelMonitor.LogChannelSummary()

	//TODO: DA VERIFICARE
	if Config.Global.Software.Settings.SimplexWithMute {
		err := volume.Unmute(Config.Global.Software.Settings.OutputDevice)
		if err != nil {
			log.Println("error: Unable to Unmute ", err)
		} else {
			log.Println("info: Speaker UnMuted ")
		}
	}

}

// ChangeChannel sposta il client nel canale Mumble con il nome specificato.
// @param ChannelName nome del canale di destinazione
func (b *DoorPhoneServer) ChangeChannel(ChannelName string) {
	if !IsConnected.Load() {
		return
	}
	channel := b.Client.Channels.Find(ChannelName)
	if channel != nil {
		b.Client.Self.Move(channel)
		log.Println("info: Joined Channel Name: ", channel.Name, " ID ", channel.ID)
		prevChannelID = b.Client.Self.Channel.ID
	} else {
		log.Println("warn: Unable to Find Channel Name: ", ChannelName)
		prevChannelID = 0
	}
}

// ParticipantLEDUpdate aggiorna i LED in base al numero di partecipanti nel canale corrente.
// Opzionalmente registra informazioni dettagliate e annuncia via TTS il conteggio utenti.
// @param verbose se true registra informazioni aggiuntive e annuncia via TTS
func (b *DoorPhoneServer) ParticipantLEDUpdate(verbose bool) {

	if !IsConnected.Load() {
		return
	}

	var participantCount = len(b.Client.Self.Channel.Users)

	if participantCount > 1 && participantCount != prevParticipantCount {


		prevParticipantCount = participantCount

		if verbose {
			log.Println("info: Current Channel ", b.Client.Self.Channel.Name, " has (", participantCount, ") participants")
			b.ListUsers()
		}

	}

	if participantCount > 1 {

	} else {
		if verbose {
				log.Println("info: Channel ", b.Client.Self.Channel.Name, " has no other participants")
			prevParticipantCount = 0
		}
	}
}

// ListUsers elenca nel log tutti gli utenti presenti nel canale corrente.
func (b *DoorPhoneServer) ListUsers() {
	if !IsConnected.Load() {
		return
	}

	item := 0
	for _, usr := range b.Client.Users {
		if usr.Channel.ID == b.Client.Self.Channel.ID {
			item++
			//log.Println(fmt.Sprintf("info: %d. User %#v is online. [%v]", item, usr.Name, usr.Comment))
			log.Printf("info: %d. User %#v is online. [%v]", item, usr.Name, usr.Comment)
		}

	}

}

// SendMessageToUser invia un messaggio di testo diretto a un utente specifico nel canale corrente.
// Il messaggio viene prefissato con "cmd-" per identificarlo come comando.
// @param username nome utente destinatario
// @param message testo del messaggio da inviare
func (b *DoorPhoneServer) SendMessageToUser(username string, message string) {

	message = "cmd-" + message

	if !IsConnected.Load() {
		return
	}

	item := 0
	for _, usr := range b.Client.Users {
		if usr.Channel.ID == b.Client.Self.Channel.ID {
			item++
			if usr.Name == username {
				usr.Send(message)
				log.Printf("info: %d. User %#v is online. [%v]", item, usr.Name, usr.Comment)
				log.Printf("info: Sent message '%s' to user %s", message, username)
			}
		}
	}

}

// SendMessageToSession invia un messaggio diretto alla session ID specificata (targeting garantito).
func (b *DoorPhoneServer) SendMessageToSession(session uint32, message string) {
	message = "cmd-" + message
	if !IsConnected.Load() {
		return
	}
	for _, usr := range b.Client.Users {
		if usr.Session == session {
			usr.Send(message)
			log.Printf("info: Sent message '%s' to session %d (%s)", message, session, usr.Name)
			return
		}
	}
	log.Printf("warn: Session %d not found for message '%s'", session, message)
}

// ListChannels elenca tutti i canali disponibili sul server Mumble.
// @param verbose se true registra informazioni dettagliate sui permessi di ogni canale
func (b *DoorPhoneServer) ListChannels(verbose bool) {

	if !IsConnected.Load() {
		return
	}

	var records = int(len(b.Client.Channels))
	channelsList := make([]ChannelsListStruct, len(b.Client.Channels))
	counter := 0

	for _, ch := range b.Client.Channels {
		channelsList[counter].chanID = ch.ID
		channelsList[counter].chanName = ch.Name
		channelsList[counter].chanParent = ch.Parent
		channelsList[counter].chanUsers = len(ch.Users)
		if verbose {
			if ch.Permission() != nil {
				if (*ch.Permission() & gumble.PermissionWrite) > 0 {
					log.Printf("info: Channel %v Write Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionTraverse) > 0 {
					log.Printf("info: Channel %v Transverse Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionEnter) > 0 {
					log.Printf("info: Channel %v Enter Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionSpeak) > 0 {
					log.Printf("info: Channel %v Speak Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionMuteDeafen) > 0 {
					log.Printf("info: Channel %v MuteDefen Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionMove) > 0 {
					log.Printf("info: Channel %v Move Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionMakeChannel) > 0 {
					log.Printf("info: Channel %v Make Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionLinkChannel) > 0 {
					log.Printf("info: Channel %v Link Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionWhisper) > 0 {
					log.Printf("info: Channel %v Whisper Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionTextMessage) > 0 {
					log.Printf("info: Channel %v Message Permissions\n", ch.Name)
				}
				if (*ch.Permission() & gumble.PermissionMakeTemporaryChannel) > 0 {
					log.Printf("info: Channel %v Make Temp Channel Permissions\n", ch.Name)
				}
			} else {
				log.Printf("info: Channel %v Nil Permissions\n", ch.Name)
			}
		}
		if ch.ID > maxchannelid {
			maxchannelid = ch.ID
		}

		counter++
	}

	for i := 0; i < int(records); i++ {
		if channelsList[i].chanID == 0 || channelsList[i].chanParent.ID == 0 {
			if verbose {
				log.Printf("info: Parent -> ID=%2d | Name=%-12v (%v) Users | ",
					channelsList[i].chanID, channelsList[i].chanName, channelsList[i].chanUsers)
			}
		} else {
			if verbose {
				log.Printf("info: Child  -> ID=%2d | Name=%-12v (%v) Users | PID=%2d | PName=%-12s",
					channelsList[i].chanID, channelsList[i].chanName, channelsList[i].chanUsers,
					channelsList[i].chanParent.ID, channelsList[i].chanParent.Name)
			}
		}
	}

}

// Scan scansiona tutti i canali disponibili alla ricerca di utenti attivi.
// Si ferma quando trova un canale con altri utenti, altrimenti ritorna al canale radice.
func (b *DoorPhoneServer) Scan() {
	if !IsConnected.Load() {
		return
	}

	for scanDepth := 0; scanDepth < 100; scanDepth++ {
		b.ListChannels(false)

		if b.Client.Self.Channel.ID+1 > maxchannelid {
			prevChannelID = 0
			if channel, ok := b.Client.Channels[prevChannelID]; ok {
				b.Client.Self.Move(channel)
			}
			return
		}

		found := false
		if prevChannelID < maxchannelid {
			prevChannelID++

			for i := prevChannelID; uint32(i) < maxchannelid+1; i++ {
				channel := b.Client.Channels[i]
				if channel != nil {
					b.Client.Self.Move(channel)
					time.Sleep(1000 * time.Millisecond)
					if len(b.Client.Self.Channel.Users) == 1 {
						found = true
						break
					} else {
						log.Println("info: Found Someone Online Stopped Scan on Channel ", b.Client.Self.Channel.Name)
						return
					}
				}
			}
		}

		if !found {
			return
		}
	}
	log.Println("warn: Scan reached maximum depth limit")
}

// SendMessage invia un messaggio di testo nel canale Mumble corrente.
// @param textmessage testo del messaggio da inviare
// @param PRecursive se true invia il messaggio anche ai sotto-canali
func (b *DoorPhoneServer) SendMessage(textmessage string, PRecursive bool) {
	if !IsConnected.Load() {
		return
	}
	b.Client.Self.Channel.Send(textmessage, PRecursive)
}

// SetComment imposta il commento/stato del client Mumble visibile agli altri utenti.
// @param comment testo del commento da impostare
func (b *DoorPhoneServer) SetComment(comment string) {
	if IsConnected.Load() {
		b.Client.Self.SetComment(comment)
	}
}

// TxLockTimer gestisce il timer per il blocco della trasmissione PTT.
// Attualmente non implementato, riservato per uso futuro.
func (b *DoorPhoneServer) TxLockTimer() {

}

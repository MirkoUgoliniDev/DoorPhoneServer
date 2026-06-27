// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/MirkoUgoliniDev/gumble/gumble"
)

// ConnectionMetrics tracks connection/disconnection statistics
type ConnectionMetrics struct {
	TotalConnects         int
	TotalDisconnects      int
	LastConnectTime       time.Time
	LastDisconnectTime    time.Time
	LastDisconnectReason  string
	ConnectionUptime      time.Duration
	DisconnectHistory     []DisconnectEvent
	ConsecutiveDisconnects int
}

// DisconnectEvent stores information about a disconnection
type DisconnectEvent struct {
	Timestamp    time.Time
	Reason       string
	UptimeBefore time.Duration
	ReconnectDelay time.Duration
}

var (
	connectionMetricsMu  sync.RWMutex
	connectionMetrics ConnectionMetrics
	maxDisconnectHistory = 50
)

// OnConnect è il callback gumble invocato quando la connessione al server Mumble è stabilita.
// Aggiorna lo stato, si sposta nel canale configurato e registra i timestamp degli utenti presenti.
// @param e evento di connessione con il client e il messaggio di benvenuto
func (b *DoorPhoneServer) OnConnect(e *gumble.ConnectEvent) {

	if IsConnected.Load() {
		return
	}

	IsConnected.Store(true)
	b.Client = e.Client
	ConnectAttempts = 0

	// Update connection metrics
	connectionMetricsMu.Lock()
	connectionMetrics.TotalConnects++
	connectionMetrics.LastConnectTime = time.Now()
	connectionMetrics.ConsecutiveDisconnects = 0
	
	// Calculate uptime if there was a previous disconnect
	if !connectionMetrics.LastDisconnectTime.IsZero() {
		downtime := time.Since(connectionMetrics.LastDisconnectTime)
		log.Printf("info: Reconnected after %v downtime (total disconnects: %d)",
			downtime.Round(time.Second), connectionMetrics.TotalDisconnects)
	}
	connectionMetricsMu.Unlock()

	log.Printf("debug: Connected to %s Address %s on attempt %d index [%d]\n ", b.Name, b.Client.Conn.RemoteAddr(), b.ConnectAttempts, AccountIndex)

	if e.WelcomeMessage != nil {
		var tmessage string = fmt.Sprintf("%v", esc(*e.WelcomeMessage))
		log.Println("info: Welcome message: ")
		for _, line := range strings.Split(strings.TrimSuffix(tmessage, "\n"), "\n") {
			log.Println("info: ", line)
		}
	}

	b.ParticipantLEDUpdate(true)

	if b.ChannelName != "" {
		b.ChangeChannel(b.ChannelName)
		prevChannelID = b.Client.Self.Channel.ID
	}

	// Registra il timestamp per tutti gli utenti già presenti al momento della connessione.
	// SetUserConnected è un fallback immediato (ora corrente); RequestStats chiede al server
	// le statistiche reali (Onlinesecs): la risposta async arriva come UserChangeStats e
	// corregge l'orario con quello effettivo di ingresso dell'utente sul server.
	for _, usr := range b.Client.Users {
		SetUserConnected(usr.Session)
		usr.RequestStats()
	}

	log.Printf("debug: ------- IsConnected: %v   -------- \n ", IsConnected.Load())

}

// OnDisconnect è il callback gumble invocato quando la connessione al server viene persa.
// Tenta la riconnessione automatica tranne nei casi di kick, ban o disconnessione volontaria.
// @param e evento di disconnessione con il tipo di causa
func (b *DoorPhoneServer) OnDisconnect(e *gumble.DisconnectEvent) {
	var reason string

	switch e.Type {
	case gumble.DisconnectError:
		reason = "connection error"
	case gumble.DisconnectKicked:
		reason = "kicked from server"
	case gumble.DisconnectBanned:
		reason = "banned from server"
	case gumble.DisconnectUser:
		reason = "user disconnected"
	}

	IsConnected.Store(false)

	// Connessione persa: tutte le sessioni correnti sono ormai invalide e gumble non
	// emette UserChangeDisconnected per ciascuna. Svuota la mappa per evitare il leak
	// e i timestamp fantasma dopo la riconnessione (i session id vengono riassegnati).
	ClearUserConnectLog()

	// Calculate uptime before disconnect
	var uptime time.Duration
	connectionMetricsMu.Lock()
	if !connectionMetrics.LastConnectTime.IsZero() {
		uptime = time.Since(connectionMetrics.LastConnectTime)
		connectionMetrics.ConnectionUptime = uptime
	}
	
	connectionMetrics.TotalDisconnects++
	connectionMetrics.LastDisconnectTime = time.Now()
	connectionMetrics.LastDisconnectReason = reason
	connectionMetrics.ConsecutiveDisconnects++
	
	// Calculate reconnect delay with exponential backoff
	reconnectDelay := calculateReconnectDelay(connectionMetrics.ConsecutiveDisconnects)
	
	// Store disconnect event in history
	event := DisconnectEvent{
		Timestamp:      time.Now(),
		Reason:         reason,
		UptimeBefore:   uptime,
		ReconnectDelay: reconnectDelay,
	}
	connectionMetrics.DisconnectHistory = append(connectionMetrics.DisconnectHistory, event)
	
	// Keep only last N disconnect events
	if len(connectionMetrics.DisconnectHistory) > maxDisconnectHistory {
		connectionMetrics.DisconnectHistory = connectionMetrics.DisconnectHistory[1:]
	}
	connectionMetricsMu.Unlock()

	//TODO: VERIFICARE SE E' MEGLIO NON SPEGNERE OUTPUT
	//GPIOOutAll("led/relay", "off")

	log.Printf("alert: Connection to %s disconnected after %v uptime", b.Address, uptime.Round(time.Second))
	log.Printf("alert: Disconnection Reason: %s (consecutive: %d, total: %d)",
		reason, connectionMetrics.ConsecutiveDisconnects, connectionMetrics.TotalDisconnects)

	// Send Pushover notification for short-lived connections (< 30 seconds)
	if uptime > 0 && uptime < 30*time.Second {
		msg := fmt.Sprintf("⚠️ Disconnessione rapida dopo %v - %s", uptime.Round(time.Second), reason)
		PushoverSendPushNotification(msg)
	}

	if e.Type == gumble.DisconnectKicked || e.Type == gumble.DisconnectBanned || e.Type == gumble.DisconnectUser {
		log.Println("alert: Not attempting reconnect for disconnect type:", reason)
		return
	}

	if MumbleServiceStopped.Load() {
		log.Println("alert: Mumble server was intentionally stopped from web panel, skipping auto-reconnect")
		return
	}

	log.Printf("alert: Attempting Reconnect in %v (backoff strategy)...", reconnectDelay.Round(time.Second))
	time.Sleep(reconnectDelay)
	b.ReConnect()
}

// calculateReconnectDelay calculates exponential backoff delay for reconnection attempts
// Starts at 5s, doubles each time up to max 60s
func calculateReconnectDelay(consecutiveDisconnects int) time.Duration {
	baseDelay := 5 * time.Second
	maxDelay := 60 * time.Second
	
	// Exponential backoff: 5s, 10s, 20s, 40s, 60s (max)
	delay := baseDelay * time.Duration(1<<uint(consecutiveDisconnects-1))
	
	if delay > maxDelay {
		delay = maxDelay
	}
	
	return delay
}

// GetConnectionMetrics returns a copy of current connection metrics (thread-safe)
func GetConnectionMetrics() ConnectionMetrics {
	connectionMetricsMu.RLock()
	defer connectionMetricsMu.RUnlock()
	
	// Create a copy to avoid race conditions
	metrics := ConnectionMetrics{
		TotalConnects:         connectionMetrics.TotalConnects,
		TotalDisconnects:      connectionMetrics.TotalDisconnects,
		LastConnectTime:       connectionMetrics.LastConnectTime,
		LastDisconnectTime:    connectionMetrics.LastDisconnectTime,
		LastDisconnectReason:  connectionMetrics.LastDisconnectReason,
		ConnectionUptime:      connectionMetrics.ConnectionUptime,
		ConsecutiveDisconnects: connectionMetrics.ConsecutiveDisconnects,
	}
	
	// Copy disconnect history
	metrics.DisconnectHistory = make([]DisconnectEvent, len(connectionMetrics.DisconnectHistory))
	copy(metrics.DisconnectHistory, connectionMetrics.DisconnectHistory)
	
	return metrics
}

// OnTextMessage è il callback gumble invocato quando viene ricevuto un messaggio di testo.
// Riproduce il suono dell'evento, registra il messaggio ed esegue eventuali comandi contenuti.
// @param e evento di messaggio di testo con mittente e contenuto
func (b *DoorPhoneServer) OnTextMessage(e *gumble.TextMessageEvent) {

	if len(cleanstring(e.Message)) > 105 {
		log.Println("warn: Message Too Long to Be Displayed on Screen")
		tmessage = strings.TrimSpace(cleanstring(e.Message)[:105])
	} else {
		tmessage = strings.TrimSpace(cleanstring(e.Message))
	}

	var sender string
	var senderSession uint32

	if e.Sender != nil {
		sender = strings.TrimSpace(cleanstring(e.Sender.Name))
		senderSession = e.Sender.Session
		log.Println("info: Sender Name is ", sender)
	} else {
		sender = ""
		senderSession = 0
	}

	log.Printf("info: ----> Message (%d) from %v %v\n", len(tmessage), sender, tmessage)

	execute_command(b, sender, senderSession, tmessage)

}

/*
func (b *DoorPhoneServer) OnUserChange(e *gumble.UserChangeEvent) {
	var info string

	switch e.Type {
	case gumble.UserChangeConnected:
		info = "conn"
	case gumble.UserChangeDisconnected:
		info = "disconnected!"
	case gumble.UserChangeKicked:
		info = "kicked"
	case gumble.UserChangeBanned:
		info = "banned"
	case gumble.UserChangeRegistered:
		info = "registered"
	case gumble.UserChangeUnregistered:
		info = "unregistered"
	case gumble.UserChangeName:
		info = "chg name"
	case gumble.UserChangeChannel:
		info = "chg channel"
		log.Println("info:", cleanstring(e.User.Name), " Changed Channel to ", e.User.Channel.Name)
	case gumble.UserChangeComment:
		info = "chg comment"
	case gumble.UserChangeAudio:
		info = "chg audio"
	case gumble.UserChangePrioritySpeaker:
		info = "is priority"
	case gumble.UserChangeRecording:
		info = "chg rec status"
	case gumble.UserChangeStats:
		info = "chg stats"

		if info != "chg channel" {
			if info != "" {
				log.Println("info: User ", cleanstring(e.User.Name), " ", info, "Event type=", e.Type, " channel=", e.User.Channel.Name)

			}

		} else {
			log.Println("info: User ", cleanstring(e.User.Name), " Event type=", e.Type, " channel=", e.User.Channel.Name)
		}

	}

    b.ParticipantLEDUpdate(true)


}
*/

// OnUserChange è il callback gumble invocato quando lo stato di un utente cambia.
// Gestisce connessioni, disconnessioni, cambi canale e aggiorna i LED partecipanti.
// @param e evento di cambio utente con il tipo di modifica e i dati dell'utente
func (b *DoorPhoneServer) OnUserChange(e *gumble.UserChangeEvent) {
	// Mappa degli eventi utente con i relativi messaggi
	eventMessages := map[gumble.UserChangeType]string{
		gumble.UserChangeConnected:       "conn",
		gumble.UserChangeDisconnected:    "disconnected!",
		gumble.UserChangeKicked:          "kicked",
		gumble.UserChangeBanned:          "banned",
		gumble.UserChangeRegistered:      "registered",
		gumble.UserChangeUnregistered:    "unregistered",
		gumble.UserChangeName:            "chg name",
		gumble.UserChangeChannel:         "chg channel",
		gumble.UserChangeComment:         "chg comment",
		gumble.UserChangeAudio:           "chg audio",
		gumble.UserChangePrioritySpeaker: "is priority",
		gumble.UserChangeRecording:       "chg rec status",
		gumble.UserChangeStats:           "chg stats",
	}

	// Ottieni il messaggio dell'evento
	info := eventMessages[e.Type]

	// Gestione speciale per il cambio di canale
	if e.Type == gumble.UserChangeChannel {
		if e.User.Channel != nil {
			log.Println("info:", cleanstring(e.User.Name), "Changed Channel to", e.User.Channel.Name)
		}
	} else if info != "" { // Per tutti gli altri eventi
		var channelName string
		if e.User.Channel != nil {
			channelName = e.User.Channel.Name
		}
		switch e.Type {
		case gumble.UserChangeDisconnected:
			log.Printf("info: User %s disconnected from channel %s", cleanstring(e.User.Name), channelName)
		case gumble.UserChangeConnected:
			log.Printf("info: User %s connected to channel %s", cleanstring(e.User.Name), channelName)
		case gumble.UserChangeAudio:
			log.Printf("info: User %s audio state changed: muted=%v deafened=%v selfMuted=%v selfDeafened=%v suppressed=%v (channel=%s)",
				cleanstring(e.User.Name), e.User.Muted, e.User.Deafened, e.User.SelfMuted, e.User.SelfDeafened, e.User.Suppressed, channelName)
		default:
			log.Println("info: User", cleanstring(e.User.Name), info, "Event type=", e.Type, "channel=", channelName)
		}
	}

	// Track connection/disconnection timestamps.
	// Nota: e.Type è un bitmask, quindi si usa Has() (un evento Stats può arrivare
	// combinato con altri flag).
	if e.Type.Has(gumble.UserChangeConnected) {
		SetUserConnected(e.User.Session)
		// Chiedi le stats per ottenere l'orario reale (utile se l'utente era già
		// presente o in caso di clock skew); la risposta arriva come UserChangeStats.
		e.User.RequestStats()
	}
	if e.Type.Has(gumble.UserChangeStats) && e.User.Stats != nil {
		// Stats.Connected = now - Onlinesecs → orario reale di ingresso sul server.
		SetUserConnectedAt(e.User.Session, e.User.Stats.Connected)
	}
	if e.Type.Has(gumble.UserChangeDisconnected) ||
		e.Type.Has(gumble.UserChangeKicked) ||
		e.Type.Has(gumble.UserChangeBanned) {
		RemoveUserConnected(e.User.Session)
	}

	// Aggiorna sempre i LED dei partecipanti
	b.ParticipantLEDUpdate(true)
}

// OnPermissionDenied è il callback gumble invocato quando un'operazione viene rifiutata per mancanza di permessi.
// Registra il tipo di negazione e il canale coinvolto.
// @param e evento di negazione permesso con tipo e canale
func (b *DoorPhoneServer) OnPermissionDenied(e *gumble.PermissionDeniedEvent) {
	var info string

	switch e.Type {
	case gumble.PermissionDeniedOther:
		info = e.String
	case gumble.PermissionDeniedPermission:
		info = "insufficient permissions"
	case gumble.PermissionDeniedSuperUser:
		info = "cannot modify SuperUser"
	case gumble.PermissionDeniedInvalidChannelName:
		info = "invalid channel name"
	case gumble.PermissionDeniedTextTooLong:
		info = "text too long"
	case gumble.PermissionDeniedTemporaryChannel:
		info = "temporary channel"
	case gumble.PermissionDeniedMissingCertificate:
		info = "missing certificate"
	case gumble.PermissionDeniedInvalidUserName:
		info = "invalid user name"
	case gumble.PermissionDeniedChannelFull:
		info = "channel full"
	case gumble.PermissionDeniedNestingLimit:
		info = "nesting limit"
	}

	log.Printf("error: Permission denied %v to Join Channel %v\n", info, e.Channel.Name)
}

// OnChannelChange è il callback gumble invocato quando la struttura dei canali del server cambia.
// @param e evento di cambio canale (non utilizzato attualmente)
func (b *DoorPhoneServer) OnChannelChange(e *gumble.ChannelChangeEvent) {
}

// OnUserList è il callback gumble invocato quando viene ricevuta la lista utenti.
// @param e evento con la lista degli utenti registrati (non utilizzato attualmente)
func (b *DoorPhoneServer) OnUserList(e *gumble.UserListEvent) {
}

// OnACL è il callback gumble invocato quando vengono ricevute le informazioni ACL.
// @param e evento ACL (non utilizzato attualmente)
func (b *DoorPhoneServer) OnACL(e *gumble.ACLEvent) {
}

// OnBanList è il callback gumble invocato quando viene ricevuta la lista dei ban.
// @param e evento con la lista dei ban (non utilizzato attualmente)
func (b *DoorPhoneServer) OnBanList(e *gumble.BanListEvent) {
}

// OnContextActionChange è il callback gumble invocato quando cambiano le azioni contestuali.
// @param e evento di cambio azione contestuale (non utilizzato attualmente)
func (b *DoorPhoneServer) OnContextActionChange(e *gumble.ContextActionChangeEvent) {
}

// OnServerConfig è il callback gumble invocato quando viene ricevuta la configurazione del server.
// @param e evento di configurazione server (non utilizzato attualmente)
func (b *DoorPhoneServer) OnServerConfig(e *gumble.ServerConfigEvent) {
}

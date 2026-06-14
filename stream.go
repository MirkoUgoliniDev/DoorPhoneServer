// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"context"
	"encoding/binary"
	"errors"
	"log"
	"math"
	"sync"
	"time"

	"github.com/MirkoUgoliniDev/go-openal/openal"
	"github.com/MirkoUgoliniDev/gumble/gumble"
	"github.com/MirkoUgoliniDev/gumble/gumbleffmpeg"
)

// audioBufferCount è il numero di buffer OpenAL pre-allocati per la riproduzione audio in streaming
const audioBufferCount = 24

var (
	// errState è l'errore restituito quando si tenta un'operazione su uno stream in stato invalido
	errState = errors.New("gumbleopenal: invalid state")
	// now memorizza il timestamp di avvio per calcoli di durata
	now = time.Now()
	// debuglevel controlla la verbosità del debug dello stream (0=nessuno, 3=massimo)
	debuglevel = 2
	// TotalStreams conta il numero totale di stream audio aperti dall'avvio
	TotalStreams int
	// NeedToKill conta le goroutine stream obsolete rilevate
	NeedToKill int
)

// sourceStopMu protects sourceStop channel from concurrent close (double-close panic).
var sourceStopMu sync.Mutex

// SpeakingEntry rappresenta un evento nel log del parlato.
// Type: "open" = apertura chiamata, "speak" = turno di parlato, "close" = chiusura chiamata.
type SpeakingEntry struct {
	Who  string    `json:"who"`
	At   time.Time `json:"at"`
	Type string    `json:"type"`
}

type speakingLog struct {
	mu      sync.RWMutex
	entries []SpeakingEntry
	maxSize int
}

// GlobalSpeakingLog è il log globale della sequenza del parlato nel canale.
var GlobalSpeakingLog = &speakingLog{
	entries: make([]SpeakingEntry, 0, 200),
	maxSize: 200,
}

// Open apre esplicitamente una nuova sessione di chiamata (chiamato su cmd-ring).
// target è il piano/utente chiamato (es. "P4").
func (s *speakingLog) Open(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Se c'è una sessione aperta senza close, chiudila prima
	if len(s.entries) > 0 {
		last := s.entries[len(s.entries)-1]
		if last.Type != "close" {
			s.entries = append(s.entries, SpeakingEntry{Who: "", At: time.Now(), Type: "close"})
		}
	}
	s.entries = append(s.entries, SpeakingEntry{Who: target, At: time.Now(), Type: "open"})
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
}

// Add registra un turno di parlato nella sessione corrente.
// Deduplica: ignora lo stesso speaker se ha già parlato negli ultimi 3 secondi.
// Non apre automaticamente una nuova sessione — usare Open() per quello.
func (s *speakingLog) Add(who string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return
	}
	last := s.entries[len(s.entries)-1]
	// Registra solo se la sessione è aperta
	if last.Type == "close" {
		return
	}
	now := time.Now()
	if last.Type == "speak" && last.Who == who && now.Sub(last.At) < 3*time.Second {
		return
	}
	s.entries = append(s.entries, SpeakingEntry{Who: who, At: now, Type: "speak"})
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
}

// Close registra la chiusura della chiamata corrente.
func (s *speakingLog) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return
	}
	last := s.entries[len(s.entries)-1]
	if last.Type == "close" || last.Type == "open" {
		return
	}
	s.entries = append(s.entries, SpeakingEntry{Who: "", At: time.Now(), Type: "close"})
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
}

// Clear svuota il log del parlato.
func (s *speakingLog) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
}

// GetRecent ritorna le ultime n voci in ordine cronologico inverso (più recente prima).
func (s *speakingLog) GetRecent(n int) []SpeakingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SpeakingEntry, len(s.entries))
	copy(result, s.entries)
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	if n > 0 && len(result) > n {
		return result[:n]
	}
	return result
}

// RxStatus tiene traccia dell'ultimo audio ricevuto da Mumble (usato dal monitor webpanel).
var RxStatus struct {
	mu    sync.RWMutex
	From  string
	Level int
	When  time.Time
}

// AudioChannelStats monitora le statistiche del canale audio bidirezionale per verificare
// la funzionalità della comunicazione tra client Android e Raspberry durante le chiamate.
type AudioChannelStats struct {
	mu sync.RWMutex

	// RX (Ricezione da client Android)
	RxActive      bool
	RxFrom        string
	RxPacketCount uint64
	RxByteCount   uint64
	RxStartTime   time.Time
	RxLastPacket  time.Time
	RxAvgLevel    float64

	// TX (Trasmissione verso client Android)
	TxActive       bool
	TxPacketCount  uint64
	TxByteCount    uint64
	TxStartTime    time.Time
	TxLastPacket   time.Time
	TxDroppedCount uint64
	TxAvgLevel     float64

	// Statistiche sessione
	SessionStart    time.Time
	SessionDuration time.Duration
}

// AudioChannelMonitor è l'istanza globale per il monitoraggio del canale audio
var AudioChannelMonitor AudioChannelStats

// StartRxSession inizia una nuova sessione di ricezione audio
func (a *AudioChannelStats) StartRxSession(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	a.RxActive = true
	a.RxFrom = username
	a.RxPacketCount = 0
	a.RxByteCount = 0
	a.RxStartTime = now
	a.RxLastPacket = now
	a.RxAvgLevel = 0

	if a.SessionStart.IsZero() {
		a.SessionStart = now
	}

	log.Printf("info: [AUDIO-RX] Sessione avviata - Client: %s", username)
}

// UpdateRxStats aggiorna le statistiche di ricezione con un nuovo pacchetto
func (a *AudioChannelStats) UpdateRxStats(username string, samples int, level int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.RxActive || a.RxFrom != username {
		a.RxActive = true
		a.RxFrom = username
		a.RxStartTime = time.Now()
		log.Printf("info: [AUDIO-RX] Nuova sorgente - Client: %s", username)
	}

	a.RxPacketCount++
	a.RxByteCount += uint64(samples * 2) // 16-bit samples
	a.RxLastPacket = time.Now()

	// Calcola media mobile del livello audio (ignora silence frames)
	if level > 0 {
		alpha := 0.1 // fattore di smoothing
		a.RxAvgLevel = alpha*float64(level) + (1-alpha)*a.RxAvgLevel
	}

}

// EndRxSession termina la sessione di ricezione audio
func (a *AudioChannelStats) EndRxSession(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.RxActive {
		return
	}

	duration := time.Since(a.RxStartTime)
	bitrate := float64(a.RxByteCount*8) / duration.Seconds() / 1000 // kbps

	log.Printf("info: [AUDIO-RX] Sessione terminata - Client: %s | Pacchetti: %d | Bytes: %d | Bitrate: %.1f kbps | Durata: %v",
		username, a.RxPacketCount, a.RxByteCount, bitrate, duration.Round(time.Second))

	a.RxActive = false
}

// StartTxSession inizia una nuova sessione di trasmissione audio
func (a *AudioChannelStats) StartTxSession() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	a.TxActive = true
	a.TxPacketCount = 0
	a.TxByteCount = 0
	a.TxDroppedCount = 0
	a.TxAvgLevel = 0
	a.TxStartTime = now
	a.TxLastPacket = now

	if a.SessionStart.IsZero() {
		a.SessionStart = now
	}

	log.Printf("info: [AUDIO-TX] Trasmissione avviata verso client Android")
}

// UpdateTxStats aggiorna le statistiche di trasmissione con un nuovo pacchetto
func (a *AudioChannelStats) UpdateTxStats(samples int, dropped bool, level int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.TxActive {
		return
	}

	if dropped {
		a.TxDroppedCount++
	} else {
		a.TxPacketCount++
		a.TxByteCount += uint64(samples * 2) // 16-bit samples
		a.TxLastPacket = time.Now()
		if level > 0 {
			alpha := 0.1 // fattore di smoothing
			a.TxAvgLevel = alpha*float64(level) + (1-alpha)*a.TxAvgLevel
		}
	}

}

// EndTxSession termina la sessione di trasmissione audio
func (a *AudioChannelStats) EndTxSession() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.TxActive {
		return
	}

	duration := time.Since(a.TxStartTime)
	bitrate := float64(a.TxByteCount*8) / duration.Seconds() / 1000 // kbps
	packetLoss := 0.0
	if total := a.TxPacketCount + a.TxDroppedCount; total > 0 {
		packetLoss = float64(a.TxDroppedCount) / float64(total) * 100
	}

	log.Printf("info: [AUDIO-TX] Trasmissione terminata | Pacchetti: %d | Bytes: %d | Dropped: %d (%.1f%%) | Bitrate: %.1f kbps | Level=%.0f%% | Durata: %v",
		a.TxPacketCount, a.TxByteCount, a.TxDroppedCount, packetLoss, bitrate, a.TxAvgLevel, duration.Round(time.Second))

	a.TxActive = false
}

// GetChannelStatus ritorna lo stato corrente del canale audio
func (a *AudioChannelStats) GetChannelStatus() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.RxActive && !a.TxActive {
		return "IDLE"
	}

	if a.RxActive && a.TxActive {
		return "DUPLEX (RX+TX)"
	}

	if a.RxActive {
		return "RX-ONLY"
	}

	return "TX-ONLY"
}

// LogChannelSummary stampa un riepilogo completo dello stato del canale audio
// e salva la chiamata nello storico se c'è stata attività RX o TX
func (a *AudioChannelStats) LogChannelSummary() {
	a.mu.RLock()
	defer a.mu.RUnlock()

	status := "IDLE"
	if a.RxActive && a.TxActive {
		status = "DUPLEX (RX+TX)"
	} else if a.RxActive {
		status = "RX-ONLY"
	} else if a.TxActive {
		status = "TX-ONLY"
	}

	log.Printf("info: [AUDIO-CHANNEL] Status: %s", status)

	var rxDuration time.Duration
	var rxBitrate float64
	var txDuration time.Duration
	var txBitrate float64
	var packetLoss float64

	if a.RxActive {
		rxDuration = time.Since(a.RxStartTime)
		rxBitrate = float64(a.RxByteCount*8) / rxDuration.Seconds() / 1000
		log.Printf("info: [AUDIO-CHANNEL] RX: Client=%s Packets=%d Bitrate=%.1f kbps Level=%.0f%% Duration=%v",
			a.RxFrom, a.RxPacketCount, rxBitrate, a.RxAvgLevel, rxDuration.Round(time.Second))
	}

	if a.TxActive {
		txDuration = time.Since(a.TxStartTime)
		txBitrate = float64(a.TxByteCount*8) / txDuration.Seconds() / 1000
		if total := a.TxPacketCount + a.TxDroppedCount; total > 0 {
			packetLoss = float64(a.TxDroppedCount) / float64(total) * 100
		}
		log.Printf("info: [AUDIO-CHANNEL] TX: Packets=%d Dropped=%d (%.1f%%) Bitrate=%.1f kbps Level=%.0f%% Duration=%v",
			a.TxPacketCount, a.TxDroppedCount, packetLoss, txBitrate, a.TxAvgLevel, txDuration.Round(time.Second))
	}

}

// Reset azzera tutte le statistiche del canale audio per la sessione successiva.
func (a *AudioChannelStats) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.RxActive = false
	a.RxFrom = ""
	a.RxPacketCount = 0
	a.RxByteCount = 0
	a.RxStartTime = time.Time{}
	a.RxLastPacket = time.Time{}
	a.RxAvgLevel = 0
	a.TxActive = false
	a.TxPacketCount = 0
	a.TxByteCount = 0
	a.TxStartTime = time.Time{}
	a.TxLastPacket = time.Time{}
	a.TxDroppedCount = 0
	a.TxAvgLevel = 0
	a.SessionStart = time.Time{}
	a.SessionDuration = 0
}

// MumbleDuplex implementa la gestione duplex audio per Mumble (ascolto e trasmissione).
type MumbleDuplex struct{}

// Stream gestisce i dispositivi audio OpenAL per la cattura e la riproduzione audio Mumble.
type Stream struct {
	client          *gumble.Client
	link            gumble.Detacher
	deviceSource    *openal.CaptureDevice
	sourceFrameSize int
	sourceStop      chan bool
	deviceSink      *openal.Device
	contextSink     *openal.Context
}

// New crea e inizializza un nuovo Stream audio con dispositivi OpenAL per cattura e riproduzione.
// @param client client gumble attivo a cui collegare lo stream
// @return puntatore al nuovo Stream o errore di inizializzazione
func (b *DoorPhoneServer) New(client *gumble.Client) (*Stream, error) {
	s := &Stream{
		client:          client,
		sourceFrameSize: client.Config.AudioFrameSize(),
	}
	s.deviceSource = openal.CaptureOpenDevice("", gumble.AudioSampleRate, openal.FormatMono16, uint32(s.sourceFrameSize))
	if s.deviceSource == nil {
		return nil, errors.New("gumbleopenal: failed to open audio capture device")
	}

	s.deviceSink = openal.OpenDevice("")
	if s.deviceSink == nil {
		s.deviceSource.CaptureCloseDevice()
		return nil, errors.New("gumbleopenal: failed to open audio playback device")
	}

	s.contextSink = s.deviceSink.CreateContext()

	s.contextSink.Activate()

	s.link = client.Config.AttachAudio(s)

	return s, nil
}

// Destroy rilascia le risorse dello stream audio OpenAL e stacca il client Mumble.
func (b *DoorPhoneServer) Destroy() {
	if debuglevel >= 3 {
		log.Println("debug: Destroy Stream Source")
	}
	b.Stream.link.Detach()
	if b.Stream.deviceSource != nil {
		b.Stream.deviceSource.CaptureStop()
		b.Stream.deviceSource.CaptureCloseDevice()
		b.Stream.deviceSource = nil
	}
	if b.Stream.deviceSink != nil {
		b.Stream.contextSink.Destroy()
		b.Stream.deviceSink.CloseDevice()
		b.Stream.contextSink = nil
		b.Stream.deviceSink = nil
	}
}

// StartSource avvia la cattura audio dal microfono e la trasmissione verso il server Mumble.
// @return errore se lo stream è in uno stato non valido
func (b *DoorPhoneServer) StartSource() error {
	sourceStopMu.Lock()
	defer sourceStopMu.Unlock()

	if debuglevel >= 3 {
		log.Println("debug: Start Stream Source")
	}

	b.Stream.deviceSource.CaptureStart()
	stopCh := make(chan bool)
	b.Stream.sourceStop = stopCh
	go b.sourceRoutine(stopCh)
	return nil
}

// StopSource stops the audio capture source. Thread-safe: uses mutex to prevent double-close panic
// when called concurrently from HTTP API, MQTT, or keyboard handlers.
func (b *DoorPhoneServer) StopSource() error {
	sourceStopMu.Lock()
	defer sourceStopMu.Unlock()

	if debuglevel >= 3 {
		log.Println("debug: Stop Source File")
	}
	if b.Stream.sourceStop == nil {
		return errState
	}
	close(b.Stream.sourceStop)
	b.Stream.sourceStop = nil
	b.Stream.deviceSource.CaptureStop()
	b.Stream.deviceSource.CaptureCloseDevice()
	newDev := openal.CaptureOpenDevice("", gumble.AudioSampleRate, openal.FormatMono16, uint32(b.Stream.sourceFrameSize))
	if newDev == nil {
		log.Println("error: StopSource failed to reopen audio capture device")
		return errors.New("gumbleopenal: failed to reopen audio capture device")
	}
	b.Stream.deviceSource = newDev

	return nil
}

// OnAudioStream è il callback gumble invocato quando un utente inizia a trasmettere audio.
// Avvia una goroutine dedicata per la riproduzione del flusso audio ricevuto tramite OpenAL.
// Se esiste già una goroutine per questo utente, la termina prima di crearne una nuova.
// @param e evento audio con l'utente mittente e il canale del buffer audio
func (s *Stream) OnAudioStream(e *gumble.AudioStreamEvent) {
	if e.User == nil {
		log.Println("warn: OnAudioStream called with nil User, skipping")
		return
	}
	TotalStreams++
	StreamTrackerMu.Lock()

	// Cleanup goroutine stale se esiste
	if existing, userexists := StreamTracker[e.User.UserID]; userexists {
		log.Printf("warn: Stale GoRoutine Detected For UserID=%v UserName=%v Session=%v - Terminating old stream",
			e.User.UserID, e.User.Name, e.User.Session)

		// Cancella il context della vecchia goroutine
		if existing.Cancel != nil {
			existing.Cancel()
		}

		NeedToKill++

		// Attendi brevemente per permettere cleanup
		StreamTrackerMu.Unlock()
		time.Sleep(100 * time.Millisecond)
		StreamTrackerMu.Lock()
	}

	// Crea context cancellabile per la nuova goroutine
	ctx, cancel := context.WithCancel(GetGlobalContext())

	StreamTracker[e.User.UserID] = streamTrackerStruct{
		UserID:      e.User.UserID,
		UserName:    e.User.Name,
		UserSession: e.User.Session,
		C:           e.C,
		Cancel:      cancel,
		CreatedAt:   time.Now(),
	}
	StreamTrackerMu.Unlock()
	goStreamStats()

	go func() {
		defer cancel() // Cleanup context quando la goroutine termina
		
		// Avvia sessione RX per monitoraggio canale audio
		AudioChannelMonitor.StartRxSession(e.User.Name)
		defer AudioChannelMonitor.EndRxSession(e.User.Name)
		
		source := openal.NewSource()
		emptyBufs := openal.NewBuffers(audioBufferCount)
		reclaim := func() {
			if n := source.BuffersProcessed(); n > 0 {
				reclaimedBufs := make(openal.Buffers, n)
				source.UnqueueBuffers(reclaimedBufs)
				emptyBufs = append(emptyBufs, reclaimedBufs...)
			}
		}
		defer func() {
			reclaim()
			emptyBufs.Delete()
			source.Delete()
			StreamTrackerMu.Lock()
			delete(StreamTracker, e.User.UserID)
			StreamTrackerMu.Unlock()
		}()
		var raw [gumble.AudioMaximumFrameSize * 2]byte
		for {
			select {
			case <-ctx.Done():
				return
			case packet, ok := <-e.C:
				if !ok {
					return
				}
				TalkedTicker.Reset(Config.Global.Software.Settings.VoiceActivityTimermsecs * time.Millisecond)
				select {
				case Talking <- talkingStruct{true, e.User.Name}:
				case <-ctx.Done():
					return
				}
				samples := len(packet.AudioBuffer)
				if samples > cap(raw) {
					log.Printf("warn: OnAudioStream dropped packet from %s: size %d exceeds buffer cap %d", e.User.Name, samples, cap(raw))
					continue
				}
				// aggiorna monitor ricezione webpanel e statistiche canale audio
				{
					var sum float64
					for _, s := range packet.AudioBuffer {
						f := float64(s)
						sum += f * f
					}
					level := 0
					if samples > 0 {
						rms := math.Sqrt(sum / float64(samples))
						level = int(rms / 327.67)
						if level > 100 {
							level = 100
						}
					}
					RxStatus.mu.Lock()
					RxStatus.From = e.User.Name
					RxStatus.Level = level
					RxStatus.When = time.Now()
					RxStatus.mu.Unlock()
					
					// Aggiorna statistiche RX del canale audio
					AudioChannelMonitor.UpdateRxStats(e.User.Name, samples, level)
				}
				for i, value := range packet.AudioBuffer {
					binary.LittleEndian.PutUint16(raw[i*2:], uint16(value))
				}
				reclaim()
				if len(emptyBufs) == 0 {
					continue
				}
				last := len(emptyBufs) - 1
				buffer := emptyBufs[last]
				emptyBufs = emptyBufs[:last]
				buffer.SetData(openal.FormatMono16, raw[:samples*2], gumble.AudioSampleRate)
				source.QueueBuffer(buffer)
				if source.State() != openal.Playing {
					source.Play()
				}
				select {
				case Talking <- talkingStruct{false, e.User.Name}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// sourceRoutine è la goroutine che campiona l'audio dal microfono e lo invia al canale Mumble.
// Gira a intervalli regolari e si ferma quando viene chiuso il canale sourceStop.
func (b *DoorPhoneServer) sourceRoutine(stop chan bool) {
	interval := b.Stream.client.Config.AudioInterval
	frameSize := b.Stream.client.Config.AudioFrameSize()

	if frameSize != b.Stream.sourceFrameSize {
		log.Println("error: FrameSize Error!")
		b.Stream.deviceSource.CaptureCloseDevice()
		b.Stream.sourceFrameSize = frameSize
		newDev := openal.CaptureOpenDevice("", gumble.AudioSampleRate, openal.FormatMono16, uint32(b.Stream.sourceFrameSize))
		if newDev == nil {
			log.Println("error: sourceRoutine failed to reopen audio capture device after frameSize change")
			return
		}
		b.Stream.deviceSource = newDev
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	outgoing := b.Stream.client.AudioOutgoing()
	defer close(outgoing)
	
	// Avvia sessione TX per monitoraggio canale audio
	AudioChannelMonitor.StartTxSession()
	defer AudioChannelMonitor.EndTxSession()

	for {
		select {
		case <-stop:
			if debuglevel >= 3 {
				log.Println("debug: Ticker Stop!")
			}
			return
		case <-ticker.C:
			//this is for encoding (transmitting)
			buff := b.Stream.deviceSource.CaptureSamples(uint32(frameSize))
			if len(buff) != frameSize*2 {
				// Pacchetto dropped - aggiorna statistiche
				AudioChannelMonitor.UpdateTxStats(frameSize, true, 0)
				continue
			}
			int16Buffer := make([]int16, frameSize)
			for i := range int16Buffer {
				int16Buffer[i] = int16(binary.LittleEndian.Uint16(buff[i*2 : (i+1)*2]))
			}
			// Calcola livello audio TX (RMS)
			var txSum float64
			for _, s := range int16Buffer {
				f := float64(s)
				txSum += f * f
			}
			txLevel := 0
			if frameSize > 0 {
				txRms := math.Sqrt(txSum / float64(frameSize))
				txLevel = int(txRms / 327.67)
				if txLevel > 100 {
					txLevel = 100
				}
			}
			select {
			case outgoing <- gumble.AudioBuffer(int16Buffer):
				// Pacchetto inviato con successo - aggiorna statistiche
				AudioChannelMonitor.UpdateTxStats(frameSize, false, txLevel)
			case <-stop:
				return
			}
		}
	}
}


// playIntoStreamDirect riproduce un file audio nel canale Mumble senza verificare la config dei suoni.
// Usato dall'Audio Test del web panel per trasmettere file indipendentemente dalla configurazione.
func (b *DoorPhoneServer) playIntoStreamDirect(filepath string, vol float32) {
	pstreamMu.Lock()
	p := pstream
	pstreamMu.Unlock()

	if p != nil && p.State() == gumbleffmpeg.StatePlaying {
		if err := p.Stop(); err != nil {
			log.Printf("error: failed Stopping Stream: %v\n", err)
		}
		return
	}

	IsPlayStream.Store(true)
	newP := gumbleffmpeg.New(b.Client, gumbleffmpeg.SourceFile(filepath), vol/100)
	pstreamMu.Lock()
	pstream = newP
	pstreamMu.Unlock()

	if err := newP.Play(); err != nil {
		log.Printf("error: Can't play %s error %s", filepath, err)
	} else {
		log.Printf("info: File %s Playing!", filepath)
		newP.Wait()
		log.Printf("info: File %s Finished!", filepath)
	}
	IsPlayStream.Store(false)
}

/*
func (b *DoorPhoneServer) splayIntoStream(filepath string, vol float32) {
	pstream = gumbleffmpeg.New(b.Stream.client, gumbleffmpeg.SourceFile(filepath), vol/100)
	if err := pstream.Play(); err != nil {
		log.Printf("error: Can't play %s error %s", filepath, err)
	} else {
		log.Printf("info: File %s Playing!", filepath)
		pstream.Wait()
		pstream.Stop()
	}
}
*/

// OpenStream inizializza e apre lo stream audio per il client Mumble corrente.
func (b *DoorPhoneServer) OpenStream() {
	if stream, err := b.New(b.Client); err != nil {
		FatalCleanUp("Stream Open Error " + err.Error())
	} else {
		b.Stream = stream
	}
}

// ResetStream chiude correttamente lo stream audio OpenAL e lo riapre.
// Stacca il vecchio handler audio Mumble e rilascia il device sink prima
// di reinizializzare, evitando handler duplicati e device non rilasciati.
func (b *DoorPhoneServer) ResetStream() {
	b.Stream.link.Detach()
	if b.Stream.deviceSink != nil {
		b.Stream.contextSink.Destroy()
		b.Stream.deviceSink.CloseDevice()
		b.Stream.contextSink = nil
		b.Stream.deviceSink = nil
	}
	time.Sleep(50 * time.Millisecond)
	b.OpenStream()
}

// goStreamStats registra nel log le statistiche sugli stream audio attivi e le goroutine obsolete.
func goStreamStats() {
	log.Println("info: Active Streams")
	StreamTrackerMu.RLock()
	for item, value := range StreamTracker {
		log.Printf("info: Item=%v UserID=%v UserName=%v Session=%v AudioStreamChannel=%v CreatedAt=%v",
			item, value.UserID, value.UserName, value.UserSession, value.C, value.CreatedAt.Format("15:04:05"))
	}
	StreamTrackerMu.RUnlock()
	log.Printf("Total GoRoutines Open=%v, Total GoRoutines Wasted=%v \n", TotalStreams, NeedToKill)
}

// CleanupStaleStreams forza la terminazione di stream più vecchi di maxAge.
// Ritorna il numero di stream terminati.
// Questa funzione è chiamata periodicamente per prevenire accumulo di goroutine zombie.
func CleanupStaleStreams(maxAge time.Duration) int {
	cleaned := 0
	now := time.Now()

	StreamTrackerMu.Lock()
	defer StreamTrackerMu.Unlock()

	for userID, stream := range StreamTracker {
		age := now.Sub(stream.CreatedAt)
		if age > maxAge {
			log.Printf("warn: Force-cleaning stale stream for UserID=%v UserName=%v Age=%v",
				userID, stream.UserName, age.Round(time.Second))

			if stream.Cancel != nil {
				stream.Cancel()
			}
			delete(StreamTracker, userID)
			cleaned++
		}
	}

	return cleaned
}

// StartStreamCleanupMonitor avvia una goroutine che pulisce periodicamente gli stream stale.
// Controlla ogni 5 minuti e rimuove stream più vecchi di 10 minuti.
func StartStreamCleanupMonitor() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		log.Println("info: Stream cleanup monitor started (check every 5min, max age 10min)")

		for {
			select {
			case <-GetGlobalContext().Done():
				log.Println("info: Stream cleanup monitor stopped")
				return
			case <-ticker.C:
				cleaned := CleanupStaleStreams(10 * time.Minute)
				if cleaned > 0 {
					log.Printf("info: Periodic cleanup removed %d stale streams", cleaned)
				}
			}
		}
	}()
}

// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// webrtcVideoPayloadType è il payload type RTP usato per il video H.264 nella connessione WebRTC
const webrtcVideoPayloadType = 96

// handleWebRTCOffer gestisce la negoziazione WebRTC per lo streaming video dalla telecamera.
// Accetta un'offerta SDP dal browser, crea una connessione peer e avvia il bridge RTSP->WebRTC.
func (b *DoorPhoneServer) handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	panelSecurityHeaders(w)
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vs := Config.Global.Software.Camera
	if !vs.Video.Enabled || vs.Video.Endpoint == "" {
		fmt.Fprintf(w, `{"error":"RTSP stream not configured or disabled"}`)
		return
	}

	var req struct {
		SDP  string `json:"sdp"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		http.Error(w, "codec register: "+err.Error(), http.StatusInternalServerError)
		return
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	// Local network: no STUN/TURN needed
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, "pc: "+err.Error(), http.StatusInternalServerError)
		return
	}

	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
		"video", "doorphoneserver",
	)
	if err != nil {
		pc.Close()
		http.Error(w, "track: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err = pc.AddTrack(videoTrack); err != nil {
		pc.Close()
		http.Error(w, "addtrack: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err = pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  req.SDP,
	}); err != nil {
		pc.Close()
		http.Error(w, "remote sdp: "+err.Error(), http.StatusInternalServerError)
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		http.Error(w, "answer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		http.Error(w, "local sdp: "+err.Error(), http.StatusInternalServerError)
		return
	}
	select {
	case <-gatherDone:
	case <-time.After(10 * time.Second):
		log.Printf("[webrtc] ICE gather timeout")
	}

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed ||
			s == webrtc.PeerConnectionStateClosed ||
			s == webrtc.PeerConnectionStateDisconnected {
			once.Do(cancel)
		}
	})

	rtspURL := rtspURLWithCreds(vs.Video.Endpoint, vs.Username, vs.Password)
	go func() {
		defer once.Do(cancel)
		defer pc.Close()
		if err := bridgeFFmpegToWebRTC(ctx, rtspURL, videoTrack); err != nil && ctx.Err() == nil {
			log.Printf("[webrtc] bridge: %v", err)
		}
	}()

	ld := pc.LocalDescription()
	if err := json.NewEncoder(w).Encode(map[string]string{
		"sdp":  ld.SDP,
		"type": ld.Type.String(),
	}); err != nil {
		log.Printf("error: encode webrtc offer: %v", err)
	}
}

// bridgeFFmpegToWebRTC trasferisce il flusso video RTSP verso una traccia WebRTC tramite ffmpeg e UDP.
// Usa una porta UDP locale come bridge tra ffmpeg (output RTP) e la traccia WebRTC.
// @param ctx contesto per la cancellazione dello streaming
// @param rtspURL URL RTSP della telecamera con credenziali incorporate
// @param track traccia WebRTC locale su cui scrivere i pacchetti RTP H.264
// @return errore se la connessione o il trasferimento falliscono
func bridgeFFmpegToWebRTC(ctx context.Context, rtspURL string, track *webrtc.TrackLocalStaticRTP) error {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("udp listen: %w", err)
	}
	defer conn.Close()

	_, portStr, _ := net.SplitHostPort(conn.LocalAddr().String())

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", "tcp",
		"-i", rtspURL,
		"-an",
		"-c:v", "copy",
		"-f", "rtp",
		"rtp://127.0.0.1:"+portStr,
	)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	defer func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("[webrtc] ffmpeg wait error: %v", err)
		}
		if out := stderrBuf.String(); out != "" {
			sc := bufio.NewScanner(strings.NewReader(out))
			for sc.Scan() {
				l := strings.ToLower(sc.Text())
				if strings.Contains(l, "error") || strings.Contains(l, "failed") || strings.Contains(l, "invalid") || strings.Contains(l, "could not") {
					log.Printf("[webrtc] ffmpeg: %s", sc.Text())
				}
			}
		}
	}()

	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			log.Printf("[webrtc] SetDeadline error: %v", err)
			return nil
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return err
		}

		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		pkt.PayloadType = webrtcVideoPayloadType

		if err := track.WriteRTP(&pkt); err != nil && ctx.Err() == nil {
			log.Printf("[webrtc] writeRTP: %v", err)
		}
	}
}

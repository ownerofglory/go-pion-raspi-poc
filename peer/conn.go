package peer

import (
	"context"
	"fmt"
	"github.com/ownerofglory/go-pion-raspi-poc/media"
	"github.com/ownerofglory/go-pion-raspi-poc/signaling"
	"github.com/pion/webrtc/v4"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type peerConnHandler struct {
	signalingClient signaling.Client
	pc              *webrtc.PeerConnection
	lastCallerID    string
	mx              sync.RWMutex
}

func NewWebRTCPeerConnHandler(signalingClient signaling.Client, cfg *webrtc.Configuration) (*peerConnHandler, error) {
	pc, err := webrtc.NewPeerConnection(*cfg)
	if err != nil {
		slog.Warn("webrtc PC create failed, retrying with STUN-only", "err", err)
		fallback := webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{URLs: []string{"stun:stun.l.google.com:19302"}},
			},
		}
		pc, err = webrtc.NewPeerConnection(fallback)
		if err != nil {
			return nil, fmt.Errorf("NewPeerConnection failed: %w", err)
		}
	}
	return &peerConnHandler{
		signalingClient: signalingClient,
		pc:              pc,
		mx:              sync.RWMutex{},
	}, nil
}

func (ch *peerConnHandler) HandleConnection(ctx context.Context) {
	// Tracks: H264 + Opus as RTP
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "pi-h264",
	)
	if err != nil {
		slog.Error("Video track", "err", err)
		return
	}
	if _, err = ch.pc.AddTrack(videoTrack); err != nil {
		slog.Error("add video track", "err", err)
		return
	}

	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "pi-opus",
	)
	if err != nil {
		slog.Error("Audio track", "err", err)
		return
	}
	if _, err = ch.pc.AddTrack(audioTrack); err != nil {
		slog.Error("add audio track", "err", err)
		return
	}

	ch.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		cj := c.ToJSON()

		var mid string
		if cj.SDPMid != nil {
			mid = *cj.SDPMid
		}

		ch.mx.RLock()
		to := ch.lastCallerID
		ch.mx.RUnlock()
		if to == "" {
			return
		}

		msg := signaling.ClientMessage{
			Signal: &signaling.WebrtcSignal{
				Candidate:     cj.Candidate,
				SDPMid:        mid,
				SDPMLineIndex: cj.SDPMLineIndex,
			},
			To: to,
		}
		_ = ch.signalingClient.Write(&msg)
	})

	ch.pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		slog.Info("PeerConnection state", "state", s)
	})

	// On incoming remote track (from browser)
	ch.pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		slog.Info("Got remote track", "track", tr.Codec().MimeType)
	})

	ch.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		slog.Info("Data channel opened", "channel", dc.Label())
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			slog.Info("Message from data channel", "channel", dc.Label(), "message", string(m.Data))
		})
	})

	videoPort := getenv("VIDEO_PORT", "5004")
	audioPort := getenv("AUDIO_PORT", "5006")

	// Default GStreamer pipelines -> RTP to localhost
	defaultVideo := `libcamerasrc ! video/x-raw,width=640,height=480,framerate=30/1 ! videoconvert ! x264enc tune=zerolatency bitrate=800 speed-preset=ultrafast key-int-max=60 ! h264parse config-interval=1 ! rtph264pay pt=96 config-interval=1 ! udpsink host=127.0.0.1 port=` + videoPort
	defaultAudio := `alsasrc ! audioconvert ! audioresample ! opusenc bitrate=24000 ! rtpopuspay pt=111 ! udpsink host=127.0.0.1 port=` + audioPort

	gstVideo := getenv("GST_VIDEO_PIPELINE", defaultVideo)
	gstAudio := getenv("GST_AUDIO_PIPELINE", defaultAudio)

	// start GStreamer pipelines (RTP to localhost)
	videoCmd := media.StartGst(ctx, gstVideo, "video")
	audioCmd := media.StartGst(ctx, gstAudio, "audio")

	// Readers for RTP over UDP
	go media.PumpRTP(ctx, "127.0.0.1:"+videoPort, videoTrack, 1400, "video")
	go media.PumpRTP(ctx, "127.0.0.1:"+audioPort, audioTrack, 1200, "audio")

	go func() {
		for {
			var m *signaling.ClientMessage
			m, err := ch.signalingClient.Read()
			if err != nil {
				slog.Error("Read message failed", "err", err)
				return
			}

			// Initial hello {from: "<id>"} with no "signal"
			if m.Signal == nil && m.From != "" && strings.TrimSpace(m.To) == "" {
				slog.Info("Got ID", "ID", m.From)
				continue
			}

			if m.Signal == nil {
				continue
			}

			switch {
			case m.Signal.Type == "offer" && m.Signal.SDP != "":
				// Remember who to answer/trickle to
				ch.mx.Lock()
				ch.lastCallerID = m.From
				ch.mx.Unlock()

				slog.Info("Got offer from", "ID", m.From)
				slog.Info("creating answer...")
				offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.Signal.SDP}
				if err := ch.pc.SetRemoteDescription(offer); err != nil {
					slog.Warn("Set remote description failed", "err", err)
					continue
				}

				answer, err := ch.pc.CreateAnswer(nil)
				if err != nil {
					slog.Warn("Create answer failed", "err", err)
					continue
				}
				if err := ch.pc.SetLocalDescription(answer); err != nil {
					slog.Warn("Set local description failed", "err", err)
					continue
				}

				resp := signaling.ClientMessage{
					Signal: &signaling.WebrtcSignal{
						Type: answer.Type.String(),
						SDP:  answer.SDP,
					},
					To: m.From,
				}
				if err := ch.signalingClient.Write(&resp); err != nil {
					slog.Warn("Write answer failed", "err", err)
				}

			case m.Signal.Type == "answer":
				// don't place calls from Pi in this app
				slog.Warn("Unexpected answer (ignored)")

			case m.Signal.Candidate != "":
				c := webrtc.ICECandidateInit{
					Candidate:     m.Signal.Candidate,
					SDPMid:        nullableStringPtr(m.Signal.SDPMid),
					SDPMLineIndex: m.Signal.SDPMLineIndex,
				}
				if err := ch.pc.AddICECandidate(c); err != nil {
					slog.Warn("Add ICE candidate failed", "err", err)
				}
			}
		}
	}()

	<-ctx.Done()

	killProc(videoCmd)
	killProc(audioCmd)
}

func (ch *peerConnHandler) Shutdown() {
	err := ch.pc.Close()
	if err != nil {
		slog.Warn("PeerConnection shutdown failed", "err", err)
		return
	}
}

func nullableStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func killProc(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
	case <-done:
	}
}

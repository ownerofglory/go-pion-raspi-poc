package peer

import (
	"context"
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
	cfg             *webrtc.Configuration

	videoCmd, audioCmd *exec.Cmd

	callCtx    context.Context
	callCancel context.CancelFunc
	pumpsWG    sync.WaitGroup

	pendingCandidates []webrtc.ICECandidateInit
	haveRemoteDesc    bool

	inCall bool
}

func NewWebRTCPeerConnHandler(signalingClient signaling.Client, cfg *webrtc.Configuration) *peerConnHandler {
	return &peerConnHandler{
		signalingClient: signalingClient,
		cfg:             cfg,
		mx:              sync.RWMutex{},
	}
}

func (ch *peerConnHandler) HandleConnection(ctx context.Context) {
	go func() {
		for {
			var m *signaling.ClientMessage
			m, err := ch.signalingClient.Read()
			if err != nil {
				slog.Error("Read message failed", "err", err)
				return
			}

			// initial hello {from: "<id>"} with no "signal"
			if m.Signal == nil && m.From != "" && strings.TrimSpace(m.To) == "" {
				slog.Info("Got ID", "ID", m.From)
				continue
			}

			if m.Signal == nil {
				continue
			}

			switch {
			case m.Signal.Type == "offer" && m.Signal.SDP != "":
				slog.Info("Got offer from", "ID", m.From)

				// Reset previous calls
				ch.endCall()

				// answer/trickle to
				ch.mx.Lock()
				ch.haveRemoteDesc = false
				ch.lastCallerID = m.From
				ch.pendingCandidates = nil
				ch.mx.Unlock()

				slog.Debug("Creating peer connection")
				pc, err := webrtc.NewPeerConnection(*ch.cfg)
				if err != nil {
					slog.Warn("webrtc PC create failed, retrying with STUN-only", "err", err)
					fallback := webrtc.Configuration{
						ICEServers: []webrtc.ICEServer{
							{URLs: []string{"stun:stun.l.google.com:19302"}},
						},
					}
					pc, err = webrtc.NewPeerConnection(fallback)
					if err != nil {
						return
					}
				}
				ch.mx.Lock()
				ch.pc = pc
				ch.inCall = true
				ch.callCtx, ch.callCancel = context.WithCancel(ctx) // <- per-call ctx
				ch.mx.Unlock()
				slog.Debug("Peer connection created")

				slog.Debug("Initializing media: video, audio")
				videoCmd, audioCmd, err := ch.setupMedia(ch.callCtx)
				if err != nil {
					slog.Warn("failed to setup media", "err", err)
					ch.endCall()
					return
				}
				ch.mx.Lock()
				ch.videoCmd = videoCmd
				ch.audioCmd = audioCmd
				ch.mx.Unlock()

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
					switch s {
					case webrtc.PeerConnectionStateFailed,
						webrtc.PeerConnectionStateDisconnected,
						webrtc.PeerConnectionStateClosed:
						slog.Warn("Peer disconnected. Resetting...")
						ch.endCall()
					}
				})

				// On incoming remote track
				ch.pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
					slog.Info("Got remote track", "track", tr.Codec().MimeType)
				})

				ch.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
					slog.Info("Data channel opened", "channel", dc.Label())
					dc.OnMessage(func(m webrtc.DataChannelMessage) {
						slog.Info("Message from data channel", "channel", dc.Label(), "message", string(m.Data))
					})
				})

				slog.Info("creating answer...")

				offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.Signal.SDP}
				if err := ch.pc.SetRemoteDescription(offer); err != nil {
					slog.Warn("Set remote description failed", "err", err)
					ch.endCall()
					break
				}

				ch.mx.Lock()
				ch.haveRemoteDesc = true
				pcRef := ch.pc
				buffered := ch.pendingCandidates
				ch.pendingCandidates = nil
				ch.mx.Unlock()

				for _, cand := range buffered {
					_ = pcRef.AddICECandidate(cand)
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
				ch.mx.RLock()
				// ignore candidates from a different caller
				sameCaller := m.From == ch.lastCallerID
				pc := ch.pc
				ready := ch.haveRemoteDesc && pc != nil
				ch.mx.RUnlock()

				if !sameCaller {
					slog.Debug("Ignoring candidate from non-current caller", "from", m.From)
					break
				}

				c := webrtc.ICECandidateInit{
					Candidate:     m.Signal.Candidate,
					SDPMid:        nullableStringPtr(m.Signal.SDPMid),
					SDPMLineIndex: m.Signal.SDPMLineIndex,
				}

				if !ready {
					// Buffer until we have pc + remote desc
					ch.mx.Lock()
					ch.pendingCandidates = append(ch.pendingCandidates, c)
					ch.mx.Unlock()
					break
				}
				if err := ch.pc.AddICECandidate(c); err != nil {
					slog.Warn("Add ICE candidate failed", "err", err)
				}
			}
		}
	}()

	<-ctx.Done()
	slog.Info("Finishing handling connections")
	ch.endCall()
}

func (ch *peerConnHandler) setupMedia(ctx context.Context) (videoCmd, audioCmd *exec.Cmd, err error) {
	// in setupMedia
	if getenv("DISABLE_AUDIO", "") == "" {
		// start audio pipeline + PumpRTP
		var audioTrack *webrtc.TrackLocalStaticRTP
		audioTrack, err = webrtc.NewTrackLocalStaticRTP(
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

		audioPort := getenv("AUDIO_PORT", "5006")
		defaultAudio := `alsasrc ! audioconvert ! audioresample ! opusenc bitrate=24000 ! rtpopuspay pt=111 ! udpsink host=127.0.0.1 port=` + audioPort
		gstAudio := getenv("GST_AUDIO_PIPELINE", defaultAudio)

		audioCmd = media.StartGst(ctx, gstAudio, "audio")

		ch.pumpsWG.Add(1)
		go func() {
			defer ch.pumpsWG.Done()
			media.PumpRTP(ctx, "127.0.0.1:"+audioPort, audioTrack, 1200, "audio")
		}()
	} else {
		slog.Info("Audio disabled")
	}

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

	videoPort := getenv("VIDEO_PORT", "5004")

	// Default GStreamer pipelines -> RTP to localhost
	defaultVideo := `libcamerasrc ! video/x-raw,width=640,height=480,framerate=30/1 ! videoconvert ! x264enc tune=zerolatency bitrate=800 speed-preset=ultrafast key-int-max=60 ! h264parse config-interval=1 ! rtph264pay pt=96 config-interval=1 ! udpsink host=127.0.0.1 port=` + videoPort

	gstVideo := getenv("GST_VIDEO_PIPELINE", defaultVideo)

	// start GStreamer pipelines (RTP to localhost)
	videoCmd = media.StartGst(ctx, gstVideo, "video")

	// Readers for RTP over UDP
	ch.pumpsWG.Add(1)
	go func() {
		defer ch.pumpsWG.Done()
		media.PumpRTP(ctx, "127.0.0.1:"+videoPort, videoTrack, 1400, "video")
	}()

	slog.Debug("Started video and audio streams")
	return videoCmd, audioCmd, nil
}

func (ch *peerConnHandler) endCall() {
	ch.mx.Lock()
	if !ch.inCall {
		ch.mx.Unlock()
		return
	}
	ch.inCall = false

	if ch.callCancel != nil {
		ch.callCancel()
		ch.callCancel = nil
	}

	// stop GStreamer processes first
	killProc(ch.videoCmd)
	killProc(ch.audioCmd)
	ch.videoCmd, ch.audioCmd = nil, nil
	ch.mx.Unlock()

	// wait for RTP pumps to exit after ctx.Done()
	done := make(chan struct{})
	go func() {
		ch.pumpsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		slog.Warn("RTP pumps slow to exit; forcing continue")
	}

	// close PeerConnection and clear state
	ch.mx.Lock()
	if ch.pc != nil {
		_ = ch.pc.Close()
		ch.pc = nil
	}
	ch.lastCallerID = ""
	ch.haveRemoteDesc = false
	ch.pendingCandidates = nil
	ch.mx.Unlock()
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

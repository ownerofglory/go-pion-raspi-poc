package main

import (
	"context"
	"github.com/ownerofglory/go-pion-raspi-poc/peer"
	"github.com/ownerofglory/go-pion-raspi-poc/signalws"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"
)

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	//  env config
	wsURL := mustGetenv("SIGNAL_WS_URL")
	origin := mustGetenv("SIGNAL_ORIGIN")
	rtcCfgURL := mustGetenv("RTC_CONFIG_URL")

	// connect signaling
	header := http.Header{}
	header.Set("Origin", origin)

	wsClient, err := signalws.NewWebSocketClient(wsURL, header)
	if err != nil {
		slog.Error("Failed to create web socket client", "err", err)
		return
	}
	defer wsClient.Close()

	// fetch rtc configuration
	cfg, err := peer.FetchRTCConfig(rtcCfgURL)
	if err != nil {
		slog.Warn("rtc-config fetch failed, fallback to STUN only: %v", err)
		cfg = &webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{URLs: []string{"stun:stun.l.google.com:19302"}},
			},
		}
	}

	//  create PeerConnection handler
	pch := peer.NewWebRTCPeerConnHandler(wsClient, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	pch.HandleConnection(ctx)

	// graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	slog.Info("Shutting down...")
	cancel()
	time.Sleep(300 * time.Millisecond)
}

func mustGetenv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env %s", key)
	}
	return v
}

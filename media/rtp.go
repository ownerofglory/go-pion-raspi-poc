package media

import (
	"context"
	"errors"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"log/slog"
	"net"
	"time"
)

// PumpRTP listens on UDP addr and forwards RTP packets to a TrackLocalStaticRTP
func PumpRTP(ctx context.Context, addr string, track *webrtc.TrackLocalStaticRTP, mtu int, tag string) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		slog.Error("Failed to resolve UDP address", "addr", addr, "err", err)
		return
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		slog.Error("Failed to listen UDP address", "addr", addr, "err", err)
		return
	}
	defer conn.Close()

	buf := make([]byte, mtu)
	for {
		// keep the read unblocked with a short timeout
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))

		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					slog.Info("PumpRTP shutting down", "tag", tag)
					return
				default:
					continue
				}
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			slog.Error("UDP read error", "addr", addr, "err", err)
			return
		}

		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			// ignore non-RTP
			continue
		}
		if err := track.WriteRTP(&pkt); err != nil {
			slog.Error("Failed to write to track", "tag", tag, "err", err)
			return
		}
	}
}

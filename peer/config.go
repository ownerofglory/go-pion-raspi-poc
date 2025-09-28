package peer

import (
	"encoding/json"
	"fmt"
	"github.com/pion/webrtc/v4"
	"net/http"
)

type ConfigWire struct {
	ICEServers []webrtc.ICEServer `json:"iceServers"`
}

func FetchRTCConfig(url string) (*webrtc.Configuration, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rtc-config request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rtc-config bad status: %s", resp.Status)
	}

	var wire ConfigWire
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode rtc-config: %w", err)
	}
	return &webrtc.Configuration{ICEServers: wire.ICEServers}, nil
}

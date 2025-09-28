package signaling

type Client interface {
	Write(message *ClientMessage) error
	Read() (*ClientMessage, error)
}

type WebrtcSignal struct {
	Type          string  `json:"type,omitempty"` // offer/answer
	SDP           string  `json:"sdp,omitempty"`
	Candidate     string  `json:"candidate,omitempty"`
	SDPMid        string  `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
}

type ClientMessage struct {
	Signal *WebrtcSignal `json:"signal,omitempty"`
	To     string        `json:"to,omitempty"`
	From   string        `json:"from,omitempty"`
}

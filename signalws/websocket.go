package signalws

import (
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/ownerofglory/go-pion-raspi-poc/signaling"
	"log/slog"
	"net/http"
)

type client struct {
	conn *websocket.Conn
}

func NewWebSocketClient(wsURL string, header http.Header) (*client, error) {
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		slog.Error("WebSocket Dial error:", "err", err)
		return nil, fmt.Errorf("WebSocket Dial error: %w", err)
	}

	return &client{
		conn: ws,
	}, nil
}

func (c *client) Write(message *signaling.ClientMessage) error {
	err := c.conn.WriteJSON(message)
	if err != nil {
		slog.Error("error writing to websocket:", "err", err)
		return fmt.Errorf("error writing to websocket: %w", err)
	}

	return nil
}

func (c *client) Read() (*signaling.ClientMessage, error) {
	var m signaling.ClientMessage
	err := c.conn.ReadJSON(&m)
	if err != nil {
		slog.Error("error reading from client:", "err", err)
		return nil, fmt.Errorf("error reading from client: %w", err)
	}

	return &m, nil
}

func (c *client) Close() {
	slog.Debug("closing websocket client")
	if c.conn == nil {
		return
	}

	err := c.conn.Close()
	if err != nil {
		slog.Error("error when closing websocket connection", "err", err)
		return
	}
	slog.Debug("closed websocket client")
}

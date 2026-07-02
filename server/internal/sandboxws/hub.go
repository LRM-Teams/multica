package sandboxws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

type ClientIdentity struct {
	NodeID  string
	NodeKey string
}

type Hub struct {
	upgrader websocket.Upgrader

	mu     sync.RWMutex
	byNode map[string]map[*client]bool
}

type client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	identity ClientIdentity
}

func NewHub() *Hub {
	return &Hub{
		upgrader: websocket.Upgrader{
			// Sandbox nodes authenticate with Authorization headers before upgrade.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		byNode: make(map[string]map[*client]bool),
	}
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request, identity ClientIdentity) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("sandbox websocket upgrade failed", "error", err)
		return
	}
	c := &client{hub: h, conn: conn, send: make(chan []byte, 16), identity: identity}
	h.register(c)
	go c.writePump()
	go c.readPump()
}

func (h *Hub) NotifyJobAvailable(nodeID, jobID string) {
	if h == nil || nodeID == "" {
		return
	}
	frame, err := json.Marshal(protocol.Message{
		Type: protocol.EventSandboxJobAvailable,
		Payload: mustMarshalRaw(protocol.SandboxJobAvailablePayload{
			NodeID: nodeID,
			JobID:  jobID,
		}),
	})
	if err != nil {
		return
	}
	h.notifyNode(nodeID, frame)
}

func (h *Hub) notifyNode(nodeID string, frame []byte) bool {
	h.mu.RLock()
	clients := h.byNode[nodeID]
	slow := make([]*client, 0)
	delivered := false
	for c := range clients {
		select {
		case c.send <- frame:
			delivered = true
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range slow {
		h.unregister(c)
		c.conn.Close()
	}
	return delivered
}

func (h *Hub) ConnectionCount(nodeID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byNode[nodeID])
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	conns := h.byNode[c.identity.NodeID]
	if conns == nil {
		conns = make(map[*client]bool)
		h.byNode[c.identity.NodeID] = conns
	}
	conns[c] = true
	total := len(conns)
	h.mu.Unlock()
	slog.Info("sandbox websocket connected", "node_id", c.identity.NodeID, "node_key", c.identity.NodeKey, "connections", total)
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if conns := h.byNode[c.identity.NodeID]; conns != nil {
		delete(conns, c)
		if len(conns) == 0 {
			delete(h.byNode, c.identity.NodeID)
		}
	}
	h.mu.Unlock()
	slog.Info("sandbox websocket disconnected", "node_id", c.identity.NodeID, "node_key", c.identity.NodeKey)
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(64 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Debug("sandbox websocket read error", "error", err, "node_id", c.identity.NodeID)
			}
			return
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func mustMarshalRaw(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

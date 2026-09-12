package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	appmetrics "github.com/example/pulseboard/internal/metrics"
	"github.com/example/pulseboard/internal/store"
	"github.com/gorilla/websocket"
)

const maxPointsPerMessage = 128
const maxPointsPerStroke = 20000

type Client struct {
	Peer
	RoomID      string
	conn        *websocket.Conn
	send        chan []byte
	hub         *Hub
	store       store.BoardStore
	metrics     *appmetrics.Metrics
	writeWait   time.Duration
	pongWait    time.Duration
	inProgress  map[string]*store.Stroke
	onPersisted func(string, store.Stroke)
}

type incoming struct {
	Type      string        `json:"type"`
	OpID      string        `json:"opId,omitempty"`
	StrokeID  string        `json:"strokeId,omitempty"`
	Color     string        `json:"color,omitempty"`
	Width     float64       `json:"width,omitempty"`
	Points    []store.Point `json:"points,omitempty"`
	X         float64       `json:"x,omitempty"`
	Y         float64       `json:"y,omitempty"`
	LatencyMS float64       `json:"latencyMs,omitempty"`
}

func NewClient(peer Peer, roomID string, conn *websocket.Conn, hub *Hub, boardStore store.BoardStore, metrics *appmetrics.Metrics, writeWait, pongWait time.Duration, onPersisted func(string, store.Stroke)) *Client {
	return &Client{Peer: peer, RoomID: roomID, conn: conn, send: make(chan []byte, 256), hub: hub, store: boardStore, metrics: metrics, writeWait: writeWait, pongWait: pongWait, inProgress: make(map[string]*store.Stroke), onPersisted: onPersisted}
}
func (c *Client) Enqueue(payload []byte) {
	select {
	case c.send <- payload:
	default:
		c.metrics.DroppedMessages.Inc()
	}
}
func (c *Client) EnqueueJSON(value any) {
	payload, err := json.Marshal(value)
	if err == nil {
		c.Enqueue(payload)
	}
}

func (c *Client) ReadPump() {
	defer func() { c.persistInProgress(); c.hub.Unregister(c); _ = c.conn.Close() }()
	_ = c.conn.SetReadDeadline(time.Now().Add(c.pongWait))
	c.conn.SetPongHandler(func(string) error { _ = c.conn.SetReadDeadline(time.Now().Add(c.pongWait)); return nil })
	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Debug("websocket closed", "error", err)
			}
			return
		}
		started := time.Now()
		var msg incoming
		if json.Unmarshal(payload, &msg) != nil {
			continue
		}
		c.metrics.Messages.WithLabelValues("in", msg.Type).Inc()
		if !c.handle(msg) {
			continue
		}
		c.metrics.OperationLatency.Observe(time.Since(started).Seconds())
	}
}
func (c *Client) handle(msg incoming) bool {
	switch msg.Type {
	case "stroke:start":
		if !validID(msg.StrokeID) || !validColor(msg.Color) || msg.Width < 1 || msg.Width > 40 || len(msg.Points) != 1 || !validPoints(msg.Points) {
			return false
		}
		c.inProgress[msg.StrokeID] = &store.Stroke{ID: msg.StrokeID, UserID: c.ID, Color: msg.Color, Width: msg.Width, Points: append([]store.Point(nil), msg.Points...), CreatedAt: time.Now().UnixMilli()}
		c.broadcastOperation(msg)
		return true
	case "stroke:points":
		stroke, ok := c.inProgress[msg.StrokeID]
		if !ok || len(msg.Points) == 0 || len(msg.Points) > maxPointsPerMessage || len(stroke.Points)+len(msg.Points) > maxPointsPerStroke || !validPoints(msg.Points) {
			return false
		}
		stroke.Points = append(stroke.Points, msg.Points...)
		c.broadcastOperation(msg)
		return true
	case "stroke:end":
		stroke, ok := c.inProgress[msg.StrokeID]
		if !ok {
			return false
		}
		delete(c.inProgress, msg.StrokeID)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.store.SaveStroke(ctx, c.RoomID, *stroke); err != nil {
			c.metrics.PersistenceErrors.Inc()
			slog.Error("save stroke", "error", err, "room", c.RoomID)
		} else if c.onPersisted != nil {
			c.onPersisted(c.RoomID, *stroke)
		}
		c.broadcastOperation(msg)
		return true
	case "cursor":
		if !finite(msg.X) || !finite(msg.Y) {
			return false
		}
		c.hub.BroadcastEphemeralJSON(c.RoomID, map[string]any{"type": "cursor", "userId": c.ID, "x": msg.X, "y": msg.Y})
		return true
	case "latency":
		if msg.LatencyMS >= 0 && msg.LatencyMS < 60000 {
			c.metrics.StrokeLatency.Observe(msg.LatencyMS / 1000)
		}
		return false
	default:
		return false
	}
}
func (c *Client) broadcastOperation(msg incoming) {
	c.metrics.Operations.WithLabelValues(msg.Type).Inc()
	msg.Color = ""
	msg.Width = 0
	c.hub.BroadcastJSON(c.RoomID, map[string]any{"type": msg.Type, "opId": msg.OpID, "strokeId": msg.StrokeID, "userId": c.ID, "color": func() string {
		if s := c.inProgress[msg.StrokeID]; s != nil {
			return s.Color
		}
		return ""
	}(), "width": func() float64 {
		if s := c.inProgress[msg.StrokeID]; s != nil {
			return s.Width
		}
		return 0
	}(), "points": msg.Points})
}
func (c *Client) persistInProgress() {
	for _, stroke := range c.inProgress {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := c.store.SaveStroke(ctx, c.RoomID, *stroke); err != nil {
			c.metrics.PersistenceErrors.Inc()
		} else if c.onPersisted != nil {
			c.onPersisted(c.RoomID, *stroke)
		}
		cancel()
	}
}
func (c *Client) WritePump() {
	ticker := time.NewTicker(c.pongWait * 9 / 10)
	defer func() { ticker.Stop(); _ = c.conn.Close() }()
	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if c.conn.WriteMessage(websocket.TextMessage, payload) != nil {
				return
			}
			c.metrics.Messages.WithLabelValues("out", "broadcast").Inc()
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}

func validID(value string) bool {
	return len(value) > 0 && len(value) <= 80 && strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) == -1
}
func validColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
func validPoints(points []store.Point) bool {
	for _, p := range points {
		if !finite(p.X) || !finite(p.Y) || p.X < 0 || p.Y < 0 || p.X > 100000 || p.Y > 100000 {
			return false
		}
	}
	return true
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func Upgrader(origins map[string]struct{}, enableCompression bool) websocket.Upgrader {
	return websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, EnableCompression: enableCompression, CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		_, ok := origins[origin]
		return ok
	}}
}

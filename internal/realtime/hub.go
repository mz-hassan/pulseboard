package realtime

import (
	"encoding/json"
	"errors"
	"sync"

	appmetrics "github.com/example/pulseboard/internal/metrics"
)

var ErrRoomFull = errors.New("room is at capacity")

type Peer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}
type envelope struct {
	roomID  string
	payload []byte
}
type registration struct {
	client *Client
	result chan error
}

type Hub struct {
	register           chan registration
	unregister         chan *Client
	broadcast          chan envelope
	rooms              map[string]map[*Client]struct{}
	maxRoomUsers       int
	metrics            *appmetrics.Metrics
	once               sync.Once
	currentConnections int
	peakConnections    int
	roomPeaks          map[string]int
}

func NewHub(maxRoomUsers int, metrics *appmetrics.Metrics) *Hub {
	return &Hub{register: make(chan registration), unregister: make(chan *Client), broadcast: make(chan envelope, 4096), rooms: make(map[string]map[*Client]struct{}), maxRoomUsers: maxRoomUsers, metrics: metrics, roomPeaks: make(map[string]int)}
}
func (h *Hub) Start() { h.once.Do(func() { go h.run() }) }
func (h *Hub) Register(c *Client) error {
	result := make(chan error, 1)
	h.register <- registration{client: c, result: result}
	return <-result
}
func (h *Hub) Unregister(c *Client) { h.unregister <- c }
func (h *Hub) Broadcast(roomID string, payload []byte) {
	h.broadcast <- envelope{roomID: roomID, payload: payload}
}
func (h *Hub) BroadcastJSON(roomID string, value any) {
	if payload, err := json.Marshal(value); err == nil {
		h.Broadcast(roomID, payload)
	}
}

func (h *Hub) run() {
	for {
		select {
		case reg := <-h.register:
			room := h.rooms[reg.client.RoomID]
			if len(room) >= h.maxRoomUsers {
				reg.result <- ErrRoomFull
				continue
			}
			if room == nil {
				room = make(map[*Client]struct{})
				h.rooms[reg.client.RoomID] = room
				h.metrics.Rooms.Inc()
			}
			peers := make([]Peer, 0, len(room))
			for client := range room {
				peers = append(peers, client.Peer)
			}
			room[reg.client] = struct{}{}
			h.currentConnections++
			if h.currentConnections > h.peakConnections {
				h.peakConnections = h.currentConnections
				h.metrics.ConnectionPeak.Set(float64(h.peakConnections))
			}
			if len(room) > h.roomPeaks[reg.client.RoomID] {
				h.roomPeaks[reg.client.RoomID] = len(room)
				h.metrics.RoomConnectionPeak.WithLabelValues(reg.client.RoomID).Set(float64(len(room)))
			}
			h.metrics.Connections.Inc()
			h.metrics.ConnectionsTotal.Inc()
			h.metrics.RoomConnections.WithLabelValues(reg.client.RoomID).Inc()
			reg.client.EnqueueJSON(map[string]any{"type": "presence:snapshot", "peers": peers})
			h.broadcastRoom(reg.client.RoomID, mustJSON(map[string]any{"type": "presence:join", "peer": reg.client.Peer}))
			reg.result <- nil
		case client := <-h.unregister:
			room, ok := h.rooms[client.RoomID]
			if !ok {
				continue
			}
			if _, ok = room[client]; !ok {
				continue
			}
			delete(room, client)
			close(client.send)
			h.currentConnections--
			h.metrics.Connections.Dec()
			h.metrics.RoomConnections.WithLabelValues(client.RoomID).Dec()
			if len(room) == 0 {
				delete(h.rooms, client.RoomID)
				h.metrics.Rooms.Dec()
				h.metrics.RoomConnections.DeleteLabelValues(client.RoomID)
			} else {
				h.broadcastRoom(client.RoomID, mustJSON(map[string]any{"type": "presence:leave", "userId": client.ID}))
			}
		case msg := <-h.broadcast:
			h.broadcastRoom(msg.roomID, msg.payload)
		}
	}
}
func (h *Hub) broadcastRoom(roomID string, payload []byte) {
	room := h.rooms[roomID]
	if room == nil {
		return
	}
	for client := range room {
		select {
		case client.send <- payload:
		default:
			h.metrics.DroppedMessages.Inc()
			close(client.send)
			delete(room, client)
			h.currentConnections--
			h.metrics.Connections.Dec()
			h.metrics.RoomConnections.WithLabelValues(roomID).Dec()
		}
	}
	if len(room) == 0 {
		delete(h.rooms, roomID)
		h.metrics.Rooms.Dec()
		h.metrics.RoomConnections.DeleteLabelValues(roomID)
	}
}
func mustJSON(v any) []byte { payload, _ := json.Marshal(v); return payload }

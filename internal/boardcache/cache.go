// Package boardcache keeps a short-lived, serialized board snapshot close to
// the WebSocket edge. It prevents a reconnect burst from issuing one Redis
// LRANGE and one JSON encoding operation per reconnecting client.
package boardcache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/example/pulseboard/internal/store"
)

type entry struct {
	payload   []byte
	strokes   []store.Stroke
	expiresAt time.Time
}

type flight struct {
	done    chan struct{}
	payload []byte
	err     error
}

type Cache struct {
	store      store.BoardStore
	ttl        time.Duration
	maxStrokes int
	maxBytes   int
	usedBytes  int
	mu         sync.Mutex
	entries    map[string]entry
	flights    map[string]*flight
}

func New(boardStore store.BoardStore, ttl time.Duration, maxStrokes, maxBytes int) *Cache {
	return &Cache{store: boardStore, ttl: ttl, maxStrokes: maxStrokes, maxBytes: maxBytes, entries: make(map[string]entry), flights: make(map[string]*flight)}
}

// State returns a ready-to-send board:state WebSocket message. Concurrent
// cold loads for the same room share the same Redis query.
func (c *Cache) State(ctx context.Context, roomID string) ([]byte, bool, error) {
	c.mu.Lock()
	if cached, ok := c.entries[roomID]; ok && time.Now().Before(cached.expiresAt) {
		payload := append([]byte(nil), cached.payload...)
		c.mu.Unlock()
		return payload, true, nil
	}
	c.deleteLocked(roomID)
	if active := c.flights[roomID]; active != nil {
		c.mu.Unlock()
		select {
		case <-active.done:
			return append([]byte(nil), active.payload...), true, active.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	active := &flight{done: make(chan struct{})}
	c.flights[roomID] = active
	c.mu.Unlock()

	strokes, err := c.store.LoadStrokes(ctx, roomID)
	payload := []byte(nil)
	if err == nil {
		payload, err = encode(strokes)
	}

	c.mu.Lock()
	if err == nil {
		c.putLocked(roomID, entry{payload: append([]byte(nil), payload...), strokes: strokes, expiresAt: time.Now().Add(c.ttl)})
	}
	active.payload, active.err = append([]byte(nil), payload...), err
	delete(c.flights, roomID)
	close(active.done)
	c.mu.Unlock()
	return payload, false, err
}

// Append updates an already-cached board after Redis has durably accepted the
// completed stroke. If no cache entry exists, the next reconnect loads Redis.
func (c *Cache) Append(roomID string, stroke store.Stroke) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, ok := c.entries[roomID]
	if !ok || time.Now().After(cached.expiresAt) {
		return
	}
	cached.strokes = append(cached.strokes, stroke)
	if len(cached.strokes) > c.maxStrokes {
		cached.strokes = cached.strokes[len(cached.strokes)-c.maxStrokes:]
	}
	payload, err := encode(cached.strokes)
	if err != nil {
		delete(c.entries, roomID)
		return
	}
	cached.payload, cached.expiresAt = payload, time.Now().Add(c.ttl)
	c.putLocked(roomID, cached)
}

func (c *Cache) Clear(roomID string) {
	c.mu.Lock()
	c.deleteLocked(roomID)
	c.mu.Unlock()
}

func (c *Cache) putLocked(roomID string, value entry) {
	c.deleteLocked(roomID)
	if len(value.payload) > c.maxBytes {
		return
	}
	now := time.Now()
	for id, cached := range c.entries {
		if now.After(cached.expiresAt) {
			c.deleteLocked(id)
		}
	}
	for c.usedBytes+len(value.payload) > c.maxBytes && len(c.entries) > 0 {
		var oldestID string
		var oldest time.Time
		for id, cached := range c.entries {
			if oldestID == "" || cached.expiresAt.Before(oldest) {
				oldestID, oldest = id, cached.expiresAt
			}
		}
		c.deleteLocked(oldestID)
	}
	c.entries[roomID] = value
	c.usedBytes += len(value.payload)
}

func (c *Cache) deleteLocked(roomID string) {
	if cached, ok := c.entries[roomID]; ok {
		c.usedBytes -= len(cached.payload)
		delete(c.entries, roomID)
	}
}

func encode(strokes []store.Stroke) ([]byte, error) {
	return json.Marshal(map[string]any{"type": "board:state", "strokes": strokes})
}

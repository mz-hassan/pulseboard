package realtime

import (
	"math"
	"testing"

	appmetrics "github.com/example/pulseboard/internal/metrics"
	"github.com/example/pulseboard/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

func TestValidation(t *testing.T) {
	if !validID("user_123-ABC") {
		t.Fatal("expected valid id")
	}
	for _, value := range []string{"", "spaces are bad", "!"} {
		if validID(value) {
			t.Fatalf("expected %q to be invalid", value)
		}
	}
	if !validColor("#12abEF") || validColor("red") {
		t.Fatal("color validation failed")
	}
	if !validPoints([]store.Point{{X: 0, Y: 100}}) {
		t.Fatal("expected valid point")
	}
	if validPoints([]store.Point{{X: math.NaN(), Y: 0}}) || validPoints([]store.Point{{X: -1, Y: 0}}) {
		t.Fatal("expected invalid point")
	}
}

func TestHubEnforcesCapacityAndRecordsPeaks(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := appmetrics.New(registry)
	hub := NewHub(2, metrics)
	hub.Start()
	first := &Client{Peer: Peer{ID: "one"}, RoomID: "room", send: make(chan []byte, 8)}
	second := &Client{Peer: Peer{ID: "two"}, RoomID: "room", send: make(chan []byte, 8)}
	third := &Client{Peer: Peer{ID: "three"}, RoomID: "room", send: make(chan []byte, 8)}
	if err := hub.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := hub.Register(second); err != nil {
		t.Fatal(err)
	}
	if err := hub.Register(third); err != ErrRoomFull {
		t.Fatalf("expected ErrRoomFull, got %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, family := range families {
		if len(family.Metric) > 0 && family.Metric[0].Gauge != nil {
			values[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
	}
	if values["whiteboard_websocket_connections_peak"] != 2 {
		t.Fatalf("connection peak = %v", values["whiteboard_websocket_connections_peak"])
	}
	if values["whiteboard_room_connections_peak"] != 2 {
		t.Fatalf("room peak = %v", values["whiteboard_room_connections_peak"])
	}
	hub.Unregister(first)
	hub.Unregister(second)
}

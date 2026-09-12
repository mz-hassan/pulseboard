package metrics

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	Connections         prometheus.Gauge
	ConnectionPeak      prometheus.Gauge
	ConnectionsTotal    prometheus.Counter
	RoomConnections     *prometheus.GaugeVec
	RoomConnectionPeak  *prometheus.GaugeVec
	Rooms               prometheus.Gauge
	Operations          *prometheus.CounterVec
	Messages            *prometheus.CounterVec
	OperationLatency    prometheus.Histogram
	StrokeLatency       prometheus.Histogram
	ReconnectAttempts   prometheus.Counter
	ReconnectSuccesses  prometheus.Counter
	DroppedMessages     prometheus.Counter
	PersistenceErrors   prometheus.Counter
	SnapshotCacheHits   prometheus.Counter
	SnapshotCacheMisses prometheus.Counter
	RejectedConnections *prometheus.CounterVec
	EphemeralDrops      prometheus.Counter
	BroadcastQueueDepth prometheus.Gauge
}

func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Connections:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "whiteboard_websocket_connections", Help: "Current open WebSocket connections."}),
		ConnectionPeak:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "whiteboard_websocket_connections_peak", Help: "Peak simultaneous WebSocket connections since process start."}),
		ConnectionsTotal:    prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_websocket_connections_total", Help: "Accepted WebSocket connections."}),
		RoomConnections:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "whiteboard_room_connections", Help: "Current connections by room."}, []string{"room_id"}),
		RoomConnectionPeak:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "whiteboard_room_connections_peak", Help: "Peak simultaneous connections by room since process start."}, []string{"room_id"}),
		Rooms:               prometheus.NewGauge(prometheus.GaugeOpts{Name: "whiteboard_active_rooms", Help: "Rooms active on this instance."}),
		Operations:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "whiteboard_operations_total", Help: "Validated drawing operations processed."}, []string{"type"}),
		Messages:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "whiteboard_messages_total", Help: "WebSocket messages handled."}, []string{"direction", "type"}),
		OperationLatency:    prometheus.NewHistogram(prometheus.HistogramOpts{Name: "whiteboard_operation_processing_seconds", Help: "Server-side operation processing time.", Buckets: prometheus.ExponentialBuckets(0.00005, 2, 16)}),
		StrokeLatency:       prometheus.NewHistogram(prometheus.HistogramOpts{Name: "whiteboard_stroke_round_trip_seconds", Help: "Client-observed operation round-trip time.", Buckets: []float64{.001, .0025, .005, .01, .025, .05, .075, .1, .15, .25, .5, 1, 2}}),
		ReconnectAttempts:   prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_reconnect_attempts_total", Help: "Client-declared reconnect attempts."}),
		ReconnectSuccesses:  prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_reconnect_successes_total", Help: "Successful reconnects."}),
		DroppedMessages:     prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_dropped_messages_total", Help: "Messages dropped for slow clients."}),
		PersistenceErrors:   prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_persistence_errors_total", Help: "Persistence operation failures."}),
		SnapshotCacheHits:   prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_snapshot_cache_hits_total", Help: "Board state requests served from cache or a coalesced load."}),
		SnapshotCacheMisses: prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_snapshot_cache_misses_total", Help: "Board state requests that loaded Redis."}),
		RejectedConnections: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "whiteboard_rejected_connections_total", Help: "WebSocket registrations rejected before overload."}, []string{"reason"}),
		EphemeralDrops:      prometheus.NewCounter(prometheus.CounterOpts{Name: "whiteboard_ephemeral_messages_dropped_total", Help: "Cursor/presence updates dropped under backpressure."}),
		BroadcastQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{Name: "whiteboard_broadcast_queue_depth", Help: "Pending durable broadcast messages."}),
	}
	reg.MustRegister(m.Connections, m.ConnectionPeak, m.ConnectionsTotal, m.RoomConnections, m.RoomConnectionPeak, m.Rooms, m.Operations, m.Messages, m.OperationLatency, m.StrokeLatency, m.ReconnectAttempts, m.ReconnectSuccesses, m.DroppedMessages, m.PersistenceErrors, m.SnapshotCacheHits, m.SnapshotCacheMisses, m.RejectedConnections, m.EphemeralDrops, m.BroadcastQueueDepth)
	return m
}

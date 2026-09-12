package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/example/pulseboard/internal/boardcache"
	"github.com/example/pulseboard/internal/config"
	appmetrics "github.com/example/pulseboard/internal/metrics"
	"github.com/example/pulseboard/internal/realtime"
	"github.com/example/pulseboard/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var roomPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,64}$`)

type application struct {
	cfg          config.Config
	store        *store.RedisStore
	hub          *realtime.Hub
	metrics      *appmetrics.Metrics
	snapshots    *boardcache.Cache
	restoreSlots chan struct{}
}

func main() {
	cfg := config.Load()
	redisStore := store.NewRedis(cfg.RedisAddr, cfg.RedisPassword, cfg.RoomTTL, cfg.MaxStrokesPerRoom)
	defer redisStore.Close()
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	metrics := appmetrics.New(registry)
	snapshots := boardcache.New(redisStore, cfg.SnapshotCacheTTL, cfg.MaxStrokesPerRoom, cfg.SnapshotCacheMaxBytes)
	hub := realtime.NewHub(cfg.MaxRoomUsers, cfg.MaxConnections, metrics)
	hub.Start()
	app := &application{cfg: cfg, store: redisStore, hub: hub, metrics: metrics, snapshots: snapshots, restoreSlots: make(chan struct{}, cfg.MaxConcurrentRestores)}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", app.health)
	mux.HandleFunc("POST /api/rooms", app.createRoom)
	mux.HandleFunc("POST /api/rooms/{roomID}/clear", app.clearRoom)
	mux.HandleFunc("POST /api/metrics/reconnect-attempt", app.reconnectAttempt)
	mux.HandleFunc("GET /ws/{roomID}", app.websocket)
	mux.Handle("GET /", spa(http.Dir("web")))
	server := &http.Server{Addr: ":" + cfg.Port, Handler: securityHeaders(requestLog(mux)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		slog.Info("Pulseboard listening", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func (a *application) createRoom(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RoomID string `json:"roomId"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body)
	}
	body.RoomID = strings.TrimSpace(body.RoomID)
	if body.RoomID == "" {
		body.RoomID = randomID(6)
	}
	if !roomPattern.MatchString(body.RoomID) {
		writeError(w, http.StatusBadRequest, "room ID must be 3-64 letters, numbers, hyphens, or underscores")
		return
	}
	token, err := store.NewToken()
	if err != nil {
		writeError(w, 500, "could not create room")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err = a.store.CreateRoom(ctx, body.RoomID, token); err != nil {
		writeError(w, http.StatusConflict, "room ID is already in use")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"roomId": body.RoomID, "token": token})
}
func (a *application) clearRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomID")
	token := bearer(r)
	if !a.authorized(w, r, roomID, token) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Clear(ctx, roomID); err != nil {
		a.metrics.PersistenceErrors.Inc()
		writeError(w, 500, "could not clear board")
		return
	}
	a.snapshots.Clear(roomID)
	a.hub.BroadcastJSON(roomID, map[string]string{"type": "board:clear"})
	w.WriteHeader(http.StatusNoContent)
}
func (a *application) reconnectAttempt(w http.ResponseWriter, r *http.Request) {
	a.metrics.ReconnectAttempts.Inc()
	w.WriteHeader(http.StatusNoContent)
}
func (a *application) websocket(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("roomID")
	token := r.URL.Query().Get("token")
	if !a.authorized(w, r, roomID, token) {
		return
	}
	userID := r.URL.Query().Get("userId")
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	color := r.URL.Query().Get("color")
	if !roomPattern.MatchString(userID) || len(name) < 1 || len(name) > 40 || len(color) != 7 {
		writeError(w, 400, "invalid participant details")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	select {
	case a.restoreSlots <- struct{}{}:
	default:
		a.metrics.RejectedConnections.WithLabelValues("restore_busy").Inc()
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "board restore busy; retry shortly")
		cancel()
		return
	}
	statePayload, cacheHit, err := a.snapshots.State(ctx, roomID)
	// The limiter protects the persistence read, not the lifetime of the
	// WebSocket. Holding a slot until ReadPump returns caps long-lived
	// connections at MaxConcurrentRestores and turns excess clients into a
	// retry storm.
	<-a.restoreSlots
	if err == nil {
		if cacheHit {
			a.metrics.SnapshotCacheHits.Inc()
		} else {
			a.metrics.SnapshotCacheMisses.Inc()
		}
	}
	cancel()
	if err != nil {
		a.metrics.PersistenceErrors.Inc()
		writeError(w, 503, "board state unavailable")
		return
	}
	upgrader := realtime.Upgrader(a.cfg.AllowedOrigins, a.cfg.EnableCompression)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(a.cfg.MaxMessageBytes)
	client := realtime.NewClient(realtime.Peer{ID: userID, Name: name, Color: color}, roomID, conn, a.hub, a.store, a.metrics, a.cfg.WriteWait, a.cfg.PongWait, a.snapshots.Append)
	if err = a.hub.Register(client); err != nil {
		_ = conn.WriteControl(8, []byte("room full"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	client.Enqueue(statePayload)
	if r.URL.Query().Get("reconnect") == "1" {
		a.metrics.ReconnectSuccesses.Inc()
	}
	go client.WritePump()
	client.ReadPump()
}
func (a *application) authorized(w http.ResponseWriter, r *http.Request, roomID, token string) bool {
	if !roomPattern.MatchString(roomID) || token == "" {
		writeError(w, 401, "room access denied")
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	err := a.store.Authorize(ctx, roomID, token)
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrRoomNotFound):
		writeError(w, 404, "room not found")
	case errors.Is(err, store.ErrUnauthorized):
		writeError(w, 403, "room access denied")
	default:
		a.metrics.PersistenceErrors.Inc()
		writeError(w, 503, "room service unavailable")
	}
	return false
}
func (a *application) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		writeError(w, 503, "redis unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func bearer(r *http.Request) string {
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if strings.HasPrefix(value, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	return ""
}
func randomID(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("room-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" {
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started).String())
		}
	})
}
func spa(root http.FileSystem) http.Handler {
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := root.Open(path); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}

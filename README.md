# Pulseboard

Pulseboard is a real-time collaborative whiteboard built as a performance-measurable systems project. Multiple participants share freehand strokes and cursor presence over WebSockets; Redis durably restores completed strokes after refreshes or service restarts. Every room is protected by a 192-bit capability token.

## Run it

Prerequisite: Docker Engine with the Compose v2 plugin.

```bash
docker compose up --build
```

Open [http://localhost:8080](http://localhost:8080). Create a room, copy its invite link, and open the link in another browser or private window. Redis data is kept in the `redis-data` Docker volume.

Stop the stack with `docker compose down`. To remove persisted boards as well, explicitly run `docker compose down -v`.

If Docker is not installed on Fedora/RHEL, install and start it with:

```bash
sudo dnf install -y docker docker-compose-plugin
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
```

Log out and back in after the final command. On Ubuntu/Debian, follow Docker's official Engine installation instructions so the current Compose plugin is installed.

## System design

```text
Browser canvas
  ├─ REST: create / clear room
  └─ WebSocket: strokes, cursor presence, latency samples
             │
        Go HTTP server
  ┌──────────┴───────────┐
  │ real-time room hub   │  fan-out, backpressure, presence
  │ BoardStore interface │  persistence boundary
  │ Prometheus registry  │  /metrics
  └──────────┬───────────┘
             │
          Redis AOF          token hashes + append-only stroke lists
```

The in-process hub owns live connections and ephemeral cursors. It only depends on the `BoardStore` interface; the Redis adapter owns room authorization and durable board state. Slow clients have bounded queues and are disconnected instead of consuming unbounded memory. Interrupted in-progress strokes are saved during disconnect cleanup.

Security controls include constant-time token comparison, SHA-256 token storage, strict room/user ID validation, WebSocket origin allowlisting, message and point limits, request body limits, and a non-root container. Anyone holding an invite token can draw and clear that room; this is intentionally simple room-level access control, not user identity authentication.

## Metrics

Raw Prometheus metrics are exposed at [http://localhost:8080/metrics](http://localhost:8080/metrics). Go's standard process collector also exports `process_cpu_seconds_total`, `process_resident_memory_bytes`, file descriptors, and runtime metrics.

Start the optional Prometheus UI at [http://localhost:9090](http://localhost:9090):

```bash
docker compose --profile observability up -d prometheus
```

Useful PromQL queries:

| Goal | Metric / query |
|---|---|
| Current / peak connections per instance | `whiteboard_websocket_connections` / `whiteboard_websocket_connections_peak` |
| Current / peak users by room | `whiteboard_room_connections` / `whiteboard_room_connections_peak` |
| Operations per second | `sum(rate(whiteboard_operations_total[1m]))` |
| Outbound messages per second | `sum(rate(whiteboard_messages_total{direction="out"}[1m]))` |
| Stroke latency p50 | `histogram_quantile(0.50, sum by (le) (rate(whiteboard_stroke_round_trip_seconds_bucket[5m])))` |
| Stroke latency p95 / p99 | Use `0.95` / `0.99` in the same query |
| Reconnection success rate | `rate(whiteboard_reconnect_successes_total[5m]) / rate(whiteboard_reconnect_attempts_total[5m])` |
| CPU cores consumed | `rate(process_cpu_seconds_total[1m])` |
| Resident memory | `process_resident_memory_bytes` |
| Backpressure failures | `rate(whiteboard_dropped_messages_total[1m])` |
| Rejected overload connections | `sum by (reason) (rate(whiteboard_rejected_connections_total[1m]))` |
| Snapshot cache hit ratio | `rate(whiteboard_snapshot_cache_hits_total[5m]) / (rate(whiteboard_snapshot_cache_hits_total[5m]) + rate(whiteboard_snapshot_cache_misses_total[5m]))` |
| Durable broadcast backlog | `whiteboard_broadcast_queue_depth` |
| Ephemeral cursor drops | `rate(whiteboard_ephemeral_messages_dropped_total[1m])` |

Round-trip latency is measured at the browser/load client: it timestamps each drawing operation, waits until the server-broadcast copy returns, and submits the observation over the existing socket. This captures client → server → room hub → client latency without relying on synchronized clocks.

## Load tests

The included k6 workload opens real WebSockets, sends complete strokes, records round-trip latency, reconnects clients, and enforces default p95/p99 and success-rate thresholds.

```bash
# 100 users in one room, 10 strokes/sec/user, 30 seconds
docker compose --profile load run --rm k6

# tune a run
VUS=500 ROOMS=5 OPS_PER_SECOND=4 TEST_DURATION=2m \
  docker compose --profile load run --rm k6
```

In `MODEL=stress`, `OPS_PER_SECOND` means strokes per second and every client draws continuously. In the default realistic model, the activity probability settings control traffic instead. The k6 summary reports `stroke_round_trip_ms` p50/p95/p99, `drawing_operations`, WebSocket success, reconnection success, and throughput.

The default `MODEL=realistic` is intended for product-capacity testing: 25% of connected users are active, active users start strokes at a randomized average of 0.35 strokes/second, each stroke unfolds over 0.35-1.25 seconds, and cursors move occasionally. These are configurable with `ACTIVE_USER_FRACTION`, `ACTIVE_STROKES_PER_SECOND`, and `CURSOR_UPDATES_PER_SECOND`. Use `MODEL=stress OPS_PER_SECOND=...` only to deliberately find overload behavior; it makes every client draw continuously and should not be presented as normal usage.

Run a concurrency sweep and retain both k6 summaries and `/metrics` snapshots:

```bash
chmod +x load/sweep.sh
LEVELS="50 100 250 500 1000" TEST_DURATION=1m OPS_PER_SECOND=5 ./load/sweep.sh
```

Results are written under `load/results/<UTC timestamp>/` and ignored by Git. Define “maximum sustained” before publishing a resume number—for example: the highest level completing a two-minute run with p95 under 150 ms, p99 under 300 ms, connection success above 99%, zero dropped messages, and no container restart. Record the machine CPU/RAM and Docker CPU/memory limit alongside the result.

### Isolated benchmark suite

For a reproducible cloud run, deploy the service on one VM and run k6 from a second VM. `load/run-remote-suite.sh` runs a 10-user smoke check, an escalating single-room sweep, a multi-room comparison, and a forced-reconnection workload. It stops the room sweep after the first failed latency or success-rate threshold. The workload varies each stroke's position, length, direction, color, width, and number of points; it uses deterministic per-VU pseudo-randomness so a failed run can be reproduced.

Run `load/capture-server-stats.sh RESULTS_DIR` on the server during the suite. It writes five-second `docker-stats.csv` samples and Prometheus snapshots. k6 writes a per-run console log and machine-readable summary JSON. Preserve these files, plus `docker compose logs`, as the evidence for reported results.

## Development and operations

```bash
docker compose logs -f app redis
curl -fsS http://localhost:8080/healthz
node load/smoke.mjs
docker build --target build -t pulseboard-test .
docker run --rm pulseboard-test go test ./...
```

Configuration is documented in `.env.example`. Copy it to `.env` only when overrides are needed. Production deployments should terminate TLS at a reverse proxy, set `ALLOWED_ORIGINS` to the exact HTTPS origin, use a Redis password/private network, and protect `/metrics` at the network layer.

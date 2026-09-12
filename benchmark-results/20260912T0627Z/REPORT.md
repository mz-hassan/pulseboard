# Pulseboard Azure benchmark — 2026-09-12

## Environment

- Server: Azure `Standard_B2als_v2` (2 vCPU, 4 GiB), Ubuntu 22.04, Docker Compose (Go application plus Redis).
- Load generator: separate Azure `Standard_B2als_v2` VM in the same private virtual network.
- Workload: k6 real WebSocket clients, one shared room unless noted; randomized freehand paths, colors, widths, and point counts; 30-second held connections.
- Server evidence: five-second Prometheus snapshots and Docker CPU/RAM samples.

Both VMs were deallocated after artifacts were copied.

## Results

| Workload | WebSocket success | Drawing ops/sec | Stroke p50 | p95 | p99 | Outcome |
|---|---:|---:|---:|---:|---:|---|
| Smoke: 10 users, 3 strokes/user/s | 100% | 89.3 | 5 ms | 11 ms | 14 ms | Passed |
| One room: 25 users, 4 strokes/user/s | 100% | 298.8 | 13 ms | 37 ms | 46 ms | Passed |
| One room: 50 users, 4 strokes/user/s | 100% | 594.5 | 62 ms | 176 ms | 216 ms | Connection test passed; responsiveness threshold failed |
| 10 rooms: 200 users, 3 strokes/user/s | 30.8% | 1,537.4 | 926 ms | 5,293 ms | 7,923 ms | Failed under overload |
| Reconnect: 100 users, 10-second connection lifetime | 15.3% | 466.0 | 1,260 ms | 5,977 ms | 7,261 ms | Failed; reconnect success 22.9% |

## Credible claims from this run

- **Responsive room limit on this instance/configuration:** **25 concurrent users** at 4 strokes/user/s (about **299 protocol drawing operations/sec**) while meeting the predefined p95 < 150 ms and p99 < 300 ms latency targets.
- **Measured sustained WebSocket connections:** **50 held WebSocket connections** for 30 seconds at 100% connection success. The 50-user workload did not meet the latency SLO, so it is not the responsive-room result.
- **Observed overload boundary:** 50 simultaneous users in one high-activity room exceeded the p95 latency target; much larger multi-room and reconnect churn tests showed sharp connection and latency degradation.
- **Peak application container CPU sample:** 191.93% of its two-core quota. **Peak application container memory:** 34.12% of the configured 1 GiB limit. These are sampled every five seconds, not continuous traces.

## Interpretation and caveats

This is a useful baseline rather than a global capacity claim. Fan-out is proportional to room size, and the test emits three wire operations per stroke; a single busy room therefore grows outbound work rapidly. The forced-reconnect scenario is intentionally harsh: it repeatedly reloads durable board state and reconnects the same population every 10 seconds. It establishes a breaking point and exposes an optimization target (connection admission/backoff, state snapshot handling, and horizontal fan-out), not a production reconnection SLO.

Raw k6 summaries/logs are in `loadgen/`; Prometheus snapshots, Docker samples, and application logs are in `server/`.

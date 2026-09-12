# Pulseboard Engineering Report

## Executive summary

Pulseboard is a Dockerized real-time collaborative whiteboard built as a measurable backend engineering project. Users create or join token-protected rooms, draw freehand strokes together over WebSockets, see live cursors and presence, and recover persisted board state after refreshing or reconnecting.

The backend was implemented in Go with Redis as the persistence layer. A minimal Canvas frontend is served by the Go application. Prometheus-compatible instrumentation and k6 WebSocket load tests measure latency, throughput, connections, reconnections, dropped messages, and resource usage.

Two application versions were evaluated:

- **V1 (`8136fd3`)**: the original complete implementation.
- **V2 (`bf37a9d`, subsequently fixed in `8dbeaec`)**: a defensive version adding snapshot caching, overload controls, bounded queues, connection limits, and safer restoration behavior.

The final recommendation is to continue with **corrected V2** because it has the safer production architecture and fails in a controlled, observable manner. V1 achieved greater short-duration distributed capacity, but it does so partly by tolerating more backlog rather than enforcing equally strict slow-client protection. V2 should therefore be the product baseline, followed by optimization of its fan-out and slow-client handling.

## System architecture

### Technology stack

| Component | Technology | Responsibility |
|---|---|---|
| Backend | Go | HTTP API, WebSocket lifecycle, authorization, validation, room coordination, and metrics |
| Real-time transport | Gorilla WebSocket | Bidirectional strokes, cursors, presence, acknowledgements, and reconnects |
| Persistence | Redis 8.2 | Room credentials and persisted drawing operations |
| Frontend | HTML, CSS, JavaScript Canvas | Drawing UI, room creation/joining, cursor display, and automatic reconnection |
| Metrics | Prometheus Go client | `/metrics` endpoint with counters, gauges, and histograms |
| Load testing | Grafana k6 | Real WebSocket connections, probabilistic drawing, latency correlation, and reconnect simulation |
| Packaging | Docker and Docker Compose | Reproducible application, Redis, Prometheus, and optional k6 services |

### Request and message flow

1. A client creates a room through `POST /api/rooms`, optionally providing a room ID.
2. The server generates a room token and persists its authorization record in Redis.
3. Participants connect to `/ws/{roomId}` with the room token and their display metadata.
4. The backend validates authorization and restores the board snapshot through the persistence/cache layer.
5. The room hub registers the WebSocket and broadcasts presence information.
6. Stroke operations are validated, persisted, and delivered to other room participants.
7. Cursor messages use an ephemeral path because losing an old cursor position is preferable to blocking drawing traffic.
8. Rejoining clients receive persisted board state and continue collaborating.

### Separation of concerns

The service deliberately separates real-time coordination from persistence:

- `internal/realtime` owns WebSocket clients, room membership, broadcast queues, and presence.
- `internal/store` owns Redis authorization and durable board operations.
- `internal/boardcache` owns cached serialized board snapshots.
- `internal/metrics` owns Prometheus collectors.
- `cmd/server` connects these layers through HTTP handlers and configuration.

This separation makes Redis replaceable without rewriting the WebSocket hub and makes the real-time layer testable independently from persistence.

## Functional capabilities

- Create or join a room using a room ID.
- Room-level token access control.
- Real-time synchronized freehand strokes.
- Live cursors and participant presence.
- Persisted board state after refresh or rejoin.
- Board clearing by authorized room members.
- Automatic client reconnection.
- Health endpoint and Prometheus `/metrics` endpoint.
- Configurable room, server, message, history, cache, and persistence limits.
- Fully containerized local deployment with Docker Compose.

## Observability and measurable metrics

The backend exposes or supports collection of:

- Current and peak WebSocket connections per instance.
- Current and peak connections per room.
- Active room count.
- Accepted and rejected connections by reason.
- Drawing operations processed by type.
- Broadcast queue depth.
- Slow-client message drops.
- Ephemeral cursor drops.
- Persistence errors.
- Snapshot-cache hits and misses.
- Reconnection attempts and successes.
- Server-recorded stroke latency.
- Process and container CPU/memory measurements.

k6 reports end-to-end stroke latency by assigning each operation an ID and measuring the time from transmission until that operation is echoed to its originating WebSocket. This produces p50, p95, p99, average, minimum, and maximum latency.

## Realistic workload model

The original stress workload had every user drawing continuously. That is useful for destructive stress testing but unrealistic for normal whiteboard use. The revised model represents human activity probabilistically:

- 25% of connected users are drawing-active during a session.
- Active users begin strokes according to a Poisson process averaging 0.35 strokes per second.
- A user may draw during one second and remain idle during several following seconds.
- Strokes contain randomized positions, angles, lengths, colors, widths, and point counts.
- Each user emits approximately 0.5 probabilistic cursor updates per second.
- Stroke points are delivered incrementally to resemble a hand moving across a canvas.

The approximate expected stroke-start rate is:

```text
connected users x 0.25 active fraction x 0.35 strokes/second
```

This still creates significant WebSocket traffic because a stroke contains start, point-batch, and end operations, and each operation is broadcast to every recipient in its room.

## Azure benchmark infrastructure

Two isolated Azure virtual machines were used in the `swedencentral` region:

| VM role | Azure size | Resources | Purpose |
|---|---|---|---|
| Pulseboard server | `Standard_B2als_v2` | 2 vCPUs, 4 GiB RAM | Ran the Go application and Redis containers |
| Load generator | `Standard_B2als_v2` | 2 vCPUs, 4 GiB RAM | Ran k6 separately from the system under test |

Both VMs communicated through private Azure networking. Separating the load generator prevented k6 from consuming the CPU and memory being measured on the application server.

The B-series VM is burstable. Short tests can benefit from accumulated CPU credits, so these results are valid for the recorded environment but should not be treated as a guaranteed production SLA. A fixed-performance D-series VM would be preferable for formal capacity certification.

The VMs were deallocated after artifact collection. They are not currently consuming VM compute.

## Initial realistic comparison: V1 versus V2

Both versions completed the planned suite through 200 users with 100% connection success.

### Single-room latency and throughput

| Concurrent users | Version | Operations/s | p50 | p95 | p99 | Connection success |
|---:|---|---:|---:|---:|---:|---:|
| 25 | V1 | 10.5 | 1 ms | 3 ms | 4 ms | 100% |
| 25 | V2 | 10.8 | 1 ms | 3 ms | 5 ms | 100% |
| 50 | V1 | 19.5 | 2 ms | 5 ms | 8 ms | 100% |
| 50 | V2 | 18.9 | 2 ms | 4 ms | 6.9 ms | 100% |
| 100 | V1 | 38.9 | 3 ms | 7.3 ms | 15 ms | 100% |
| 100 | V2 | 39.5 | 3 ms | 9 ms | 16 ms | 100% |
| 150 | V1 | 60.2 | 5 ms | 16 ms | 29 ms | 100% |
| 150 | V2 | 59.8 | 5 ms | 25 ms | 44 ms | 100% |
| 200 | V1 | 81.2 | 10 ms | 45 ms | 74.7 ms | 100% |
| 200 | V2 | 77.6 | 11 ms | 49 ms | 80.9 ms | 100% |

V1 was marginally faster during this healthy-path comparison. The differences below 100 users were negligible. At 150-200 users, V1 had a modest tail-latency advantage.

### Multi-room and reconnection results

| Scenario | Version | Operations/s | p50 | p95 | p99 | Result |
|---|---|---:|---:|---:|---:|---|
| 200 users across 10 rooms | V1 | 80.6 | 1 ms | 3 ms | 5 ms | Pass |
| 200 users across 10 rooms | V2 | 81.0 | 2 ms | 4 ms | 7 ms | Pass |
| 100-user forced reconnect | V1 | 40.7 | 3 ms | 11 ms | 41.1 ms | 400/400 reconnects |
| 100-user forced reconnect | V2 | 41.6 | 3 ms | 13 ms | 51.3 ms | 400/400 reconnects |

The reconnect scenario created 500 WebSocket sessions per version and recorded 400 forced reconnection attempts. Both versions achieved 100% reconnection success.

### Resource usage

During the relevant initial-suite windows:

| Metric | V1 | V2 |
|---|---:|---:|
| Average application CPU | 18.5% | 21.9% |
| Peak application CPU | 80.5% | 82.3% |
| Peak application memory | 32.6 MiB | 38.4 MiB |

Memory usage was extremely small compared with the one-GiB application-container limit. CPU, socket writes, and room fan-out became limiting factors before memory capacity.

## Breaking-point campaign

The first suite validated 200 users but did not identify a maximum. A second campaign raised the test-only room and server caps and deliberately continued until latency or session stability failed.

A probe passed only when:

- Stroke p95 remained below 150 ms.
- Stroke p99 remained below 300 ms.
- Initial connection success remained above 99%.
- WebSocket clients remained stable without a replacement-upgrade storm.

### Single-room breaking points

| Probe | Stable sessions | Operations/s | p50 | p95 | p99 | Result |
|---|---:|---:|---:|---:|---:|---|
| V1, 210 users | 100% | 82.5 | 15 ms | 66 ms | 108 ms | Pass |
| V1, 225 users | 100% | 93.8 | 29 ms | 195 ms | 413 ms | Fail |
| V1, 250 users | 100% | 93.2 | 532 ms | 2,227 ms | 3,233 ms | Fail |
| V1, 300 users | 98.4% initial success | 88.1 | 4,178 ms | 10,863 ms | 14,418 ms | Fail |
| V1, 400 users | 52.6% initial success | 96.2 | 9,822 ms | 18,316 ms | 20,692 ms | Fail |
| V2, repeated 200 users | 100% | 79.5 | 12 ms | 56 ms | 86 ms | Pass |
| V2, 225 users | 100% | 89.7 | 30 ms | 145 ms | 249 ms | Pass |
| V2, 235 users | 100% | 91.7 | 64 ms | 507 ms | 906 ms | Fail |
| V2, 250 users | 100% | 93.9 | 558 ms | 2,913 ms | 4,370 ms | Fail |
| V2, 300 users | 100% | 100.4 | 3,869 ms | 11,143 ms | 15,447 ms | Fail |
| V2, 400 users | 37.6% initial success | 95.6 | 9,876 ms | 17,916 ms | 20,016 ms | Fail |

The responsive single-room limits were therefore bracketed as follows:

- **V1: 210 users passed; 225 failed.**
- **V2: 225 users passed; 235 failed.**

V2 supported approximately 7% more users at the tested single-room boundary.

### Why 200 users worked but 250 failed

Same-room load grows faster than the number of users. More users generate more activity, and every drawing operation is delivered to more recipients. A simplified sender-recipient model grows approximately with the square of room population.

Moving from 200 to 250 users changes the approximate delivery work by:

```text
(250 / 200)^2 = 1.5625
```

That is about **56% more delivery work**, despite only 25% more users. Once socket write queues begin accumulating, latency grows rapidly. Slow-client eviction and reconnection attempts then add further work, producing a nonlinear cliff rather than a smooth slowdown.

The V2 200-user test was repeated to check reproducibility:

- Original V2 200-user result: p95 49 ms, p99 80.9 ms.
- Repeated V2 200-user result: p95 56 ms, p99 86 ms.

The close results support the conclusion that 200 users was genuinely healthy. Exact boundary values still require multiple repetitions on fixed-performance hardware before being used as a production SLA.

### Distributed server capacity

The distributed tests used approximately 20 users per room to reduce per-room fan-out while increasing total WebSocket connections.

| Probe | Stable checks | Replacement attempts | Operations/s | p95 | p99 | Result |
|---|---:|---:|---:|---:|---:|---|
| V1, 600 users / 30 rooms | 100% | 0 | 238.7 | 10 ms | 33 ms | Pass |
| V1, 800 users / 40 rooms | 100% | 0 | 318.2 | 30 ms | 66 ms | Pass |
| V1, 1,000 users / 50 rooms | 100% | 0 | 395.0 | 75 ms | 117 ms | Pass |
| V1, 1,200 users / 60 rooms | 75.4% | 411 | 435.3 | 131 ms | 211 ms | Fail: unstable sessions |
| V1, 1,500 users / 75 rooms | 95.4% | 76 | 549.2 | 257 ms | 412 ms | Fail: latency and stability |
| V2, 400 users / 20 rooms | 100% | 0 | 159.7 | 6 ms | 12 ms | Pass |
| V2, 600 users / 30 rooms | 100% | 0 | 240.5 | 7 ms | 27 ms | Pass |
| V2, 700 users / 35 rooms | 79.2% | 193 | 270.0 | 22 ms | 46 ms | Fail: unstable sessions |
| V2, 800 users / 40 rooms, clean repeat | 62.4% | 506 | 318.8 | 38 ms | 70 ms | Fail: unstable sessions |

The stable distributed capacity was therefore bracketed as follows:

- **V1: 1,000 connections passed; 1,200 failed.**
- **V2: 600 connections passed; 700 failed.**

V1 achieved the larger short-duration connection count and higher passing throughput. V2 disconnected slow consumers earlier when bounded queues filled. This protects memory and prevents unlimited backlog, but its current policy is too aggressive for high distributed concurrency on this VM.

## Failures discovered and corrected

### V2 restoration semaphore lifetime

The first V2 run would not retain more than 32 simultaneous WebSockets. Prometheus showed a peak of exactly 32 and rapidly increasing `restore_busy` rejections. This matched `MAX_CONCURRENT_RESTORES=32`, indicating a software limit rather than CPU or memory exhaustion.

The semaphore slot protecting initial board restoration had been deferred until the entire WebSocket handler returned. Because a WebSocket handler lives for the full connection, every connected user permanently occupied one restoration slot.

The fix released the semaphore immediately after snapshot restoration completed. After correction, V2 passed the complete 200-user suite and reached a 225-user single-room boundary. The fix is committed as:

```text
8dbeaec Fix restore limiter lifetime
```

### Misleading connection-success metric

The initial custom `websocket_connection_success` metric counted successful `open` events. During overload, a client could connect, be dropped shortly afterward, and reconnect. The metric could consequently report 100% even while k6 recorded hundreds of replacement upgrades.

The harness was corrected to measure separately:

- WebSocket upgrade-attempt success.
- Whether sessions survive at least 90% of their expected lifetime.

The corrected harness thresholds both metrics above 99%. This change is committed as:

```text
fb36ccc Measure stable WebSocket sessions in load tests
```

## Which version was better?

There is no single winner across every dimension.

### Where V1 was better

- Slightly lower latency at 150-200 users in the original suite.
- Lower measured CPU and memory overhead.
- Stable through 1,000 distributed users and approximately 395 operations/s.
- Greater short-duration connection capacity on the small Azure VM.

### Where V2 was better

- Supported 225 users in one room while V1 failed at that level.
- Added bounded broadcast behavior and explicit slow-client handling.
- Added snapshot caching and controlled concurrent board restoration.
- Added server-wide and room-level connection limits.
- Added clearer overload rejection metrics.
- Prevented unlimited queues from silently consuming memory.
- Provides a safer foundation for production behavior and future horizontal scaling.

### Final selection

**Corrected V2 is the recommended final product version.**

V1 wins the raw short-duration distributed benchmark, but capacity without bounded failure behavior is risky. V2 exposes a clear optimization target: its per-client queues and broadcast implementation need to maintain slow-client protection while avoiding premature disconnections.

The correct engineering direction is to optimize V2 rather than reverting to V1:

1. Coalesce cursor updates so only the newest position per user is retained.
2. Batch or shard large-room broadcast delivery.
3. Avoid repeating JSON serialization for every recipient.
4. Profile write-pump blocking and kernel socket-buffer pressure.
5. Use queue age and message type when deciding which data to drop.
6. Apply reconnect jitter and exponential backoff to prevent retry amplification.
7. Repeat boundary tests on a non-burstable VM using longer holds.

## Credible project metrics

The following statements are supported by the saved benchmark evidence:

- Validated **225 concurrent users in one collaborative room** on corrected V2 while meeting p95/p99 latency objectives.
- Sustained **1,000 distributed WebSocket users and 395 drawing operations/s** on V1 during a 30-second Azure benchmark.
- Sustained **600 distributed WebSocket users and 240.5 operations/s** on corrected V2 with no replacement connections.
- Achieved **400/400 successful forced reconnections** for both V1 and V2.
- Recorded **49 ms p95 and 81 ms p99 stroke latency** for corrected V2 at 200 users in one room; a repeated run measured 56/86 ms.
- Demonstrated **4 ms p95 and 7 ms p99** for 200 users spread across ten rooms.
- Identified and repaired a concurrency-limiter bug using Prometheus rejection and connection-peak metrics.
- Identified and repaired a load-test measurement flaw exposed only during overload testing.

These should be stated as tested results, not universal guarantees. Test duration, VM type, room distribution, workload model, network placement, and success thresholds should remain attached to any formal performance claim.

## Resume-ready summary

- Built a Dockerized collaborative whiteboard using Go, WebSockets, Redis, and Canvas, supporting persisted rooms, token access control, real-time strokes, live cursors, presence, and reconnection.
- Designed Prometheus instrumentation and probabilistic k6 workloads measuring latency percentiles, throughput, connections, reconnection success, drops, CPU, and memory.
- Validated 225 responsive users in one room, 1,000 distributed WebSocket users, 395 drawing operations/s, and 400/400 forced reconnections on Azure.
- Diagnosed concurrency-limiter and benchmark-accounting defects from telemetry, corrected them, and preserved reproducible before/after evidence.

## Repository versions

| Commit | Description |
|---|---|
| `8136fd3` | Complete V1 collaborative whiteboard |
| `dd02163` | Reproducible cloud benchmark harness |
| `bf37a9d` | Defensive V2 backend and realistic activity model |
| `8dbeaec` | Restoration limiter lifetime fix |
| `fb36ccc` | Stable WebSocket session metrics for load tests |

## Saved evidence and reports

- Main repository: `outputs/whiteboard-service`
- Initial realistic comparison: `outputs/whiteboard-service/benchmark-results/20260912T1638Z-v1-v2`
- Breaking-point evidence: `outputs/whiteboard-service/benchmark-results/20260912T1706Z-breakpoint`
- Initial visual report: `outputs/Pulseboard_V1_V2_Benchmark_Report.pdf`
- Breaking-point addendum: `outputs/Pulseboard_Breakpoint_Benchmark_Addendum.pdf`

The result directories contain k6 summary JSON, human-readable logs, Prometheus snapshots, Docker CPU/memory samples, server logs, run configuration, commit identifiers, and preserved failed-run evidence.

## Limitations

- Boundary probes were generally one 30-second sample rather than several repeated long-duration trials.
- Azure B-series CPU-credit state was not independently recorded.
- Both VMs were in the same Azure region, so latency excludes normal public-internet distance.
- Most distributed scenarios used approximately 20 users per room.
- Board histories were clean or small; extremely large persisted boards need a separate restoration benchmark.
- The load generator can itself become a bottleneck at very high virtual-user counts.
- The exact boundary can change with operating-system scheduling, Redis state, Go runtime behavior, and network buffering.

For a production capacity claim, repeat every boundary three to five times on fixed-performance hardware, report confidence intervals, use five-to-fifteen-minute hold periods, and monitor both the server and load-generator saturation levels.

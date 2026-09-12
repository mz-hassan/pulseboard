#!/usr/bin/env sh
# Run on the server VM while a k6 test runs.  Writes CPU/RAM plus a Prometheus
# snapshot every five seconds; stop it with Ctrl-C or SIGTERM.
set -eu
OUT_DIR="${1:?usage: capture-server-stats.sh RESULTS_DIR}"
mkdir -p "$OUT_DIR"
printf 'utc_timestamp,container,cpu_percent,memory_usage,memory_percent,net_io,block_io\n' > "$OUT_DIR/docker-stats.csv"
while true; do
  date -u +%Y-%m-%dT%H:%M:%SZ >> "$OUT_DIR/timestamps.log"
  docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}},{{.MemPerc}},{{.NetIO}},{{.BlockIO}}' pulseboard-app-1 pulseboard-redis-1 \
    | sed "s|^|$(date -u +%Y-%m-%dT%H:%M:%SZ),|" >> "$OUT_DIR/docker-stats.csv" || true
  curl -fsS http://localhost:8080/metrics > "$OUT_DIR/metrics-$(date -u +%Y%m%dT%H%M%SZ).prom" || true
  sleep 5
done

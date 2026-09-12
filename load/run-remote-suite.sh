#!/usr/bin/env sh
# Run on the isolated load-generator VM. Results are JSON/text files that can
# be copied back after the benchmark.
set -eu
SERVER_URL="${SERVER_URL:?set SERVER_URL, e.g. http://10.0.0.4:8080}"
WS_URL="$(printf '%s' "$SERVER_URL" | sed 's|^http|ws|')"
RESULTS="${RESULTS_DIR:-$HOME/pulseboard-results/$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$RESULTS"
run() {
  name="$1"; shift
  echo "[$(date -u +%FT%TZ)] $name" | tee "$RESULTS/${name}.log"
  if docker run --rm --network host --user "$(id -u):$(id -g)" -v "$PWD/load:/scripts:ro" -v "$RESULTS:/results" \
    -e BASE_URL="$SERVER_URL" -e WS_URL="$WS_URL" -e RUN_ID="$name-$(date +%s)" \
    -e MODEL="${MODEL:-realistic}" -e ACTIVE_USER_FRACTION="${ACTIVE_USER_FRACTION:-0.25}" \
    -e ACTIVE_STROKES_PER_SECOND="${ACTIVE_STROKES_PER_SECOND:-0.35}" -e CURSOR_UPDATES_PER_SECOND="${CURSOR_UPDATES_PER_SECOND:-0.5}" \
    "$@" grafana/k6:1.3.0 run --quiet --summary-export "/results/${name}.json" /scripts/collaboration.js \
    > "$RESULTS/${name}.log" 2>&1; then
    cat "$RESULTS/${name}.log"
    return 0
  fi
  cat "$RESULTS/${name}.log"
  echo "Stopping after ${name}: a required threshold failed." >&2
  return 1
}
run smoke -e VUS=10 -e ROOMS=1 -e OPS_PER_SECOND=3 -e TEST_DURATION=20s -e CONNECTION_LIFETIME=20s
for level in 25 50 100 150 200; do
  run "room-${level}" -e VUS="$level" -e ROOMS=1 -e OPS_PER_SECOND=4 -e TEST_DURATION=30s -e CONNECTION_LIFETIME=30s || break
done
run multiroom-200 -e VUS=200 -e ROOMS=10 -e OPS_PER_SECOND=3 -e TEST_DURATION=30s -e CONNECTION_LIFETIME=30s || true
run reconnect-100 -e VUS=100 -e ROOMS=1 -e OPS_PER_SECOND=3 -e TEST_DURATION=45s -e CONNECTION_LIFETIME=10s -e RECONNECT_MODE=true || true
echo "Results: $RESULTS"

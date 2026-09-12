#!/usr/bin/env sh
set -eu

levels="${LEVELS:-25 50 100 200 400}"
duration="${TEST_DURATION:-45s}"
ops="${OPS_PER_SECOND:-5}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
result_dir="load/results/${stamp}"
mkdir -p "$result_dir"

docker compose up -d --build app
for vus in $levels; do
  echo "Running ${vus} concurrent clients for ${duration}"
  VUS="$vus" TEST_DURATION="$duration" OPS_PER_SECOND="$ops" \
    docker compose --profile load run --rm k6 2>&1 | tee "${result_dir}/${vus}-users.txt"
  curl -fsS http://localhost:${APP_PORT:-8080}/metrics > "${result_dir}/${vus}-users.prom"
done

echo "Results saved in ${result_dir}"

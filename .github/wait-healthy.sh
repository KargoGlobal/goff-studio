#!/usr/bin/env bash
# Waits for a container's /healthz, then tears it down. Dumps logs on failure.
set -euo pipefail

container="$1"
port="$2"

for _ in $(seq 1 30); do
  if curl -fsS "http://localhost:${port}/healthz" >/dev/null 2>&1; then
    echo "healthz ok on ${port}"
    docker rm -f "$container" >/dev/null
    exit 0
  fi
  sleep 2
done

echo "${container} never became healthy:"
docker logs "$container"
docker rm -f "$container" >/dev/null
exit 1

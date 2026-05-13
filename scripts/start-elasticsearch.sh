#!/bin/bash
set -e

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "Starting Elasticsearch (many_faces_elastic)..."
docker compose up -d

echo "Waiting for Elasticsearch HTTP..."
for i in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:${ELASTIC_HTTP_HOST_PORT:-59200}/" >/dev/null 2>&1; then
    echo "Elasticsearch is up on http://localhost:${ELASTIC_HTTP_HOST_PORT:-59200}"
    exit 0
  fi
  sleep 2
done

echo "Elasticsearch did not become ready in time."
exit 1

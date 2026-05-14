#!/usr/bin/env bash
# End-to-end TLS + mTLS smoke for the search-worker: generates a demo CA + server + client PEM chain,
# starts docker-compose.tls-smoke.yml (Elasticsearch + worker), grpcurl Ping, then optional .NET client test.
#
# Prerequisites: bash, openssl, docker with compose v2, grpcurl (go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest),
# dotnet 10 SDK when RUN_DOTNET_TLS_SMOKE=1 (default in CI usage from workflow).
#
# Usage (from monorepo root):
#   chmod +x many_faces_elastic/scripts/smoke-grpc-tls.sh
#   many_faces_elastic/scripts/smoke-grpc-tls.sh
#
# Environment:
#   RUN_DOTNET_TLS_SMOKE=0 — skip dotnet test (grpcurl only).
#   ELASTIC_DIR — override path to many_faces_elastic (default: script dir parent).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ELASTIC_DIR="${ELASTIC_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)}"
COMPOSE_FILE="$ELASTIC_DIR/docker-compose.tls-smoke.yml"
PROJECT_NAME="${TLS_SMOKE_PROJECT_NAME:-mf-search-tls-smoke}"
RUN_DOTNET_TLS_SMOKE="${RUN_DOTNET_TLS_SMOKE:-1}"

if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "docker compose v2 is required" >&2
  exit 1
fi
if ! command -v grpcurl >/dev/null 2>&1; then
  echo "grpcurl is required (e.g. go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest)" >&2
  exit 1
fi

CERT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mf-search-tls-smoke.XXXXXX")"
export SEARCH_TLS_SMOKE_CERT_DIR="$(cd "$CERT_DIR" && pwd)"

cleanup() {
  if [[ -f "$COMPOSE_FILE" ]]; then
    (cd "$ELASTIC_DIR" && docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" down -v 2>/dev/null) || true
  fi
  rm -rf "$CERT_DIR" 2>/dev/null || true
}
trap cleanup EXIT

echo "== Generating demo CA + server + client PEMs in $SEARCH_TLS_SMOKE_CERT_DIR"
openssl genrsa -out "$CERT_DIR/ca.key" 2048
openssl req -x509 -new -nodes -key "$CERT_DIR/ca.key" -sha256 -days 1 \
  -subj "/CN=Many Faces TLS Smoke CA" -out "$CERT_DIR/ca.crt"

openssl genrsa -out "$CERT_DIR/server.key" 2048
openssl req -new -key "$CERT_DIR/server.key" -out "$CERT_DIR/server.csr" \
  -subj "/CN=localhost"
openssl x509 -req -in "$CERT_DIR/server.csr" -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
  -out "$CERT_DIR/server.crt" -days 1 -sha256 \
  -extfile <(printf '%s\n' 'subjectAltName=DNS:localhost,IP:127.0.0.1')

openssl genrsa -out "$CERT_DIR/client.key" 2048
openssl req -new -key "$CERT_DIR/client.key" -out "$CERT_DIR/client.csr" \
  -subj "/CN=many-faces-tls-smoke-client"
openssl x509 -req -in "$CERT_DIR/client.csr" -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
  -out "$CERT_DIR/client.crt" -days 1 -sha256

# The search-worker image runs as nonroot (distroless). Bind-mounted PEMs must be readable by any UID
# inside the container (ephemeral smoke material only — do not use this pattern for real secrets).
chmod 755 "$CERT_DIR"
chmod a+r "$CERT_DIR"/*.crt "$CERT_DIR/server.key" 2>/dev/null || true
chmod 600 "$CERT_DIR/client.key"

echo "== Starting Elasticsearch + search-worker (TLS + mTLS)"
(cd "$ELASTIC_DIR" && docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" up -d --build)

echo "== Waiting for Elasticsearch on localhost:59210"
for _ in $(seq 1 90); do
  if curl -sf "http://127.0.0.1:59210" >/dev/null 2>&1; then
    echo "   Elasticsearch responded"
    break
  fi
  sleep 2
done
if ! curl -sf "http://127.0.0.1:59210" >/dev/null 2>&1; then
  echo "Elasticsearch did not become ready on :59210" >&2
  docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" logs --tail 80 >&2 || true
  exit 1
fi

echo "== Waiting for gRPC Ping (grpcurl, TLS + mTLS)"
GRPC_OK=0
for _ in $(seq 1 60); do
  if OUT="$(grpcurl -servername localhost -cacert "$CERT_DIR/ca.crt" -cert "$CERT_DIR/client.crt" -key "$CERT_DIR/client.key" \
    -d '{"correlation_id":"smoke-grpcurl"}' \
    127.0.0.1:59211 manyfaces.search.v1.SearchService/Ping 2>&1)"; then
    if echo "$OUT" | grep -q '"elasticsearchReachable": true'; then
      echo "   grpcurl Ping succeeded"
      GRPC_OK=1
      break
    fi
  fi
  sleep 2
done
if [[ "$GRPC_OK" != "1" ]]; then
  echo "grpcurl Ping did not return elasticsearchReachable true in time" >&2
  docker compose -f "$COMPOSE_FILE" -p "$PROJECT_NAME" logs --tail 120 search-worker >&2 || true
  exit 1
fi

if [[ "$RUN_DOTNET_TLS_SMOKE" == "1" ]]; then
  if ! command -v dotnet >/dev/null 2>&1; then
    echo "dotnet not on PATH; set RUN_DOTNET_TLS_SMOKE=0 to skip .NET smoke" >&2
    exit 1
  fi
  ROOT="$(cd "$ELASTIC_DIR/.." && pwd)"
  echo "== .NET GrpcChannel + SearchWorkerGrpcProbe Ping (monorepo: $ROOT)"
  export SEARCH_TLS_SMOKE=1
  export SEARCH_TLS_SMOKE_GRPC_PORT=59211
  export SEARCH_TLS_SMOKE_CA="$CERT_DIR/ca.crt"
  export SEARCH_TLS_SMOKE_CLIENT_CERT="$CERT_DIR/client.crt"
  export SEARCH_TLS_SMOKE_CLIENT_KEY="$CERT_DIR/client.key"
  (cd "$ROOT/many_faces_backend" && dotnet test BeDemo.Api.Tests/BeDemo.Api.Tests.csproj -c Release \
    --filter "FullyQualifiedName~SearchWorkerTlsEndToEndSmokeTests" -v minimal --nologo)
fi

echo "== TLS smoke completed successfully"

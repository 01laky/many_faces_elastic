#!/usr/bin/env bash
# regen-go-stubs.sh — regenerate the Go protobuf + gRPC stubs for the search worker
# from the canonical nested submodule proto. Run from the repository root.
#
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc on PATH (or installed into
# "$(go env GOPATH)/bin"). See README.md "Regenerating Go stubs" for the Docker variant.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export PATH="$PATH:$(go env GOPATH)/bin"

# Install the codegen plugins if they are not already present (idempotent).
if ! command -v protoc-gen-go >/dev/null 2>&1; then
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
fi
if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
fi

mkdir -p gen
protoc -I many_faces_proto/proto \
  --go_out=gen --go_opt=paths=source_relative \
  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
  manyfaces/search/v1/search.proto

echo "Regenerated gen/manyfaces/search/v1/search.pb.go and search_grpc.pb.go"

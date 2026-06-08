# Makefile for the many_faces_elastic search-worker.
#
# Common targets:
#   make gen    — regenerate the Go protobuf + gRPC stubs from the canonical proto submodule
#   make build  — go build ./...
#   make test   — go test ./...
#   make lint    — run scripts/lint.sh

.PHONY: gen build test lint

# Regenerate gen/manyfaces/search/v1/*.pb.go from many_faces_proto/proto/manyfaces/search/v1/search.proto.
# Installs protoc-gen-go / protoc-gen-go-grpc into $(go env GOPATH)/bin if missing. Requires protoc on PATH.
gen:
	./scripts/regen-go-stubs.sh

build:
	go build ./...

test:
	go test ./...

lint:
	./scripts/lint.sh

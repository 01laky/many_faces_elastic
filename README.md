# many_faces_elastic

**Canonical GitHub repository:** [github.com/01laky/many_faces_elastic](https://github.com/01laky/many_faces_elastic) — default branch **`main`**.  
Standalone clone: `git clone git@github.com:01laky/many_faces_elastic.git` (HTTPS: `https://github.com/01laky/many_faces_elastic.git`). In the **many_faces_main** monorepo this tree is typically checked out as the `many_faces_elastic/` git submodule ([monorepo submodule guide](https://github.com/01laky/many_faces_main/blob/main/docs/guides/git-submodules.md)).

Optional **Elasticsearch** stack plus a colocated **Go gRPC search-worker** for the Many Faces monorepo. Together they provide a **read-optimized search projection** (full-text, facets, autocomplete later). **PostgreSQL remains the system of record**; this repository ships Docker tooling, the worker source, and the **canonical `.proto`** contract consumed by **`many_faces_backend`** (C# gRPC client) and eventually **`many_faces_ai`** (Python client).

## Image and licensing

This submodule uses the **official Elastic** image `docker.elastic.co/elasticsearch/elasticsearch`. Elastic Stack components are subject to the **Elastic License v2** (not Apache 2.0). For strict OSS-only deployments, evaluate **OpenSearch** instead and align client libraries and documentation across the monorepo.

## Architecture (v1)

| Component | Role |
| --------- | ---- |
| **Elasticsearch** | Stores the search index; exposes HTTP on port `9200` inside the compose network. |
| **search-worker** (`cmd/search-worker`) | The **only** shipping path that may call Elasticsearch HTTP for application logic. Exposes **gRPC** on `50052` inside the container. |
| **many_faces_backend** | REST/OpenAPI for products; calls the worker via **gRPC** (`Grpc.Net.Client`). Does **not** use Elasticsearch HTTP for the main search path. |

Browsers, SPAs, and mobile apps **never** call the worker or Elasticsearch directly.

## Ports (development defaults)

| Direction | Value |
| --------- | ----- |
| Host → Elasticsearch HTTP | `localhost:59200` → container `9200` |
| Host → worker gRPC (debugging / grpcurl) | `localhost:59202` → container `50052` |
| Backend container → worker | `http://search-worker-dev:50052` on `many_faces_main_dev-network` |
| Worker → Elasticsearch (inside `many_faces_elastic` compose) | `http://elasticsearch:9200` (Docker **service** DNS name) |

## Requirements

- Docker with Compose v2.
- Roughly **1 GiB+** free RAM for a comfortable single-node dev Elasticsearch (`512m` JVM heap by default).
- **Go 1.23+** on the host only if you build outside Docker; CI and the provided Dockerfile compile the worker for you.

## Quick start (standalone)

```bash
cp .env.example .env   # optional overrides
./scripts/start-elasticsearch.sh
```

- Elasticsearch HTTP: `http://localhost:59200`
- Worker gRPC: `http://localhost:59202` (plaintext h2c — dev only)

`docker compose up` starts **both** `elasticsearch` and `search-worker`. The worker refuses to start without `SEARCH_WORKER_ELASTICSEARCH_URLS` (set in `docker-compose.yml` by default).

## Regenerating Go stubs from `proto/`

If you change `proto/manyfaces/search/v1/search.proto`, regenerate Go into `gen/` (example using Docker when `protoc` is not installed on the host):

```bash
docker run --rm -v "$(pwd)":/w -w /w golang:1.23-bookworm bash -c '
  apt-get update -qq && apt-get install -y -qq protobuf-compiler >/dev/null
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
  export PATH="$PATH:$(go env GOPATH)/bin"
  mkdir -p gen
  protoc -I proto \
    --go_out=gen --go_opt=paths=source_relative \
    --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
    proto/manyfaces/search/v1/search.proto
'
```

Generated files appear under `gen/manyfaces/search/v1/` and must stay aligned with the `go_package` option in the `.proto` file (`github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1`).

## Authenticating callers (dev → prod path)

- **Dev:** optional shared secret: set `SEARCH_WORKER_EXPECTED_TOKEN` for the worker and the same value in **`Search__WorkerAuthToken`** on the API. The worker enforces metadata header `x-search-worker-token` for application RPCs; **gRPC health** checks are exempt.
- **Prod (recommended direction):** TLS for gRPC plus **mTLS** or stronger service identity (see monorepo security docs). Network allowlisting alone is insufficient.

## Monorepo integration

- Submodule path: `many_faces_elastic/` under `many_faces_main`.
- Full dev stack: `ENABLE_ELASTICSEARCH=1 ./scripts/start-all-dev.sh` attaches **`elasticsearch-dev`** and **`search-worker-dev`** to **`many_faces_main_dev-network`**.
- Backend configuration: see **`docs/guides/elasticsearch-local-dev.md`** in the monorepo (`Search__Enabled`, `Search__WorkerGrpcUrl`, optional `Search__WorkerAuthToken`).

## Stop

```bash
./scripts/stop-elasticsearch.sh
```

This runs `docker compose down` for this project (Elasticsearch + worker).

## Out of scope (later phases)

- Production clustering, TLS everywhere, Elastic Cloud auth wiring beyond placeholders.
- Outbox/indexer workers, portal/admin search UI (tracked in `docs/prompts/elasticsearch-search-infra-agent-prompt.md`).

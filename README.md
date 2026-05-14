# many_faces_elastic

**Canonical GitHub repository:** [github.com/01laky/many_faces_elastic](https://github.com/01laky/many_faces_elastic) — default branch **`main`**.  
Standalone clone: `git clone git@github.com:01laky/many_faces_elastic.git` (HTTPS: `https://github.com/01laky/many_faces_elastic.git`). In the **many_faces_main** monorepo this tree is typically checked out as the `many_faces_elastic/` git submodule ([monorepo submodule guide](https://github.com/01laky/many_faces_main/blob/main/docs/guides/git-submodules.md)).

Optional **Elasticsearch** stack plus a colocated **Go gRPC search-worker** for the Many Faces monorepo. Together they provide a **read-optimized search projection** (full-text, facets, autocomplete later). **PostgreSQL remains the system of record**; this repository ships Docker tooling and the worker source. The **canonical `.proto`** contract lives in **`many_faces_proto`** and is consumed by **`many_faces_backend`** (C# gRPC client) and eventually **`many_faces_ai`** (Python client).

**Operator notes (TLS, smoke, CI pointers):** [`docs/search-stack.md`](./docs/search-stack.md).

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

## TLS / mTLS smoke (CI + manual)

From the monorepo root (requires **OpenSSL**, **Docker**, **grpcurl**, **.NET 10 SDK**):

```bash
chmod +x many_faces_elastic/scripts/smoke-grpc-tls.sh
many_faces_elastic/scripts/smoke-grpc-tls.sh
```

This uses **`docker-compose.tls-smoke.yml`** (host ports **59210** / **59211**), then **`dotnet test`** against **`SearchWorkerTlsEndToEndSmokeTests`**. Set **`RUN_DOTNET_TLS_SMOKE=0`** to run **grpcurl** only. The smoke script sets **world-readable permissions on the ephemeral PEM directory** so the **distroless nonroot** worker process can read the bind-mounted certs. See **[`docs/guides/elasticsearch-grpc-tls-mtls.md`](../docs/guides/elasticsearch-grpc-tls-mtls.md)**.

## Regenerating Go stubs (from `many_faces_proto`)

If you change **`many_faces_proto/proto/manyfaces/search/v1/search.proto`**, regenerate Go into **`gen/`** from this repo root inside **`many_faces_main`**:

```bash
docker run --rm \
  -v "$(pwd)":/w \
  -v "$(pwd)/../many_faces_proto":/mfproto:ro \
  -w /w golang:1.23-bookworm bash -c '
  apt-get update -qq && apt-get install -y -qq protobuf-compiler >/dev/null
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.5
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
  export PATH="$PATH:$(go env GOPATH)/bin"
  mkdir -p gen
  protoc -I /mfproto/proto \
    --go_out=gen --go_opt=paths=source_relative \
    --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
    manyfaces/search/v1/search.proto
'
```

Generated files appear under `gen/manyfaces/search/v1/` and must stay aligned with the `go_package` option in the `.proto` file (`github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1`).

**Standalone clone:** mount or clone **`many_faces_proto`** beside this repository so `/mfproto/proto` resolves (same pattern as **`many_faces_push`**).

## Authenticating callers (dev → prod path)

- **Dev:** optional shared secret: set `SEARCH_WORKER_EXPECTED_TOKEN` for the worker and the same value in **`Search__WorkerAuthToken`** on the API. The worker enforces metadata header `x-search-worker-token` for application RPCs; **gRPC health** checks are exempt.
- **TLS / mTLS:** set **`SEARCH_WORKER_GRPC_TLS_CERT_FILE`** and **`SEARCH_WORKER_GRPC_TLS_KEY_FILE`** (PEM) to enable TLS on the gRPC listener; set **`SEARCH_WORKER_GRPC_MTLS_CLIENT_CA_FILE`** to require client certificates. On **`many_faces_backend`**, use **`Search__WorkerGrpcUrl=https://…`** and optional **`Search__WorkerTlsServerCaPath`**, **`Search__WorkerTlsClientCertPath`**, **`Search__WorkerTlsClientKeyPath`**, **`Search__WorkerGrpcTlsServerName`** (see monorepo **[`docs/guides/elasticsearch-grpc-tls-mtls.md`](../docs/guides/elasticsearch-grpc-tls-mtls.md)**).
- **Prod (recommended direction):** TLS for gRPC plus **mTLS** and/or a strong service identity (token alone is insufficient on hostile networks). Network allowlisting alone is insufficient.

## Monorepo integration

- Submodule path: `many_faces_elastic/` under `many_faces_main`.
- **Parent-repo documentation (features, TLS, CI):** [`docs/guides/elasticsearch-search-features-overview.md`](../docs/guides/elasticsearch-search-features-overview.md), [`docs/guides/elasticsearch-grpc-tls-mtls.md`](../docs/guides/elasticsearch-grpc-tls-mtls.md), [`docs/guides/elasticsearch-local-dev.md`](../docs/guides/elasticsearch-local-dev.md).
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

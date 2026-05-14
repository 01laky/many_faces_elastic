# Search stack — operator notes (`many_faces_elastic`)

This repository ships **Elasticsearch** and the **Go gRPC search-worker** used as an optional **read-optimized search projection** in the Many Faces monorepo. **PostgreSQL remains the system of record**; product UIs never call Elasticsearch or this worker directly.

For the **full feature matrix** (TLS/mTLS, CI smoke, .NET tests, health endpoints), see the monorepo guide (canonical, updated with parent CI and scripts):

**[many_faces_main — `docs/guides/elasticsearch-search-features-overview.md`](https://github.com/01laky/many_faces_main/blob/main/docs/guides/elasticsearch-search-features-overview.md)**

TLS/mTLS details (env vars, `openssl`, Docker mounts):

**[`elasticsearch-grpc-tls-mtls.md`](https://github.com/01laky/many_faces_main/blob/main/docs/guides/elasticsearch-grpc-tls-mtls.md)**

Local dev (ports, `ENABLE_ELASTICSEARCH`, Docker DNS, grpcurl):

**[`elasticsearch-local-dev.md`](https://github.com/01laky/many_faces_main/blob/main/docs/guides/elasticsearch-local-dev.md)**

## Quick reference (this repo)

| Artifact | Role |
| -------- | ---- |
| `docker-compose.yml` | Default dev: Elasticsearch + `search-worker` (plaintext gRPC). |
| `docker-compose.tls-smoke.yml` | Isolated stack for TLS/mTLS verification (host ports **59210** / **59211**). |
| `scripts/smoke-grpc-tls.sh` | Generates demo PEMs, runs TLS compose, **grpcurl** `Ping`, optional **dotnet** e2e test. Sets **755** on the temp cert dir and **world-readable** `.crt`/`.key` so the **nonroot** distroless worker can read bind mounts (CI only). |
| `internal/grpccreds` | Server TLS credentials loader used by `cmd/search-worker`. |
| `proto/README.md` | Pointer to nested **`many_faces_proto`** submodule (search contract). |

## Worker environment (TLS)

| Variable | Purpose |
| -------- | ------- |
| `SEARCH_WORKER_GRPC_TLS_CERT_FILE` | Server certificate PEM path. |
| `SEARCH_WORKER_GRPC_TLS_KEY_FILE` | Server private key PEM path. |
| `SEARCH_WORKER_GRPC_MTLS_CLIENT_CA_FILE` | Optional: PEM CA bundle to verify **client** certificates (mTLS). |

When cert and key paths are both empty, the worker listens in **plaintext** (development only).

## Tests

```bash
go vet ./...
go test ./... -count=1
```

## Submodule / monorepo workflow

When you change this tree, **commit and push this repository first**, then in **`many_faces_main`** update the `many_faces_elastic` gitlink and push the monorepo so CI and teammates resolve the same commit.

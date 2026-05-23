# Search stack — operator quick reference (`many_faces_elastic`)

**Monorepo guides (canonical):**

| Guide | Contents |
| ----- | -------- |
| [`elasticsearch-local-dev.md`](../../docs/guides/elasticsearch-local-dev.md) | Compose, ports, proto sync, backend `Search:*` env |
| [`elasticsearch-search-features-overview.md`](../../docs/guides/elasticsearch-search-features-overview.md) | Capability summary, CI, smoke |
| [`elasticsearch-grpc-tls-mtls.md`](../../docs/guides/elasticsearch-grpc-tls-mtls.md) | TLS/mTLS for backend ↔ worker |
| [`admin-settings-infrastructure-smoke-tests.md`](../../docs/guides/admin-settings-infrastructure-smoke-tests.md) | Admin UI search health panel |

## Ports (host)

| Service | Host port | Container |
| ------- | --------- | --------- |
| Elasticsearch HTTP | **59200** | `elasticsearch-dev:9200` |
| Search worker gRPC | **59202** | `search-worker-dev:50052` |

## Diagram: data path

```mermaid
flowchart LR
  BE[many_faces_backend]
  SW[search-worker Go gRPC]
  ES[(Elasticsearch)]
  BE -->|Search enabled| SW
  SW --> ES
```

## Submodule README

Run scripts, proto regen, Docker details: [`../README.md`](../README.md).

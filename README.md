# many_faces_elastic

Optional **Elasticsearch** stack for the Many Faces monorepo: a **read-optimized search index** (full-text, facets, autocomplete later). **PostgreSQL remains the system of record**; this repo only ships Docker tooling and dev defaults.

## Image and licensing

This submodule uses the **official Elastic** image `docker.elastic.co/elasticsearch/elasticsearch`. Elastic Stack components are subject to the **Elastic License v2** (not Apache 2.0). For strict OSS-only deployments, evaluate **OpenSearch** instead and align client libraries and docs across the monorepo.

## What is in scope (v1 groundwork)

- Single-node **development** Elasticsearch (HTTP, security disabled for local use only).
- Pinned image tag (no `:latest` in committed defaults).
- Host port **59200** → container `9200` (avoids colliding with Postgres `54320` and typical Redis host mappings).

## Out of scope (for later phases)

- Production clustering, TLS, Elastic Cloud auth wiring (placeholders exist in app settings only).
- Index mappings, outbox/indexer workers, and portal/admin search UI (see `docs/prompts/elasticsearch-search-infra-agent-prompt.md` in `many_faces_main`).

## Requirements

- Docker with Compose v2.
- Roughly **1 GiB+** free RAM for a comfortable dev node (`512m` JVM heap by default).

## Quick start (standalone)

```bash
cp .env.example .env   # optional overrides
./scripts/start-elasticsearch.sh
```

HTTP API: `http://localhost:59200` (from host). Inside the monorepo dev Docker network the hostname is **`elasticsearch-dev`** on port **9200** once attached (see `many_faces_main/scripts/start-all-dev.sh` when `ENABLE_ELASTICSEARCH=1`).

## Monorepo integration

- Submodule path: `many_faces_elastic/` under `many_faces_main`.
- Optional full stack: set **`ENABLE_ELASTICSEARCH=1`** before `./scripts/start-all-dev.sh` in the parent repo so Elasticsearch starts and joins `many_faces_main_dev-network`.
- Backend: set `Search__ElasticsearchUri` (e.g. `http://elasticsearch-dev:9200/` in Docker) when the API runs on the same network. If unset, search features stay off (see `many_faces_backend` `Search` configuration).

## Stop

```bash
./scripts/stop-elasticsearch.sh
```

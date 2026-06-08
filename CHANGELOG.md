# Changelog

All notable changes to **`many_faces_elastic`** are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) — **version headings only, no dates**. SemVer: [`VERSION`](./VERSION).

### Release index

| Version       | Theme                          |
| ------------- | ------------------------------ |
| [0.6.0](#060) | Operator-AI RAG knowledge RPCs |
| [0.5.2](#052) | Patch release index sync       |
| [0.5.1](#051) | Patch                          |
| [0.5.0](#050) | Admin search autocomplete RPCs |
| [0.4.0](#040) | verify-edge-contracts          |
| [0.3.0](#030) | many_faces_proto submodule     |
| [0.2.0](#020) | gRPC TLS/mTLS search-worker    |
| [0.1.0](#010) | Elasticsearch dev stack        |

## [Unreleased]

### Added

### Changed

### Fixed

---

## [0.6.0]

### Added

- Operator-AI RAG knowledge surface on the Go search-worker (operator-ai-rag-retrieval-refactor v1):
  - New gRPC handlers `IndexKnowledge`, `DeleteKnowledge`, `SemanticSearch`, `KnowledgeIndexStatus` on
    `SearchService`, implemented against the updated `manyfaces.search.v1` proto. The user-facing search
    RPCs (`Autocomplete`, `IndexDocument`, …) are unchanged.
  - Versioned `operator-ai-knowledge-v{n}` index + `operator-ai-knowledge` alias lifecycle: build a new
    versioned index (`dense_vector` cosine kNN, BM25 text fields, keyword filters, integer `bundle_index`),
    repoint the alias atomically, drop the old index (zero-downtime re-embed, §17.3).
  - `SemanticSearch` hybrid retrieval: kNN on `vector` + BM25 `multi_match` on text fields, both filtered by
    `source_types`, fused with Reciprocal Rank Fusion (`score = Σ 1/(rrf_k + rank)`, default `rrf_k=60`);
    deterministic tie-break (score, then `bundle_index`, then `knowledge_id`); `degraded` flag when only one
    retriever is available.
  - `vector_dim` drift guard: `IndexKnowledge` rejects any document whose embedded vector length differs from
    the configured embedding dimension and reports it as a `BulkIndexItemError`.
  - `KnowledgeIndexStatus` readiness/health: alias, active index, doc count vs expected, embed model version,
    vector dim, `ready` (alias exists AND doc_count == expected AND model matches), `degraded`,
    `last_indexed_unix_ms` — backs the backend cold-start planner-fallback gate and the admin status panel.
- Worker config keys `OPERATOR_AI_EMBED_DIM` (default 768), `OPERATOR_AI_EMBED_MODEL`
  (default `nomic-embed-text`), `OPERATOR_AI_EXPECTED_DOC_COUNT` (default 61) — single source of truth for
  the dense_vector mapping, the drift guard, and the readiness check.
- `scripts/regen-go-stubs.sh` and a `Makefile` (`gen`/`build`/`test`/`lint`) to regenerate the Go stubs.

### Changed

### Fixed

---

## [0.5.2]

### Added

- Add README shield badges (version, CI, stack tech) via sync-readme-badges.py.

### Added

- Add README shield badges (version, CI, stack tech) via sync-readme-badges.py.

### Changed

### Fixed

---

## [0.5.1]

### Changed

- Document project author (Ladislav Kostolny, 01laky@gmail.com) in README and standard manifests.

### Added

### Changed

- Document project author (Ladislav Kostolny, 01laky@gmail.com) in README and standard manifests.

### Fixed

---

## [0.5.0]

### Added

- Admin search index and autocomplete RPCs; bulk upsert and navigation fixes.

### Fixed

- Prefix-match admin autocomplete while typing.

## [0.4.0]

### Added

- verify-edge-contracts for config and auth interceptor; lint.sh.

## [0.3.0]

### Added

- Nested many_faces_proto submodule; search-stack documentation.

## [0.2.0]

### Added

- Go gRPC search-worker; TLS/mTLS smoke compose and credential tests.

### Fixed

- TLS smoke PEM permissions for distroless nonroot.

## [0.1.0]

### Added

- Single-node Elasticsearch Docker compose for local dev.

[Unreleased]: https://github.com/01laky/many_faces_elastic/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/01laky/many_faces_elastic/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/01laky/many_faces_elastic/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/01laky/many_faces_elastic/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/01laky/many_faces_elastic/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/01laky/many_faces_elastic/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/01laky/many_faces_elastic/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/01laky/many_faces_elastic/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/01laky/many_faces_elastic/releases/tag/v0.1.0

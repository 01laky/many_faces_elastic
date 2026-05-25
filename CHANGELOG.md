# Changelog

All notable changes to **`many_faces_elastic`** are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) — **version headings only, no dates**. SemVer: [`VERSION`](./VERSION).

### Release index

| Version       | Theme                          |
| ------------- | ------------------------------ |
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

[Unreleased]: https://github.com/01laky/many_faces_elastic/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/01laky/many_faces_elastic/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/01laky/many_faces_elastic/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/01laky/many_faces_elastic/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/01laky/many_faces_elastic/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/01laky/many_faces_elastic/releases/tag/v0.1.0

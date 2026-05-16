# Search stack — operator index

Short pointer for **`many_faces_elastic`**. Deep guides live in the monorepo **`many_faces_main`** repo.

| Topic                                                          | Document                                                                                                                                                             |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Local dev (ports, compose, skip with `ENABLE_ELASTICSEARCH=0`) | [`docs/guides/elasticsearch-local-dev.md`](https://github.com/01laky/many_faces_main/blob/main/docs/guides/elasticsearch-local-dev.md)                               |
| Features, TLS smoke, CI                                        | [`docs/guides/elasticsearch-search-features-overview.md`](https://github.com/01laky/many_faces_main/blob/main/docs/guides/elasticsearch-search-features-overview.md) |
| Backend ↔ worker gRPC TLS/mTLS                                 | [`docs/guides/elasticsearch-grpc-tls-mtls.md`](https://github.com/01laky/many_faces_main/blob/main/docs/guides/elasticsearch-grpc-tls-mtls.md)                       |
| Submodule README                                               | [`../README.md`](../README.md)                                                                                                                                       |

**Trust boundary:** clients (`many_faces_portal`, `many_faces_admin`, `many_faces_mobile`) call **`many_faces_backend` REST only** — never Elasticsearch or the worker gRPC port directly.

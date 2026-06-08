package search

// AdminIndexName is bumped when mappings change (v1 used plain text; v2 uses search_as_you_type).
const AdminIndexName = "manyfaces-admin-v2"

const DefaultListPageSize = 500

const MaxAutocompletePageSize = 100

// ───────────────────────────────────────────────────────────────────────────
// Operator-AI knowledge index (operator-ai-rag-retrieval-refactor v1, spec §17.3).
// Reads/queries always target the ALIAS; writes target a concrete versioned index
// `operator-ai-knowledge-v{n}` so re-embeds are zero-downtime (build new → repoint
// alias atomically → drop old).
// ───────────────────────────────────────────────────────────────────────────

// KnowledgeAlias is the stable name every read/query (SemanticSearch, status) targets.
const KnowledgeAlias = "operator-ai-knowledge"

// KnowledgeIndexPrefix is the versioned concrete-index prefix; the full name is
// `operator-ai-knowledge-v{n}` where n is derived from embed model + dim + mapping version.
const KnowledgeIndexPrefix = "operator-ai-knowledge-v"

// KnowledgeMappingVersion bumps whenever the knowledge mapping structure changes (independent of dims/model).
// It feeds the versioned index name so a mapping change forces a fresh index + alias swap.
const KnowledgeMappingVersion = 1

// DefaultRRFK is the reciprocal-rank-fusion constant used when SemanticSearchRequest.rrf_k == 0 (spec §5.4).
const DefaultRRFK = 60

// KnowledgeRetrieverCandidates is how many candidates each retriever (kNN, BM25) fetches before fusion.
// Fetching more than top_k per list gives RRF enough overlap to rank well; the worker still returns global top_k.
const KnowledgeRetrieverCandidates = 50

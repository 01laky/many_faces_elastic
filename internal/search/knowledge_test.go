package search_test

// knowledge_test.go covers the operator-AI RAG knowledge store (spec §14 edge cases):
//   RT-1  RRF ordering + deterministic tie-break (score, then bundle_index, then id)
//   RT-2  vector_dim drift reject
//   RT-16 alias swap atomicity (build new versioned index → atomic alias repoint → drop old)
//   RT-22 status readiness (alias + doc_count == expected + model match)
//   source_type filter is applied to both retrievers
//   degraded flag set when only one retriever is available
//
// Per repo convention every test drives a real *elasticsearch.Client pointed at an httptest server that
// emulates the relevant Elasticsearch endpoints.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/01laky/many_faces_elastic/internal/search"
	"github.com/elastic/go-elasticsearch/v8"
)

func newKnowledgeStore(t *testing.T, handler http.HandlerFunc, dim int, model string, expected int) *search.KnowledgeStore {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	es, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return search.NewKnowledgeStore(es, dim, model, expected)
}

func writeKES(w http.ResponseWriter, status int, body string) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// vec builds a vector of the given length filled with v (helper for dim tests).
func vec(n int, v float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// ── RT-2: vector_dim drift reject ────────────────────────────────────────────

func TestKnowledge_BulkUpsert_RejectsDimDrift_RT2(t *testing.T) {
	var bulkBody string
	s := newKnowledgeStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead: // index exists check
			writeKES(w, http.StatusOK, ``)
		case strings.Contains(r.URL.Path, "_aliases"):
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
		case strings.Contains(r.URL.Path, "_cat/indices"):
			writeKES(w, http.StatusOK, `[]`)
		case strings.Contains(r.URL.Path, "_bulk"):
			raw, _ := io.ReadAll(r.Body)
			bulkBody = string(raw)
			// Only one doc (the in-dim one) should reach ES.
			writeKES(w, http.StatusOK, `{"errors":false,"items":[{"index":{"status":201}}]}`)
		default:
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
		}
	}, 4, "nomic-embed-text", 61)

	docs := []search.KnowledgeDoc{
		{KnowledgeID: "good", Vector: vec(4, 0.1)},
		{KnowledgeID: "drift", Vector: vec(8, 0.1)}, // wrong dimension → rejected pre-ES
	}
	res, err := s.BulkUpsertKnowledge(context.Background(), docs)
	if err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}
	if res.IndexedCount != 1 {
		t.Fatalf("expected 1 indexed, got %d", res.IndexedCount)
	}
	if res.FailedCount != 1 {
		t.Fatalf("expected 1 failed, got %d", res.FailedCount)
	}
	if len(res.Errors) != 1 || res.Errors[0].EntityID != "drift" {
		t.Fatalf("expected drift error for 'drift', got %+v", res.Errors)
	}
	if !strings.Contains(res.Errors[0].ErrorMessage, "vector_dim drift") {
		t.Fatalf("expected drift message, got %q", res.Errors[0].ErrorMessage)
	}
	if strings.Contains(bulkBody, `"drift"`) {
		t.Fatalf("drift doc must not be sent to ES, body=%s", bulkBody)
	}
	if !strings.Contains(bulkBody, `"good"`) {
		t.Fatalf("good doc should be sent to ES, body=%s", bulkBody)
	}
}

// ── RT-16: alias swap atomicity ──────────────────────────────────────────────

func TestKnowledge_AliasSwap_Atomic_RT16(t *testing.T) {
	var aliasBody string
	var deletedStale bool
	concrete := ""
	s := newKnowledgeStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			writeKES(w, http.StatusNotFound, ``) // index missing → must be created
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "operator-ai-knowledge-v"):
			concrete = strings.TrimPrefix(r.URL.Path, "/")
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
		case strings.Contains(r.URL.Path, "_aliases"):
			raw, _ := io.ReadAll(r.Body)
			aliasBody = string(raw)
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
		case strings.Contains(r.URL.Path, "_cat/indices"):
			// Report an old version index that must be dropped after the swap.
			writeKES(w, http.StatusOK, `[{"index":"operator-ai-knowledge-v1-d4-deadbeef-OLD"}]`)
		case r.Method == http.MethodDelete:
			deletedStale = true
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
		case strings.Contains(r.URL.Path, "_bulk"):
			writeKES(w, http.StatusOK, `{"errors":false,"items":[{"index":{"status":201}}]}`)
		default:
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
		}
	}, 4, "nomic-embed-text", 61)

	if _, err := s.BulkUpsertKnowledge(context.Background(), []search.KnowledgeDoc{{KnowledgeID: "a", Vector: vec(4, 0.2)}}); err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}

	if concrete == "" {
		t.Fatal("expected versioned index to be created")
	}
	// The alias update must be a single _aliases call containing BOTH a remove and an add (atomic repoint).
	var parsed struct {
		Actions []map[string]json.RawMessage `json:"actions"`
	}
	if err := json.Unmarshal([]byte(aliasBody), &parsed); err != nil {
		t.Fatalf("parse alias body %q: %v", aliasBody, err)
	}
	hasRemove, hasAdd := false, false
	for _, a := range parsed.Actions {
		if _, ok := a["remove"]; ok {
			hasRemove = true
		}
		if _, ok := a["add"]; ok {
			hasAdd = true
		}
	}
	if !hasRemove || !hasAdd {
		t.Fatalf("alias repoint must remove+add atomically, got %s", aliasBody)
	}
	if !deletedStale {
		t.Fatal("expected stale old versioned index to be deleted after swap")
	}
}

// ── RT-1: RRF ordering + tie-break + degraded flag + source filter ───────────

// rrfHandler emulates ES for SemanticSearch: it inspects the body to decide whether the request is the kNN
// retriever or the BM25 retriever and returns the configured hit list for each. It also records the
// source_type filter so the test can assert it was forwarded to both retrievers.
func rrfHandler(t *testing.T, knnHits, bm25Hits string, knnErr, bm25Err bool, capturedFilters *[]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			writeKES(w, http.StatusOK, ``)
			return
		}
		if !strings.Contains(r.URL.Path, "_search") {
			writeKES(w, http.StatusOK, `{"acknowledged":true}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		// Capture any source_type terms filter for the assertion.
		if strings.Contains(body, `"source_type"`) {
			*capturedFilters = append(*capturedFilters, body)
		}
		if strings.Contains(body, `"knn"`) {
			if knnErr {
				writeKES(w, http.StatusInternalServerError, `{"error":"knn down"}`)
				return
			}
			writeKES(w, http.StatusOK, knnHits)
			return
		}
		// BM25 path
		if bm25Err {
			writeKES(w, http.StatusInternalServerError, `{"error":"bm25 down"}`)
			return
		}
		writeKES(w, http.StatusOK, bm25Hits)
	}
}

func hitsJSON(ids ...string) string {
	var sb strings.Builder
	sb.WriteString(`{"took":3,"hits":{"hits":[`)
	for i, id := range ids {
		if i > 0 {
			sb.WriteByte(',')
		}
		// bundle_index encoded from a trailing number-free id by position; tests set it explicitly via map below.
		sb.WriteString(`{"_source":{"knowledge_id":"` + id + `","source_type":"stat_bundle","bundle_index":` + bundleIndexFor(id) + `,"title":"` + id + `"}}`)
	}
	sb.WriteString(`]}}`)
	return sb.String()
}

// bundleIndexFor maps test ids to stable bundle indices so tie-break ordering is exercised.
func bundleIndexFor(id string) string {
	switch id {
	case "A":
		return "10"
	case "B":
		return "20"
	case "C":
		return "5"
	default:
		return "-1"
	}
}

func TestKnowledge_SemanticSearch_RRFOrder_RT1(t *testing.T) {
	var filters []string
	// kNN ranks: A(1), B(2). BM25 ranks: B(1), A(2). With rrf_k=60:
	//   A = 1/61 + 1/62 = 0.016393 + 0.016129 = 0.032522
	//   B = 1/62 + 1/61 = same 0.032522  → exact tie → tie-break by bundle_index: A(10) < B(20) ⇒ A first.
	s := newKnowledgeStore(t, rrfHandler(t, hitsJSON("A", "B"), hitsJSON("B", "A"), false, false, &filters), 4, "nomic-embed-text", 61)

	res, err := s.SemanticSearch(context.Background(), vec(4, 0.5), "how many albums", 10, []string{"stat_bundle"}, 0)
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if res.Degraded {
		t.Fatal("expected non-degraded with both retrievers")
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(res.Hits))
	}
	if res.Hits[0].KnowledgeID != "A" || res.Hits[1].KnowledgeID != "B" {
		t.Fatalf("expected tie broken to A then B (lower bundle_index first), got %s,%s", res.Hits[0].KnowledgeID, res.Hits[1].KnowledgeID)
	}
	// Both retrievers must have received the source_type filter.
	if len(filters) < 2 {
		t.Fatalf("expected source_type filter on both retrievers, captured %d", len(filters))
	}
	for _, f := range filters {
		if !strings.Contains(f, "stat_bundle") {
			t.Fatalf("filter missing stat_bundle: %s", f)
		}
	}
}

func TestKnowledge_SemanticSearch_TopKCap_RT1(t *testing.T) {
	var filters []string
	s := newKnowledgeStore(t, rrfHandler(t, hitsJSON("A", "B", "C"), hitsJSON("C", "B", "A"), false, false, &filters), 4, "nomic-embed-text", 61)
	res, err := s.SemanticSearch(context.Background(), vec(4, 0.5), "q", 2, []string{"stat_bundle"}, 0)
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected top_k=2 hits, got %d", len(res.Hits))
	}
}

func TestKnowledge_SemanticSearch_DegradedWhenKnnDown_RT4(t *testing.T) {
	var filters []string
	// kNN errors, BM25 returns hits → degraded, but still answers from BM25.
	s := newKnowledgeStore(t, rrfHandler(t, "", hitsJSON("A", "B"), true, false, &filters), 4, "nomic-embed-text", 61)
	res, err := s.SemanticSearch(context.Background(), vec(4, 0.5), "q", 10, []string{"stat_bundle"}, 0)
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if !res.Degraded {
		t.Fatal("expected degraded=true when only BM25 available")
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected BM25 hits, got %d", len(res.Hits))
	}
}

func TestKnowledge_SemanticSearch_DegradedWhenTextOnly(t *testing.T) {
	var filters []string
	// Caller passes no vector → only BM25 runs → degraded.
	s := newKnowledgeStore(t, rrfHandler(t, "", hitsJSON("A"), false, false, &filters), 4, "nomic-embed-text", 61)
	res, err := s.SemanticSearch(context.Background(), nil, "q", 10, []string{"stat_bundle"}, 0)
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if !res.Degraded {
		t.Fatal("expected degraded when only text retriever requested")
	}
}

// ── RT-22: status readiness ──────────────────────────────────────────────────

func statusHandler(t *testing.T, aliasResp string, aliasStatus, docCount int, model string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "_alias"):
			writeKES(w, aliasStatus, aliasResp)
		case strings.Contains(r.URL.Path, "_search"):
			body := `{"hits":{"total":{"value":` + itoa(docCount) + `}},"aggregations":{"last_indexed":{"value":1700000000000},"models":{"buckets":[{"key":"` + model + `"}]}}}`
			writeKES(w, http.StatusOK, body)
		default:
			writeKES(w, http.StatusOK, `{}`)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestKnowledge_Status_Ready_RT22(t *testing.T) {
	alias := `{"operator-ai-knowledge-v1-d768-abcd1234":{"aliases":{"operator-ai-knowledge":{}}}}`
	s := newKnowledgeStore(t, statusHandler(t, alias, http.StatusOK, 61, "nomic-embed-text"), 768, "nomic-embed-text", 61)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Ready {
		t.Fatalf("expected ready, got %+v", st)
	}
	if st.Degraded {
		t.Fatal("ready index must not be degraded")
	}
	if st.DocCount != 61 || st.ExpectedDocCount != 61 {
		t.Fatalf("doc counts: got=%d expected=%d", st.DocCount, st.ExpectedDocCount)
	}
	if st.VectorDim != 768 || st.EmbedModelVersion != "nomic-embed-text" {
		t.Fatalf("dim/model: %d %q", st.VectorDim, st.EmbedModelVersion)
	}
	if st.LastIndexedUnixMs != 1700000000000 {
		t.Fatalf("last indexed: %d", st.LastIndexedUnixMs)
	}
}

func TestKnowledge_Status_NotReadyWrongCount(t *testing.T) {
	alias := `{"operator-ai-knowledge-v1-d768-abcd1234":{"aliases":{"operator-ai-knowledge":{}}}}`
	s := newKnowledgeStore(t, statusHandler(t, alias, http.StatusOK, 60, "nomic-embed-text"), 768, "nomic-embed-text", 61)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Ready {
		t.Fatal("expected not ready when doc_count != expected")
	}
	if !st.Degraded {
		t.Fatal("not-ready should be degraded")
	}
}

func TestKnowledge_Status_NotReadyModelMismatch(t *testing.T) {
	alias := `{"operator-ai-knowledge-v1-d768-abcd1234":{"aliases":{"operator-ai-knowledge":{}}}}`
	s := newKnowledgeStore(t, statusHandler(t, alias, http.StatusOK, 61, "other-model"), 768, "nomic-embed-text", 61)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Ready {
		t.Fatal("expected not ready on model mismatch")
	}
	if !strings.Contains(st.ErrorMessage, "mismatch") {
		t.Fatalf("expected mismatch message, got %q", st.ErrorMessage)
	}
}

// RT-15: cold start — alias missing → not ready, degraded, with explanatory message (planner fallback gate).
func TestKnowledge_Status_ColdStart_RT15(t *testing.T) {
	s := newKnowledgeStore(t, statusHandler(t, `{}`, http.StatusNotFound, 0, ""), 768, "nomic-embed-text", 61)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Ready {
		t.Fatal("cold start must be not-ready")
	}
	if !st.Degraded || st.ActiveIndex != "" {
		t.Fatalf("expected degraded with empty active index, got %+v", st)
	}
	if !strings.Contains(st.ErrorMessage, "does not exist") {
		t.Fatalf("expected cold-start message, got %q", st.ErrorMessage)
	}
}

func TestKnowledge_IndexName_DerivedFromModelAndDim(t *testing.T) {
	a := search.NewKnowledgeStore(nil, 768, "nomic-embed-text", 61)
	b := search.NewKnowledgeStore(nil, 1024, "nomic-embed-text", 61)
	c := search.NewKnowledgeStore(nil, 768, "other-model", 61)
	if a.IndexName() == b.IndexName() {
		t.Fatal("different dims must yield different index names")
	}
	if a.IndexName() == c.IndexName() {
		t.Fatal("different models must yield different index names")
	}
	if !strings.HasPrefix(a.IndexName(), "operator-ai-knowledge-v") {
		t.Fatalf("unexpected index name %q", a.IndexName())
	}
}

func TestKnowledge_Delete_MissingIsSuccess(t *testing.T) {
	s := newKnowledgeStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writeKES(w, http.StatusNotFound, `{"result":"not_found"}`)
			return
		}
		writeKES(w, http.StatusOK, `{}`)
	}, 4, "nomic-embed-text", 61)
	if err := s.DeleteKnowledge(context.Background(), "missing"); err != nil {
		t.Fatalf("delete missing should succeed, got %v", err)
	}
}

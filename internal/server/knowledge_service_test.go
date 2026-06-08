package server

// knowledge_service_test.go covers the gRPC handler layer for the operator-AI RAG knowledge RPCs
// (spec §14). The handlers delegate to internal/search.KnowledgeStore (covered in depth in that package's
// tests); here we assert the proto<->store adaptation, argument validation, and the drift/degraded/status
// fields are surfaced correctly through the RPC boundary. Each test drives a real *elasticsearch.Client
// pointed at an httptest server, matching the repo convention.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
	"github.com/elastic/go-elasticsearch/v8"
)

func newKnowledgeService(t *testing.T, handler http.HandlerFunc, dim int, model string, expected int) *SearchService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	es, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return NewSearchServiceWithKnowledge(es, slog.Default(), dim, model, expected)
}

func writeKnowledgeES(w http.ResponseWriter, status int, body string) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func vecN(n int, v float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestHandler_IndexKnowledge_DriftCountedFailed_RT2(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			writeKnowledgeES(w, http.StatusOK, ``)
		case strings.Contains(r.URL.Path, "_aliases"):
			writeKnowledgeES(w, http.StatusOK, `{"acknowledged":true}`)
		case strings.Contains(r.URL.Path, "_cat/indices"):
			writeKnowledgeES(w, http.StatusOK, `[]`)
		case strings.Contains(r.URL.Path, "_bulk"):
			writeKnowledgeES(w, http.StatusOK, `{"errors":false,"items":[{"index":{"status":201}}]}`)
		default:
			writeKnowledgeES(w, http.StatusOK, `{"acknowledged":true}`)
		}
	}, 4, "nomic-embed-text", 61)

	resp, err := svc.IndexKnowledge(context.Background(), &searchv1.IndexKnowledgeRequest{
		CorrelationId: "c1",
		Documents: []*searchv1.KnowledgeDocument{
			{KnowledgeId: "ok", Vector: vecN(4, 0.1), VectorDim: 4},
			{KnowledgeId: "bad", Vector: vecN(8, 0.1), VectorDim: 8},
		},
	})
	if err != nil {
		t.Fatalf("index knowledge: %v", err)
	}
	if resp.IndexedCount != 1 || resp.FailedCount != 1 {
		t.Fatalf("expected 1 indexed / 1 failed, got %d / %d", resp.IndexedCount, resp.FailedCount)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].EntityId != "bad" {
		t.Fatalf("expected drift error for 'bad', got %+v", resp.Errors)
	}
}

func TestHandler_IndexKnowledge_EmptyRejected(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeKnowledgeES(w, http.StatusOK, `{}`)
	}, 4, "nomic-embed-text", 61)
	_, err := svc.IndexKnowledge(context.Background(), &searchv1.IndexKnowledgeRequest{})
	if err == nil || !strings.Contains(err.Error(), "documents are required") {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func TestHandler_SemanticSearch_FilterAndDegraded(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			writeKnowledgeES(w, http.StatusOK, ``)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"knn"`) {
			// kNN down → degraded path.
			writeKnowledgeES(w, http.StatusInternalServerError, `{"error":"knn"}`)
			return
		}
		writeKnowledgeES(w, http.StatusOK, `{"took":2,"hits":{"hits":[{"_source":{"knowledge_id":"X","source_type":"stat_bundle","bundle_index":3,"title":"X"}}]}}`)
	}, 4, "nomic-embed-text", 61)

	resp, err := svc.SemanticSearch(context.Background(), &searchv1.SemanticSearchRequest{
		QueryVector: vecN(4, 0.3),
		QueryText:   "albums pending",
		TopK:        4,
		SourceTypes: []string{"stat_bundle"},
	})
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if !resp.Degraded {
		t.Fatal("expected degraded when kNN unavailable")
	}
	if len(resp.Hits) != 1 || resp.Hits[0].KnowledgeId != "X" {
		t.Fatalf("expected hit X, got %+v", resp.Hits)
	}
	if resp.Hits[0].BundleIndex != 3 {
		t.Fatalf("expected bundle_index 3, got %d", resp.Hits[0].BundleIndex)
	}
}

func TestHandler_SemanticSearch_RequiresQuery(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeKnowledgeES(w, http.StatusOK, `{}`)
	}, 4, "nomic-embed-text", 61)
	_, err := svc.SemanticSearch(context.Background(), &searchv1.SemanticSearchRequest{TopK: 4})
	if err == nil || !strings.Contains(err.Error(), "query_vector or query_text is required") {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func TestHandler_KnowledgeIndexStatus_Ready(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "_alias"):
			writeKnowledgeES(w, http.StatusOK, `{"operator-ai-knowledge-v1-d768-abcd1234":{"aliases":{"operator-ai-knowledge":{}}}}`)
		case strings.Contains(r.URL.Path, "_search"):
			writeKnowledgeES(w, http.StatusOK, `{"hits":{"total":{"value":61}},"aggregations":{"last_indexed":{"value":1700000000000},"models":{"buckets":[{"key":"nomic-embed-text"}]}}}`)
		default:
			writeKnowledgeES(w, http.StatusOK, `{}`)
		}
	}, 768, "nomic-embed-text", 61)

	resp, err := svc.KnowledgeIndexStatus(context.Background(), &searchv1.KnowledgeIndexStatusRequest{CorrelationId: "c2"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !resp.Ready {
		t.Fatalf("expected ready, got %+v", resp)
	}
	if resp.DocCount != 61 || resp.ExpectedDocCount != 61 {
		t.Fatalf("counts: %d / %d", resp.DocCount, resp.ExpectedDocCount)
	}
	if resp.Alias != "operator-ai-knowledge" {
		t.Fatalf("alias: %q", resp.Alias)
	}
	if resp.VectorDim != 768 {
		t.Fatalf("dim: %d", resp.VectorDim)
	}
}

func TestHandler_DeleteKnowledge_RequiresId(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeKnowledgeES(w, http.StatusOK, `{}`)
	}, 4, "nomic-embed-text", 61)
	_, err := svc.DeleteKnowledge(context.Background(), &searchv1.DeleteKnowledgeRequest{})
	if err == nil || !strings.Contains(err.Error(), "knowledge_id is required") {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func TestHandler_DeleteKnowledge_Success(t *testing.T) {
	svc := newKnowledgeService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writeKnowledgeES(w, http.StatusOK, `{"result":"deleted"}`)
			return
		}
		writeKnowledgeES(w, http.StatusOK, `{}`)
	}, 4, "nomic-embed-text", 61)
	resp, err := svc.DeleteKnowledge(context.Background(), &searchv1.DeleteKnowledgeRequest{KnowledgeId: "bundle:albums"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %q", resp.ErrorMessage)
	}
}

package search_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/01laky/many_faces_elastic/internal/search"
)

func newTestStore(t *testing.T, handler http.HandlerFunc) *search.Store {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	es, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return search.NewStore(es)
}

func writeES(w http.ResponseWriter, status int, body string) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestStore_Autocomplete_EmptyQuery_GSH1_T_W01(t *testing.T) {
	s := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_, err := s.Autocomplete(context.Background(), "", 100, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func TestStore_Autocomplete_PrefixQuery_GSH1_T_W11(t *testing.T) {
	var searchBody map[string]any
	s := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			writeES(w, http.StatusOK, ``)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "_search"):
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &searchBody); err != nil {
				t.Fatalf("parse search body: %v", err)
			}
			writeES(w, http.StatusOK, `{"took":1,"hits":{"total":{"value":1},"hits":[{"_score":1,"_source":{"document_type":"user","entity_id":"1","title":"user30@demo.com","search_text":"user30@demo.com Patrik Zeleny"}}]}}`)
		default:
			writeES(w, http.StatusOK, `{"acknowledged":true}`)
		}
	})

	result, err := s.Autocomplete(context.Background(), "patr", 100, 0, nil)
	if err != nil {
		t.Fatalf("autocomplete: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(result.Hits))
	}

	bodyJSON, _ := json.Marshal(searchBody)
	body := string(bodyJSON)
	if !strings.Contains(body, "bool_prefix") && !strings.Contains(body, "match_phrase_prefix") {
		t.Fatalf("expected prefix query clauses, got %s", body)
	}
}

func TestStore_BulkUpsert_GSH1_T_W12(t *testing.T) {
	var bulkCalled bool
	s := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			writeES(w, http.StatusOK, ``)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "_bulk"):
			bulkCalled = true
			writeES(w, http.StatusOK, `{"errors":false,"items":[{"index":{"status":201}},{"index":{"status":201}}]}`)
		default:
			writeES(w, http.StatusOK, `{"acknowledged":true}`)
		}
	})

	result, err := s.BulkUpsert(context.Background(), []search.Document{
		{DocumentType: "user", EntityID: "1", Title: "a@demo.com", SearchText: "a"},
		{DocumentType: "user", EntityID: "2", Title: "b@demo.com", SearchText: "b"},
	})
	if err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}
	if !bulkCalled {
		t.Fatal("expected ES _bulk call")
	}
	if result.IndexedCount != 2 {
		t.Fatalf("expected 2 indexed, got %d", result.IndexedCount)
	}
}

func TestStore_UpsertAndDelete_GSH1_T_W04_W05(t *testing.T) {
	var indexed bool
	s := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			writeES(w, http.StatusNotFound, ``)
		case (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.Contains(r.URL.Path, "manyfaces-admin-v2"):
			indexed = true
			writeES(w, http.StatusCreated, `{"result":"created"}`)
		case r.Method == http.MethodDelete:
			writeES(w, http.StatusOK, `{"result":"deleted"}`)
		default:
			writeES(w, http.StatusOK, `{"acknowledged":true}`)
		}
	})

	doc := search.Document{DocumentType: "user", EntityID: "1", Title: "demo", SearchText: "demo"}
	if err := s.Upsert(context.Background(), doc); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !indexed {
		t.Fatal("expected index call")
	}
	if err := s.Delete(context.Background(), "user", "missing"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestAdminIndexName_GSH1_T_W13_IsV2(t *testing.T) {
	if search.AdminIndexName != "manyfaces-admin-v2" {
		t.Fatalf("expected v2 index name, got %s", search.AdminIndexName)
	}
}

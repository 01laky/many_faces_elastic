package search_test

import (
	"context"
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

func TestStore_Autocomplete_EmptyQuery_GSH1_T_W01(t *testing.T) {
	s := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	_, err := s.Autocomplete(context.Background(), "", 100, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}

func writeES(w http.ResponseWriter, status int, body string) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestStore_UpsertAndDelete_GSH1_T_W04_W05(t *testing.T) {
	var indexed bool
	s := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			writeES(w, http.StatusNotFound, ``)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "manyfaces-admin-v1"):
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

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
)

func writeElasticsearchInfo(w http.ResponseWriter, status int, body string) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func newTestSearchService(t *testing.T, handler http.HandlerFunc) *SearchService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{srv.URL},
	})
	if err != nil {
		t.Fatalf("elasticsearch client: %v", err)
	}
	return NewSearchService(es, slog.Default())
}

func TestSearchService_Ping_SuccessWithClusterName(t *testing.T) {
	svc := newTestSearchService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeElasticsearchInfo(w, http.StatusOK, `{"cluster_name":"mf-dev","version":{"number":"8.15.0"}}`)
	})

	resp, err := svc.Ping(context.Background(), &searchv1.PingRequest{CorrelationId: "corr-1"})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !resp.ElasticsearchReachable {
		t.Fatalf("expected reachable, error=%q", resp.ErrorMessage)
	}
	if resp.ClusterName != "mf-dev" {
		t.Fatalf("cluster_name: %q", resp.ClusterName)
	}
}

func TestSearchService_Ping_NonOKStatus(t *testing.T) {
	svc := newTestSearchService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeElasticsearchInfo(w, http.StatusServiceUnavailable, `{"error":"unavailable"}`)
	})

	resp, err := svc.Ping(context.Background(), &searchv1.PingRequest{})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if resp.ElasticsearchReachable {
		t.Fatal("expected unreachable")
	}
	if !strings.Contains(resp.ErrorMessage, "503") {
		t.Fatalf("error message: %q", resp.ErrorMessage)
	}
}

func TestSearchService_Ping_InvalidJSON(t *testing.T) {
	svc := newTestSearchService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeElasticsearchInfo(w, http.StatusOK, `not-json`)
	})

	resp, err := svc.Ping(context.Background(), nil)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if resp.ElasticsearchReachable {
		t.Fatal("expected unreachable for invalid JSON")
	}
	if !strings.Contains(resp.ErrorMessage, "invalid JSON") {
		t.Fatalf("error message: %q", resp.ErrorMessage)
	}
}

func TestSearchService_Ping_ReachableWithoutClusterName(t *testing.T) {
	svc := newTestSearchService(t, func(w http.ResponseWriter, _ *http.Request) {
		writeElasticsearchInfo(w, http.StatusOK, `{"ok":true}`)
	})

	resp, err := svc.Ping(context.Background(), &searchv1.PingRequest{})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !resp.ElasticsearchReachable || resp.ClusterName != "" {
		t.Fatalf("got reachable=%v cluster=%q", resp.ElasticsearchReachable, resp.ClusterName)
	}
}

func TestSearchService_Ping_ClientConnectionFailure(t *testing.T) {
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	svc := NewSearchService(es, slog.Default())

	resp, err := svc.Ping(context.Background(), &searchv1.PingRequest{})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if resp.ElasticsearchReachable {
		t.Fatal("expected unreachable when ES HTTP is down")
	}
	if resp.ErrorMessage == "" {
		t.Fatal("expected error message")
	}
}

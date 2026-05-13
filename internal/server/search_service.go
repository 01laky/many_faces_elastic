package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8"
	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
)

// SearchService implements manyfaces.search.v1.SearchService. It is the only place in this repository
// that may perform HTTP calls to Elasticsearch for application purposes.
type SearchService struct {
	searchv1.UnimplementedSearchServiceServer
	// es is the official Elasticsearch Go client; it is safe for concurrent use.
	es *elasticsearch.Client
	// log is the structured logger for this service (request-scoped fields may be added later).
	log *slog.Logger
}

// NewSearchService wires an Elasticsearch client into the gRPC service implementation.
func NewSearchService(es *elasticsearch.Client, log *slog.Logger) *SearchService {
	return &SearchService{es: es, log: log}
}

// Ping implements the contract used by many_faces_backend for readiness: it verifies Elasticsearch HTTP
// from inside the worker container and returns cluster_name when the root endpoint responds with JSON.
func (s *SearchService) Ping(ctx context.Context, req *searchv1.PingRequest) (*searchv1.PingResponse, error) {
	if req != nil && req.CorrelationId != "" {
		s.log.InfoContext(ctx, "search Ping", "correlation_id", req.CorrelationId)
	}

	res, err := s.es.Info(s.es.Info.WithContext(ctx))
	if err != nil {
		s.log.WarnContext(ctx, "elasticsearch Info request failed", "error", err)
		return &searchv1.PingResponse{
			ElasticsearchReachable: false,
			ErrorMessage:           err.Error(),
		}, nil
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		s.log.WarnContext(ctx, "elasticsearch Info non-OK", "status", res.StatusCode, "body", string(body))
		return &searchv1.PingResponse{
			ElasticsearchReachable: false,
			ErrorMessage:           "elasticsearch returned HTTP " + res.Status(),
		}, nil
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return &searchv1.PingResponse{
			ElasticsearchReachable: false,
			ErrorMessage:           "invalid JSON from elasticsearch: " + err.Error(),
		}, nil
	}

	clusterName := ""
	if v, ok := root["cluster_name"].(string); ok {
		clusterName = v
	}

	return &searchv1.PingResponse{
		ElasticsearchReachable: true,
		ClusterName:            clusterName,
	}, nil
}

// mustEmbedUnimplementedSearchServiceServer is satisfied by embedding UnimplementedSearchServiceServer in SearchService.
var _ searchv1.SearchServiceServer = (*SearchService)(nil)

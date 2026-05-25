package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
	"github.com/01laky/many_faces_elastic/internal/search"
)

// SearchService implements manyfaces.search.v1.SearchService. It is the only place in this repository
// that may perform HTTP calls to Elasticsearch for application purposes.
type SearchService struct {
	searchv1.UnimplementedSearchServiceServer
	es    *elasticsearch.Client
	store *search.Store
	log   *slog.Logger
}

// NewSearchService wires an Elasticsearch client into the gRPC service implementation.
func NewSearchService(es *elasticsearch.Client, log *slog.Logger) *SearchService {
	return &SearchService{es: es, store: search.NewStore(es), log: log}
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

func (s *SearchService) IndexDocument(ctx context.Context, req *searchv1.IndexDocumentRequest) (*searchv1.IndexDocumentResponse, error) {
	if req == nil || req.Document == nil {
		return nil, status.Error(codes.InvalidArgument, "document is required")
	}
	doc := protoToDoc(req.Document)
	if doc.DocumentType == "" || doc.EntityID == "" {
		return nil, status.Error(codes.InvalidArgument, "document_type and entity_id are required")
	}
	if err := s.store.Upsert(ctx, doc); err != nil {
		return &searchv1.IndexDocumentResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &searchv1.IndexDocumentResponse{Success: true}, nil
}

func (s *SearchService) DeleteDocument(ctx context.Context, req *searchv1.DeleteDocumentRequest) (*searchv1.DeleteDocumentResponse, error) {
	if req == nil || req.DocumentType == "" || req.EntityId == "" {
		return nil, status.Error(codes.InvalidArgument, "document_type and entity_id are required")
	}
	if err := s.store.Delete(ctx, req.DocumentType, req.EntityId); err != nil {
		return &searchv1.DeleteDocumentResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &searchv1.DeleteDocumentResponse{Success: true}, nil
}

func (s *SearchService) BulkIndexDocuments(ctx context.Context, req *searchv1.BulkIndexDocumentsRequest) (*searchv1.BulkIndexDocumentsResponse, error) {
	if req == nil || len(req.Documents) == 0 {
		return nil, status.Error(codes.InvalidArgument, "documents are required")
	}
	docs := make([]search.Document, 0, len(req.Documents))
	for _, d := range req.Documents {
		docs = append(docs, protoToDoc(d))
	}
	result, err := s.store.BulkUpsert(ctx, docs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "bulk index: %v", err)
	}
	resp := &searchv1.BulkIndexDocumentsResponse{
		IndexedCount: int32(result.IndexedCount),
		FailedCount:  int32(result.FailedCount),
	}
	for _, item := range result.Errors {
		resp.Errors = append(resp.Errors, &searchv1.BulkIndexItemError{
			EntityId:     item.EntityID,
			ErrorMessage: item.ErrorMessage,
		})
	}
	return resp, nil
}

func (s *SearchService) ListDocumentIds(ctx context.Context, req *searchv1.ListDocumentIdsRequest) (*searchv1.ListDocumentIdsResponse, error) {
	if req == nil || req.DocumentType == "" {
		return nil, status.Error(codes.InvalidArgument, "document_type is required")
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = search.DefaultListPageSize
	}
	ids, next, err := s.store.ListEntityIDs(ctx, req.DocumentType, req.Cursor, pageSize)
	if err != nil {
		return nil, err
	}
	return &searchv1.ListDocumentIdsResponse{EntityIds: ids, NextCursor: next}, nil
}

func (s *SearchService) Autocomplete(ctx context.Context, req *searchv1.AutocompleteRequest) (*searchv1.AutocompleteResponse, error) {
	if req == nil || req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = search.MaxAutocompletePageSize
	}
	result, err := s.store.Autocomplete(ctx, req.Query, pageSize, int(req.Offset), req.DocumentTypes)
	if err != nil {
		return nil, err
	}

	hits := make([]*searchv1.AutocompleteHit, 0, len(result.Hits))
	for _, doc := range result.Hits {
		highlights := result.Highlights[doc.EntityID]
		score := result.Scores[doc.EntityID]
		hits = append(hits, &searchv1.AutocompleteHit{
			DocumentType: doc.DocumentType,
			EntityId:     doc.EntityID,
			FaceId:       doc.FaceID,
			Title:        doc.Title,
			Subtitle:     doc.Subtitle,
			Highlights:   highlights,
			Score:        score,
			RouteParams:  buildRouteParams(doc),
		})
	}

	nextOffset := int32(req.Offset) + int32(len(hits))
	hasMore := int(req.Offset)+len(hits) < result.Total
	return &searchv1.AutocompleteResponse{
		Hits:       hits,
		TookMs:     result.TookMs,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

func protoToDoc(d *searchv1.SearchDocument) search.Document {
	if d == nil {
		return search.Document{}
	}
	return search.Document{
		DocumentType:   d.DocumentType,
		EntityID:       d.EntityId,
		FaceID:         d.FaceId,
		RoutingUserID:  d.RoutingUserId,
		Title:          d.Title,
		Subtitle:       d.Subtitle,
		SearchText:     d.SearchText,
		ApprovalStatus: d.ApprovalStatus,
		UpdatedAtUnix:  d.UpdatedAtUnixMs,
	}
}

func buildRouteParams(doc search.Document) *searchv1.RouteParams {
	ids := map[string]string{}
	switch doc.DocumentType {
	case "user":
		ids["userId"] = doc.EntityID
	case "face":
		ids["faceId"] = doc.EntityID
	case "page":
		ids["pageId"] = doc.EntityID
	case "album":
		ids["albumId"] = doc.EntityID
	case "blog":
		ids["blogId"] = doc.EntityID
	case "reel":
		ids["reelId"] = doc.EntityID
	case "story":
		ids["storyId"] = doc.EntityID
	case "face_chat_room":
		ids["faceId"] = doc.FaceID
		ids["roomId"] = doc.EntityID
	case "video_lounge":
		ids["faceId"] = doc.FaceID
		ids["loungeId"] = doc.EntityID
	case "face_profile":
		ids["faceId"] = doc.FaceID
		if doc.RoutingUserID != "" {
			ids["userId"] = doc.RoutingUserID
		} else {
			ids["profileId"] = doc.EntityID
		}
	case "wall_ticket":
		ids["faceId"] = doc.FaceID
		ids["ticketId"] = doc.EntityID
	}
	return &searchv1.RouteParams{Type: doc.DocumentType, Ids: ids}
}

var _ searchv1.SearchServiceServer = (*SearchService)(nil)

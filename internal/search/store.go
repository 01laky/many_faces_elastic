package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Store wraps Elasticsearch HTTP operations for the admin search index.
type Store struct {
	es *elasticsearch.Client
}

func NewStore(es *elasticsearch.Client) *Store {
	return &Store{es: es}
}

type Document struct {
	DocumentType   string `json:"document_type"`
	EntityID       string `json:"entity_id"`
	FaceID         string `json:"face_id,omitempty"`
	Title          string `json:"title"`
	Subtitle       string `json:"subtitle,omitempty"`
	SearchText     string `json:"search_text"`
	ApprovalStatus string `json:"approval_status,omitempty"`
	UpdatedAtUnix  int64  `json:"updated_at_unix_ms"`
}

func docID(documentType, entityID string) string {
	return documentType + ":" + entityID
}

func (s *Store) EnsureIndex(ctx context.Context) error {
	res, err := s.es.Indices.Exists([]string{AdminIndexName}, s.es.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		return nil
	}

	mapping := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"document_type":      map[string]string{"type": "keyword"},
				"entity_id":          map[string]string{"type": "keyword"},
				"face_id":            map[string]string{"type": "keyword"},
				"title":              map[string]string{"type": "text"},
				"subtitle":           map[string]string{"type": "text"},
				"search_text":        map[string]string{"type": "text"},
				"approval_status":    map[string]string{"type": "keyword"},
				"updated_at_unix_ms": map[string]string{"type": "long"},
			},
		},
	}
	body, _ := json.Marshal(mapping)
	createRes, err := s.es.Indices.Create(AdminIndexName, s.es.Indices.Create.WithContext(ctx), s.es.Indices.Create.WithBody(bytes.NewReader(body)))
	if err != nil {
		return err
	}
	defer createRes.Body.Close()
	if createRes.IsError() && createRes.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(createRes.Body)
		return fmt.Errorf("create index: %s", string(b))
	}
	return nil
}

func (s *Store) Upsert(ctx context.Context, doc Document) error {
	if err := s.EnsureIndex(ctx); err != nil {
		return err
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	res, err := s.es.Index(
		AdminIndexName,
		bytes.NewReader(body),
		s.es.Index.WithContext(ctx),
		s.es.Index.WithDocumentID(docID(doc.DocumentType, doc.EntityID)),
		s.es.Index.WithRefresh("false"),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("index document: %s", string(b))
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, documentType, entityID string) error {
	res, err := s.es.Delete(AdminIndexName, docID(documentType, entityID), s.es.Delete.WithContext(ctx), s.es.Delete.WithRefresh("false"))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("delete document: %s", string(b))
	}
	return nil
}

type AutocompleteResult struct {
	Hits       []Document
	Highlights map[string][]string
	Scores     map[string]float64
	Total      int
	TookMs     int64
}

func (s *Store) Autocomplete(ctx context.Context, query string, pageSize, offset int, documentTypes []string) (*AutocompleteResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	if pageSize <= 0 {
		pageSize = MaxAutocompletePageSize
	}
	if pageSize > MaxAutocompletePageSize {
		pageSize = MaxAutocompletePageSize
	}
	if offset < 0 {
		offset = 0
	}

	if err := s.EnsureIndex(ctx); err != nil {
		return nil, status.Errorf(codes.Unavailable, "elasticsearch index: %v", err)
	}

	must := []any{
		map[string]any{
			"multi_match": map[string]any{
				"query":  query,
				"fields": []string{"search_text", "title^2", "subtitle"},
				"type":   "best_fields",
			},
		},
	}
	if len(documentTypes) > 0 {
		must = append(must, map[string]any{
			"terms": map[string]any{"document_type": documentTypes},
		})
	}

	searchBody := map[string]any{
		"from": offset,
		"size": pageSize,
		"query": map[string]any{
			"bool": map[string]any{"must": must},
		},
		"highlight": map[string]any{
			"fields": map[string]any{
				"search_text": map[string]any{},
				"title":       map[string]any{},
			},
			"pre_tags":  []string{"<em>"},
			"post_tags": []string{"</em>"},
		},
		"sort": []any{
			map[string]any{"_score": map[string]string{"order": "desc"}},
			map[string]any{"document_type": map[string]string{"order": "asc"}},
			map[string]any{"entity_id": map[string]string{"order": "asc"}},
		},
	}
	body, _ := json.Marshal(searchBody)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(AdminIndexName),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "elasticsearch search: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return nil, status.Errorf(codes.Unavailable, "elasticsearch search: %s", string(raw))
	}

	var parsed struct {
		Took int `json:"took"`
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID        string              `json:"_id"`
				Score     float64             `json:"_score"`
				Source    Document            `json:"_source"`
				Highlight map[string][]string `json:"highlight"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, status.Errorf(codes.Internal, "parse search response: %v", err)
	}

	out := &AutocompleteResult{
		Highlights: make(map[string][]string),
		Scores:     make(map[string]float64),
		Total:      parsed.Hits.Total.Value,
		TookMs:     int64(parsed.Took),
	}
	for _, h := range parsed.Hits.Hits {
		out.Hits = append(out.Hits, h.Source)
		out.Scores[h.Source.EntityID] = h.Score
		if len(h.Highlight) > 0 {
			var parts []string
			for _, vals := range h.Highlight {
				parts = append(parts, vals...)
			}
			out.Highlights[h.Source.EntityID] = parts
		}
	}
	return out, nil
}

func (s *Store) ListEntityIDs(ctx context.Context, documentType string, cursor string, pageSize int) ([]string, string, error) {
	if documentType == "" {
		return nil, "", status.Error(codes.InvalidArgument, "document_type is required")
	}
	if pageSize <= 0 {
		pageSize = DefaultListPageSize
	}
	if err := s.EnsureIndex(ctx); err != nil {
		return nil, "", err
	}

	offset := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil {
			return nil, "", status.Error(codes.InvalidArgument, "invalid cursor")
		}
		offset = parsed
	}

	searchBody := map[string]any{
		"from":    offset,
		"size":    pageSize,
		"_source": []string{"entity_id"},
		"query": map[string]any{
			"term": map[string]any{"document_type": documentType},
		},
		"sort": []any{map[string]any{"entity_id": map[string]string{"order": "asc"}}},
	}
	body, _ := json.Marshal(searchBody)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(AdminIndexName),
		s.es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return nil, "", fmt.Errorf("list ids: %s", string(raw))
	}

	var parsed struct {
		Hits struct {
			Hits []struct {
				Source struct {
					EntityID string `json:"entity_id"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", err
	}

	ids := make([]string, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		if h.Source.EntityID != "" {
			ids = append(ids, h.Source.EntityID)
		}
	}
	next := ""
	if len(ids) == pageSize {
		next = strconv.Itoa(offset + pageSize)
	}
	return ids, next, nil
}

func UnixMsNow() int64 {
	return time.Now().UTC().UnixMilli()
}

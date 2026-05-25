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
	DocumentType    string `json:"document_type"`
	EntityID        string `json:"entity_id"`
	FaceID          string `json:"face_id,omitempty"`
	RoutingUserID   string `json:"routing_user_id,omitempty"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle,omitempty"`
	SearchText      string `json:"search_text"`
	SearchTextEmail string `json:"search_text_email,omitempty"`
	ApprovalStatus  string `json:"approval_status,omitempty"`
	UpdatedAtUnix   int64  `json:"updated_at_unix_ms"`
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

	body, _ := json.Marshal(adminIndexMapping())
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

func enrichDocumentForIndex(doc *Document) {
	if doc == nil {
		return
	}
	if doc.SearchTextEmail == "" {
		if email := extractEmailToken(doc.Title); email != "" {
			doc.SearchTextEmail = email
		} else if email := extractEmailToken(doc.SearchText); email != "" {
			doc.SearchTextEmail = email
		}
	}
}

func extractEmailToken(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "@") {
		return value
	}
	return ""
}

func (s *Store) Upsert(ctx context.Context, doc Document) error {
	if err := s.EnsureIndex(ctx); err != nil {
		return err
	}
	enrichDocumentForIndex(&doc)
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

type BulkUpsertResult struct {
	IndexedCount int
	FailedCount  int
	Errors       []BulkItemError
}

type BulkItemError struct {
	EntityID     string
	ErrorMessage string
}

func (s *Store) BulkUpsert(ctx context.Context, docs []Document) (*BulkUpsertResult, error) {
	if len(docs) == 0 {
		return &BulkUpsertResult{}, nil
	}
	if err := s.EnsureIndex(ctx); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	for _, doc := range docs {
		enrichDocumentForIndex(&doc)
		meta, _ := json.Marshal(map[string]any{
			"index": map[string]any{
				"_index": AdminIndexName,
				"_id":    docID(doc.DocumentType, doc.EntityID),
			},
		})
		payload, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		buf.Write(meta)
		buf.WriteByte('\n')
		buf.Write(payload)
		buf.WriteByte('\n')
	}

	res, err := s.es.Bulk(bytes.NewReader(buf.Bytes()), s.es.Bulk.WithContext(ctx), s.es.Bulk.WithRefresh("false"))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return nil, fmt.Errorf("bulk index: %s", string(raw))
	}

	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
			Error  struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse bulk response: %w", err)
	}

	out := &BulkUpsertResult{}
	for i, item := range parsed.Items {
		for _, v := range item {
			if v.Status >= 200 && v.Status < 300 {
				out.IndexedCount++
			} else {
				out.FailedCount++
				entityID := ""
				if i < len(docs) {
					entityID = docs[i].EntityID
				}
				out.Errors = append(out.Errors, BulkItemError{
					EntityID:     entityID,
					ErrorMessage: v.Error.Reason,
				})
			}
		}
	}
	return out, nil
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

	searchBody := buildAutocompleteSearchBody(query, pageSize, offset, documentTypes)
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

// buildAutocompleteSearchBody combines search_as_you_type bool_prefix with legacy prefix clauses.
func buildAutocompleteSearchBody(query string, pageSize, offset int, documentTypes []string) map[string]any {
	sayFields := []string{
		"search_text",
		"search_text._2gram",
		"search_text._3gram",
		"title",
		"title._2gram",
		"title._3gram",
	}
	should := []any{
		map[string]any{
			"multi_match": map[string]any{
				"query":  query,
				"type":   "bool_prefix",
				"fields": sayFields,
			},
		},
		map[string]any{
			"match": map[string]any{
				"search_text_email": map[string]any{
					"query": query,
					"boost": 3,
				},
			},
		},
	}
	for _, field := range []string{"search_text", "title", "subtitle"} {
		should = append(should, map[string]any{
			"match_phrase_prefix": map[string]any{
				field: map[string]any{"query": query},
			},
		})
	}

	must := []any{
		map[string]any{
			"bool": map[string]any{
				"should":               should,
				"minimum_should_match": 1,
			},
		},
	}
	if len(documentTypes) > 0 {
		must = append(must, map[string]any{
			"terms": map[string]any{"document_type": documentTypes},
		})
	}

	return map[string]any{
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
}

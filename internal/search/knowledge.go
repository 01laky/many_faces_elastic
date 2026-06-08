package search

// knowledge.go implements the Elasticsearch lifecycle and query logic for the operator-AI
// RAG knowledge index (operator-ai-rag-retrieval-refactor v1, spec §5 and §17.3/§17.4/§17.9).
//
// Design summary:
//   - Reads/queries ALWAYS target the alias `operator-ai-knowledge` (KnowledgeAlias).
//   - Writes target a concrete versioned index `operator-ai-knowledge-v{n}` (KnowledgeStore.IndexName())
//     whose suffix is derived from {embed model, embed dim, mapping version}. A change in any of those
//     yields a different concrete index, so a re-embed builds a fresh index and the alias is repointed
//     atomically (zero-downtime), then the old index is dropped.
//   - Indexing rejects any document whose vector_dim differs from the configured dim (drift guard, §5.5).
//   - SemanticSearch runs a kNN query on `vector` AND a BM25 multi_match on text fields, each filtered by
//     source_types, then fuses the two ranked lists with Reciprocal Rank Fusion (RRF, §5.4). If only one
//     retriever is available it returns that one and flags `degraded`.
//
// The store talks to Elasticsearch through the official *elasticsearch.Client directly, matching the
// existing admin Store. Unit tests drive a real client pointed at an httptest server (repo convention).

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"
)

// KnowledgeDoc is the Go projection of the proto KnowledgeDocument persisted to Elasticsearch.
// The `vector` is a dense_vector; the analyzed text fields drive BM25; the keyword fields drive filters.
// No counts/PII are ever stored here (spec §4 rule 1) — only the question-space descriptor.
type KnowledgeDoc struct {
	KnowledgeID       string    `json:"knowledge_id"`
	SourceType        string    `json:"source_type"`
	BundleIndex       int32     `json:"bundle_index"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	Synonyms          []string  `json:"synonyms,omitempty"`
	SampleQuestions   []string  `json:"sample_questions,omitempty"`
	ContentText       string    `json:"content_text"`
	Vector            []float32 `json:"vector"`
	EmbedModelVersion string    `json:"embed_model_version"`
	UpdatedAtUnixMs   int64     `json:"updated_at_unix_ms"`
}

// KnowledgeStore wraps Elasticsearch operations for the operator-AI knowledge index. It is configured with
// the expected embedding dimension, model version, and expected document count so index creation, the drift
// guard, and the readiness check all share one source of truth.
type KnowledgeStore struct {
	es               *elasticsearch.Client
	embedDim         int
	embedModel       string
	expectedDocCount int
}

// NewKnowledgeStore builds a knowledge store. embedDim/embedModel/expectedDocCount come from worker config.
func NewKnowledgeStore(es *elasticsearch.Client, embedDim int, embedModel string, expectedDocCount int) *KnowledgeStore {
	return &KnowledgeStore{
		es:               es,
		embedDim:         embedDim,
		embedModel:       embedModel,
		expectedDocCount: expectedDocCount,
	}
}

// EmbedDim returns the configured expected embedding dimension.
func (s *KnowledgeStore) EmbedDim() int { return s.embedDim }

// EmbedModel returns the configured embedding model version.
func (s *KnowledgeStore) EmbedModel() string { return s.embedModel }

// ExpectedDocCount returns the configured fully-built document count (61 in v1).
func (s *KnowledgeStore) ExpectedDocCount() int { return s.expectedDocCount }

// IndexName derives the concrete versioned index name from the embed model, dimension, and mapping version.
// Changing any of those produces a new name so the alias swap rebuilds into a fresh index (spec §17.3).
// We hash the model string to keep the name short and ES-legal regardless of model naming.
func (s *KnowledgeStore) IndexName() string {
	h := sha1.Sum([]byte(s.embedModel))
	modelTag := fmt.Sprintf("%x", h[:4]) // first 4 bytes → 8 hex chars, stable per model
	return fmt.Sprintf("%s%d-d%d-%s", KnowledgeIndexPrefix, KnowledgeMappingVersion, s.embedDim, modelTag)
}

// knowledgeMapping builds the ES create-index body: dense_vector for `vector` (dims from config, cosine,
// indexed for kNN), analyzed text for BM25, keyword for filters, integer for bundle_index (spec §5.3).
func (s *KnowledgeStore) knowledgeMapping() map[string]any {
	return map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"vector": map[string]any{
					"type":       "dense_vector",
					"dims":       s.embedDim,
					"index":      true,
					"similarity": "cosine",
				},
				"description":         map[string]string{"type": "text"},
				"synonyms":            map[string]string{"type": "text"},
				"sample_questions":    map[string]string{"type": "text"},
				"content_text":        map[string]string{"type": "text"},
				"knowledge_id":        map[string]string{"type": "keyword"},
				"source_type":         map[string]string{"type": "keyword"},
				"embed_model_version": map[string]string{"type": "keyword"},
				"bundle_index":        map[string]string{"type": "integer"},
				"updated_at_unix_ms":  map[string]string{"type": "long"},
			},
		},
	}
}

// EnsureIndexAndAlias guarantees the concrete versioned index exists and the alias points at it.
//   - If the concrete index is missing it is created with the dense_vector mapping.
//   - The alias is then (idempotently) pointed at the concrete index. If the alias previously pointed at an
//     older versioned index, this is the atomic repoint that completes a zero-downtime re-embed: the alias
//     action removes the alias from every index and adds it to the current one in a single _aliases call,
//     and any stale `operator-ai-knowledge-v*` indices are dropped afterwards.
//
// Returns the concrete index name in use. Callers (IndexKnowledge bulk upsert) invoke this first so a fresh
// worker / cold cluster self-heals before the first write.
func (s *KnowledgeStore) EnsureIndexAndAlias(ctx context.Context) (string, error) {
	concrete := s.IndexName()

	exists, err := s.indexExists(ctx, concrete)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := s.createIndex(ctx, concrete); err != nil {
			return "", err
		}
	}

	if err := s.repointAlias(ctx, concrete); err != nil {
		return "", err
	}
	return concrete, nil
}

func (s *KnowledgeStore) indexExists(ctx context.Context, name string) (bool, error) {
	res, err := s.es.Indices.Exists([]string{name}, s.es.Indices.Exists.WithContext(ctx))
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK, nil
}

func (s *KnowledgeStore) createIndex(ctx context.Context, name string) error {
	body, _ := json.Marshal(s.knowledgeMapping())
	res, err := s.es.Indices.Create(name,
		s.es.Indices.Create.WithContext(ctx),
		s.es.Indices.Create.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	// A concurrent creator may have won the race (400 resource_already_exists) — that is benign.
	if res.IsError() && res.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("create knowledge index %s: %s", name, string(b))
	}
	return nil
}

// repointAlias atomically removes the alias from all current targets and adds it to `concrete`, then deletes
// any other `operator-ai-knowledge-v*` indices so only the freshly built version remains (spec §17.3).
func (s *KnowledgeStore) repointAlias(ctx context.Context, concrete string) error {
	actions := map[string]any{
		"actions": []any{
			// Remove the alias wherever it currently lives. must_exist=false makes this a no-op when no index
			// (e.g. cold start) currently carries the alias, so the swap is safe on a fresh cluster. The
			// remove+add pair is applied by ES in a single atomic _aliases transaction (spec §17.3 / RT-16).
			map[string]any{"remove": map[string]any{"index": KnowledgeIndexPrefix + "*", "alias": KnowledgeAlias, "must_exist": false}},
			map[string]any{"add": map[string]any{"index": concrete, "alias": KnowledgeAlias}},
		},
	}
	body, _ := json.Marshal(actions)
	res, err := s.es.Indices.UpdateAliases(bytes.NewReader(body), s.es.Indices.UpdateAliases.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("repoint alias %s -> %s: %s", KnowledgeAlias, concrete, string(b))
	}

	// Drop stale versioned indices (everything matching the prefix except the active concrete index).
	if err := s.dropStaleIndices(ctx, concrete); err != nil {
		// Stale-index cleanup is best-effort: the alias swap already succeeded, so retrieval is correct even
		// if an old index lingers. Surface the error so the caller can log it but do not fail the rebuild.
		return fmt.Errorf("alias repointed but stale-index cleanup failed: %w", err)
	}
	return nil
}

// dropStaleIndices deletes every `operator-ai-knowledge-v*` index except `keep`.
func (s *KnowledgeStore) dropStaleIndices(ctx context.Context, keep string) error {
	names, err := s.listVersionedIndices(ctx)
	if err != nil {
		return err
	}
	var toDelete []string
	for _, n := range names {
		if n != keep {
			toDelete = append(toDelete, n)
		}
	}
	if len(toDelete) == 0 {
		return nil
	}
	res, err := s.es.Indices.Delete(toDelete, s.es.Indices.Delete.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("delete stale indices %v: %s", toDelete, string(b))
	}
	return nil
}

// listVersionedIndices returns concrete index names matching the knowledge prefix via the _cat/indices API.
func (s *KnowledgeStore) listVersionedIndices(ctx context.Context) ([]string, error) {
	res, err := s.es.Cat.Indices(
		s.es.Cat.Indices.WithContext(ctx),
		s.es.Cat.Indices.WithIndex(KnowledgeIndexPrefix+"*"),
		s.es.Cat.Indices.WithFormat("json"),
		s.es.Cat.Indices.WithH("index"),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.IsError() {
		if res.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("cat indices: %s", string(raw))
	}
	var rows []struct {
		Index string `json:"index"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("parse cat indices: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Index != "" {
			out = append(out, r.Index)
		}
	}
	return out, nil
}

// KnowledgeBulkResult is returned by BulkUpsertKnowledge: counts plus per-item errors (drift + ES failures).
type KnowledgeBulkResult struct {
	IndexedCount int
	FailedCount  int
	Errors       []BulkItemError // EntityID carries the knowledge_id for the operator-AI surface
}

// BulkUpsertKnowledge upserts documents into the concrete versioned index keyed by knowledge_id.
//
// Drift guard (spec §5.5 / RT-2): every document's vector length must equal the configured embed dim. Any
// document whose vector length differs is NOT sent to ES; it is counted as failed with a descriptive
// BulkIndexItemError. This is the worker-side half of the "backend/worker/ES can never silently drift" rule.
//
// Inputs: docs already projected to KnowledgeDoc. Output: indexed/failed counts and the failure list.
func (s *KnowledgeStore) BulkUpsertKnowledge(ctx context.Context, docs []KnowledgeDoc) (*KnowledgeBulkResult, error) {
	out := &KnowledgeBulkResult{}
	if len(docs) == 0 {
		return out, nil
	}

	concrete, err := s.EnsureIndexAndAlias(ctx)
	if err != nil {
		return nil, err
	}

	// Partition into accepted (correct dim) and rejected (drift) documents before touching ES.
	accepted := make([]KnowledgeDoc, 0, len(docs))
	for _, d := range docs {
		if len(d.Vector) != s.embedDim {
			out.FailedCount++
			out.Errors = append(out.Errors, BulkItemError{
				EntityID: d.KnowledgeID,
				ErrorMessage: fmt.Sprintf(
					"vector_dim drift: got %d, expected %d (embed_model=%s)",
					len(d.Vector), s.embedDim, s.embedModel,
				),
			})
			continue
		}
		accepted = append(accepted, d)
	}
	if len(accepted) == 0 {
		return out, nil
	}

	var buf bytes.Buffer
	for _, d := range accepted {
		meta, _ := json.Marshal(map[string]any{
			"index": map[string]any{"_index": concrete, "_id": d.KnowledgeID},
		})
		payload, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		buf.Write(meta)
		buf.WriteByte('\n')
		buf.Write(payload)
		buf.WriteByte('\n')
	}

	res, err := s.es.Bulk(bytes.NewReader(buf.Bytes()),
		s.es.Bulk.WithContext(ctx),
		s.es.Bulk.WithRefresh("wait_for"),
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return nil, fmt.Errorf("bulk index knowledge: %s", string(raw))
	}

	var parsed struct {
		Items []map[string]struct {
			Status int `json:"status"`
			Error  struct {
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse knowledge bulk response: %w", err)
	}
	for i, item := range parsed.Items {
		for _, v := range item {
			if v.Status >= 200 && v.Status < 300 {
				out.IndexedCount++
			} else {
				out.FailedCount++
				id := ""
				if i < len(accepted) {
					id = accepted[i].KnowledgeID
				}
				out.Errors = append(out.Errors, BulkItemError{EntityID: id, ErrorMessage: v.Error.Reason})
			}
		}
	}
	return out, nil
}

// DeleteKnowledge removes a document by knowledge_id from the active index (via the alias). A missing
// document is treated as success (idempotent delete). Used by phase-2 content removals.
func (s *KnowledgeStore) DeleteKnowledge(ctx context.Context, knowledgeID string) error {
	res, err := s.es.Delete(KnowledgeAlias, knowledgeID,
		s.es.Delete.WithContext(ctx),
		s.es.Delete.WithRefresh("wait_for"),
	)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("delete knowledge %s: %s", knowledgeID, string(b))
	}
	return nil
}

// KnowledgeHit is one fused retrieval result returned to the handler.
type KnowledgeHit struct {
	KnowledgeID string
	SourceType  string
	BundleIndex int32
	Title       string
	Score       float64 // fused RRF score
	VectorRank  float64 // 1-based rank in the kNN list, 0 if absent
	TextRank    float64 // 1-based rank in the BM25 list, 0 if absent
}

// SemanticSearchResult is the output of SemanticSearch: fused hits, elapsed ms, and the degraded flag.
type SemanticSearchResult struct {
	Hits     []KnowledgeHit
	TookMs   int64
	Degraded bool // true when only one of the two retrievers produced results
}

// rankedDoc holds a single retriever's ranked output for one document.
type rankedDoc struct {
	id          string
	sourceType  string
	bundleIndex int32
	title       string
}

// SemanticSearch runs the hybrid kNN + BM25 retrieval over the alias and fuses the two ranked lists with RRF.
//
// Inputs:
//   - queryVector: the embedded query (BM25-only callers may pass nil/empty to skip kNN)
//   - queryText:   the BM25 side (kNN-only callers may pass "" to skip BM25)
//   - topK:        global number of fused hits to return
//   - sourceTypes: keyword filter applied to BOTH retrievers (v1 = ["stat_bundle"])
//   - rrfK:        RRF constant; if <= 0 it defaults to DefaultRRFK (60)
//
// Fusion (spec §5.4): score(d) = Σ_i 1/(rrfK + rank_i(d)) over the retrievers in which d appears, where
// rank is 1-based. Ties are broken by higher score, then lower bundle_index, then knowledge_id, for
// determinism (RT-1). If exactly one retriever yields results the other is treated as unavailable and the
// result is flagged degraded.
//
// Output: the global top-K fused hits with their per-retriever ranks for debugging.
func (s *KnowledgeStore) SemanticSearch(
	ctx context.Context,
	queryVector []float32,
	queryText string,
	topK int,
	sourceTypes []string,
	rrfK int,
) (*SemanticSearchResult, error) {
	if topK <= 0 {
		topK = 1
	}
	if rrfK <= 0 {
		rrfK = DefaultRRFK
	}

	wantVector := len(queryVector) > 0
	wantText := strings.TrimSpace(queryText) != ""

	var vectorList, textList []rankedDoc
	var vectorErr, textErr error
	var tookMs int64

	if wantVector {
		vectorList, tookMs, vectorErr = s.knnSearch(ctx, queryVector, sourceTypes)
	}
	if wantText {
		var t int64
		textList, t, textErr = s.bm25Search(ctx, queryText, sourceTypes)
		if t > tookMs {
			tookMs = t
		}
	}

	// If a requested retriever hard-failed (ES error), surface it only when BOTH failed; otherwise we run
	// degraded on the surviving retriever so the operator path never crashes (spec §6 / RT-4).
	vectorOK := wantVector && vectorErr == nil
	textOK := wantText && textErr == nil
	if !vectorOK && !textOK {
		// Prefer returning the underlying ES error if there was one; else nothing was requested.
		if vectorErr != nil {
			return nil, vectorErr
		}
		if textErr != nil {
			return nil, textErr
		}
		return &SemanticSearchResult{TookMs: tookMs}, nil
	}

	degraded := !(vectorOK && textOK)

	fused := fuseRRF(vectorList, textList, rrfK)
	if len(fused) > topK {
		fused = fused[:topK]
	}
	return &SemanticSearchResult{Hits: fused, TookMs: tookMs, Degraded: degraded}, nil
}

// knnSearch runs the dense-vector kNN query filtered by source_types, returning a ranked list.
func (s *KnowledgeStore) knnSearch(ctx context.Context, vector []float32, sourceTypes []string) ([]rankedDoc, int64, error) {
	body := map[string]any{
		"size":    KnowledgeRetrieverCandidates,
		"_source": []string{"knowledge_id", "source_type", "bundle_index", "title"},
		"knn": map[string]any{
			"field":          "vector",
			"query_vector":   vector,
			"k":              KnowledgeRetrieverCandidates,
			"num_candidates": KnowledgeRetrieverCandidates * 2,
			"filter":         sourceTypeFilter(sourceTypes),
		},
	}
	return s.runRetriever(ctx, body)
}

// bm25Search runs the BM25 multi_match over the analyzed text fields filtered by source_types.
func (s *KnowledgeStore) bm25Search(ctx context.Context, queryText string, sourceTypes []string) ([]rankedDoc, int64, error) {
	must := []any{
		map[string]any{
			"multi_match": map[string]any{
				"query":  queryText,
				"fields": []string{"content_text", "description", "synonyms", "sample_questions", "title"},
				"type":   "best_fields",
			},
		},
	}
	boolQuery := map[string]any{"must": must}
	if f := sourceTypeFilter(sourceTypes); f != nil {
		boolQuery["filter"] = []any{f}
	}
	body := map[string]any{
		"size":    KnowledgeRetrieverCandidates,
		"_source": []string{"knowledge_id", "source_type", "bundle_index", "title"},
		"query":   map[string]any{"bool": boolQuery},
	}
	return s.runRetriever(ctx, body)
}

// sourceTypeFilter builds a `terms` filter on source_type, or nil when no filter was requested.
func sourceTypeFilter(sourceTypes []string) map[string]any {
	clean := make([]string, 0, len(sourceTypes))
	for _, t := range sourceTypes {
		if strings.TrimSpace(t) != "" {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return map[string]any{"terms": map[string]any{"source_type": clean}}
}

// runRetriever executes a search body against the alias and returns the hits as a ranked list (in ES order).
func (s *KnowledgeStore) runRetriever(ctx context.Context, body map[string]any) ([]rankedDoc, int64, error) {
	raw, _ := json.Marshal(body)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(KnowledgeAlias),
		s.es.Search.WithBody(bytes.NewReader(raw)),
	)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return nil, 0, fmt.Errorf("knowledge search: %s", string(data))
	}
	var parsed struct {
		Took int `json:"took"`
		Hits struct {
			Hits []struct {
				Source struct {
					KnowledgeID string `json:"knowledge_id"`
					SourceType  string `json:"source_type"`
					BundleIndex int32  `json:"bundle_index"`
					Title       string `json:"title"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, 0, fmt.Errorf("parse knowledge search: %w", err)
	}
	out := make([]rankedDoc, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		out = append(out, rankedDoc{
			id:          h.Source.KnowledgeID,
			sourceType:  h.Source.SourceType,
			bundleIndex: h.Source.BundleIndex,
			title:       h.Source.Title,
		})
	}
	return out, int64(parsed.Took), nil
}

// fuseRRF fuses two ranked lists with Reciprocal Rank Fusion and returns hits sorted deterministically.
//
// For each document d present at 1-based rank r in a list, it contributes 1/(rrfK + r) to d's score. The
// per-retriever rank is recorded (0 = absent) for debug. Final ordering: score desc, then bundle_index asc,
// then knowledge_id asc — a total order so identical inputs always yield identical output (RT-1).
func fuseRRF(vectorList, textList []rankedDoc, rrfK int) []KnowledgeHit {
	type agg struct {
		meta       rankedDoc
		score      float64
		vectorRank float64
		textRank   float64
	}
	byID := map[string]*agg{}

	accumulate := func(list []rankedDoc, isVector bool) {
		for i, d := range list {
			rank := float64(i + 1) // 1-based
			a, ok := byID[d.id]
			if !ok {
				a = &agg{meta: d}
				byID[d.id] = a
			}
			a.score += 1.0 / (float64(rrfK) + rank)
			if isVector {
				a.vectorRank = rank
			} else {
				a.textRank = rank
			}
		}
	}
	accumulate(vectorList, true)
	accumulate(textList, false)

	hits := make([]KnowledgeHit, 0, len(byID))
	for _, a := range byID {
		hits = append(hits, KnowledgeHit{
			KnowledgeID: a.meta.id,
			SourceType:  a.meta.sourceType,
			BundleIndex: a.meta.bundleIndex,
			Title:       a.meta.title,
			Score:       a.score,
			VectorRank:  a.vectorRank,
			TextRank:    a.textRank,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score // higher fused score first
		}
		if hits[i].BundleIndex != hits[j].BundleIndex {
			return hits[i].BundleIndex < hits[j].BundleIndex // then lower catalog index
		}
		return hits[i].KnowledgeID < hits[j].KnowledgeID // then stable id order
	})
	return hits
}

// KnowledgeStatus is the readiness/health snapshot returned by Status (backs KnowledgeIndexStatus, §17.4/§17.9).
type KnowledgeStatus struct {
	Alias             string
	ActiveIndex       string
	DocCount          int
	EmbedModelVersion string
	VectorDim         int
	Ready             bool
	Degraded          bool
	LastIndexedUnixMs int64
	ExpectedDocCount  int
	ErrorMessage      string
}

// Status reports the knowledge index health used by the cold-start readiness gate and the admin panel.
//
// It resolves which concrete index the alias points at, counts its documents, finds the newest
// updated_at_unix_ms (last-indexed marker), reads back the configured model/dim, and computes:
//
//	ready = alias exists AND doc_count == expectedDocCount AND embed_model_version matches config
//
// If the alias is missing (cold start / unbuilt index) the result is not-ready with degraded=true and an
// explanatory message rather than an error — the backend uses this to fall back to the planner (RT-15).
func (s *KnowledgeStore) Status(ctx context.Context) (*KnowledgeStatus, error) {
	st := &KnowledgeStatus{
		Alias:             KnowledgeAlias,
		EmbedModelVersion: s.embedModel,
		VectorDim:         s.embedDim,
		ExpectedDocCount:  s.expectedDocCount,
	}

	active, err := s.aliasTarget(ctx)
	if err != nil {
		return nil, err
	}
	if active == "" {
		// Cold start: alias not yet created. Not ready, degraded; backend falls back to the planner.
		st.Ready = false
		st.Degraded = true
		st.ErrorMessage = "alias " + KnowledgeAlias + " does not exist yet (index not built)"
		return st, nil
	}
	st.ActiveIndex = active

	count, lastIndexed, modelInIndex, err := s.indexStats(ctx, active)
	if err != nil {
		return nil, err
	}
	st.DocCount = count
	st.LastIndexedUnixMs = lastIndexed

	modelMatches := modelInIndex == "" || modelInIndex == s.embedModel
	st.Ready = count == s.expectedDocCount && modelMatches
	st.Degraded = !st.Ready
	if !modelMatches {
		st.ErrorMessage = fmt.Sprintf("embed_model_version mismatch: index=%q config=%q", modelInIndex, s.embedModel)
	}
	return st, nil
}

// aliasTarget returns the concrete index the alias points at, or "" if the alias does not exist.
func (s *KnowledgeStore) aliasTarget(ctx context.Context) (string, error) {
	res, err := s.es.Indices.GetAlias(
		s.es.Indices.GetAlias.WithContext(ctx),
		s.es.Indices.GetAlias.WithName(KnowledgeAlias),
	)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", nil
	}
	raw, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return "", fmt.Errorf("get alias: %s", string(raw))
	}
	// Response shape: { "<index>": { "aliases": { "operator-ai-knowledge": {} } }, ... }
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("parse alias: %w", err)
	}
	// Deterministic: if multiple indices carry the alias (shouldn't after a clean swap) pick the lexically
	// greatest name, which corresponds to the most recent versioned build.
	best := ""
	for idx := range parsed {
		if idx > best {
			best = idx
		}
	}
	return best, nil
}

// indexStats returns doc count, the newest updated_at_unix_ms, and the embed_model_version seen in the index.
func (s *KnowledgeStore) indexStats(ctx context.Context, index string) (int, int64, string, error) {
	body := map[string]any{
		"size":             0,
		"track_total_hits": true,
		"aggs": map[string]any{
			"last_indexed": map[string]any{"max": map[string]any{"field": "updated_at_unix_ms"}},
			"models":       map[string]any{"terms": map[string]any{"field": "embed_model_version", "size": 1}},
		},
	}
	raw, _ := json.Marshal(body)
	res, err := s.es.Search(
		s.es.Search.WithContext(ctx),
		s.es.Search.WithIndex(index),
		s.es.Search.WithBody(bytes.NewReader(raw)),
	)
	if err != nil {
		return 0, 0, "", err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return 0, 0, "", fmt.Errorf("index stats: %s", string(data))
	}
	var parsed struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
		} `json:"hits"`
		Aggregations struct {
			LastIndexed struct {
				Value float64 `json:"value"`
			} `json:"last_indexed"`
			Models struct {
				Buckets []struct {
					Key string `json:"key"`
				} `json:"buckets"`
			} `json:"models"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return 0, 0, "", fmt.Errorf("parse index stats: %w", err)
	}
	model := ""
	if len(parsed.Aggregations.Models.Buckets) > 0 {
		model = parsed.Aggregations.Models.Buckets[0].Key
	}
	return parsed.Hits.Total.Value, int64(parsed.Aggregations.LastIndexed.Value), model, nil
}

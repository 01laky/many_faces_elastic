package server

// knowledge_service.go implements the four operator-AI RAG knowledge RPCs on SearchService
// (operator-ai-rag-retrieval-refactor v1, spec §5): IndexKnowledge, DeleteKnowledge, SemanticSearch,
// and KnowledgeIndexStatus. The user-facing search RPCs (Autocomplete, IndexDocument, …) are untouched.
//
// The worker is the only component that talks to Elasticsearch. The backend embeds (via the AI worker's
// EmbedText RPC) and supplies vectors; here the Go worker owns the ES kNN + BM25 hybrid (RRF fusion) and
// the versioned-index + alias lifecycle. The heavy lifting lives in internal/search/knowledge.go; these
// handlers are thin adapters between the proto messages and that store.

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
	"github.com/01laky/many_faces_elastic/internal/search"
)

// IndexKnowledge bulk-upserts knowledge documents into the active versioned index, keyed by knowledge_id.
//
// What/why: this is the index plane of the RAG refactor — the backend pushes the 61 stat-bundle descriptors
// (and, in phase 2, content/doc chunks) here after embedding them. Upsert-by-id makes re-indexing idempotent.
//
// Drift guard (spec §5.5 / RT-2): each document carries a vector_dim; any document whose embedded vector
// length differs from the worker-configured embedding dimension is rejected (counted as failed with a
// BulkIndexItemError) instead of being written, so the backend, worker, and ES mapping can never silently
// disagree on dimensionality.
//
// Inputs: IndexKnowledgeRequest.documents. Output: indexed_count, failed_count, and per-item errors.
func (s *SearchService) IndexKnowledge(ctx context.Context, req *searchv1.IndexKnowledgeRequest) (*searchv1.IndexKnowledgeResponse, error) {
	if req == nil || len(req.Documents) == 0 {
		return nil, status.Error(codes.InvalidArgument, "documents are required")
	}
	if req.CorrelationId != "" {
		s.log.InfoContext(ctx, "IndexKnowledge", "correlation_id", req.CorrelationId, "doc_count", len(req.Documents))
	}

	docs := make([]search.KnowledgeDoc, 0, len(req.Documents))
	for _, d := range req.Documents {
		docs = append(docs, protoToKnowledgeDoc(d))
	}

	result, err := s.knowledge.BulkUpsertKnowledge(ctx, docs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "index knowledge: %v", err)
	}

	resp := &searchv1.IndexKnowledgeResponse{
		IndexedCount: int32(result.IndexedCount),
		FailedCount:  int32(result.FailedCount),
	}
	for _, e := range result.Errors {
		// BulkIndexItemError.entity_id carries the knowledge_id on this surface (reused message, spec §5.1).
		resp.Errors = append(resp.Errors, &searchv1.BulkIndexItemError{
			EntityId:     e.EntityID,
			ErrorMessage: e.ErrorMessage,
		})
	}
	return resp, nil
}

// DeleteKnowledge removes one knowledge document by knowledge_id (phase-2 content removals). Idempotent:
// deleting a missing id succeeds. Inputs: knowledge_id. Output: success + optional error_message.
func (s *SearchService) DeleteKnowledge(ctx context.Context, req *searchv1.DeleteKnowledgeRequest) (*searchv1.DeleteKnowledgeResponse, error) {
	if req == nil || req.KnowledgeId == "" {
		return nil, status.Error(codes.InvalidArgument, "knowledge_id is required")
	}
	if req.CorrelationId != "" {
		s.log.InfoContext(ctx, "DeleteKnowledge", "correlation_id", req.CorrelationId, "knowledge_id", req.KnowledgeId)
	}
	if err := s.knowledge.DeleteKnowledge(ctx, req.KnowledgeId); err != nil {
		return &searchv1.DeleteKnowledgeResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	return &searchv1.DeleteKnowledgeResponse{Success: true}, nil
}

// SemanticSearch runs the hybrid kNN + BM25 retrieval over the alias and returns RRF-fused global top-K hits.
//
// What/why: this is the query plane — it replaces the LLM planner's bundle selection. The backend supplies a
// query_vector (embedded operator message) for the kNN side and query_text for the BM25 side; both are
// filtered by source_types (v1 = ["stat_bundle"]). Results are fused with Reciprocal Rank Fusion
// (score = Σ 1/(rrf_k + rank), spec §5.4) so no BM25-vs-kNN score normalization is needed. rrf_k defaults to
// 60 when the request leaves it 0. If only one retriever is available the response is flagged degraded so the
// backend can react (RT-4).
//
// Inputs: query_vector, query_text, top_k, source_types, rrf_k. Output: fused hits (with per-retriever ranks
// for debugging), took_ms, and the degraded flag.
func (s *SearchService) SemanticSearch(ctx context.Context, req *searchv1.SemanticSearchRequest) (*searchv1.SemanticSearchResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	// At least one retriever must be drivable; otherwise there is nothing to search.
	if len(req.QueryVector) == 0 && req.QueryText == "" {
		return nil, status.Error(codes.InvalidArgument, "query_vector or query_text is required")
	}
	if req.CorrelationId != "" {
		s.log.InfoContext(ctx, "SemanticSearch",
			"correlation_id", req.CorrelationId,
			"top_k", req.TopK,
			"has_vector", len(req.QueryVector) > 0,
			"has_text", req.QueryText != "",
		)
	}

	result, err := s.knowledge.SemanticSearch(
		ctx,
		req.QueryVector,
		req.QueryText,
		int(req.TopK),
		req.SourceTypes,
		int(req.RrfK),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "semantic search: %v", err)
	}

	resp := &searchv1.SemanticSearchResponse{
		TookMs:   result.TookMs,
		Degraded: result.Degraded,
	}
	for _, h := range result.Hits {
		resp.Hits = append(resp.Hits, &searchv1.SemanticSearchHit{
			KnowledgeId: h.KnowledgeID,
			SourceType:  h.SourceType,
			BundleIndex: h.BundleIndex,
			Title:       h.Title,
			Score:       h.Score,
			VectorRank:  h.VectorRank,
			TextRank:    h.TextRank,
		})
	}
	return resp, nil
}

// KnowledgeIndexStatus reports the knowledge index readiness + health used by the backend cold-start gate
// (spec §17.4) and the admin status panel (§17.9).
//
// What/why: before the operator chat trusts retrieval it checks ready = alias exists AND doc_count ==
// expected(61) AND embed_model_version matches config. Until ready (cold start, mid-rebuild, model bump) the
// backend short-circuits to the legacy planner instead of wrongly refusing.
//
// Inputs: correlation_id (optional). Output: alias, active_index, doc_count, embed_model_version, vector_dim,
// ready, degraded, last_indexed_unix_ms, expected_doc_count, and an error_message for non-fatal states.
func (s *SearchService) KnowledgeIndexStatus(ctx context.Context, req *searchv1.KnowledgeIndexStatusRequest) (*searchv1.KnowledgeIndexStatusResponse, error) {
	if req != nil && req.CorrelationId != "" {
		s.log.InfoContext(ctx, "KnowledgeIndexStatus", "correlation_id", req.CorrelationId)
	}
	st, err := s.knowledge.Status(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "knowledge status: %v", err)
	}
	return &searchv1.KnowledgeIndexStatusResponse{
		Alias:             st.Alias,
		ActiveIndex:       st.ActiveIndex,
		DocCount:          int32(st.DocCount),
		EmbedModelVersion: st.EmbedModelVersion,
		VectorDim:         int32(st.VectorDim),
		Ready:             st.Ready,
		Degraded:          st.Degraded,
		LastIndexedUnixMs: st.LastIndexedUnixMs,
		ExpectedDocCount:  int32(st.ExpectedDocCount),
		ErrorMessage:      st.ErrorMessage,
	}, nil
}

// protoToKnowledgeDoc maps the proto KnowledgeDocument to the store projection. Note vector_dim is NOT copied
// into the stored doc — the store derives the true dimension from len(vector) and enforces the drift guard
// against the configured dim, so a lying vector_dim cannot bypass the check.
func protoToKnowledgeDoc(d *searchv1.KnowledgeDocument) search.KnowledgeDoc {
	if d == nil {
		return search.KnowledgeDoc{}
	}
	return search.KnowledgeDoc{
		KnowledgeID:       d.KnowledgeId,
		SourceType:        d.SourceType,
		BundleIndex:       d.BundleIndex,
		Title:             d.Title,
		Description:       d.Description,
		Synonyms:          d.Synonyms,
		SampleQuestions:   d.SampleQuestions,
		ContentText:       d.ContentText,
		Vector:            d.Vector,
		EmbedModelVersion: d.EmbedModelVersion,
		UpdatedAtUnixMs:   d.UpdatedAtUnixMs,
	}
}

package search

// adminIndexMapping defines Elasticsearch settings for admin autocomplete (search_as_you_type).
func adminIndexMapping() map[string]any {
	searchAsYouType := map[string]any{
		"type":             "search_as_you_type",
		"max_shingle_size": 3,
	}
	return map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
			"analysis": map[string]any{
				"analyzer": map[string]any{
					"admin_email_prefix": map[string]any{
						"type":      "custom",
						"tokenizer": "keyword",
						"filter":    []string{"lowercase", "edge_ngram_filter"},
					},
				},
				"filter": map[string]any{
					"edge_ngram_filter": map[string]any{
						"type":     "edge_ngram",
						"min_gram": 2,
						"max_gram": 20,
					},
				},
			},
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"document_type":      map[string]string{"type": "keyword"},
				"entity_id":          map[string]string{"type": "keyword"},
				"face_id":            map[string]string{"type": "keyword"},
				"routing_user_id":    map[string]string{"type": "keyword"},
				"title":              searchAsYouType,
				"subtitle":           map[string]string{"type": "text"},
				"search_text":        searchAsYouType,
				"search_text_email":  map[string]string{"type": "text", "analyzer": "admin_email_prefix", "search_analyzer": "admin_email_prefix"},
				"approval_status":    map[string]string{"type": "keyword"},
				"updated_at_unix_ms": map[string]string{"type": "long"},
			},
		},
	}
}

// Package config centralizes environment-driven settings for the search-worker process.
// The worker is designed to run inside Docker next to Elasticsearch; all defaults assume in-cluster DNS.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds every runtime knob for the search worker. Fields are populated only from environment
// variables so the same binary can be reused across dev/stage/prod with different compose files.
type Config struct {
	// GRPCListen is the bind address for the gRPC server, e.g. ":50052" (all interfaces) or "127.0.0.1:50052".
	GRPCListen string

	// ElasticsearchAddresses is forwarded verbatim to github.com/elastic/go-elasticsearch/v8 Config.Addresses.
	// Example single-node dev: "http://elasticsearch:9200".
	ElasticsearchAddresses []string

	// ExpectedWorkerToken, when non-empty, requires every unary RPC to include metadata key "x-search-worker-token"
	// matching this value exactly. many_faces_backend sends the same value from Search:WorkerAuthToken.
	// Leave empty only on trusted dev networks; production should always set this or use mTLS (future).
	ExpectedWorkerToken string
}

const (
	// EnvGRPCListen is the primary bind address for gRPC.
	EnvGRPCListen = "SEARCH_WORKER_GRPC_LISTEN"
	// EnvElasticsearchURLs is a comma-separated list of Elasticsearch HTTP base URLs.
	EnvElasticsearchURLs = "SEARCH_WORKER_ELASTICSEARCH_URLS"
	// EnvExpectedToken enables lightweight shared-secret authentication between internal services.
	EnvExpectedToken = "SEARCH_WORKER_EXPECTED_TOKEN"
)

// LoadFromEnv builds Config from process environment. It returns an error if mandatory values are missing
// so the container fails fast in orchestrators instead of serving half-configured RPCs.
func LoadFromEnv() (*Config, error) {
	listen := strings.TrimSpace(os.Getenv(EnvGRPCListen))
	if listen == "" {
		listen = ":50052"
	}

	rawURLs := strings.TrimSpace(os.Getenv(EnvElasticsearchURLs))
	if rawURLs == "" {
		return nil, fmt.Errorf("%s is required (comma-separated Elasticsearch base URLs)", EnvElasticsearchURLs)
	}

	parts := strings.Split(rawURLs, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		u := strings.TrimSpace(p)
		if u != "" {
			addrs = append(addrs, u)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s contained no valid URLs", EnvElasticsearchURLs)
	}

	return &Config{
		GRPCListen:               listen,
		ElasticsearchAddresses:   addrs,
		ExpectedWorkerToken:      strings.TrimSpace(os.Getenv(EnvExpectedToken)),
	}, nil
}

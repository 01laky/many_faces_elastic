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
	// Leave empty only on trusted dev networks; production should combine TLS with token and/or mTLS.
	ExpectedWorkerToken string

	// GrpcTLSCertFile and GrpcTLSKeyFile, when both set, enable TLS on the gRPC server (listen still on the same TCP port).
	// Mount PEM files into the container (read-only). See docs/guides/elasticsearch-grpc-tls-mtls.md in many_faces_main.
	GrpcTLSCertFile string
	GrpcTLSKeyFile  string

	// GrpcMTLSClientCAFile, when set together with GrpcTLSCertFile/GrpcTLSKeyFile, requires presenting a client certificate
	// issued by this CA (PEM bundle). many_faces_backend must then use Search:WorkerTlsClientCertPath / WorkerTlsClientKeyPath.
	GrpcMTLSClientCAFile string
}

const (
	// EnvGRPCListen is the primary bind address for gRPC.
	EnvGRPCListen = "SEARCH_WORKER_GRPC_LISTEN"
	// EnvElasticsearchURLs is a comma-separated list of Elasticsearch HTTP base URLs.
	EnvElasticsearchURLs = "SEARCH_WORKER_ELASTICSEARCH_URLS"
	// EnvExpectedToken enables lightweight shared-secret authentication between internal services.
	EnvExpectedToken = "SEARCH_WORKER_EXPECTED_TOKEN"
	// EnvGrpcTLSCertFile is the server certificate PEM path for gRPC over TLS.
	EnvGrpcTLSCertFile = "SEARCH_WORKER_GRPC_TLS_CERT_FILE"
	// EnvGrpcTLSKeyFile is the server private key PEM path matching EnvGrpcTLSCertFile.
	EnvGrpcTLSKeyFile = "SEARCH_WORKER_GRPC_TLS_KEY_FILE"
	// EnvGrpcMTLSClientCAFile is an optional PEM bundle of CAs used to verify client certificates (mTLS).
	EnvGrpcMTLSClientCAFile = "SEARCH_WORKER_GRPC_MTLS_CLIENT_CA_FILE"
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
		ExpectedWorkerToken:        strings.TrimSpace(os.Getenv(EnvExpectedToken)),
		GrpcTLSCertFile:            strings.TrimSpace(os.Getenv(EnvGrpcTLSCertFile)),
		GrpcTLSKeyFile:             strings.TrimSpace(os.Getenv(EnvGrpcTLSKeyFile)),
		GrpcMTLSClientCAFile:       strings.TrimSpace(os.Getenv(EnvGrpcMTLSClientCAFile)),
	}, nil
}

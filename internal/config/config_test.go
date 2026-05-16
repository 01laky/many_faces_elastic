package config

import (
	"testing"
)

func TestLoadFromEnv_RequiresElasticsearchURLs(t *testing.T) {
	t.Setenv(EnvGRPCListen, "")
	t.Setenv(EnvElasticsearchURLs, "")
	t.Setenv(EnvExpectedToken, "")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when SEARCH_WORKER_ELASTICSEARCH_URLS is empty")
	}
}

func TestLoadFromEnv_RejectsWhitespaceOnlyURLs(t *testing.T) {
	t.Setenv(EnvElasticsearchURLs, " ,  , ")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when URL list has no usable entries")
	}
}

func TestLoadFromEnv_DefaultListenAndSingleURL(t *testing.T) {
	t.Setenv(EnvGRPCListen, "")
	t.Setenv(EnvElasticsearchURLs, "http://elasticsearch:9200")
	t.Setenv(EnvExpectedToken, "secret")
	c, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.GRPCListen != ":50052" {
		t.Fatalf("listen: got %q", c.GRPCListen)
	}
	if len(c.ElasticsearchAddresses) != 1 || c.ElasticsearchAddresses[0] != "http://elasticsearch:9200" {
		t.Fatalf("addresses: %#v", c.ElasticsearchAddresses)
	}
	if c.ExpectedWorkerToken != "secret" {
		t.Fatalf("token: %q", c.ExpectedWorkerToken)
	}
}

func TestLoadFromEnv_TrimsAndSplitsCommaURLs(t *testing.T) {
	t.Setenv(EnvGRPCListen, ":6000")
	t.Setenv(EnvElasticsearchURLs, " http://a:9200 , http://b:9200 ")
	c, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.GRPCListen != ":6000" {
		t.Fatalf("listen: %q", c.GRPCListen)
	}
	if len(c.ElasticsearchAddresses) != 2 {
		t.Fatalf("want 2 addresses, got %#v", c.ElasticsearchAddresses)
	}
}

func TestLoadFromEnv_TrimsTLSPaths(t *testing.T) {
	t.Setenv(EnvGRPCListen, ":50052")
	t.Setenv(EnvElasticsearchURLs, "http://elasticsearch:9200")
	t.Setenv(EnvGrpcTLSCertFile, "  /certs/server.crt  ")
	t.Setenv(EnvGrpcTLSKeyFile, " /certs/server.key ")
	t.Setenv(EnvGrpcMTLSClientCAFile, " /certs/ca.pem ")

	c, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.GrpcTLSCertFile != "/certs/server.crt" || c.GrpcTLSKeyFile != "/certs/server.key" {
		t.Fatalf("tls paths: cert=%q key=%q", c.GrpcTLSCertFile, c.GrpcTLSKeyFile)
	}
	if c.GrpcMTLSClientCAFile != "/certs/ca.pem" {
		t.Fatalf("mtls ca: %q", c.GrpcMTLSClientCAFile)
	}
}

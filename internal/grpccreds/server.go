// Package grpccreds builds gRPC server transport credentials for the search-worker.
// When certificate paths are unset the caller should omit grpc.Creds(...) so the server listens in plaintext (dev only).
package grpccreds

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
)

// LoadServerCredentials returns TLS transport credentials for the gRPC server when both certFile and keyFile are set.
// If both certFile and keyFile are empty, returns (nil, nil) meaning the server should use plaintext.
// If clientCAFile is non-empty, clients must present a certificate signed by that CA (mTLS).
func LoadServerCredentials(certFile, keyFile, clientCAFile string) (credentials.TransportCredentials, error) {
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	clientCAFile = strings.TrimSpace(clientCAFile)

	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("TLS requires both SEARCH_WORKER_GRPC_TLS_CERT_FILE and SEARCH_WORKER_GRPC_TLS_KEY_FILE when either is set")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server TLS key pair: %w", err)
	}

	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if clientCAFile != "" {
		raw, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read client CA bundle: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw) {
			return nil, fmt.Errorf("no PEM certificates found in SEARCH_WORKER_GRPC_MTLS_CLIENT_CA_FILE")
		}
		tlsConf.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConf.ClientCAs = pool
	}

	return credentials.NewTLS(tlsConf), nil
}

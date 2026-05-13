// Package server hosts the gRPC stack for the search worker (interceptors + SearchService implementation).
package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Metadata key agreed with many_faces_backend SearchOptions.WorkerAuthToken (HTTP/gRPC metadata is case-insensitive for keys, but we use lowercase consistently).
const metadataWorkerTokenKey = "x-search-worker-token"

// UnaryAuthInterceptor returns a grpc.UnaryServerInterceptor that enforces SEARCH_WORKER_EXPECTED_TOKEN when non-empty.
// Health checks use grpc.health.v1 and typically do not carry custom metadata; those methods are exempt so Kubernetes-style probes keep working.
// Application RPCs on SearchService still require the metadata header when a token is configured.
func UnaryAuthInterceptor(expectedToken string) grpc.UnaryServerInterceptor {
	if expectedToken == "" {
		// No authentication gate: suitable only for tightly coupled dev Docker networks.
		return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			return handler(ctx, req)
		}
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Allow Kubernetes-style and Docker health probes without shared secrets on the health service only.
		if strings.HasPrefix(info.FullMethod, "/grpc.health.v1.Health/") {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing gRPC metadata")
		}
		vals := md.Get(metadataWorkerTokenKey)
		if len(vals) != 1 || vals[0] != expectedToken {
			return nil, status.Error(codes.Unauthenticated, "invalid or missing x-search-worker-token")
		}
		return handler(ctx, req)
	}
}

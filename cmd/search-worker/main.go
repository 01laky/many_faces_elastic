// Command search-worker is the long-lived gRPC process colocated with Elasticsearch in many_faces_elastic.
// It is the only Many Faces component that should open Elasticsearch HTTP connections for application traffic;
// many_faces_backend and many_faces_ai call this binary over gRPC instead.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
	"github.com/01laky/many_faces_elastic/internal/config"
	"github.com/01laky/many_faces_elastic/internal/grpccreds"
	"github.com/01laky/many_faces_elastic/internal/server"
)

func main() {
	// Structured JSON logs go to stdout so Docker log drivers and Seq-sidecar patterns in the monorepo stay consistent.
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	log := slog.New(handler)
	slog.SetDefault(log)

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Official Elasticsearch Go client: thread-safe, connection-pooled HTTP transport to the cluster root URL(s).
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: cfg.ElasticsearchAddresses,
	})
	if err != nil {
		log.Error("failed to create elasticsearch client", "error", err)
		os.Exit(1)
	}

	lis, err := net.Listen("tcp", cfg.GRPCListen)
	if err != nil {
		log.Error("failed to listen for gRPC", "addr", cfg.GRPCListen, "error", err)
		os.Exit(1)
	}

	serverCreds, err := grpccreds.LoadServerCredentials(cfg.GrpcTLSCertFile, cfg.GrpcTLSKeyFile, cfg.GrpcMTLSClientCAFile)
	if err != nil {
		log.Error("failed to configure gRPC TLS", "error", err)
		os.Exit(1)
	}

	var serverOpts []grpc.ServerOption
	if serverCreds != nil {
		serverOpts = append(serverOpts, grpc.Creds(serverCreds))
	}
	// ChainUnaryInterceptor applies shared-secret metadata checks before application RPCs (health is exempted inside the interceptor).
	serverOpts = append(serverOpts, grpc.ChainUnaryInterceptor(server.UnaryAuthInterceptor(cfg.ExpectedWorkerToken)))
	grpcServer := grpc.NewServer(serverOpts...)

	searchv1.RegisterSearchServiceServer(grpcServer, server.NewSearchService(es, log))

	// Standard gRPC health service for orchestrators; kept separate from SearchService.Ping (which also checks Elasticsearch).
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Server reflection helps grpcurl and IDE tooling during local development; it does not expose HTTP product APIs.
	reflection.Register(grpcServer)

	go func() {
		tlsMode := "plaintext"
		if serverCreds != nil {
			tlsMode = "tls"
			if cfg.GrpcMTLSClientCAFile != "" {
				tlsMode = "mtls"
			}
		}
		log.Info("search-worker gRPC listening", "addr", cfg.GRPCListen, "tls", tlsMode, "elasticsearch_urls", cfg.ElasticsearchAddresses)
		if err := grpcServer.Serve(lis); err != nil {
			log.Error("gRPC server stopped with error", "error", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Info("shutdown signal received, stopping gRPC gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Info("gRPC graceful stop completed")
	case <-shutdownCtx.Done():
		log.Warn("graceful stop timed out, forcing stop")
		grpcServer.Stop()
	}
}

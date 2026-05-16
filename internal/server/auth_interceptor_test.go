package server

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	searchv1 "github.com/01laky/many_faces_elastic/gen/manyfaces/search/v1"
)

// noopSearchService is a minimal SearchService implementation used only to exercise transport + interceptors in tests.
type noopSearchService struct {
	searchv1.UnimplementedSearchServiceServer
}

func (noopSearchService) Ping(context.Context, *searchv1.PingRequest) (*searchv1.PingResponse, error) {
	return &searchv1.PingResponse{ElasticsearchReachable: true}, nil
}

func TestUnaryAuthInterceptor_RejectsPingWithoutTokenWhenExpected(t *testing.T) {
	const secret = "unit-test-secret"

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(UnaryAuthInterceptor(secret)))
	searchv1.RegisterSearchServiceServer(srv, noopSearchService{})

	lis := bufconn.Listen(1024 * 1024)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server exit: %v", err)
		}
	}()
	t.Cleanup(func() { srv.Stop() })

	ctx := context.Background()
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := searchv1.NewSearchServiceClient(conn)
	_, err = client.Ping(ctx, &searchv1.PingRequest{})
	if err == nil {
		t.Fatal("expected Unauthenticated error")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnaryAuthInterceptor_AllowsPingWithTokenWhenExpected(t *testing.T) {
	const secret = "unit-test-secret"

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(UnaryAuthInterceptor(secret)))
	searchv1.RegisterSearchServiceServer(srv, noopSearchService{})

	lis := bufconn.Listen(1024 * 1024)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server exit: %v", err)
		}
	}()
	t.Cleanup(func() { srv.Stop() })

	md := metadata.Pairs(metadataWorkerTokenKey, secret)
	ctx := metadata.NewOutgoingContext(context.Background(), md)
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := searchv1.NewSearchServiceClient(conn)
	resp, err := client.Ping(ctx, &searchv1.PingRequest{})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !resp.ElasticsearchReachable {
		t.Fatalf("expected reachable=true from stub")
	}
}

func TestUnaryAuthInterceptor_AllowsPingWhenSecretEmpty(t *testing.T) {
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(UnaryAuthInterceptor("")))
	searchv1.RegisterSearchServiceServer(srv, noopSearchService{})

	lis := bufconn.Listen(1024 * 1024)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server exit: %v", err)
		}
	}()
	t.Cleanup(func() { srv.Stop() })

	ctx := context.Background()
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := searchv1.NewSearchServiceClient(conn)
	if _, err := client.Ping(ctx, &searchv1.PingRequest{}); err != nil {
		t.Fatalf("ping without token when secret empty: %v", err)
	}
}

func TestUnaryAuthInterceptor_AllowsHealthRPCWithoutToken(t *testing.T) {
	ic := UnaryAuthInterceptor("unit-test-secret")
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	called := false
	_, err := ic(context.Background(), nil, info, func(context.Context, any) (any, error) {
		called = true
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	if !called {
		t.Fatal("handler was not invoked")
	}
}

func TestUnaryAuthInterceptor_RejectsWhenMetadataMissing(t *testing.T) {
	ic := UnaryAuthInterceptor("secret")
	info := &grpc.UnaryServerInfo{FullMethod: "/manyfaces.search.v1.SearchService/Ping"}
	_, err := ic(context.Background(), nil, info, func(context.Context, any) (any, error) {
		t.Fatal("handler should not run")
		return nil, nil
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestUnaryAuthInterceptor_RejectsDuplicateTokenHeaders(t *testing.T) {
	ic := UnaryAuthInterceptor("secret")
	info := &grpc.UnaryServerInfo{FullMethod: "/manyfaces.search.v1.SearchService/Ping"}
	md := metadata.Pairs(metadataWorkerTokenKey, "secret", metadataWorkerTokenKey, "secret")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	_, err := ic(ctx, nil, info, func(context.Context, any) (any, error) {
		t.Fatal("handler should not run")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error for duplicate metadata values")
	}
}

func TestUnaryAuthInterceptor_RejectsPingWithWrongTokenWhenExpected(t *testing.T) {
	const secret = "expected"
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(UnaryAuthInterceptor(secret)))
	searchv1.RegisterSearchServiceServer(srv, noopSearchService{})

	lis := bufconn.Listen(1024 * 1024)
	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server exit: %v", err)
		}
	}()
	t.Cleanup(func() { srv.Stop() })

	md := metadata.Pairs(metadataWorkerTokenKey, "wrong")
	ctx := metadata.NewOutgoingContext(context.Background(), md)
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := searchv1.NewSearchServiceClient(conn)
	_, err = client.Ping(ctx, &searchv1.PingRequest{})
	if err == nil {
		t.Fatal("expected Unauthenticated")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("unexpected: %v", err)
	}
}

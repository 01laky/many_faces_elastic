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

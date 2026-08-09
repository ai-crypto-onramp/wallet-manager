package authtoken

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGRPCInterceptorBypass(t *testing.T) {
	called := false
	intc := GRPCUnaryInterceptor(testSecret, true)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("bypass should call handler")
	}
}

func TestGRPCInterceptorSkipMethods(t *testing.T) {
	for _, m := range []string{
		"/grpc.health.v1.Health/Check",
		"/grpc.reflection.v1alpha.ServerReflectionInfo",
		"/grpc.reflection.v1.ServerReflectionInfo",
	} {
		called := false
		intc := GRPCUnaryInterceptor(testSecret, false)
		info := &grpc.UnaryServerInfo{FullMethod: m}
		_, err := intc(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
			called = true
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("skip method %s: %v", m, err)
		}
		if !called {
			t.Errorf("skip method %s should call handler", m)
		}
	}
}

func TestGRPCInterceptorNoMetadata(t *testing.T) {
	intc := GRPCUnaryInterceptor(testSecret, false)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should not be called")
		return nil, nil
	})
	if err == nil || status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", err)
	}
}

func TestGRPCInterceptorMissingToken(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", ""))
	intc := GRPCUnaryInterceptor(testSecret, false)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should not be called")
		return nil, nil
	})
	if err == nil || status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", err)
	}
}

func TestGRPCInterceptorXServiceToken(t *testing.T) {
	tok, _ := Issue("svc", testSecret)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-service-token", tok))
	called := false
	intc := GRPCUnaryInterceptor(testSecret, false)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should be called with valid x-service-token")
	}
}

func TestGRPCInterceptorBearerToken(t *testing.T) {
	tok, _ := Issue("svc", testSecret)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+tok))
	called := false
	intc := GRPCUnaryInterceptor(testSecret, false)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should be called with valid bearer token")
	}
}

func TestGRPCInterceptorBadToken(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer bad-token"))
	intc := GRPCUnaryInterceptor(testSecret, false)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should not be called")
		return nil, nil
	})
	if err == nil || status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", err)
	}
}

func TestGRPCInterceptorExpiredToken(t *testing.T) {
	now := time.Now().UTC()
	claims := Claims{Sub: "svc", Iat: now.Add(-2 * time.Hour).Unix(), Exp: now.Add(-1 * time.Hour).Unix()}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	tok, _ := sign(header, claims, testSecret)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+tok))
	intc := GRPCUnaryInterceptor(testSecret, false)
	info := &grpc.UnaryServerInfo{FullMethod: "/wallet.WalletService/Foo"}
	_, err := intc(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should not be called")
		return nil, nil
	})
	if err == nil || status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated for expired token, got %v", err)
	}
}
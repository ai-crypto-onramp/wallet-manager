package otel

import (
	"context"
	"testing"
	"time"
)

func TestInitNoEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := Init("wallet-management")
	if err != nil {
		t.Fatalf("Init with no endpoint: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func shortCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 500*time.Millisecond)
}

func TestInitWithEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_SERVICE_NAME", "test-svc")
	shutdown, err := Init("wallet-management")
	if err != nil {
		t.Fatalf("Init with endpoint: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown")
	}
	ctx, cancel := shortCtx()
	defer cancel()
	_ = shutdown(ctx)
}

func TestInitEndpointStripsScheme(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "grpc://127.0.0.1:4317")
	shutdown, err := Init("wallet-management")
	if err != nil {
		t.Fatalf("Init with grpc:// scheme: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown")
	}
	ctx, cancel := shortCtx()
	defer cancel()
	_ = shutdown(ctx)
}

func TestInitServiceNameOverride(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_SERVICE_NAME", "  overridden  ")
	shutdown, err := Init("default")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown")
	}
	ctx, cancel := shortCtx()
	defer cancel()
	_ = shutdown(ctx)
}

func TestInitEmptyServiceName(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_SERVICE_NAME", "   ")
	shutdown, err := Init("default")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	ctx, cancel := shortCtx()
	defer cancel()
	_ = shutdown(ctx)
}
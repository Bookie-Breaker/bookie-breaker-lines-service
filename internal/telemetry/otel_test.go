package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestInitConfiguresProvidersAndShutdown(t *testing.T) {
	ctx := context.Background()

	// OTLP gRPC exporters dial lazily, so Init succeeds without a collector.
	shutdown, err := Init(ctx, "lines-service-test", "localhost:4317")
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init must return a shutdown function")
	}

	if otel.GetTracerProvider() == nil || otel.GetMeterProvider() == nil {
		t.Error("global providers should be registered")
	}
	// The composite propagator must carry W3C trace context for cross-service
	// propagation.
	fields := otel.GetTextMapPropagator().Fields()
	hasTraceparent := false
	for _, f := range fields {
		if f == "traceparent" {
			hasTraceparent = true
		}
	}
	if !hasTraceparent {
		t.Errorf("propagator fields = %v, want traceparent", fields)
	}

	// Shutdown flushes to the (absent) collector; with a short deadline it
	// must return rather than hang. The error itself is expected since
	// nothing is listening.
	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = shutdown(shutdownCtx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not return within its deadline")
	}
}

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestJSONOutputAndLevel(t *testing.T) {
	var buf bytes.Buffer
	InitWith(&buf, slog.LevelInfo, "json")

	Info(nil, "startup complete", "component", "limiter")
	Debug(nil, "hidden debug")
	Warn(nil, "degraded", "fail_open", true)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 info/warn lines, got %d: %q", len(lines), buf.String())
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if entry["level"] != "INFO" {
		t.Fatalf("expected INFO level, got %v", entry["level"])
	}
	if entry["msg"] != "startup complete" {
		t.Fatalf("unexpected msg: %v", entry["msg"])
	}
	if _, ok := entry["request_id"]; ok {
		t.Fatal("startup log must not contain request_id")
	}
}

func TestLOGLevelSuppressesDebug(t *testing.T) {
	var buf bytes.Buffer
	InitWith(&buf, slog.LevelInfo, "json")

	Debug(nil, "should not appear")
	if buf.Len() != 0 {
		t.Fatalf("debug should be suppressed at info level: %q", buf.String())
	}
}

func TestInvalidLOGLevelDefaultsInfo(t *testing.T) {
	level, invalid := resolveLevel("verbose")
	if !invalid || level != slog.LevelInfo {
		t.Fatalf("expected invalid info fallback, got level=%v invalid=%v", level, invalid)
	}
}

func TestRequestIDInjection(t *testing.T) {
	var buf bytes.Buffer
	InitWith(&buf, slog.LevelInfo, "json")

	ctx := telemetry.WithRequestID(context.Background(), "req-abc-123")
	Info(ctx, "handled request", "component", "sidecar")

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if entry["request_id"] != "req-abc-123" {
		t.Fatalf("expected request_id, got %v", entry["request_id"])
	}
}

func TestCorrelationAttrsFromOTel(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

	ctx, span := otel.Tracer("test").Start(context.Background(), "test-span")
	defer span.End()

	sc := span.SpanContext()
	attrs := CorrelationAttrs(ctx)
	if len(attrs) != 4 {
		t.Fatalf("expected trace_id and span_id attrs, got %v", attrs)
	}
	if attrs[0] != "trace_id" || attrs[2] != "span_id" {
		t.Fatalf("unexpected attr keys: %v", attrs)
	}
	if attrs[1] != sc.TraceID().String() || attrs[3] != sc.SpanID().String() {
		t.Fatalf("trace/span mismatch: %v vs %v", attrs, sc)
	}
}

func TestMissingSpanContextOmitsTraceFields(t *testing.T) {
	attrs := CorrelationAttrs(context.Background())
	if len(attrs) != 0 {
		t.Fatalf("expected no correlation attrs, got %v", attrs)
	}
}

func TestInvalidSpanContextOmitsTraceFields(t *testing.T) {
	ctx := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.SpanContext{})
	attrs := CorrelationAttrs(ctx)
	if len(attrs) != 0 {
		t.Fatalf("expected no trace fields for invalid span context, got %v", attrs)
	}
}

func TestSensitiveIdentifiersAbsentInErrorLog(t *testing.T) {
	var buf bytes.Buffer
	InitWith(&buf, slog.LevelError, "json")

	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	ctx, _ := otel.Tracer("test").Start(context.Background(), "limiter.check")

	Error(ctx, "token bucket lua script failed",
		"component", "limiter",
		"operation", "redis_lua",
		"algorithm", "token_bucket",
		"error", "connection refused",
	)

	out := buf.String()
	if strings.Contains(out, "user-123") || strings.Contains(out, "idempotency") {
		t.Fatalf("log leaked sensitive identifiers: %s", out)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if entry["level"] != "ERROR" {
		t.Fatalf("expected ERROR, got %v", entry["level"])
	}
}

func TestQuotaDenialProducesNoErrorLog(t *testing.T) {
	var buf bytes.Buffer
	InitWith(&buf, slog.LevelDebug, "json")
	if strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatal("unexpected ERROR log on empty buffer")
	}
}

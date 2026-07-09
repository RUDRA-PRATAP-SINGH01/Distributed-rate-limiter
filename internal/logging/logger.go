package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
)

var initOnce sync.Once

// Init configures the process-wide slog logger from LOG_LEVEL and LOG_FORMAT.
func Init() {
	initOnce.Do(func() {
		level, invalid := resolveLevel(os.Getenv("LOG_LEVEL"))
		handler := newHandler(os.Stdout, os.Getenv("LOG_FORMAT"), level)
		logger := slog.New(handler)
		slog.SetDefault(logger)
		if invalid {
			logger.Warn("invalid LOG_LEVEL, defaulting to info", "value", os.Getenv("LOG_LEVEL"))
		}
	})
}

// InitWith configures logging for tests.
func InitWith(w io.Writer, level slog.Level, format string) {
	initOnce.Do(func() {})
	handler := newHandler(w, format, level)
	slog.SetDefault(slog.New(handler))
}

func resolveLevel(raw string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, false
	case "debug":
		return slog.LevelDebug, false
	case "warn", "warning":
		return slog.LevelWarn, false
	case "error":
		return slog.LevelError, false
	default:
		return slog.LevelInfo, true
	}
}

func newHandler(w io.Writer, format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return slog.NewJSONHandler(w, opts)
	case "text":
		return slog.NewTextHandler(w, opts)
	default:
		return slog.NewJSONHandler(w, opts)
	}
}

// CorrelationAttrs returns request/trace fields present in ctx. Used by tests.
func CorrelationAttrs(ctx context.Context) []any {
	return attrsFromContext(ctx)
}

func attrsFromContext(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}
	var attrs []any
	if reqID := telemetry.RequestIDFromContext(ctx); reqID != "" {
		attrs = append(attrs, "request_id", reqID)
	}
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() {
		if sc.HasTraceID() {
			attrs = append(attrs, "trace_id", sc.TraceID().String())
		}
		if sc.HasSpanID() {
			attrs = append(attrs, "span_id", sc.SpanID().String())
		}
	}
	return attrs
}

func emit(ctx context.Context, level slog.Level, msg string, args ...any) {
	all := append(attrsFromContext(ctx), args...)
	slog.Default().Log(ctx, level, msg, all...)
}

func Debug(ctx context.Context, msg string, args ...any) { emit(ctx, slog.LevelDebug, msg, args...) }
func Info(ctx context.Context, msg string, args ...any)  { emit(ctx, slog.LevelInfo, msg, args...) }
func Warn(ctx context.Context, msg string, args ...any)  { emit(ctx, slog.LevelWarn, msg, args...) }
func Error(ctx context.Context, msg string, args ...any) { emit(ctx, slog.LevelError, msg, args...) }

// Fatal logs at error severity and terminates the process.
func Fatal(msg string, args ...any) {
	slog.Default().Error(msg, args...)
	os.Exit(1)
}

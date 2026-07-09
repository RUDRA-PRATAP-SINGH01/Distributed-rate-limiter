package telemetry

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type requestIDKey struct{}

const RequestIDHeader = "X-Request-ID"
const TraceIDHeader = "X-Trace-ID"
const SpanIDHeader = "X-Span-ID"

// WithRequestID attaches a correlation ID to ctx for logging and downstream use.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestIDFromContext returns the correlation ID for this request.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// SkipHealthMetrics avoids trace noise from scrapers and probes.
func SkipHealthMetrics(r *http.Request) bool {
	switch r.URL.Path {
	case "/health", "/metrics":
		return false
	default:
		return true
	}
}

// WrapHandler adds request IDs, W3C trace propagation, and response trace headers.
func WrapHandler(handler http.Handler, serviceName string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		sc := span.SpanContext()
		if reqID := RequestIDFromContext(r.Context()); reqID != "" {
			w.Header().Set(RequestIDHeader, reqID)
		}
		if sc.HasTraceID() {
			w.Header().Set(TraceIDHeader, sc.TraceID().String())
			w.Header().Set(SpanIDHeader, sc.SpanID().String())
		}
		handler.ServeHTTP(w, r)
	})

	traced := otelhttp.NewHandler(inner, serviceName,
		otelhttp.WithFilter(SkipHealthMetrics),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)

	return requestIDMiddleware(traced)
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(RequestIDHeader)
		if reqID == "" {
			reqID = uuid.New().String()
		}
		ctx := context.WithValue(r.Context(), requestIDKey{}, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NewHTTPTransport wraps the default transport with W3C trace propagation for outbound calls.
func NewHTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &clientTransport{base: base}
}

// StartSpan begins a child span in the service tracer.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("").Start(ctx, name, trace.WithAttributes(attrs...))
}

// RecordError marks a span failed and records the error.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// SetHTTPStatus records the HTTP status on a span.
func SetHTTPStatus(span trace.Span, code int) {
	span.SetAttributes(attribute.Int("http.status_code", code))
	if code >= 500 {
		span.SetStatus(codes.Error, http.StatusText(code))
	}
}

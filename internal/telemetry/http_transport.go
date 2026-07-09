package telemetry

import (
	"io"
	"net/http"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// clientTransport instruments outbound HTTP with W3C propagation. Unlike stock
// otelhttp client spans, HTTP 429 is treated as an expected quota outcome, not
// a span error — matching limiter.check and sidecar.rate_limit_check semantics.
type clientTransport struct {
	base http.RoundTripper
}

func (t *clientTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	propagators := otel.GetTextMapPropagator()
	tracer := otel.Tracer("")

	ctx, span := tracer.Start(
		r.Context(),
		"HTTP "+r.Method,
		trace.WithSpanKind(trace.SpanKindClient),
	)
	propagators.Inject(ctx, propagation.HeaderCarrier(r.Header))

	resp, err := t.base.RoundTrip(r.Clone(ctx))
	if err != nil {
		RecordError(span, err)
		span.End()
		return resp, err
	}

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	setClientHTTPStatus(span, resp.StatusCode)

	if resp.Body == nil || resp.Body == http.NoBody {
		span.End()
		return resp, nil
	}
	resp.Body = &spanEndingBody{ReadCloser: resp.Body, span: span}
	return resp, nil
}

// setClientHTTPStatus applies outbound HTTP span status. Quota denials (429) stay
// unset; infrastructure failures (5xx) are errors.
func setClientHTTPStatus(span trace.Span, code int) {
	if code == http.StatusTooManyRequests {
		return
	}
	if code >= 500 {
		span.SetStatus(codes.Error, http.StatusText(code))
	}
}

type spanEndingBody struct {
	io.ReadCloser
	span  trace.Span
	mu    sync.Mutex
	ended bool
}

func (b *spanEndingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.end()
	}
	return n, err
}

func (b *spanEndingBody) Close() error {
	b.end()
	if b.ReadCloser != nil {
		return b.ReadCloser.Close()
	}
	return nil
}

func (b *spanEndingBody) end() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ended {
		return
	}
	b.ended = true
	b.span.End()
}

package routing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

const (
	HeaderGatewayID     = "X-Gateway-ID"
	HeaderGatewayScore  = "X-Gateway-Score"
	HeaderGatewayFailover = "X-Gateway-Failover"
)

// Router performs intelligent gateway selection with failover.
type Router struct {
	store    *RedisStore
	selector *Selector
	client   *http.Client
	cfg      Config
}

func NewRouter(store *RedisStore, client *http.Client, cfg Config) *Router {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Router{
		store:    store,
		selector: NewSelector(cfg),
		client:   client,
		cfg:      cfg,
	}
}

// Seed registers gateways from static config on startup.
func (r *Router) Seed(ctx context.Context, gateways []Gateway) error {
	for _, gw := range gateways {
		if err := r.store.RegisterGateway(ctx, gw); err != nil {
			return err
		}
	}
	return nil
}

// StartHealthProbes runs background /health checks on all gateways.
func (r *Router) StartHealthProbes(ctx context.Context) {
	if r.cfg.ProbeIntervalSec <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Duration(r.cfg.ProbeIntervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.probeAll(context.Background())
			}
		}
	}()
}

func (r *Router) probeAll(ctx context.Context) {
	states, err := r.store.ListGateways(ctx)
	if err != nil {
		log.Printf("[routing] probe list error: %v", err)
		return
	}
	for _, st := range states {
		start := time.Now()
		probeURL := strings.TrimRight(st.URL, "/") + "/health"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			continue
		}
		resp, err := r.client.Do(req)
		latency := time.Since(start)
		success := err == nil && resp != nil && resp.StatusCode < 500
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		_ = r.store.UpdateHealthProbe(ctx, st.ID, success, latency)
	}
}

// Forward proxies the request to the best gateway with automatic failover.
func (r *Router) Forward(ctx context.Context, w http.ResponseWriter, req *http.Request, body []byte) error {
	ctx, span := telemetry.StartSpan(ctx, "sidecar.intelligent_route")
	defer span.End()

	states, err := r.store.ListGateways(ctx)
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	if len(states) == 0 {
		return fmt.Errorf("no gateways configured")
	}

	primary, ok := r.selector.PickPrimary(states)
	if !ok {
		return fmt.Errorf("no healthy gateways available")
	}

	metrics.RecordRoutingDecision(primary.State.ID, false)

	tried := []ScoredGateway{primary}
	tried = append(tried, r.selector.FailoverOrder(states, primary.State.ID)...)

	var lastErr error
	failover := false
	for i, candidate := range tried {
		if i > 0 {
			failover = true
			metrics.RecordRoutingFailover(candidate.State.ID)
		}

		start := time.Now()
		resp, err := r.execute(ctx, req, body, candidate.State)
		latency := time.Since(start)
		success := err == nil && resp != nil && resp.StatusCode < 500

		_ = r.store.RecordOutcome(ctx, Outcome{
			GatewayID: candidate.State.ID,
			Latency:   latency,
			Success:   success,
		})

		if !success {
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil {
				lastErr = err
			} else {
				lastErr = fmt.Errorf("gateway %s returned %d", candidate.State.ID, resp.StatusCode)
			}
			continue
		}

		w.Header().Set(HeaderGatewayID, candidate.State.ID)
		w.Header().Set(HeaderGatewayScore, fmt.Sprintf("%.2f", candidate.Score))
		if failover {
			w.Header().Set(HeaderGatewayFailover, "true")
		}
		span.SetAttributes(
			attribute.String("gateway.id", candidate.State.ID),
			attribute.Float64("gateway.score", candidate.Score),
			attribute.Bool("gateway.failover", failover),
		)
		copyResponse(w, resp)
		resp.Body.Close()
		return nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all gateways failed")
	}
	telemetry.RecordError(span, lastErr)
	return lastErr
}

func (r *Router) execute(ctx context.Context, original *http.Request, body []byte, gw GatewayState) (*http.Response, error) {
	target, err := url.Parse(gw.URL)
	if err != nil {
		return nil, err
	}
	path := original.URL.Path
	if path == "" {
		path = "/"
	}
	fullURL := strings.TrimRight(gw.URL, "/") + path
	if original.URL.RawQuery != "" {
		fullURL += "?" + original.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, original.Method, fullURL, nil)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	} else if original.Body != nil {
		req.Body = original.Body
	}
	req.Header = original.Header.Clone()
	req.Host = target.Host

	return r.client.Do(req)
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// Store exposes the Redis store for admin APIs.
func (r *Router) Store() *RedisStore {
	return r.store
}

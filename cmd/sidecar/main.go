// Sidecar proxy: sits in front of the application and enforces limits before upstream work runs.
//
// Architecture pattern (service mesh lite):
//
//	Client -> Sidecar -> [central limiter + Redis] -> upstream backend
//
// Denials can be cached briefly to protect Redis under abuse; allowances always re-check
// so token counts stay accurate. singleflight collapses concurrent misses for the same key.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/idempotency"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/routing"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/singleflight"
)

type CacheEntry struct {
	Allowed    bool
	Remaining  int
	Limit      int
	RetryAfter string
	ExpiresAt  time.Time
}

type limitResult struct {
	allowed    bool
	remaining  int
	limit      int
	retryAfter string
}

type Sidecar struct {
	upstreamURL      string
	limiterURL       string
	internalAPIKey   string
	metricsAPIKey    string
	allowQueryUserID bool
	allowedPaths     []string
	cache            sync.Map
	limitFlight      singleflight.Group
	ttl              time.Duration
	failOpen         bool
	defaultLimit     int
	useHierarchical  bool
	httpClient       *http.Client
	proxy            *httputil.ReverseProxy // created once — reusing per request would leak goroutines
	idempotency      idempotency.Store
	idempotencyCfg   idempotency.Config
	router           *routing.Router
	limiterCircuit   *circuitbreaker.Breaker
}

func NewSidecar(
	upstream, limiter, internalAPIKey, metricsAPIKey string,
	ttl time.Duration,
	failOpen bool,
	defaultLimit int,
	useHierarchical, allowQueryUserID bool,
	allowedPaths []string,
) *Sidecar {
	target, err := url.Parse(upstream)
	if err != nil {
		logging.Fatal("invalid UPSTREAM_URL", "error", err)
	}
	return &Sidecar{
		upstreamURL:      upstream,
		limiterURL:       limiter,
		internalAPIKey:   internalAPIKey,
		metricsAPIKey:    metricsAPIKey,
		allowQueryUserID: allowQueryUserID,
		allowedPaths:     allowedPaths,
		ttl:              ttl,
		failOpen:         failOpen,
		defaultLimit:     defaultLimit,
		useHierarchical:  useHierarchical,
		httpClient:       newLimiterHTTPClient(loadLimiterHTTPConfigFromEnv()),
		proxy: func() *httputil.ReverseProxy {
			p := httputil.NewSingleHostReverseProxy(target)
			p.Transport = telemetry.NewHTTPTransport(http.DefaultTransport)
			return p
		}(),
	}
}

func (s *Sidecar) StartCacheSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				s.cache.Range(func(key, value interface{}) bool {
					entry, ok := value.(CacheEntry)
					if ok && now.After(entry.ExpiresAt) {
						s.cache.Delete(key)
					}
					return true
				})
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *Sidecar) SetIdempotency(store idempotency.Store, cfg idempotency.Config) {
	s.idempotency = store
	s.idempotencyCfg = cfg
}

func (s *Sidecar) SetRouter(router *routing.Router) {
	s.router = router
}

func (s *Sidecar) SetLimiterCircuit(b *circuitbreaker.Breaker) {
	s.limiterCircuit = b
}

func (s *Sidecar) tenantID(r *http.Request) string {
	if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" {
		return tenantID
	}
	if tenantID := r.URL.Query().Get("tenant_id"); tenantID != "" {
		return tenantID
	}
	return "default"
}

// cacheKey scopes entries by tenant + user + path in hierarchical mode so
// /api/login and /api/search maintain separate endpoint buckets.
func (s *Sidecar) cacheKey(r *http.Request, userID string) string {
	if !s.useHierarchical {
		return userID
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = r.URL.Query().Get("tenant_id")
	}
	if tenantID == "" {
		tenantID = "default"
	}
	return tenantID + "|" + userID + "|" + r.URL.Path
}

func (s *Sidecar) pathAllowed(path string) bool {
	if len(s.allowedPaths) == 0 {
		return true
	}
	for _, prefix := range s.allowedPaths {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (s *Sidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
		http.NotFound(w, r)
		return
	}

	if !s.pathAllowed(r.URL.Path) {
		http.Error(w, "path not allowed", http.StatusNotFound)
		return
	}

	userID, err := identity.ResolveUserID(r, s.allowQueryUserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if s.idempotency != nil && idemKey != "" && idempotency.IsMutatingMethod(r.Method) {
		s.serveIdempotent(w, r, userID, idemKey)
		return
	}

	s.serveNormal(w, r, userID)
}

func (s *Sidecar) serveIdempotent(w http.ResponseWriter, r *http.Request, userID, idemKey string) {
	ctx := r.Context()
	ctx, span := telemetry.StartSpan(ctx, "sidecar.idempotency")
	defer span.End()
	r = r.WithContext(ctx)
	start := time.Now()

	if err := idempotency.ValidateKey(idemKey); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := idempotency.ReadBody(r, s.idempotencyCfg.MaxBodyBytes)
	if err != nil {
		if errors.Is(err, idempotency.ErrBodyTooLarge) {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	scope := idempotency.BuildScope(s.tenantID(r), userID)
	reqHash := idempotency.Fingerprint(r.Method, r.URL.Path, r.URL.RawQuery, body)

	claim, err := s.idempotency.Claim(ctx, scope, idemKey, reqHash)
	if err != nil {
		logging.Error(ctx, "idempotency claim failed",
			"component", "sidecar",
			"operation", "idempotency_claim",
			"key_present", true,
			"error", err,
		)
		if s.idempotencyCfg.FailOpen {
			logging.Warn(ctx, "idempotency fail-open proceeding without dedup",
				"component", "sidecar",
				"operation", "idempotency_claim",
				"fail_open", true,
			)
			s.serveNormal(w, r, userID)
			return
		}
		http.Error(w, "Idempotency store unavailable", http.StatusServiceUnavailable)
		return
	}

	switch claim.Result {
	case idempotency.ResultReplay:
		logging.Debug(ctx, "idempotency replay served",
			"component", "sidecar",
			"operation", "idempotency_replay",
			"key_present", true,
		)
		idempotency.WriteClaimResponse(w, claim)
		return
	case idempotency.ResultInProgress, idempotency.ResultHashMismatch:
		idempotency.WriteClaimResponse(w, claim)
		return
	case idempotency.ResultClaimed:
		logging.Debug(ctx, "idempotency key claimed",
			"component", "sidecar",
			"operation", "idempotency_claim",
			"key_present", true,
		)
	default:
		http.Error(w, "idempotency error", http.StatusInternalServerError)
		return
	}

	result, err := s.checkRateLimit(ctx, r, userID, false)
	if err != nil {
		metrics.RecordDependencyFailure("central_limiter", "sidecar_idempotent", time.Since(start).Seconds())
		logging.Error(ctx, "rate limit check failed",
			"component", "sidecar",
			"operation", "rate_limit_check",
			"error", err,
		)
		if s.failOpen {
			s.forwardIdempotent(w, r, scope, idemKey, claim.FenceToken)
			return
		}
		_ = s.failIdempotent(r.Context(), scope, idemKey, claim.FenceToken, http.StatusServiceUnavailable,
			map[string]string{"Content-Type": "application/json"},
			[]byte(`{"error":"Rate limiter unavailable"}`))
		http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
		return
	}

	if !result.allowed {
		body := []byte(`{"error":"Too many requests"}`)
		_ = s.completeIdempotent(r.Context(), scope, idemKey, claim.FenceToken, http.StatusTooManyRequests, map[string]string{"Content-Type": "application/json"}, body)
		w.Header().Set("X-Idempotency-Status", "created")
		s.writeDenial(w, result.limit, result.remaining, result.retryAfter)
		return
	}

	idempotency.SetCreatedHeader(w)
	s.writeRateLimitHeaders(w, result.limit, result.remaining)
	s.forwardIdempotent(w, r, scope, idemKey, claim.FenceToken)
}

func (s *Sidecar) forwardIdempotent(w http.ResponseWriter, r *http.Request, scope, idemKey, fenceToken string) {
	ctx, span := telemetry.StartSpan(r.Context(), "sidecar.upstream_proxy",
		attribute.String("http.path", r.URL.Path),
	)
	defer span.End()
	r = r.WithContext(ctx)

	capturer := idempotency.NewResponseCapturer(w)
	if s.router != nil {
		body, _ := readRequestBody(r)
		if err := s.router.Forward(r.Context(), capturer, r, body); err != nil {
			logging.Error(r.Context(), "routing forward failed",
				"component", "sidecar",
				"operation", "upstream_proxy",
				"error", err,
			)
			_ = s.failIdempotent(r.Context(), scope, idemKey, fenceToken, http.StatusServiceUnavailable,
				map[string]string{"Content-Type": "application/json"},
				[]byte(`{"error":"all gateways unavailable"}`))
			http.Error(w, "all gateways unavailable", http.StatusServiceUnavailable)
			return
		}
	} else {
		target, _ := url.Parse(s.upstreamURL)
		r.Host = target.Host
		s.proxy.ServeHTTP(capturer, r)
	}
	captured := capturer.Commit()
	_ = s.completeIdempotent(r.Context(), scope, idemKey, fenceToken, captured.StatusCode, captured.Headers, captured.Body)
}

func (s *Sidecar) completeIdempotent(ctx context.Context, scope, key, fenceToken string, status int, headers map[string]string, body []byte) error {
	return s.idempotency.Complete(ctx, idempotency.CompleteRequest{
		Scope:      scope,
		Key:        key,
		FenceToken: fenceToken,
		HTTPStatus: status,
		Headers:    headers,
		Body:       body,
	})
}

func (s *Sidecar) failIdempotent(ctx context.Context, scope, key, fenceToken string, status int, headers map[string]string, body []byte) error {
	return s.idempotency.Fail(ctx, idempotency.FailRequest{
		Scope:      scope,
		Key:        key,
		FenceToken: fenceToken,
		HTTPStatus: status,
		Headers:    headers,
		Body:       body,
	})
}

func (s *Sidecar) serveNormal(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	ctx, span := telemetry.StartSpan(ctx, "sidecar.proxy",
		attribute.String("http.path", r.URL.Path),
	)
	defer span.End()
	r = r.WithContext(ctx)
	start := time.Now()

	cacheKey := s.cacheKey(r, userID)
	logging.Debug(ctx, "processing proxied request",
		"component", "sidecar",
		"operation", "proxy",
		"http.path", r.URL.Path,
		"hierarchical", s.useHierarchical,
	)

	if val, ok := s.cache.Load(cacheKey); ok {
		entry := val.(CacheEntry)
		if time.Now().Before(entry.ExpiresAt) {
			if !entry.Allowed {
				logging.Debug(ctx, "serving cached denial",
					"component", "sidecar",
					"operation", "cache",
					"cache_hit", true,
				)
				metrics.RecordCacheHit()
				s.writeDenial(w, entry.Limit, entry.Remaining, entry.RetryAfter)
				return
			}
			logging.Debug(ctx, "allowed cache entry ignored",
				"component", "sidecar",
				"operation", "cache",
			)
		} else {
			s.cache.Delete(cacheKey)
		}
	} else {
		logging.Debug(ctx, "cache miss",
			"component", "sidecar",
			"operation", "cache",
		)
	}

	metrics.RecordCacheMiss()

	// singleflight: 100 concurrent requests for the same user share one limiter round-trip.
	resultAny, err, _ := s.limitFlight.Do(cacheKey, func() (interface{}, error) {
		return s.checkRateLimit(ctx, r, userID, false)
	})
	if err != nil {
		elapsed := time.Since(start).Seconds()
		metrics.RecordDependencyFailure("central_limiter", "sidecar", elapsed)
		logging.Error(ctx, "rate limit check failed",
			"component", "sidecar",
			"operation", "rate_limit_check",
			"error", err,
		)
		if s.failOpen {
			logging.Warn(ctx, "fail-open forwarding despite limiter error",
				"component", "sidecar",
				"operation", "rate_limit_check",
				"fail_open", true,
			)
			s.forwardRequest(w, r)
			return
		}
		http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
		return
	}

	result := resultAny.(limitResult)
	s.cache.Store(cacheKey, CacheEntry{
		Allowed:    result.allowed,
		Remaining:  result.remaining,
		Limit:      result.limit,
		RetryAfter: result.retryAfter,
		ExpiresAt:  time.Now().Add(s.ttl),
	})

	if !result.allowed {
		s.writeDenial(w, result.limit, result.remaining, result.retryAfter)
		return
	}

	s.writeRateLimitHeaders(w, result.limit, result.remaining)
	s.forwardRequest(w, r)
}

func (s *Sidecar) writeRateLimitHeaders(w http.ResponseWriter, limit, remaining int) {
	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}

func (s *Sidecar) writeDenial(w http.ResponseWriter, limit, remaining int, retryAfter string) {
	s.writeRateLimitHeaders(w, limit, remaining)
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	http.Error(w, "Too many requests", http.StatusTooManyRequests)
}

func (s *Sidecar) checkRateLimit(ctx context.Context, r *http.Request, userID string, idempotentReplay bool) (limitResult, error) {
	ctx, span := telemetry.StartSpan(ctx, "sidecar.rate_limit_check",
		attribute.Bool("hierarchical", s.useHierarchical),
	)
	defer span.End()

	start := time.Now()
	var (
		callErr    error
		statusCode int
	)
	defer func() {
		if s.limiterCircuit == nil {
			return
		}
		input := circuitbreaker.ClassifyHTTP(callErr, statusCode, time.Since(start), s.limiterCircuit.Config().LatencyThresholdMs)
		_ = s.limiterCircuit.Record(ctx, circuitbreaker.TargetCentralLimiter, input)
	}()

	if s.limiterCircuit != nil {
		allow, err := s.limiterCircuit.Allow(ctx, circuitbreaker.TargetCentralLimiter)
		if err != nil {
			if !s.limiterCircuit.Config().FailOpen {
				callErr = err
				telemetry.RecordError(span, err)
				return limitResult{}, fmt.Errorf("circuit breaker unavailable: %w", err)
			}
		} else if !allow.Allowed {
			err := fmt.Errorf("central limiter circuit %s", allow.State)
			telemetry.RecordError(span, err)
			return limitResult{}, err
		}
	}

	var req *http.Request
	var err error

	replayParam := ""
	if idempotentReplay {
		replayParam = "&idempotent_replay=true"
	}

	if s.useHierarchical {
		endpoint := r.URL.Path
		if endpoint == "" {
			endpoint = "/"
		}
		checkURL := fmt.Sprintf(
			"%s/check_hierarchical?endpoint=%s%s",
			s.limiterURL,
			url.QueryEscape(endpoint),
			replayParam,
		)
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	} else {
		checkURL := fmt.Sprintf("%s/check", s.limiterURL)
		if idempotentReplay {
			checkURL += "?idempotent_replay=true"
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	}
	if err != nil {
		callErr = err
		return limitResult{}, err
	}

	// Identity travels in headers, not query strings — harder to spoof from the browser.
	req.Header.Set(identity.UserIDHeader, userID)
	if s.internalAPIKey != "" {
		req.Header.Set(auth.InternalAPIKeyHeader, s.internalAPIKey)
	}
	if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	} else if tenantID := r.URL.Query().Get("tenant_id"); tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		callErr = err
		telemetry.RecordError(span, err)
		return limitResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	statusCode = resp.StatusCode
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	// A malformed header keeps the local default rather than a partially
	// scanned value, so a limiter bug cannot silently widen the quota shown
	// to clients.
	limit := s.defaultLimit
	if val := resp.Header.Get("X-RateLimit-Limit"); val != "" {
		if parsed, convErr := strconv.Atoi(val); convErr == nil {
			limit = parsed
		}
	}
	remaining := 0
	if val := resp.Header.Get("X-RateLimit-Remaining"); val != "" {
		if parsed, convErr := strconv.Atoi(val); convErr == nil {
			remaining = parsed
		}
	}
	retryAfter := resp.Header.Get("Retry-After")

	if resp.StatusCode == http.StatusTooManyRequests {
		span.SetAttributes(attribute.Bool("rate_limit.allowed", false))
		telemetry.SetHTTPStatus(span, resp.StatusCode)
		return limitResult{allowed: false, remaining: remaining, limit: limit, retryAfter: retryAfter}, nil
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("limiter returned %d", resp.StatusCode)
		callErr = err
		telemetry.RecordError(span, err)
		return limitResult{}, err
	}

	var body struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return limitResult{}, err
	}
	span.SetAttributes(attribute.Bool("rate_limit.allowed", body.Allowed))
	return limitResult{allowed: body.Allowed, remaining: remaining, limit: limit, retryAfter: retryAfter}, nil
}

func (s *Sidecar) forwardRequest(w http.ResponseWriter, r *http.Request) {
	ctx, span := telemetry.StartSpan(r.Context(), "sidecar.upstream_proxy",
		attribute.String("http.path", r.URL.Path),
	)
	defer span.End()
	r = r.WithContext(ctx)

	if s.router != nil {
		body, _ := readRequestBody(r)
		if err := s.router.Forward(ctx, w, r, body); err != nil {
			logging.Error(ctx, "routing forward failed",
				"component", "sidecar",
				"operation", "upstream_proxy",
				"error", err,
			)
			telemetry.RecordError(span, err)
			http.Error(w, "all gateways unavailable", http.StatusServiceUnavailable)
		}
		return
	}

	target, _ := url.Parse(s.upstreamURL)
	r.Host = target.Host
	s.proxy.ServeHTTP(w, r)
}

func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func parseAllowedPaths(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sidecarMetricsAuthKey(internalKey, metricsKey string) string {
	if metricsKey != "" {
		return metricsKey
	}
	return internalKey
}

func main() {
	logging.Init()

	upstream := os.Getenv("UPSTREAM_URL")
	limiter := os.Getenv("RATE_LIMITER_URL")
	if limiter == "" {
		logging.Fatal("RATE_LIMITER_URL must be set")
	}
	if upstream == "" && os.Getenv("ENABLE_ROUTING") != "true" {
		logging.Fatal("UPSTREAM_URL must be set when ENABLE_ROUTING is false")
	}

	otelCfg := telemetry.LoadConfigFromEnv("rate-sidecar")
	otelShutdown, err := telemetry.Init(context.Background(), otelCfg)
	if err != nil {
		logging.Fatal("OpenTelemetry init failed", "error", err)
	}

	ttl := 30 * time.Millisecond
	if raw := os.Getenv("CACHE_TTL_MS"); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			ttl = time.Duration(ms) * time.Millisecond
		}
	}

	failOpen := os.Getenv("FAIL_OPEN") == "true"
	if failOpen {
		logging.Warn(context.Background(), "FAIL_OPEN enabled — sidecar forwards traffic when limiter or Redis is down",
			"component", "sidecar",
			"fail_open", true,
		)
	}

	useHierarchical := os.Getenv("USE_HIERARCHICAL") == "true"
	allowQueryUserID := os.Getenv("ALLOW_QUERY_USER_ID") == "true"
	internalAPIKey := os.Getenv("INTERNAL_API_KEY")
	metricsRequireAuth := os.Getenv("METRICS_REQUIRE_AUTH") == "true"
	metricsAPIKey := sidecarMetricsAuthKey(internalAPIKey, os.Getenv("METRICS_API_KEY"))
	metricsKey := ""
	if metricsRequireAuth {
		metricsKey = metricsAPIKey
	}
	allowedPaths := parseAllowedPaths(os.Getenv("ALLOWED_PATHS"))

	if len(allowedPaths) == 0 {
		logging.Warn(context.Background(), "ALLOWED_PATHS is not set — all paths are proxied",
			"component", "sidecar",
			"security_dev_mode", true,
		)
	}

	// Silently falling back on a typo would run the fleet at the wrong quota,
	// so a malformed RATE_LIMIT stops startup instead.
	defaultLimit := 10
	if l := os.Getenv("RATE_LIMIT"); l != "" {
		parsed, err := strconv.Atoi(l)
		if err != nil || parsed <= 0 {
			logging.Fatal("RATE_LIMIT must be a positive integer")
		}
		defaultLimit = parsed
	}

	tlsCert := os.Getenv("TLS_CERT_FILE")
	tlsKey := os.Getenv("TLS_KEY_FILE")
	if (tlsCert != "" || tlsKey != "") && (tlsCert == "" || tlsKey == "") {
		logging.Fatal("TLS_CERT_FILE and TLS_KEY_FILE must both be set to enable TLS")
	}

	sidecar := NewSidecar(
		upstream, limiter, internalAPIKey, metricsAPIKey,
		ttl, failOpen, defaultLimit, useHierarchical, allowQueryUserID, allowedPaths,
	)

	var sharedRdb redis.UniversalClient
	var redisCfg redisclient.Config
	var probeCancel context.CancelFunc
	needsRedis := os.Getenv("ENABLE_IDEMPOTENCY") == "true" || os.Getenv("ENABLE_ROUTING") == "true"
	if needsRedis {
		redisCfg = redisclient.LoadConfigFromEnv()
		sharedRdb = connectSidecarRedis(otelCfg, redisCfg)
	}

	if os.Getenv("ENABLE_IDEMPOTENCY") == "true" {
		rdb := sharedRdb
		idemCfg := idempotency.DefaultConfig()
		idemCfg.FailOpen = os.Getenv("IDEMPOTENCY_FAIL_OPEN") == "true"
		if raw := os.Getenv("IDEMPOTENCY_LOCK_TTL_MS"); raw != "" {
			if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
				idemCfg.LockTTL = ms
			}
		}
		if raw := os.Getenv("IDEMPOTENCY_COMPLETED_TTL_MS"); raw != "" {
			if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
				idemCfg.CompletedTTL = ms
			}
		}
		if raw := os.Getenv("IDEMPOTENCY_MAX_BODY_BYTES"); raw != "" {
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
				idemCfg.MaxBodyBytes = n
			}
		}
		sidecar.SetIdempotency(idempotency.NewRedisStore(rdb, idemCfg), idemCfg)
		if os.Getenv("ENABLE_CIRCUIT_BREAKER") != "false" {
			cbCfg := circuitbreaker.LoadConfigFromEnv()
			sidecar.SetLimiterCircuit(circuitbreaker.NewBreaker(circuitbreaker.NewRedisStore(rdb, cbCfg)))
		}
		logging.Info(context.Background(), "Idempotency layer enabled",
			"component", "sidecar",
			"lock_ttl_ms", idemCfg.LockTTL,
			"completed_ttl_ms", idemCfg.CompletedTTL,
		)
	}

	if os.Getenv("ENABLE_ROUTING") == "true" {
		rdb := sharedRdb
		routeCfg := routing.LoadConfigFromEnv()
		cbCfg := circuitbreaker.LoadConfigFromEnv()
		breaker := circuitbreaker.NewBreaker(circuitbreaker.NewRedisStore(rdb, cbCfg))
		store := routing.NewRedisStore(rdb, routeCfg)
		store.SetBreaker(breaker)
		router := routing.NewRouter(store, sidecar.httpClient, routeCfg, breaker)
		sidecar.SetLimiterCircuit(breaker)
		gateways := routing.GatewaysFromEnv()
		if len(gateways) == 0 {
			logging.Fatal("ENABLE_ROUTING=true requires GATEWAYS env")
		}
		if err := router.Seed(context.Background(), gateways); err != nil {
			logging.Fatal("gateway seed failed", "error", err)
		}
		probeCtx, cancel := context.WithCancel(context.Background())
		probeCancel = cancel
		router.StartHealthProbes(probeCtx)
		sidecar.SetRouter(router)
		logging.Info(context.Background(), "Intelligent routing enabled",
			"component", "sidecar",
			"gateway_count", len(gateways),
		)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", auth.RequireAPIKey(metricsKey, promhttp.Handler().ServeHTTP))
	mux.HandleFunc("/health", newSidecarHealthHandler(sidecarHealthDeps{
		needsRedis:  needsRedis,
		limiterURL:  limiter,
		httpClient:  sidecar.httpClient,
		redisClient: sharedRdb,
		redisCfg:    redisCfg,
	}))
	mux.Handle("/", sidecar)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}

	handler := telemetry.WrapHandler(mux, otelCfg.ServiceName)

	mode := "simple /check"
	if useHierarchical {
		mode = "hierarchical /check_hierarchical"
	}
	logging.Info(context.Background(), "Sidecar starting",
		"component", "sidecar",
		"port", port,
		"upstream", upstream,
		"limiter_mode", mode,
	)

	sweeperCtx, sweeperCancel := context.WithCancel(context.Background())
	sidecar.StartCacheSweeper(sweeperCtx, 10*time.Second)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		var err error
		if tlsCert != "" {
			err = srv.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logging.Fatal("listen failed", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logging.Info(context.Background(), "Shutting down sidecar", "component", "sidecar")

	sweeperCancel()
	if probeCancel != nil {
		probeCancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logging.Fatal("Sidecar forced to shutdown", "error", err)
	}
	if err := otelShutdown(shutdownCtx); err != nil {
		logging.Error(shutdownCtx, "OpenTelemetry shutdown error", "component", "sidecar", "error", err)
	}
	if sharedRdb != nil {
		if err := redisclient.Close(sharedRdb); err != nil {
			logging.Error(shutdownCtx, "Redis close error", "component", "sidecar", "error", err)
		} else {
			logging.Info(shutdownCtx, "Redis client closed", "component", "sidecar")
		}
	}
	logging.Info(context.Background(), "Sidecar exited", "component", "sidecar")
}

func connectSidecarRedis(otelCfg telemetry.Config, cfg redisclient.Config) redis.UniversalClient {
	if err := cfg.Validate(); err != nil {
		logging.Fatal("Invalid Redis configuration", "error", err)
	}
	if cfg.Mode == redisclient.ModeStandalone && cfg.Addr == "" {
		logging.Fatal("REDIS_ADDR is required when ENABLE_IDEMPOTENCY or ENABLE_ROUTING is true")
	}
	if cfg.Mode == redisclient.ModeSentinel && len(cfg.SentinelAddrs) == 0 {
		logging.Fatal("REDIS_SENTINEL_ADDRS is required when REDIS_MODE=sentinel")
	}
	rdb := redisclient.New(cfg)
	if err := redisclient.Ping(rdb); err != nil {
		logging.Fatal("Redis unreachable", "redis", redisclient.Describe(cfg), "error", err)
	}
	if otelCfg.Enabled {
		if err := telemetry.InstrumentRedis(rdb); err != nil {
			logging.Fatal("Redis OpenTelemetry instrumentation failed", "error", err)
		}
	}
	logging.Info(context.Background(), "Sidecar Redis connected", "component", "sidecar", "redis", redisclient.Describe(cfg))
	return rdb
}

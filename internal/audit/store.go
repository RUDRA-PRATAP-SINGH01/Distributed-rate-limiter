package audit

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/append.lua
var appendLua string

// Store persists audit events in Redis with searchable indexes.
type Store struct {
	rdb    redis.UniversalClient
	cfg    Config
	script *redis.Script
}

func NewStore(rdb redis.UniversalClient, cfg Config) *Store {
	return &Store{
		rdb:    rdb,
		cfg:    cfg,
		script: redis.NewScript(appendLua),
	}
}

func eventKey(id string) string       { return fmt.Sprintf("audit:event:%s", id) }
func tsIndexKey() string              { return "audit:idx:ts" }
func tenantIndexKey(tenant string) string { return fmt.Sprintf("audit:idx:tenant:%s", tenant) }
func userIndexKey(user string) string     { return fmt.Sprintf("audit:idx:user:%s", user) }
func requestIndexKey(req string) string   { return fmt.Sprintf("audit:idx:req:%s", req) }

// Record appends one audit event (optionally async).
func (s *Store) Record(ctx context.Context, in RecordInput) (Event, error) {
	if !s.cfg.Enabled {
		return Event{}, nil
	}
	if s.cfg.Async {
		go func() {
			_, _ = s.record(context.Background(), in)
		}()
		return Event{Decision: in.Decision, RequestID: in.RequestID}, nil
	}
	return s.record(ctx, in)
}

func (s *Store) record(ctx context.Context, in RecordInput) (Event, error) {
	start := time.Now()
	id := uuid.New().String()
	now := time.Now().UnixMilli()
	tenant := in.TenantID
	if tenant == "" {
		tenant = "default"
	}

	result, err := s.script.Run(ctx, s.rdb,
		[]string{
			eventKey(id),
			tsIndexKey(),
			tenantIndexKey(tenant),
			userIndexKey(in.UserID),
			requestIndexKey(in.RequestID),
		},
		id,
		in.RequestID,
		tenant,
		in.UserID,
		string(in.Decision),
		in.Reason,
		in.Handler,
		strconv.Itoa(in.Remaining),
		now,
		s.cfg.Retention.Milliseconds(),
		s.cfg.MaxEvents,
	).Result()
	metrics.RecordAuditAppend(time.Since(start).Seconds(), err == nil)

	if err != nil {
		metrics.RecordAuditEvent(string(DecisionError), "append_failed")
		return Event{}, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 2 || luaInt(values[0]) != 1 {
		return Event{}, fmt.Errorf("audit append failed")
	}

	ev := Event{
		ID:          id,
		RequestID:   in.RequestID,
		TenantID:    tenant,
		UserID:      in.UserID,
		Decision:    in.Decision,
		Reason:      in.Reason,
		Handler:     in.Handler,
		Remaining:   in.Remaining,
		TimestampMs: now,
	}
	metrics.RecordAuditEvent(string(in.Decision), in.Handler)
	return ev, nil
}

// Get returns one event by ID.
func (s *Store) Get(ctx context.Context, id string) (*Event, error) {
	fields, err := s.rdb.HGetAll(ctx, eventKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return parseEvent(fields), nil
}

// GetByRequestID finds the latest event for a request ID.
func (s *Store) GetByRequestID(ctx context.Context, requestID string) (*Event, error) {
	id, err := s.rdb.Get(ctx, requestIndexKey(requestID)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Search queries audit events with filters.
func (s *Store) Search(ctx context.Context, q Query) ([]Event, error) {
	start := time.Now()
	defer func() { metrics.RecordAuditSearch(time.Since(start).Seconds()) }()

	from, to := q.timeRange(time.Now(), s.cfg.Retention)
	limit := q.normalizedLimit()

	var indexKey string
	switch {
	case q.RequestID != "":
		ev, err := s.GetByRequestID(ctx, q.RequestID)
		if err != nil {
			return nil, err
		}
		if ev == nil {
			return nil, nil
		}
		if q.matches(*ev, from, to) {
			return []Event{*ev}, nil
		}
		return nil, nil
	case q.TenantID != "":
		indexKey = tenantIndexKey(q.TenantID)
	case q.UserID != "":
		indexKey = userIndexKey(q.UserID)
	default:
		indexKey = tsIndexKey()
	}

	ids, err := s.rdb.ZRevRangeByScore(ctx, indexKey, &redis.ZRangeBy{
		Min:   strconv.FormatInt(from, 10),
		Max:   strconv.FormatInt(to, 10),
		Count: int64(limit * 3),
	}).Result()
	if err != nil {
		return nil, err
	}

	out := make([]Event, 0, limit)
	for _, id := range ids {
		ev, err := s.Get(ctx, id)
		if err != nil || ev == nil {
			continue
		}
		if !q.matches(*ev, from, to) {
			continue
		}
		out = append(out, *ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Replay returns a payload suitable for re-evaluating or documenting a past decision.
func (s *Store) Replay(ctx context.Context, id string) (*ReplayPayload, error) {
	ev, err := s.Get(ctx, id)
	if err != nil || ev == nil {
		return nil, err
	}
	hint := "Re-run the same /check or /check_hierarchical call with identical user/tenant headers."
	if ev.Decision == DecisionDenied {
		hint = "Request would be denied again unless quota refilled or overrides changed."
	}
	return &ReplayPayload{Event: *ev, ReplayHint: hint}, nil
}

// Stats returns coarse index sizes for observability.
func (s *Store) Stats(ctx context.Context) (map[string]int64, error) {
	n, err := s.rdb.ZCard(ctx, tsIndexKey()).Result()
	if err != nil {
		return nil, err
	}
	return map[string]int64{"events_indexed": n}, nil
}

func (q Query) matches(ev Event, from, to int64) bool {
	if ev.TimestampMs < from || ev.TimestampMs > to {
		return false
	}
	if q.TenantID != "" && ev.TenantID != q.TenantID {
		return false
	}
	if q.UserID != "" && ev.UserID != q.UserID {
		return false
	}
	if q.Decision != "" && ev.Decision != q.Decision {
		return false
	}
	if q.Handler != "" && ev.Handler != q.Handler {
		return false
	}
	return true
}

func parseEvent(fields map[string]string) *Event {
	ev := &Event{
		ID:        fields["id"],
		RequestID: fields["request_id"],
		TenantID:  fields["tenant_id"],
		UserID:    fields["user_id"],
		Decision:  Decision(fields["decision"]),
		Reason:    fields["reason"],
		Handler:   fields["handler"],
	}
	if v := fields["remaining"]; v != "" {
		ev.Remaining, _ = strconv.Atoi(v)
	}
	if v := fields["timestamp_ms"]; v != "" {
		ev.TimestampMs, _ = strconv.ParseInt(v, 10, 64)
	}
	return ev
}

func luaInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		var parsed int64
		fmt.Sscan(n, &parsed)
		return parsed
	default:
		return 0
	}
}

// PurgeTenant removes tenant index entries older than retention (ops).
func (s *Store) PurgeTenant(ctx context.Context, tenantID string) (int64, error) {
	cutoff := time.Now().Add(-s.cfg.Retention).UnixMilli()
	return s.rdb.ZRemRangeByScore(ctx, tenantIndexKey(tenantID), "0", strconv.FormatInt(cutoff, 10)).Result()
}

func DecisionFromAllowed(allowed bool) Decision {
	if allowed {
		return DecisionAllowed
	}
	return DecisionDenied
}

func ReasonFor(allowed bool, handler string) string {
	if allowed {
		return fmt.Sprintf("%s: quota available", handler)
	}
	return fmt.Sprintf("%s: rate limit exceeded", handler)
}

func NormalizeTenant(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "default"
	}
	return tenant
}

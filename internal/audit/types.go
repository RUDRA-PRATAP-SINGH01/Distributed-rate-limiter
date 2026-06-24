package audit

import "time"

// Decision is the rate-limit outcome recorded in the audit trail.
type Decision string

const (
	DecisionAllowed Decision = "allowed"
	DecisionDenied  Decision = "denied"
	DecisionError   Decision = "error"
)

// Event is one immutable audit record.
type Event struct {
	ID          string   `json:"id"`
	RequestID   string   `json:"request_id"`
	TenantID    string   `json:"tenant_id"`
	UserID      string   `json:"user_id"`
	Decision    Decision `json:"decision"`
	Reason      string   `json:"reason"`
	Handler     string   `json:"handler"`
	Remaining   int      `json:"remaining,omitempty"`
	TimestampMs int64    `json:"timestamp_ms"`
}

// RecordInput is passed when logging a limiter decision.
type RecordInput struct {
	RequestID string
	TenantID  string
	UserID    string
	Decision  Decision
	Reason    string
	Handler   string
	Remaining int
}

// Query filters audit search results.
type Query struct {
	RequestID string
	TenantID  string
	UserID    string
	Decision  Decision
	Handler   string
	FromMs    int64
	ToMs      int64
	Limit     int
}

// ReplayPayload is returned for decision replay / forensic review.
type ReplayPayload struct {
	Event
	ReplayHint string `json:"replay_hint"`
}

func (q Query) normalizedLimit() int {
	if q.Limit <= 0 {
		return 50
	}
	if q.Limit > 500 {
		return 500
	}
	return q.Limit
}

func (q Query) timeRange(now time.Time, retention time.Duration) (int64, int64) {
	to := q.ToMs
	if to <= 0 {
		to = now.UnixMilli()
	}
	from := q.FromMs
	if from <= 0 {
		from = to - retention.Milliseconds()
	}
	if from < 0 {
		from = 0
	}
	return from, to
}

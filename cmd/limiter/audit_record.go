package main

import (
	"context"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/audit"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
)

func recordAudit(ctx context.Context, store *audit.Store, in audit.RecordInput) {
	if store == nil {
		return
	}
	if in.RequestID == "" {
		in.RequestID = telemetry.RequestIDFromContext(ctx)
	}
	in.TenantID = audit.NormalizeTenant(in.TenantID)
	_, _ = store.Record(ctx, in)
}

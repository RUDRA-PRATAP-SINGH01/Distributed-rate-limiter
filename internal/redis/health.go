package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Health reports Redis connectivity and Sentinel topology when available.
type Health struct {
	Mode        Mode   `json:"mode"`
	Connected   bool   `json:"connected"`
	Role        string `json:"role,omitempty"`
	Replication string `json:"replication,omitempty"`
	MasterAddr  string `json:"master_addr,omitempty"`
	Error       string `json:"error,omitempty"`
}

// CheckHealth pings Redis and, in Sentinel mode, reads replication INFO.
func CheckHealth(ctx context.Context, client redis.UniversalClient, cfg Config) Health {
	h := Health{Mode: cfg.Mode}
	if err := client.Ping(ctx).Err(); err != nil {
		h.Error = err.Error()
		return h
	}
	h.Connected = true

	info, err := client.Info(ctx, "replication").Result()
	if err != nil {
		return h
	}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "role:") {
			h.Role = strings.TrimPrefix(line, "role:")
		}
		if strings.HasPrefix(line, "master_host:") {
			h.MasterAddr = strings.TrimPrefix(line, "master_host:")
		}
	}
	h.Replication = summarizeReplication(info)
	return h
}

func summarizeReplication(info string) string {
	role := ""
	slaves := ""
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "role:") {
			role = strings.TrimPrefix(line, "role:")
		}
		if strings.HasPrefix(line, "connected_slaves:") {
			slaves = strings.TrimPrefix(line, "connected_slaves:")
		}
	}
	if role == "" {
		return ""
	}
	if slaves != "" {
		return fmt.Sprintf("role=%s slaves=%s", role, slaves)
	}
	return "role=" + role
}

func (h Health) JSON() []byte {
	b, _ := json.Marshal(h)
	return b
}

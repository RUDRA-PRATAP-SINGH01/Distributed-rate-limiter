package redis

import "strings"

// ClusterSlotTag returns the Redis Cluster hash tag for key.
// Keys that share a tag are guaranteed to hash to the same slot, so they may
// appear together in one EVAL. Empty "{}" tags are ignored (Redis rule).
func ClusterSlotTag(key string) string {
	start := strings.IndexByte(key, '{')
	if start < 0 {
		return key
	}
	rest := key[start+1:]
	end := strings.IndexByte(rest, '}')
	if end <= 0 {
		return key
	}
	return rest[:end]
}

// SameClusterSlot reports whether every key would land on one Cluster slot.
func SameClusterSlot(keys ...string) bool {
	if len(keys) == 0 {
		return true
	}
	tag := ClusterSlotTag(keys[0])
	if tag == "" {
		return false
	}
	for _, k := range keys[1:] {
		if ClusterSlotTag(k) != tag {
			return false
		}
	}
	return true
}

// SanitizeHashTag strips braces so a user-controlled string cannot inject a
// nested Redis hash tag and split a multi-key EVAL across slots.
func SanitizeHashTag(s string) string {
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	return s
}

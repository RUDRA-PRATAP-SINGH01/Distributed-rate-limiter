package redis

import "testing"

func TestClusterSlotTag(t *testing.T) {
	if got := ClusterSlotTag("audit:{audit}:idx:ts"); got != "audit" {
		t.Fatalf("got %q", got)
	}
	if got := ClusterSlotTag("idem:{abc}:meta:k"); got != "abc" {
		t.Fatalf("got %q", got)
	}
	if got := ClusterSlotTag("no-tag"); got != "no-tag" {
		t.Fatalf("untagged key should use the whole key, got %q", got)
	}
	if got := ClusterSlotTag("x:{}:y"); got != "x:{}:y" {
		t.Fatalf("empty tag is ignored by Redis, got %q", got)
	}
}

func TestSameClusterSlot(t *testing.T) {
	if !SameClusterSlot("a:{t}:1", "a:{t}:2", "b:{t}:3") {
		t.Fatal("expected same slot")
	}
	if SameClusterSlot("a:{t}:1", "a:{u}:1") {
		t.Fatal("different tags must not share a slot")
	}
}

func TestSanitizeHashTag(t *testing.T) {
	if got := SanitizeHashTag("foo{bar}baz"); got != "foobarbaz" {
		t.Fatalf("got %q", got)
	}
	if got := SanitizeHashTag("  {}  "); got != "_" {
		t.Fatalf("got %q", got)
	}
}

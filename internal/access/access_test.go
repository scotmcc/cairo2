package access

import (
	"context"
	"testing"
)

func TestCanAddressAlwaysAllowed(t *testing.T) {
	allowed, reason := CanAddress(context.Background(), "alice", "sessions")
	if !allowed {
		t.Errorf("want allowed=true, got false")
	}
	if reason != "stub: no-op access control" {
		t.Errorf("want reason=%q, got %q", "stub: no-op access control", reason)
	}
}

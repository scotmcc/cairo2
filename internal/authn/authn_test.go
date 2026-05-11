package authn

import (
	"net/http"
	"testing"
)

func TestVerifyHeader(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("X-Operator-Identity", "alice@example.com")
	id, err := Verify(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.User != "alice@example.com" {
		t.Errorf("want User=alice@example.com, got %q", id.User)
	}
	if id.Source != "header" {
		t.Errorf("want Source=header, got %q", id.Source)
	}
}

func TestVerifyMissing(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	id, err := Verify(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.User != "local" {
		t.Errorf("want User=local, got %q", id.User)
	}
	if id.Source != "local" {
		t.Errorf("want Source=local, got %q", id.Source)
	}
}

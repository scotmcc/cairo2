package authn

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

type fakeResolver struct {
	resp *apitype.WhoIsResponse
	err  error
}

func (f *fakeResolver) WhoIs(_ context.Context, _ string) (*apitype.WhoIsResponse, error) {
	return f.resp, f.err
}

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

func TestVerifyWithLoginName(t *testing.T) {
	node := &tailcfg.Node{}
	resp := &apitype.WhoIsResponse{
		Node:        node,
		UserProfile: &tailcfg.UserProfile{LoginName: "alice@ts.net"},
	}
	r, _ := http.NewRequest("GET", "/", nil)
	id, err := VerifyWith(r, &fakeResolver{resp: resp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.User != "alice@ts.net" {
		t.Errorf("want User=alice@ts.net, got %q", id.User)
	}
	if id.Source != "tsnet" {
		t.Errorf("want Source=tsnet, got %q", id.Source)
	}
	// NodeKey is whatever zero-value key.NodePublic.String() returns; assert extraction path runs
	if id.NodeKey != node.Key.String() {
		t.Errorf("want NodeKey=%q, got %q", node.Key.String(), id.NodeKey)
	}
}

func TestVerifyWithTagOnly(t *testing.T) {
	resp := &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{Tags: []string{"tag:ci"}},
		UserProfile: &tailcfg.UserProfile{},
	}
	r, _ := http.NewRequest("GET", "/", nil)
	id, err := VerifyWith(r, &fakeResolver{resp: resp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.User != "tag:ci" {
		t.Errorf("want User=tag:ci, got %q", id.User)
	}
	if id.Source != "tsnet" {
		t.Errorf("want Source=tsnet, got %q", id.Source)
	}
}

func TestVerifyWithNilResponse(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	id, err := VerifyWith(r, &fakeResolver{resp: nil, err: nil})
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

func TestVerifyWithResolverError(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	id, err := VerifyWith(r, &fakeResolver{err: errors.New("peer not found")})
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

func TestVerifyWithNoLoginNoTags(t *testing.T) {
	resp := &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{},
		UserProfile: &tailcfg.UserProfile{},
	}
	r, _ := http.NewRequest("GET", "/", nil)
	id, err := VerifyWith(r, &fakeResolver{resp: resp})
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

func TestVerifyWithNilResolver(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("X-Operator-Identity", "ops@example.com")
	id, err := VerifyWith(r, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.User != "ops@example.com" {
		t.Errorf("want User=ops@example.com, got %q", id.User)
	}
	if id.Source != "header" {
		t.Errorf("want Source=header, got %q", id.Source)
	}
}

package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// T19
func TestFetchModelInfo_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/model_group/info" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"model_group":"strong","max_input_tokens":32768}]}`)
	}))
	defer srv.Close()

	info, err := FetchModelInfo(context.Background(), srv.URL, "", "strong")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ContextLength != 32768 {
		t.Errorf("ContextLength = %d, want 32768", info.ContextLength)
	}
}

// T20
func TestFetchModelInfo_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"model_group":"strong","max_input_tokens":32768}]}`)
	}))
	defer srv.Close()

	_, err := FetchModelInfo(context.Background(), srv.URL, "", "absent")
	if err != ErrModelNotFound {
		t.Errorf("want ErrModelNotFound, got %v", err)
	}
}

// T21
func TestFetchModelInfo_HTTP401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := FetchModelInfo(context.Background(), srv.URL, "badkey", "strong")
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

// T22
func TestFetchModelInfo_HTTP403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := FetchModelInfo(context.Background(), srv.URL, "badkey", "strong")
	if err == nil {
		t.Fatal("expected error for HTTP 403, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}

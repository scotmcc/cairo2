package llm

import (
	"errors"
	"testing"
)

func TestParseOpenAIError_Unauthorized401(t *testing.T) {
	err := parseOpenAIError(401, []byte(`{"error":{"message":"Unauthorized","type":"invalid_request_error"}}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected error to wrap ErrUnauthorized, got %v", err)
	}
}

func TestParseOpenAIError_Forbidden403(t *testing.T) {
	err := parseOpenAIError(403, []byte(`{"error":{"message":"Forbidden","type":"invalid_request_error"}}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected error to wrap ErrUnauthorized, got %v", err)
	}
}

func TestParseOpenAIError_ServerError500(t *testing.T) {
	err := parseOpenAIError(500, []byte(`{"error":{"message":"Internal Server Error"}}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected error to NOT wrap ErrUnauthorized, got %v", err)
	}
}

package audit

import (
	"context"
	"testing"
	"time"
)

func TestLogAcceptsAnyEvent(t *testing.T) {
	e := Event{
		Timestamp: time.Now(),
		Actor:     "alice",
		Gate:      "access",
		Action:    "session.list",
		Target:    "sessions",
		Decision:  "granted",
		Reason:    "",
		Metadata:  map[string]string{"key": "val"},
	}
	Log(context.Background(), e)
}

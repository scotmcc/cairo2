package agent

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestSummarizerStatus_InitiallyIdle verifies that a freshly-created Agent
// returns a zero RanAt and nil Err before any summarizer run.
func TestSummarizerStatus_InitiallyIdle(t *testing.T) {
	a := &Agent{}
	st := a.SummarizerStatus()
	if !st.RanAt.IsZero() {
		t.Fatalf("expected zero RanAt before first run, got %v", st.RanAt)
	}
	if st.Err != nil {
		t.Fatalf("expected nil Err before first run, got %v", st.Err)
	}
}

// TestSummarizerStatus_AfterSuccess verifies that recording a successful run
// stores a recent timestamp and nil error.
func TestSummarizerStatus_AfterSuccess(t *testing.T) {
	a := &Agent{}
	before := time.Now()
	a.mu.Lock()
	a.summarizerRanAt = time.Now()
	a.summarizerErr = nil
	a.mu.Unlock()
	after := time.Now()

	st := a.SummarizerStatus()
	if st.RanAt.Before(before) || st.RanAt.After(after) {
		t.Fatalf("RanAt %v not within [%v, %v]", st.RanAt, before, after)
	}
	if st.Err != nil {
		t.Fatalf("expected nil Err after success, got %v", st.Err)
	}
}

// TestSummarizerStatus_AfterFailure verifies that recording a failed run
// stores a recent timestamp and the error.
func TestSummarizerStatus_AfterFailure(t *testing.T) {
	a := &Agent{}
	sentinel := errors.New("llm stream: connection refused")
	before := time.Now()
	a.mu.Lock()
	a.summarizerRanAt = time.Now()
	a.summarizerErr = sentinel
	a.mu.Unlock()
	after := time.Now()

	st := a.SummarizerStatus()
	if st.RanAt.Before(before) || st.RanAt.After(after) {
		t.Fatalf("RanAt %v not within [%v, %v]", st.RanAt, before, after)
	}
	if !errors.Is(st.Err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", st.Err)
	}
}

// TestSummarizerStatus_ConcurrentReadWrite verifies that concurrent writes and
// reads do not race. Run with -race to catch data races.
func TestSummarizerStatus_ConcurrentReadWrite(t *testing.T) {
	a := &Agent{}
	sentinel := errors.New("test error")

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Writers alternate success and failure.
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			a.mu.Lock()
			a.summarizerRanAt = time.Now()
			if i%2 == 0 {
				a.summarizerErr = nil
			} else {
				a.summarizerErr = sentinel
			}
			a.mu.Unlock()
		}(i)
	}

	// Readers just call the accessor — any panic or data-race is a failure.
	for range goroutines {
		go func() {
			defer wg.Done()
			_ = a.SummarizerStatus()
		}()
	}

	wg.Wait()
}

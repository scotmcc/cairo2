package agent

// shutdown.go — phased post-TUI shutdown with stdout progress + Ctrl-C abort.
//
// At process exit Cairo has up to three things to do:
//
//   1. Drain any unsummarized messages (one summarizer batch per LLM round-trip).
//   2. Generate session feedback (a single LLM call that reflects on the session
//      and writes a feedback memory).
//   3. Run session_end hooks (user-defined shell commands).
//
// Previously this all happened silently inside Agent.Close(): the TUI tore
// down, the terminal looked frozen, GPUs spun, and users assumed the app
// hung and Ctrl-C'd or killed the terminal — losing the work.
//
// Shutdown() runs the same phases but writes plain-text progress to an
// io.Writer (stdout in production), and listens for SIGINT. The first SIGINT
// cancels the current phase via a shared context — that propagates into the
// in-flight LLM call so the abort is fast, not "wait for the 3-minute timeout."
// A second SIGINT within doubleSigintWindow exits the process immediately
// with status 130.
//
// This file owns no state; it composes existing primitives (SummarizeAll,
// RunSessionFeedback, RunHooks) which now all accept a context.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// doubleSigintWindow is the deadline within which a second Ctrl-C escalates
// from "cancel current phase" to "force exit". 2s is long enough that a
// distracted second tap doesn't accidentally hard-kill, short enough that an
// intentional double-tap feels responsive.
const doubleSigintWindow = 2 * time.Second

// Shutdown runs the post-TUI exit sequence with visible progress and a clean
// abort path. Call this from outside the TUI loop (after tea.Program.Run
// returns) so progress lines don't fight the alternate screen. Writes to w;
// pass os.Stdout in production.
//
// Phases run sequentially. If ctx is cancelled (Ctrl-C), the current phase
// returns early and remaining phases are skipped — Cairo prints what it
// did, what it skipped, and exits cleanly. The per-turn summarizer goroutine
// is cancelled and drained before phases start so we don't double-fire; any
// interrupted work is picked up by shutdownPhaseSummarize.
func (a *Agent) Shutdown(w io.Writer) {
	if a.session == nil {
		a.summCancel()
		a.wg.Wait()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var firstSigintNs int64 // unix nanos of first Ctrl-C; 0 = none yet
	var aborted atomic.Bool
	go func() {
		for sig := range sigCh {
			_ = sig
			now := time.Now().UnixNano()
			prev := atomic.SwapInt64(&firstSigintNs, now)
			if prev != 0 && time.Duration(now-prev) < doubleSigintWindow {
				fmt.Fprintln(w, "\nForced exit.")
				os.Exit(130)
			}
			aborted.Store(true)
			cancel()
			fmt.Fprintln(w, "\n[abort] cancelling current phase — press Ctrl-C again within 2s to force exit")
		}
	}()

	fmt.Fprintln(w, "Cairo: finishing up before exit...")

	// Cancel the per-turn summarizer goroutine so wg.Wait returns immediately
	// rather than blocking up to 3 minutes on an in-flight LLM call.
	// shutdownPhaseSummarize will pick up any work the cancelled goroutine left.
	a.summCancel()
	a.wg.Wait()

	a.shutdownPhaseSummarize(ctx, w)
	if aborted.Load() {
		a.printShutdownAborted(w)
		return
	}

	a.shutdownPhaseFeedback(ctx, w)
	if aborted.Load() {
		a.printShutdownAborted(w)
		return
	}

	a.shutdownPhaseHooks(w)

	fmt.Fprintln(w, "Done.")
}

// shutdownPhaseSummarize force-drains unsummarized messages with a 90s
// timeout and a 2s heartbeat. SummarizeAllForce loops internally with a
// no-progress guard; we just wrap it in a phase-scoped timeout and surface
// progress so the user sees we're alive. The summarizer's LLM context
// derives from drainCtx, so Ctrl-C (parent ctx) or the 90s deadline
// interrupts the in-flight call rather than waiting on its 3-minute
// LLM timeout.
func (a *Agent) shutdownPhaseSummarize(ctx context.Context, w io.Writer) {
	count, err := a.db.Messages.CountUnsummarized(a.session.ID)
	if err != nil {
		fmt.Fprintf(w, "  [1/3] summarize: count failed (%v) — skip\n", err)
		return
	}
	if count == 0 {
		fmt.Fprintln(w, "  [1/3] summarize: nothing pending — skip")
		return
	}

	fmt.Fprintf(w, "  [1/3] summarize: %d unsummarized message(s)\n", count)
	start := time.Now()
	initial := count

	drainCtx, drainCancel := context.WithTimeout(ctx, 90*time.Second)
	defer drainCancel()

	done := make(chan struct{})
	go func() {
		SummarizeAllForce(drainCtx, a.db, a.llm, a.session.ID, "shutdown")
		close(done)
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			final, _ := a.db.Messages.CountUnsummarized(a.session.ID)
			drained := initial - final
			if final > 0 && ctx.Err() == nil && drainCtx.Err() == nil {
				fmt.Fprintf(w, "        no progress at boundary — stopping (%d remain)\n", final)
				return
			}
			fmt.Fprintf(w, "        done — %d/%d drained in %s\n",
				drained, initial, time.Since(start).Round(time.Second))
			return
		case <-drainCtx.Done():
			<-done
			final, _ := a.db.Messages.CountUnsummarized(a.session.ID)
			if ctx.Err() != nil {
				fmt.Fprintf(w, "        cancelled at %s — %d message(s) remain\n",
					time.Since(start).Round(time.Second), final)
			} else {
				fmt.Fprintf(w, "        timed out after 90s — %d message(s) remain (will retry on next startup)\n", final)
			}
			return
		case <-ticker.C:
			c, _ := a.db.Messages.CountUnsummarized(a.session.ID)
			fmt.Fprintf(w, "        %d remaining, %s elapsed\r",
				c, time.Since(start).Round(time.Second))
		}
	}
}

// shutdownPhaseFeedback runs RunSessionFeedback with a stdout spinner that
// updates every second so the user sees we're alive. The LLM call inside
// RunSessionFeedback derives its context from ctx, so Ctrl-C interrupts it
// rather than waiting on the 60s timeout.
func (a *Agent) shutdownPhaseFeedback(ctx context.Context, w io.Writer) {
	fmt.Fprintln(w, "  [2/3] session feedback (LLM reflection, up to 60s)...")
	start := time.Now()
	done := make(chan struct{})

	go func() {
		RunSessionFeedback(ctx, a.db, a.llm, a.session.ID)
		close(done)
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	spinner := []rune{'|', '/', '-', '\\'}
	tick := 0
	for {
		select {
		case <-done:
			fmt.Fprintf(w, "        done in %s\n", time.Since(start).Round(time.Second))
			return
		case <-ctx.Done():
			// Wait briefly for the goroutine to unwind so we don't print
			// "skipped" while it's still writing a memory.
			<-done
			fmt.Fprintf(w, "        cancelled at %s\n", time.Since(start).Round(time.Second))
			return
		case <-ticker.C:
			tick++
			fmt.Fprintf(w, "        %c %s elapsed\r", spinner[tick%len(spinner)],
				time.Since(start).Round(time.Second))
		}
	}
}

// shutdownPhaseHooks runs session_end hooks. These are user shell commands —
// no LLM, typically fast. Synchronous; we don't try to interrupt them.
func (a *Agent) shutdownPhaseHooks(w io.Writer) {
	fmt.Fprintln(w, "  [3/3] session_end hooks...")
	start := time.Now()
	RunHooks(a.db, "session_end", "", nil)
	fmt.Fprintf(w, "        done in %s\n", time.Since(start).Round(time.Second))
}

func (a *Agent) printShutdownAborted(w io.Writer) {
	count, err := a.db.Messages.CountUnsummarized(a.session.ID)
	if err == nil && count > 0 {
		fmt.Fprintf(w, "Aborted. %d message(s) left unsummarized — they'll be drained on next startup.\n", count)
	} else {
		fmt.Fprintln(w, "Aborted.")
	}
}

package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
)

// BackgroundRenderer subscribes to the agent bus and writes plain text to w.
// Used by background task workers — no ANSI, no interactive UI.
func BackgroundRenderer(bus *agent.Bus, w io.Writer) (stop func()) {
	ch, unsub := bus.Subscribe()

	var mu sync.Mutex
	toolStartedAt := make(map[string]time.Time)

	go func() {
		for event := range ch {
			switch event.Type {

			case agent.EventAgentStart:
				fmt.Fprintf(w, "[%s] agent started\n", timestamp())

			case agent.EventTurnStart:
				fmt.Fprintf(w, "[%s] turn start\n", timestamp())

			case agent.EventTokens:
				p := event.Payload.(agent.PayloadTokens)
				fmt.Fprint(w, p.Token)

			case agent.EventToolStart:
				p := event.Payload.(agent.PayloadToolStart)
				mu.Lock()
				toolStartedAt[p.Name] = time.Now()
				mu.Unlock()
				args := formatToolArgs(p.Args, 150)
				if args != "" {
					fmt.Fprintf(w, "\n[%s] tool: %s args=%s\n", timestamp(), p.Name, args)
				} else {
					fmt.Fprintf(w, "\n[%s] tool: %s\n", timestamp(), p.Name)
				}

			case agent.EventToolEnd:
				p := event.Payload.(agent.PayloadToolEnd)
				mu.Lock()
				started, ok := toolStartedAt[p.Name]
				delete(toolStartedAt, p.Name)
				mu.Unlock()
				var dur string
				if ok {
					dur = fmt.Sprintf("%.2fs", time.Since(started).Seconds())
				} else {
					dur = "?"
				}
				if p.IsError {
					preview := formatToolPreview(p.Result, 200)
					if preview == "" {
						fmt.Fprintf(w, "[%s] tool: %s [error] %s\n", timestamp(), p.Name, dur)
					} else {
						fmt.Fprintf(w, "[%s] tool: %s [error] %s: %s\n", timestamp(), p.Name, dur, preview)
					}
				} else {
					preview := formatToolPreview(p.Result, 200)
					if preview == "" {
						fmt.Fprintf(w, "[%s] tool: %s [done] %s %d chars\n", timestamp(), p.Name, dur, len(p.Result))
					} else {
						fmt.Fprintf(w, "[%s] tool: %s [done] %s %d chars preview=%q\n", timestamp(), p.Name, dur, len(p.Result), preview)
					}
				}

			case agent.EventTurnEnd:
				fmt.Fprintf(w, "\n[%s] turn end\n", timestamp())

			case agent.EventAgentEnd:
				fmt.Fprintf(w, "[%s] agent finished\n", timestamp())

			case agent.EventError:
				p := event.Payload.(agent.PayloadError)
				fmt.Fprintf(w, "[%s] ERROR: %v\n", timestamp(), p.Err)
			}
		}
	}()

	return unsub
}

// OpenTaskLog opens (or creates) the log file for a background task.
// Falls back to os.Stderr if the path can't be opened.
func OpenTaskLog(logPath string) (*os.File, error) {
	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
)

// Renderer subscribes to the agent bus and prints events to stdout.
// This is the CLI's "TUI" — replace with a real BubbleTea subscriber later.
func Renderer(bus *agent.Bus) (stop func()) {
	ch, unsub := bus.Subscribe()

	var mu sync.Mutex
	toolStartedAt := make(map[string]time.Time)

	go func() {
		for event := range ch {
			switch event.Type {

			case agent.EventTokens:
				p := event.Payload.(agent.PayloadTokens)
				fmt.Print(p.Token)

			case agent.EventThinking:
				// dim thinking output
				p := event.Payload.(agent.PayloadThinking)
				fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", p.Token)

			case agent.EventToolStart:
				p := event.Payload.(agent.PayloadToolStart)
				mu.Lock()
				toolStartedAt[p.Name] = time.Now()
				mu.Unlock()
				args := formatToolArgs(p.Args, 150)
				if args != "" {
					fmt.Printf("\n\033[33m⚙ %s\033[0m args=%s ", p.Name, args)
				} else {
					fmt.Printf("\n\033[33m⚙ %s\033[0m ", p.Name)
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
				preview := formatToolPreview(p.Result, 200)
				if p.IsError {
					if preview == "" {
						fmt.Printf("\033[31m[error]\033[0m %s\n", dur)
					} else {
						fmt.Printf("\033[31m[error]\033[0m %s: %s\n", dur, preview)
					}
				} else {
					if preview == "" {
						fmt.Printf("\033[32m[done]\033[0m %s %d chars\n", dur, len(p.Result))
					} else {
						fmt.Printf("\033[32m[done]\033[0m %s %d chars preview=%q\n", dur, len(p.Result), preview)
					}
				}

			case agent.EventAgentStart:
				// nothing — prompt already printed by CLI

			case agent.EventTurnEnd:
				fmt.Println() // newline after streamed response

			case agent.EventError:
				p := event.Payload.(agent.PayloadError)
				fmt.Fprintf(os.Stderr, "\n\033[31merror: %v\033[0m\n", p.Err)
			}
		}
	}()

	return unsub
}

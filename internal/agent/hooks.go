package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/db"
)

// ValidHookEvents is the canonical list of lifecycle events the hook system
// supports. Update this slice (and only this slice) when adding new hook events;
// tools that expose the enum to users (e.g. hooks_tool.go) read from here.
var ValidHookEvents = []string{
	"session_start",
	"session_end",
	"pre_tool",
	"post_tool",
	"pre_turn",
	"post_turn",
	"dream_completed",
	"learn_indexed",
	"task_completed",
	"fact_promoted", // fired by memory_tool.doAdd when tags contain "promoted-fact"
	"summarizer_ran",
	"pre_summarize",
}

// hookEnvMaxBytes is the cap applied to env vars that carry large content
// (tool results, message text, summaries, etc.) before they reach a hook
// process. Keeps hook processes from receiving multi-MB blobs via the
// environment and matches the tool_output_limit audit concern.
const hookEnvMaxBytes = 64 * 1024

// CapHookEnv truncates s to hookEnvMaxBytes and appends "\n[truncated]" if cut.
// Exported so callers outside the agent package (e.g. cmd/cairo/dream.go) can
// apply the same cap without duplicating the constant.
func CapHookEnv(s string) string {
	if len(s) <= hookEnvMaxBytes {
		return s
	}
	return s[:hookEnvMaxBytes] + "\n[truncated]"
}

// capHookEnv is the package-internal alias used by loop.go and summarizer.go.
func capHookEnv(s string) string { return CapHookEnv(s) }

// buildContextJSON builds a CAIRO_CONTEXT_JSON value from the event name and
// the flat extraEnv slice (which is a list of "KEY=VALUE" strings).
// The JSON object contains "event" plus one key per env pair, with keys
// lowercased and the "CAIRO_" prefix stripped for readability.
func buildContextJSON(event string, extraEnv []string) string {
	m := make(map[string]string, len(extraEnv)+1)
	m["event"] = event
	for _, kv := range extraEnv {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimPrefix(kv[:idx], "CAIRO_"))
		m[key] = kv[idx+1:]
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// HookResult is returned by RunHooks to communicate chain-control signals
// back to the caller.
type HookResult struct {
	Continue       bool   // false = skip remaining hooks for this event
	SuppressOutput bool   // true = swallow stdout from renderer
	Message        string // optional message passed back to caller
}

// regexCache caches compiled hook matcher regexes to avoid recompilation on
// every invocation. The cache has no invalidation path — if a hook's matcher
// is updated in the DB while cairo is running, the stale compiled regex stays
// resident until restart. Pattern changes require a cairo restart to take
// effect; acceptable since hooks are operator-edited, not hot-reloaded.
var regexCache sync.Map // map[string]*regexp.Regexp

// RunHooks fires all enabled hooks for the given event.
// target is the event-specific string the matcher regex tests against (e.g.
// tool name for pre_tool/post_tool, role for pre_turn/post_turn). Pass ""
// for events with no natural target.
// CAIRO_EVENT and CAIRO_CONTEXT_JSON are always set; any additional key=value
// pairs in extraEnv are appended on top of the current process environment.
// Errors are logged but do not abort — hooks are advisory.
func RunHooks(database *db.DB, event string, target string, extraEnv []string) HookResult {
	result := HookResult{Continue: true}
	hooks, err := database.Hooks.ForEvent(event)
	if err != nil || len(hooks) == 0 {
		return result
	}
	ctxJSON := buildContextJSON(event, extraEnv)
	base := []string{
		"CAIRO_EVENT=" + event,
		"CAIRO_CONTEXT_JSON=" + ctxJSON,
	}
	env := append(os.Environ(), append(base, extraEnv...)...)
	for _, h := range hooks {
		if h.Matcher != "" {
			var re *regexp.Regexp
			if cached, ok := regexCache.Load(h.Matcher); ok {
				re = cached.(*regexp.Regexp)
			} else {
				compiled, compErr := regexp.Compile(h.Matcher)
				if compErr != nil {
					log.Printf("hook %d: invalid matcher regex %q: %v", h.ID, h.Matcher, compErr)
					continue
				}
				regexCache.Store(h.Matcher, compiled)
				re = compiled
			}
			if !re.MatchString(target) {
				continue
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, "bash", "-c", h.Command)
		cmd.Env = env
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		runErr := cmd.Run()
		cancel()
		if runErr != nil {
			log.Printf("hook %d (%s): %v\n%s", h.ID, event, runErr, stderrBuf.String())
		}

		if raw := bytes.TrimSpace(stdoutBuf.Bytes()); len(raw) > 0 {
			var resp struct {
				Continue       bool   `json:"continue"`
				SuppressOutput bool   `json:"suppressOutput"`
				Message        string `json:"message"`
			}
			if jsonErr := json.Unmarshal(raw, &resp); jsonErr == nil {
				result.Continue = resp.Continue
				result.SuppressOutput = resp.SuppressOutput
				result.Message = resp.Message
			}
		}

		if !result.Continue {
			return result
		}
	}
	return result
}

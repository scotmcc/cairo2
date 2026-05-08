package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

type sayTool struct{ db *sqliteopen.DB }

func Say(database *sqliteopen.DB) agent.Tool { return sayTool{db: database} }

func (sayTool) Name() string { return "say" }

func (sayTool) Description() string {
	return "Speak text aloud via the configured TTS backend. See the `say_tool_guidance` prompt_part for voice-blend conventions."
}

func (sayTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":  prop("string", "Required. What to say. Brief and conversational."),
			"voice": propOptional("string", "Voice blend override, e.g. \"af_heart(6)+af_nicole(4)\". Defaults to kokoro_voice config.", "af_heart(8)+af_nicole(2)"),
			"speed": propOptional("number", "Playback speed. 1.0=normal, 1.2=excited, 0.8=thoughtful. Range 0.5–2.0.", "1.0"),
		},
		"required": []string{"text"},
	}
}

func (t sayTool) Execute(args map[string]any, tc *agent.ToolContext) agent.ToolResult {
	// say requires scoped mode (tier 2) — it has an outward side effect (audio).
	if r, refused := checkDiscipline(tc, "say", "", 2); refused {
		return r
	}
	text := strArg(args, "text")
	if text == "" {
		return agent.ToolResult{Content: "error: text is required", IsError: true}
	}

	kokoroURL, _ := t.db.Config.Get(config.KeyKokoroURL)
	if kokoroURL == "" {
		fmt.Fprintln(os.Stderr, "say: kokoro_url not configured; refusing to no-op silently")
		return agent.ToolResult{
			Content: "say: kokoro_url is not configured. Voice output is unavailable until the user sets it. " +
				"Ask Scot to run `/config set kokoro_url <url>` (e.g. http://mac-studio-instruct:8880), then try again.",
			IsError: true,
		}
	}

	// Voice: per-call override > config > default
	voice := strArg(args, "voice")
	if voice == "" {
		voice, _ = t.db.Config.Get(config.KeyKokoroVoice)
	}
	if voice == "" {
		voice = "af_heart(8)+af_nicole(2)"
	}

	// Speed: per-call override, clamped to 0.5–2.0
	speed := floatArg(args, "speed", 1.0)
	if speed < 0.5 {
		speed = 0.5
	} else if speed > 2.0 {
		speed = 2.0
	}

	ctx := context.Background()
	if tc != nil {
		ctx = tc.Ctx
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := playTTS(ctx, kokoroURL, voice, text, speed); err != nil {
			fmt.Fprintf(os.Stderr, "say: tts error: %v\n", err)
		}
	}()
	// Detach: we don't block the agent turn on audio playback, but the
	// goroutine respects ctx cancellation so shutdown can interrupt it.
	go wg.Wait()

	return agent.ToolResult{Content: fmt.Sprintf("say: speaking — %q (voice: %s, speed: %.1f)", text, voice, speed)}
}

func playTTS(ctx context.Context, kokoroURL, voice, text string, speed float64) error {
	payload := map[string]any{
		"model":           "kokoro",
		"input":           text,
		"voice":           voice,
		"response_format": "mp3",
		"speed":           speed,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kokoroURL+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tts returned %d", resp.StatusCode)
	}

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	f, err := os.CreateTemp("", "cairo-say-*.mp3")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpPath := f.Name()
	if _, err := f.Write(audio); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write audio: %w", err)
	}
	f.Close()

	cmd := exec.Command("afplay", filepath.Clean(tmpPath))
	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("afplay: %w", err)
	}
	os.Remove(tmpPath)
	return nil
}

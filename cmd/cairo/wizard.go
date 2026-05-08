package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// chooseOllamaURL prompts the user for the LLM server URL on first run, defaulting
// to whatever's currently in config (typically http://localhost:11434).
func chooseOllamaURL(database *sqliteopen.DB, current string) (string, error) {
	if current == "" {
		current = llm.DefaultURL
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Println()
	fmt.Println("─── Model server ───")
	fmt.Printf("Use [%s] (Enter to keep, or type a different URL): ", current)
	line, err := reader.ReadString('\n')
	if err != nil {
		return current, wizardErr(err)
	}
	line = strings.TrimSpace(line)
	if line == "" || line == current {
		return current, nil
	}
	if err := database.Config.Set("ollama_url", line); err != nil {
		return current, fmt.Errorf("save ollama_url: %w", err)
	}
	return line, nil
}

// runFirstRunWizard performs first-run "base config" setup before the CLI/TUI
// launches. Triggered when config.setup_complete != "true".
func runFirstRunWizard(database *sqliteopen.DB, client *llm.Client) error {
	if configValue(database, "setup_complete") == "true" {
		return nil
	}

	models, err := client.ListModels()
	if err != nil {
		return fmt.Errorf("list models: %w", err)
	}
	if len(models) == 0 {
		fmt.Fprintln(os.Stderr, "cairo: no models available on the server. Install a model and then relaunch cairo.")
		os.Exit(1)
	}

	needModel := !contains(models, configValue(database, "model"))
	needEmbed := !contains(models, configValue(database, "embed_model"))
	needSummary := !contains(models, configValue(database, "summary_model"))
	needUser := configValue(database, "user_name") == ""

	if !needModel && !needEmbed && !needSummary && !needUser {
		return database.Config.Set("setup_complete", "true")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("─── First-run setup ───")
	fmt.Println("Quick base config before you meet Selene.")

	if needModel {
		choice, err := pickFromList(reader, "Default chat model", models)
		if err != nil {
			return wizardErr(err)
		}
		if err := database.Config.Set("model", choice); err != nil {
			return fmt.Errorf("save model: %w", err)
		}
		ollamaURL := configValue(database, "ollama_url")
		initialAPIKey := resolveLLMAPIKey(database)
		apiKey := initialAPIKey
		modelCtx := 8192
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		info, err := llm.FetchModelInfo(ctx, ollamaURL, apiKey, choice)
		cancel()

		if err != nil && errors.Is(err, llm.ErrUnauthorized) {
			if initialAPIKey == "" {
				fmt.Print("\nThe LLM endpoint requires an API key. Enter API key (or press Enter to skip): ")
				keyLine, _ := reader.ReadString('\n')
				keyLine = strings.TrimSpace(keyLine)
				if keyLine != "" {
					apiKey = keyLine
					if err := database.Config.Set(config.KeyLLMAPIKey, keyLine); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to save API key: %v\n", err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					info, err = llm.FetchModelInfo(ctx, ollamaURL, apiKey, choice)
					cancel()
					if err != nil {
						fmt.Println("\nKey was rejected — falling back to manual context entry. Run `cairo config set llm_api_key <value>` to update later.")
					}
				}
			} else {
				fmt.Printf("\nWARN: API key in config was rejected by /model_group/info. Run `cairo config set llm_api_key <value>` to fix.\n")
			}
		}

		if err == nil && info.ContextLength > 0 {
			modelCtx = info.ContextLength
		} else {
			fmt.Printf("\nContext window size for %s [8192]: ", choice)
			ctxLine, _ := reader.ReadString('\n')
			if n, err2 := strconv.Atoi(strings.TrimSpace(ctxLine)); err2 == nil && n > 0 {
				modelCtx = n
			}
		}
		if err := database.Config.Set("model_ctx", fmt.Sprintf("%d", modelCtx)); err != nil {
			return fmt.Errorf("save model_ctx: %w", err)
		}
	}

	if needEmbed {
		display := filterEmbedModels(models)
		if len(display) == 0 {
			fmt.Println("\n(no obvious embedding models found — pick the right one for embeddings)")
			display = models
		}
		choice, err := pickFromList(reader, "Embedding model", display)
		if err != nil {
			return wizardErr(err)
		}
		if err := database.Config.Set("embed_model", choice); err != nil {
			return fmt.Errorf("save embed_model: %w", err)
		}
	}

	if needSummary {
		fmt.Println("\n(typically a smaller chat model — used to summarize old turns in the background)")
		display := filterChatModels(models)
		if len(display) == 0 {
			display = models
		}
		choice, err := pickFromList(reader, "Summary model", display)
		if err != nil {
			return wizardErr(err)
		}
		if err := database.Config.Set("summary_model", choice); err != nil {
			return fmt.Errorf("save summary_model: %w", err)
		}
	}

	if needUser {
		fmt.Print("\nWhat should Selene call you? (blank to skip): ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return wizardErr(err)
		}
		if name := strings.TrimSpace(line); name != "" {
			if err := database.Config.Set("user_name", name); err != nil {
				return fmt.Errorf("save user_name: %w", err)
			}
		}
	}

	currentAI := configValue(database, "ai_name")
	if currentAI == "" {
		currentAI = "Selene"
	}
	fmt.Printf("\nCairo's identity name [%s] (Enter to keep): ", currentAI)
	line, err := reader.ReadString('\n')
	if err != nil {
		return wizardErr(err)
	}
	if name := strings.TrimSpace(line); name != "" && name != currentAI {
		if err := database.Config.Set("ai_name", name); err != nil {
			return fmt.Errorf("save ai_name: %w", err)
		}
	}

	if err := database.Config.Set("setup_complete", "true"); err != nil {
		return fmt.Errorf("save setup_complete: %w", err)
	}

	fmt.Println()
	fmt.Println("Setup done. Once you're in, run /init to introduce yourself")
	fmt.Println("to Selene and have her learn about your project.")
	fmt.Println()

	return nil
}

func configValue(database *sqliteopen.DB, key string) string {
	v, _ := database.Config.Get(key)
	return v
}

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func filterEmbedModels(all []string) []string {
	var out []string
	for _, m := range all {
		lower := strings.ToLower(m)
		if strings.Contains(lower, "embed") || strings.Contains(lower, "nomic") {
			out = append(out, m)
		}
	}
	return out
}

func filterChatModels(all []string) []string {
	var out []string
	for _, m := range all {
		lower := strings.ToLower(m)
		if strings.Contains(lower, "embed") || strings.Contains(lower, "nomic") {
			continue
		}
		out = append(out, m)
	}
	return out
}

func pickFromList(reader *bufio.Reader, label string, items []string) (string, error) {
	for {
		fmt.Printf("\n%s — pick one:\n", label)
		for i, item := range items {
			fmt.Printf("  %d) %s\n", i+1, item)
		}
		fmt.Print("Enter number, or type an exact model name: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n, err := strconv.Atoi(line); err == nil {
			if n >= 1 && n <= len(items) {
				return items[n-1], nil
			}
			fmt.Printf("Number out of range (1-%d).\n", len(items))
			continue
		}
		return line, nil
	}
}

func wizardErr(err error) error {
	if errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, "\nSetup aborted. Rerun cairo when ready.")
		os.Exit(0)
	}
	return err
}

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// RunContext holds shared resources initialized at startup.
type RunContext struct {
	DB         *sqliteopen.DB
	LLM        *llm.Client
	OllamaURL  string
	EmbedModel string
}

// newRunContext opens the DB, connects to the LLM server, and returns a ready RunContext.
// Caller is responsible for calling rc.DB.Close() when done.
func newRunContext() (*RunContext, error) {
	database, err := sqliteopen.Open()
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	ollamaURL := resolveOllamaURL(database)
	embedModel, _ := database.Config.Get(config.KeyEmbedModel)
	client := llm.New(ollamaURL, resolveLLMAPIKey(database))
	if err := client.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("llm server unreachable: %w", err)
	}
	return &RunContext{
		DB:         database,
		LLM:        client,
		OllamaURL:  ollamaURL,
		EmbedModel: embedModel,
	}, nil
}

// resolveOllamaURL returns the Ollama URL, preferring the OLLAMA_URL env var
// over the stored DB config value.
func resolveOllamaURL(database *sqliteopen.DB) string {
	if env := strings.TrimSpace(os.Getenv("OLLAMA_URL")); env != "" {
		return env
	}
	url, _ := database.Config.Get("ollama_url")
	return url
}

// resolveLLMAPIKey returns the LLM API key, preferring the LLM_API_KEY env var
// over the stored DB config value.
func resolveLLMAPIKey(database *sqliteopen.DB) string {
	if env := strings.TrimSpace(os.Getenv("LLM_API_KEY")); env != "" {
		return env
	}
	key, _ := database.Config.Get(config.KeyLLMAPIKey)
	return key
}

// connectOllama pings the LLM server and, on failure, prompts for either an
// API key (on 401/403) or a new URL. Loops until ping succeeds or stdin closes.
func connectOllama(database *sqliteopen.DB, url string) (*llm.Client, error) {
	reader := bufio.NewReader(os.Stdin)
	promptedForKey := false
	for {
		currentKey := resolveLLMAPIKey(database)
		client := llm.New(url, currentKey)
		if err := client.Ping(); err == nil {
			return client, nil
		} else if errors.Is(err, llm.ErrUnauthorized) {
			if !promptedForKey && currentKey == "" {
				fmt.Fprint(os.Stderr, "\nThe LLM endpoint requires an API key. Enter API key (or press Enter to skip): ")
				keyLine, _ := reader.ReadString('\n')
				keyLine = strings.TrimSpace(keyLine)
				if keyLine != "" {
					if err := database.Config.Set(config.KeyLLMAPIKey, keyLine); err != nil {
						fmt.Fprintf(os.Stderr, "warning: failed to save API key: %v\n", err)
					}
					promptedForKey = true
					continue
				}
				promptedForKey = true
			} else if currentKey != "" {
				fmt.Fprintf(os.Stderr, "\nWARN: existing API key was rejected. Run `cairo config set llm_api_key <value>` to update.\n")
			}
		} else {
			fmt.Fprintf(os.Stderr, "cairo: llm server unreachable at %s: %v\n", url, err)
		}
		fmt.Fprint(os.Stderr, "Enter LLM server URL (blank to retry, Ctrl+C to quit): ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		line = strings.TrimSpace(line)
		if line != "" {
			url = line
			if err := database.Config.Set("ollama_url", url); err != nil {
				fmt.Fprintf(os.Stderr, "cairo: warning: failed to save URL to config: %v\n", err)
			}
			promptedForKey = false
		}
	}
}

func resolveSession(database *sqliteopen.DB, llmClient *llm.Client, wg *sync.WaitGroup, forceNew bool, id int64, name, role string) (*sessions.Session, error) {
	cwd, _ := os.Getwd()

	if pending, _ := database.Config.Get(config.KeyPendingSessionID); pending != "" {
		_ = database.Config.Set(config.KeyPendingSessionID, "")
		if pid, err := strconv.ParseInt(pending, 10, 64); err == nil && pid != 0 {
			if s, err := database.Sessions.Get(pid); err == nil && s != nil {
				return s, nil
			}
		}
	}

	if id != 0 {
		return database.Sessions.Get(id)
	}

	if forceNew {
		prev, _ := database.Sessions.Latest()
		if prev != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				agent.SummarizeAll(context.Background(), database, llmClient, prev.ID, "startup")
			}()
		}
		return database.Sessions.Create(name, cwd, role)
	}

	if role != "" {
		s, err := database.Sessions.LatestByRole(role)
		if err != nil {
			return nil, err
		}
		if s == nil {
			return database.Sessions.Create(name, cwd, role)
		}
		return s, nil
	}

	s, err := database.Sessions.Latest()
	if err != nil {
		return nil, err
	}
	if s == nil {
		return database.Sessions.Create(name, cwd, role)
	}
	return s, nil
}

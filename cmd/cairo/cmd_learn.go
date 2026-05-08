package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/scotmcc/cairo2/internal/learn"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// runLearn implements the `cairo learn [flags] <path>` subcommand.
func runLearn(args []string) error {
	fs := flag.NewFlagSet("learn", flag.ExitOnError)
	pathFlag := fs.String("path", "", "Directory to index (default: current directory)")
	projectFlag := fs.String("project", "", "Project name (default: directory basename)")
	summaryFlag := fs.String("summary-model", "", "Override summary_model from config")
	excludeFlag := fs.String("exclude", "", "Comma-separated additional glob patterns to exclude")
	taskFlag := fs.Int64("task", 0, "Task ID for progress reporting (background mode)")
	backgroundFlag := fs.Bool("background", false, "Background mode — silence stderr progress")
	reembedFlag := fs.Bool("reembed", false, "Bypass SHA-based change detection and re-index every file from scratch (use after changing embed_model_code)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo learn [flags] [path]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Walks a directory, summarizes each file with the summary model, embeds")
		fmt.Fprintln(os.Stderr, "the summary, and stores everything in the projects + indexed_files tables.")
		fmt.Fprintln(os.Stderr, "Honors .gitignore and a builtin set of always-skipped directories.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 && *pathFlag == "" {
		*pathFlag = fs.Arg(0)
	}
	if *pathFlag == "" {
		*pathFlag = "."
	}

	root, err := filepath.Abs(*pathFlag)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("path %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path %q is not a directory", root)
	}

	project := *projectFlag
	if project == "" {
		project = filepath.Base(root)
	}

	rc, err := newRunContext()
	if err != nil {
		return err
	}
	defer rc.DB.Close()

	summaryModel := *summaryFlag
	if summaryModel == "" {
		summaryModel, _ = rc.DB.Config.Get(config.KeySummaryModel)
	}
	if summaryModel == "" {
		return fmt.Errorf("summary_model not configured — pass --summary-model or run: cairo config set summary_model <model>")
	}

	embedModelCode, err := sqliteopen.ResolveCodeEmbedModel(rc.DB)
	if err != nil {
		return err
	}

	var extraExcludes []string
	if *excludeFlag != "" {
		for _, pat := range strings.Split(*excludeFlag, ",") {
			if pat = strings.TrimSpace(pat); pat != "" {
				extraExcludes = append(extraExcludes, pat)
			}
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *reembedFlag {
		fmt.Fprintln(os.Stderr, "Re-embedding all chunks (--reembed flag)")
	}

	cfg := learn.Config{
		DB:           rc.DB,
		LLM:          rc.LLM,
		Project:      project,
		Root:         root,
		SummaryModel: summaryModel,
		EmbedModel:   embedModelCode,
		ExtraExclude: extraExcludes,
		TaskID:       *taskFlag,
		ForceReembed: *reembedFlag,
	}

	if !*backgroundFlag {
		cfg.ProgressFn = func(current, total int, label, detail string) {
			if total <= 0 {
				fmt.Fprintf(os.Stderr, "\r%-72s", label)
				return
			}
			pct := 0
			if total > 0 {
				pct = current * 100 / total
			}
			line := fmt.Sprintf("[%3d%%] %d/%d  %s", pct, current, total, detail)
			if len(line) > 100 {
				line = line[:97] + "..."
			}
			fmt.Fprintf(os.Stderr, "\r%-100s", line)
		}
	}

	stats, err := learn.Run(ctx, cfg)
	if err != nil {
		if *taskFlag > 0 {
			rc.DB.Tasks.SetStatusAndResult(*taskFlag, jobs.StatusFailed,
				fmt.Sprintf("learn failed: %v", err))
		}
		return err
	}

	if !*backgroundFlag {
		fmt.Fprintf(os.Stderr, "\r%-100s\r", " ")
		fmt.Printf("learn complete: %d indexed, %d skipped, %d errors in %s\n",
			stats.Indexed, stats.Skipped, stats.Errors, stats.Duration.Round(time.Second))
		fmt.Printf("project %q is now available via the learn tool.\n", project)
	}

	if *taskFlag > 0 {
		rc.DB.Tasks.SetStatusAndResult(*taskFlag, jobs.StatusDone,
			fmt.Sprintf("indexed %d, skipped %d, errors %d in %s",
				stats.Indexed, stats.Skipped, stats.Errors, stats.Duration.Round(time.Second)))
	}
	return nil
}

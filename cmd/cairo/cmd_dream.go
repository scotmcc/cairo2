package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/cli"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
)

// runDream runs a headless maintenance session in the dream role.
func runDream(args []string) error {
	fs := flag.NewFlagSet("dream", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo dream")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Run a headless maintenance cycle in the dream role: review and")
		fmt.Fprintln(os.Stderr, "consolidate memories, facts, and summaries, then exit. A backup of")
		fmt.Fprintln(os.Stderr, "the live DB is written to ~/.cairo/backups/ before any work begins.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return fmt.Errorf("dream takes no positional arguments")
	}

	rc, err := newRunContext()
	if err != nil {
		return err
	}
	database := rc.DB
	defer database.Close()
	llmClient := rc.LLM
	embedModel := rc.EmbedModel

	backupDir := filepath.Join(sqliteopen.DefaultDataDir(), "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	backupPath := filepath.Join(backupDir, time.Now().Format("dream-2006-01-02-15-04.cairo"))

	src := cairoDBPath()
	tmp, err := os.CreateTemp("", "cairo-dream-backup-*.db")
	if err != nil {
		return fmt.Errorf("create backup temp: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath)

	if err := vacuumInto(src, tmpPath); err != nil {
		return fmt.Errorf("backup vacuum: %w", err)
	}
	defer os.Remove(tmpPath)

	counts, err := countEntities(tmpPath)
	if err != nil {
		return fmt.Errorf("backup count: %w", err)
	}
	manifest := bundleManifest{
		Version:         manifestVersion,
		ExportedAt:      time.Now().UTC(),
		IncludesHistory: true,
		Counts:          counts,
	}
	if err := writeBundle(backupPath, tmpPath, manifest); err != nil {
		return fmt.Errorf("write backup bundle: %w", err)
	}
	fmt.Printf("backup saved to %s\n", backupPath)

	cwd, _ := os.Getwd()
	session, err := database.Sessions.Create(identity.RoleDream, cwd, identity.RoleDream)
	if err != nil {
		return fmt.Errorf("create session: %v", err)
	}

	model, err := sqliteopen.ResolveModel(database, identity.RoleDream, "qwen3.6:35b-a3b-mlx-bf16")
	if err != nil {
		return fmt.Errorf("resolve model: %v", err)
	}

	builtins := tools.Default(database, llmClient, embedModel, nil)
	if allowed, _ := database.Roles.AllowedTools(identity.RoleDream); len(allowed) > 0 {
		builtins = tools.FilterByAllowlist(builtins, allowed)
	}
	custom, _ := tools.LoadCustom(database)
	allTools := append(builtins, custom...)

	a, err := agent.New(agent.Config{
		DB:      database,
		LLM:     llmClient,
		Model:   model,
		Session: session,
		Tools:   allTools,
	})
	if err != nil {
		return fmt.Errorf("create agent: %v", err)
	}

	stopRenderer := cli.BackgroundRenderer(a.Bus(), os.Stdout)
	defer stopRenderer()

	drainCtx, drainCancel := context.WithCancel(context.Background())
	drainSessions, drainErr := database.Messages.SessionsWithUnsummarized()
	if drainErr != nil {
		fmt.Fprintf(os.Stderr, "dream: list sessions with backlog: %v\n", drainErr)
	} else if len(drainSessions) > 0 {
		fmt.Printf("dream: pre-flight summarizer drain — %d session(s) with backlog\n", len(drainSessions))
		for _, sid := range drainSessions {
			before, _ := database.Messages.CountUnsummarized(sid)
			if before == 0 {
				continue
			}
			fmt.Printf("  session %d: draining %d unsummarized turn(s)... ", sid, before)
			start := time.Now()
			agent.SummarizeAllForce(drainCtx, database, llmClient, sid, "dream")
			after, _ := database.Messages.CountUnsummarized(sid)
			fmt.Printf("%d -> %d in %s\n", before, after, time.Since(start).Round(time.Second))
		}
		fmt.Println("dream: drain complete")
	}
	drainCancel()

	lastDreamAtStr, _ := database.Config.Get(config.KeyLastDreamAt)
	var lastDreamUnix int64
	if lastDreamAtStr != "" {
		lastDreamUnix, _ = strconv.ParseInt(lastDreamAtStr, 10, 64)
	}
	sessionWindowIDs, sessErr := database.Sessions.SinceUnix(lastDreamUnix)
	if sessErr != nil {
		fmt.Fprintf(os.Stderr, "dream: session window query: %v\n", sessErr)
		sessionWindowIDs = nil
	}

	preDreamMemoryIDs := captureUnreviewedMemoryIDs(database)
	preDreamFactIDs := captureUnreviewedFactIDs(database)
	preDreamSummaryIDs := captureUnreviewedSummaryIDsForSessions(database, sessionWindowIDs)
	preDreamMessageIDs := captureSessionMessageIDs(database, sessionWindowIDs)

	date := time.Now().Format("2006-01-02")
	if existing, _ := database.Dreams.GetByDate(date); existing != nil {
		fmt.Fprintf(os.Stderr, "dream: found existing row for %s from prior failed run; clearing\n", date)
		if delErr := database.Dreams.Delete(existing.ID); delErr != nil {
			fmt.Fprintf(os.Stderr, "dream: clear pending dream row: %v\n", delErr)
		}
	}
	dreamID, err := database.Dreams.Add(date, "<pending>", "", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dream: record session: %v\n", err)
		dreamID = 0
	}

	if dreamID != 0 {
		if writerErr := agent.RunWriter(context.Background(), database, sessionWindowIDs, dreamID, llmClient); writerErr != nil {
			fmt.Fprintf(os.Stderr, "dream: writer role: %v\n", writerErr)
		}
	}

	if dreamID != 0 {
		if curatorErr := agent.RunCurator(context.Background(), database, dreamID, llmClient); curatorErr != nil {
			fmt.Fprintf(os.Stderr, "dream: curator role: %v\n", curatorErr)
		}
	}

	if raterErr := agent.RunRater(context.Background(), database, llmClient); raterErr != nil {
		fmt.Fprintf(os.Stderr, "dream: rater role: %v\n", raterErr)
	}

	decayed, dumped, promoted, err := database.Memories.RunNightlyDecay()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dream: nightly decay: %v\n", err)
	}
	decayMsg := fmt.Sprintf("Nightly decay: %d memories decayed, %d soft-deleted, %d auto-promoted to importance=1.0 before this run.", decayed, dumped, promoted)
	fmt.Println(decayMsg)

	ritualMsg := ""
	if ritual, ritualErr := identity.RunDreamRitual(database.State); ritualErr != nil {
		fmt.Fprintf(os.Stderr, "dream: state ritual: %v\n", ritualErr)
	} else {
		ritualMsg = ritual.Summary()
		fmt.Println(ritualMsg)
	}

	startTime := time.Now()
	prompt := "Begin your maintenance cycle. Review and consolidate all memories, facts, and summaries."
	fullPrompt := decayMsg + "\n\n" + ritualMsg + "\n\n" + prompt
	if err := a.Prompt(context.Background(), fullPrompt); err != nil {
		return err
	}
	_ = database.Config.Set("last_dream_at", fmt.Sprintf("%d", time.Now().Unix()))

	if dreamID != 0 {
		if dreamerErr := agent.RunDreamer(context.Background(), database, dreamID, sessionWindowIDs, ritualMsg, llmClient); dreamerErr != nil {
			fmt.Fprintf(os.Stderr, "dream: dreamer role: %v\n", dreamerErr)
		}
	}

	markReviewedAfterDream(database, preDreamMemoryIDs, preDreamFactIDs, preDreamSummaryIDs, preDreamMessageIDs)

	if dreamID != 0 {
		agent.RunHooks(database, "dream_completed", "", []string{
			"CAIRO_DREAM_ID=" + fmt.Sprintf("%d", dreamID),
			"CAIRO_DREAM_DATE=" + date,
			"CAIRO_DREAM_STARTED_AT=" + startTime.UTC().Format(time.RFC3339),
			"CAIRO_DREAM_ENDED_AT=" + time.Now().UTC().Format(time.RFC3339),
			"CAIRO_BACKUP_PATH=" + backupPath,
		})
	}

	cleanDreamTaskLogs(sqliteopen.DefaultDataDir())
	return nil
}

func captureUnreviewedMemoryIDs(database *sqliteopen.DB) []int64 {
	mems, err := database.Memories.Unreviewed()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dream: capture unreviewed memory IDs: %v\n", err)
		return nil
	}
	ids := make([]int64, len(mems))
	for i, m := range mems {
		ids[i] = m.ID
	}
	return ids
}

func captureUnreviewedFactIDs(database *sqliteopen.DB) []int64 {
	facts, err := database.Facts.Unreviewed()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dream: capture unreviewed fact IDs: %v\n", err)
		return nil
	}
	ids := make([]int64, len(facts))
	for i, f := range facts {
		ids[i] = f.ID
	}
	return ids
}

func captureUnreviewedSummaryIDsForSessions(database *sqliteopen.DB, sessionIDs []int64) []int64 {
	ids, err := database.Summaries.UnreviewedIDsForSessions(sessionIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dream: capture unreviewed summary IDs: %v\n", err)
		return nil
	}
	return ids
}

func captureSessionMessageIDs(database *sqliteopen.DB, sessionIDs []int64) []int64 {
	ids, err := database.Messages.SessionMessageIDs(sessionIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dream: capture session message IDs: %v\n", err)
		return nil
	}
	return ids
}

func markReviewedAfterDream(database *sqliteopen.DB, memoryIDs, factIDs, summaryIDs, messageIDs []int64) {
	if err := database.Memories.MarkReviewed(memoryIDs); err != nil {
		fmt.Fprintf(os.Stderr, "dream: mark memories reviewed: %v\n", err)
	} else if len(memoryIDs) > 0 {
		fmt.Printf("dream: marked %d memories reviewed\n", len(memoryIDs))
	}
	if err := database.Facts.MarkReviewed(factIDs); err != nil {
		fmt.Fprintf(os.Stderr, "dream: mark facts reviewed: %v\n", err)
	} else if len(factIDs) > 0 {
		fmt.Printf("dream: marked %d facts reviewed\n", len(factIDs))
	}
	if err := database.Summaries.MarkReviewed(summaryIDs); err != nil {
		fmt.Fprintf(os.Stderr, "dream: mark summaries reviewed: %v\n", err)
	} else if len(summaryIDs) > 0 {
		fmt.Printf("dream: marked %d summaries reviewed\n", len(summaryIDs))
	}
	if err := database.Messages.MarkReviewed(messageIDs); err != nil {
		fmt.Fprintf(os.Stderr, "dream: mark messages reviewed: %v\n", err)
	} else if len(messageIDs) > 0 {
		fmt.Printf("dream: marked %d messages reviewed\n", len(messageIDs))
	}
}

func cleanDreamTaskLogs(dataDir string) {
	logsDir := filepath.Join(dataDir, "logs")
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	var deleted int
	_ = filepath.WalkDir(logsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			if os.Remove(path) == nil {
				deleted++
			}
		}
		return nil
	})
	if deleted > 0 {
		fmt.Printf("dream: cleaned %d task logs older than 30 days\n", deleted)
	}
}

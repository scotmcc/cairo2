package main

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// postLoopJobWriteback is called after a.Prompt returns in runTask for any
// task that belongs to a job (task.JobID != 0). For v0.3.0 worktree-backed
// jobs it:
//
//   - On success: computes diff stats from the job's worktree, derives a
//     one-line summary, writes them via Jobs.SetV030Fields, and transitions
//     the job to StatusAwaitingReview.
//
//   - On failure (runErr != nil OR result starts with "BLOCKED:"): writes
//     jobs.error and transitions the job to StatusFailed. Diff stats are
//     intentionally not populated on the failure path — a partial diff from
//     a failed run is not meaningful signal for the review flow.
//
// For non-worktree jobs (worktree_id IS NULL) the function is a no-op;
// the existing ResolveAndUpdateJobStatus path handles those.
func postLoopJobWriteback(database *sqliteopen.DB, jobID int64, result string, runErr error) {
	job, err := database.Jobs.Get(jobID)
	if err != nil {
		log.Printf("job %d: postLoopJobWriteback: get job: %v", jobID, err)
		return
	}
	if job.WorktreeID == nil {
		// Non-worktree job — legacy reconcile handles it.
		return
	}

	isBlocked := strings.HasPrefix(strings.TrimSpace(result), "BLOCKED:")
	if runErr != nil || isBlocked {
		var errMsg string
		if runErr != nil {
			errMsg = runErr.Error()
		} else {
			errMsg = firstLine(result)
		}
		if ferr := database.Jobs.SetV030Fields(jobID, nil, nil, nil, &errMsg, nil, nil, nil); ferr != nil {
			log.Printf("job %d: set error field: %v", jobID, ferr)
		}
		if ferr := database.Jobs.SetStatus(jobID, jobs.StatusFailed); ferr != nil {
			log.Printf("job %d: set status failed: %v", jobID, ferr)
		}
		return
	}

	wt, err := database.Worktrees.Get(*job.WorktreeID)
	if err != nil {
		log.Printf("job %d: get worktree %d: %v", jobID, *job.WorktreeID, err)
		return
	}

	files, ins, dels, diffErr := computeDiffStats(wt.Path, wt.ParentBranch, wt.Branch)
	if diffErr != nil {
		log.Printf("job %d: compute diff stats: %v", jobID, diffErr)
	}

	summary := firstLine(result)
	if err := database.Jobs.SetV030Fields(jobID, nil, nil, &summary, nil, &files, &ins, &dels); err != nil {
		log.Printf("job %d: SetV030Fields: %v", jobID, err)
		return
	}
	if err := database.Jobs.SetStatus(jobID, jobs.StatusAwaitingReview); err != nil {
		log.Printf("job %d: set status awaiting_review: %v", jobID, err)
	}
}

// computeDiffStats runs `git -C <worktreePath> diff <parentBranch>...<branch> --shortstat`
// and returns (files, insertions, deletions). Honors D1: parentBranch is a
// symbolic branch name, not a commit hash.
func computeDiffStats(worktreePath, parentBranch, branch string) (files, insertions, deletions int64, err error) {
	ref := fmt.Sprintf("%s...%s", parentBranch, branch)
	cmd := exec.Command("git", "-C", worktreePath, "diff", ref, "--shortstat")
	out, execErr := cmd.Output()
	if execErr != nil {
		return 0, 0, 0, fmt.Errorf("git diff %s: %w", ref, execErr)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0, 0, 0, nil
	}
	files, insertions, deletions = parseShortstat(line)
	return files, insertions, deletions, nil
}

var shortstatRe = regexp.MustCompile(
	`(\d+) files? changed` +
		`(?:, (\d+) insertions?\(\+\))?` +
		`(?:, (\d+) deletions?\(-\))?`,
)

func parseShortstat(line string) (files, insertions, deletions int64) {
	m := shortstatRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, 0
	}
	files, _ = strconv.ParseInt(m[1], 10, 64)
	if m[2] != "" {
		insertions, _ = strconv.ParseInt(m[2], 10, 64)
	}
	if m[3] != "" {
		deletions, _ = strconv.ParseInt(m[3], 10, 64)
	}
	return files, insertions, deletions
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

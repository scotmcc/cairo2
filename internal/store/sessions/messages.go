package sessions

import (
	"database/sql"
	"strings"
	"time"
)

// buildPlaceholders returns n comma-separated SQL placeholders ("?,?,?").
func buildPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// int64SliceToAny converts []int64 to []any for use as variadic sql args.
func int64SliceToAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

type Message struct {
	ID            int64
	SessionID     int64
	Role          string    // user | assistant | tool
	Content       string    // raw text or JSON
	ToolCalls     string    // JSON array, non-empty for assistant messages that call tools
	ToolName      string    // non-empty for tool result messages
	ToolID        string    // non-empty for tool result messages
	InnerVoice    string    // consider summary that framed this message; empty when consider was disabled or didn't fire. Set on user rows only.
	ToolStatus    string    // "ok" | "error" | "" — tool-result audit (role='tool' rows only). Written by AddTool.
	ToolLatencyMs int64     // tool call wall-time in ms; tool-result audit (role='tool' rows only). 0 on non-tool rows.
	ReviewedAt    time.Time // zero when not yet reviewed; non-zero after MarkReviewed.
	CreatedAt     time.Time
}

// selectMessageCols is the column list used by every SELECT that scans into
// Message via scanMessage. Centralising it keeps the 7+ readers in lockstep
// when columns are added.
const selectMessageCols = `id, session_id, role, content,
		COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		created_at`

type MessageQ struct{ db *sql.DB }

func NewMessageQ(db *sql.DB) *MessageQ { return &MessageQ{db: db} }

func (q *MessageQ) Add(sessionID int64, role, content, toolCalls, toolName, toolID string) (*Message, error) {
	return q.AddWithInnerVoice(sessionID, role, content, toolCalls, toolName, toolID, "")
}

// AddWithInnerVoice inserts a message and persists the consider summary that
// framed it. Pass "" for innerVoice on every role except "user" (and on user
// rows when consider was disabled or didn't produce a summary).
func (q *MessageQ) AddWithInnerVoice(sessionID int64, role, content, toolCalls, toolName, toolID, innerVoice string) (*Message, error) {
	res, err := q.db.Exec(
		`INSERT INTO messages(session_id, role, content, tool_calls, tool_name, tool_id, inner_voice)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		sessionID, role, content,
		nullStr(toolCalls), nullStr(toolName), nullStr(toolID), nullStr(innerVoice),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

// AddTool inserts a tool-result message and stamps tool_status / tool_latency_ms
// for SQL-level audit of tool error rate and latency. Status is "ok" or "error";
// latencyMs is wall-time of the tool call. Use this instead of Add for role='tool'
// rows so the audit columns are populated consistently. Returns the new row id.
func (q *MessageQ) AddTool(sessionID int64, content, toolName, toolID, status string, latencyMs int64) (int64, error) {
	res, err := q.db.Exec(
		`INSERT INTO messages(session_id, role, content, tool_name, tool_id, tool_status, tool_latency_ms)
		 VALUES(?, 'tool', ?, ?, ?, ?, ?)`,
		sessionID, content, nullStr(toolName), nullStr(toolID), nullStr(status), latencyMs,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (q *MessageQ) Get(id int64) (*Message, error) {
	row := q.db.QueryRow(
		`SELECT id, session_id, role, content,
		        COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		        COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		        created_at
		 FROM messages WHERE id = ?`, id)
	return scanMessage(row)
}

// UnsummarizedForSession returns messages not yet included in any summary.
// These form the "hot" context window sent to the LLM. All roles are included
// (user, assistant, tool) because tool-call and tool-result rows are required
// for LLM context continuity. Note that CountUnsummarized counts only
// user+assistant rows — tool messages inflate the payload relative to the
// trigger count, but this is intentional: tool rows are fragments of a turn,
// not turns themselves, so the summarization threshold is correctly turn-based.
func (q *MessageQ) UnsummarizedForSession(sessionID int64) ([]*Message, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, role, content,
		        COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		        COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		        created_at
		 FROM messages WHERE session_id = ? AND summarized = 0 ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountForSession returns the total number of messages in a session (all
// roles, summarized or not). Used by the session browser UI for at-a-glance
// "how much has been said here" display.
func (q *MessageQ) CountForSession(sessionID int64) (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID).Scan(&n)
	return n, err
}

// SessionsWithUnsummarized returns the IDs of every session that has at least
// one unsummarized user/assistant turn. Used by the dream maintenance flow
// to find sessions that need a backlog drain before dream agent work runs.
// Tool-call/tool-result rows are not counted — same definition as
// CountUnsummarized — so a session whose only "unsummarized" rows are tool
// fragments is treated as fully drained.
func (q *MessageQ) SessionsWithUnsummarized() ([]int64, error) {
	rows, err := q.db.Query(
		`SELECT DISTINCT session_id FROM messages
		 WHERE summarized = 0 AND role IN ('user','assistant') AND content != ''
		 ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CountUnsummarized returns the number of unsummarized user/assistant messages.
// Tool-call and tool-result rows are not counted — they're part of a turn, not a turn themselves.
func (q *MessageQ) CountUnsummarized(sessionID int64) (int, error) {
	var n int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM messages
		 WHERE session_id = ? AND summarized = 0 AND role IN ('user','assistant') AND content != ''`,
		sessionID).Scan(&n)
	return n, err
}

// MarkSummarized marks a range of messages as summarized by ID.
func (q *MessageQ) MarkSummarized(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.db.Exec(
		"UPDATE messages SET summarized = 1 WHERE id IN ("+buildPlaceholders(len(ids))+")",
		int64SliceToAny(ids)...)
	return err
}

// MarkSummarizedRange marks every message in a session whose id lies
// between firstID and lastID inclusive — summarizing user/assistant
// rows but leaving the intervening tool calls and tool results
// unsummarized leaks them into every future turn's history. The
// summarizer calls this after a successful run with the bounds of the
// batch it just folded into a summary row.
func (q *MessageQ) MarkSummarizedRange(sessionID, firstID, lastID int64) error {
	_, err := q.db.Exec(
		`UPDATE messages SET summarized = 1
		 WHERE session_id = ? AND id >= ? AND id <= ?`,
		sessionID, firstID, lastID)
	return err
}

// BetweenIDs returns all messages in a session within the inclusive id
// range, ordered by id. Used by the summarizer to pull tool rows into
// the transcript alongside the user/assistant rows that triggered the
// batch — without this the summary loses the most informative part of
// each turn (what tools were called and what they returned).
func (q *MessageQ) BetweenIDs(sessionID, firstID, lastID int64) ([]*Message, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, role, content,
		        COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		        COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		        created_at
		 FROM messages
		 WHERE session_id = ? AND id >= ? AND id <= ?
		 ORDER BY id ASC`, sessionID, firstID, lastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// EstimateUnsummarizedTokens returns a rough token count for all unsummarized
// messages in the session. The estimate is len(content)/4 summed across every
// unsummarized row (all roles, not just user/assistant) — tool results and
// tool calls are included because they can dominate context cost. Used by the
// secondary summarizer trigger (summary_token_threshold).
func (q *MessageQ) EstimateUnsummarizedTokens(sessionID int64) (int, error) {
	var total int
	err := q.db.QueryRow(
		`SELECT COALESCE(SUM(LENGTH(content) + LENGTH(COALESCE(tool_calls,''))), 0)
		 FROM messages
		 WHERE session_id = ? AND summarized = 0`,
		sessionID).Scan(&total)
	return total / 4, err
}

// OldestUnsummarized returns up to n oldest unsummarized user/assistant messages.
// The summarizer processes these as one batch.
func (q *MessageQ) OldestUnsummarized(sessionID int64, n int) ([]*Message, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, role, content,
		        COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		        COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		        created_at
		 FROM messages
		 WHERE session_id = ? AND summarized = 0 AND role IN ('user','assistant') AND content != ''
		 ORDER BY id ASC LIMIT ?`, sessionID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (q *MessageQ) ForSession(sessionID int64) ([]*Message, error) {
	rows, err := q.db.Query(
		`SELECT id, session_id, role, content,
		        COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		        COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		        created_at
		 FROM messages WHERE session_id = ? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateInnerVoice overwrites the inner_voice column on the given message id.
// Used by the consider-input path to attach a synthesized summary to a user
// message that was already persisted (e.g. when the model invokes the consider
// tool mid-turn, after the user message row has landed).
func (q *MessageQ) UpdateInnerVoice(messageID int64, innerVoice string) error {
	_, err := q.db.Exec(
		`UPDATE messages SET inner_voice = ? WHERE id = ?`,
		nullStr(innerVoice), messageID)
	return err
}

// LatestUserForSession returns the most recent user-role message in the given
// session, or nil with sql.ErrNoRows when none exists. Used by the consider
// tool to discover which user message to attach inner_voice to.
func (q *MessageQ) LatestUserForSession(sessionID int64) (*Message, error) {
	row := q.db.QueryRow(
		`SELECT id, session_id, role, content,
		        COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
		        COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
		        created_at
		 FROM messages WHERE session_id = ? AND role = 'user' ORDER BY id DESC LIMIT 1`, sessionID)
	return scanMessage(row)
}

// LatestInnerVoice returns the inner_voice string from the most recent user
// message in the given session that has a non-empty inner_voice. Returns ""
// when no such row exists (first turn, or consider was disabled last turn).
func (q *MessageQ) LatestInnerVoice(sessionID int64) (string, error) {
	var voice string
	err := q.db.QueryRow(
		`SELECT COALESCE(inner_voice, '') FROM messages
		 WHERE session_id = ? AND role = 'user' AND inner_voice IS NOT NULL AND inner_voice != ''
		 ORDER BY id DESC LIMIT 1`, sessionID).Scan(&voice)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return voice, err
}

// UnreviewedForSessions returns user and assistant messages from the given
// sessions that have not yet been reviewed by the dream-pass (reviewed_at IS
// NULL). Only non-empty user/assistant rows are returned — tool call/result
// fragments are excluded because the writer role needs readable prose, not
// raw tool JSON. Results are ordered by session_id then message id.
// Returns an empty slice (not an error) when sessionIDs is empty.
func (q *MessageQ) UnreviewedForSessions(sessionIDs []int64) ([]*Message, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	query := `SELECT id, session_id, role, content,
	                 COALESCE(tool_calls,''), COALESCE(tool_name,''), COALESCE(tool_id,''), COALESCE(inner_voice,''),
	                 COALESCE(tool_status,''), COALESCE(tool_latency_ms, 0), COALESCE(reviewed_at, 0),
	                 created_at
	          FROM messages
	          WHERE session_id IN (` + buildPlaceholders(len(sessionIDs)) + `)
	            AND role IN ('user','assistant')
	            AND content != ''
	            AND reviewed_at IS NULL
	          ORDER BY session_id ASC, id ASC`
	rows, err := q.db.Query(query, int64SliceToAny(sessionIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SessionMessageIDs returns all message IDs belonging to any of the given
// sessions. Used by the dream-pass to collect the full message window for
// reviewed_at stamping. Returns an empty slice (not an error) when sessionIDs
// is empty.
func (q *MessageQ) SessionMessageIDs(sessionIDs []int64) ([]int64, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	rows, err := q.db.Query(
		`SELECT id FROM messages WHERE session_id IN (`+buildPlaceholders(len(sessionIDs))+`) ORDER BY id ASC`,
		int64SliceToAny(sessionIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkReviewed stamps reviewed_at on the given message IDs. Used by the
// dream-pass to mark the full session window as reviewed so future passes
// skip already-scanned messages.
func (q *MessageQ) MarkReviewed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.db.Exec(
		`UPDATE messages SET reviewed_at = datetime('now') WHERE id IN (`+buildPlaceholders(len(ids))+`)`,
		int64SliceToAny(ids)...)
	return err
}

func scanMessage(row scanner) (*Message, error) {
	var m Message
	var createdAt, reviewedAt int64
	err := row.Scan(
		&m.ID, &m.SessionID, &m.Role, &m.Content,
		&m.ToolCalls, &m.ToolName, &m.ToolID, &m.InnerVoice,
		&m.ToolStatus, &m.ToolLatencyMs, &reviewedAt,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	if reviewedAt != 0 {
		m.ReviewedAt = time.Unix(reviewedAt, 0)
	}
	return &m, nil
}

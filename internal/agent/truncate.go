package agent

import (
	"fmt"
	"strconv"

	"github.com/scotmcc/cairo2/internal/db"
)

// truncateToolOutput caps tool result content at the configured limit.
// When truncated, appends a clear notice so the model knows content was cut.
func truncateToolOutput(database *db.DB, content string) string {
	limit := 65536
	if database != nil {
		if limitStr, _ := database.Config.Get(db.KeyToolOutputLimit); limitStr != "" {
			if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
				limit = n
			}
		}
	}
	if len(content) <= limit {
		return content
	}
	cut := len(content) - limit
	return content[:limit] + fmt.Sprintf("\n\n[... %d bytes truncated — use offset/limit args or a more specific query to get the rest]", cut)
}

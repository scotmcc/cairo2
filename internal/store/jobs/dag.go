package jobs

import (
	"encoding/json"
	"fmt"
)

// checkForCycles performs a DFS over the prospective dependency graph starting
// from startID and reports an error if any cycle is detected. adj maps each
// task ID to its direct dependencies.
func checkForCycles(startID int64, adj map[int64][]int64) error {
	visited := make(map[int64]bool)
	inStack := make(map[int64]bool)

	var dfs func(id int64) error
	dfs = func(id int64) error {
		if inStack[id] {
			return fmt.Errorf("dependency cycle detected: task %d is reachable from itself", id)
		}
		if visited[id] {
			return nil
		}
		visited[id] = true
		inStack[id] = true
		for _, dep := range adj[id] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		inStack[id] = false
		return nil
	}

	return dfs(startID)
}

func parseDeps(raw string) ([]int64, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

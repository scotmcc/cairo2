package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ToolListBuiltin_EmptyNames(t *testing.T) {
	tool := ToolListBuiltin([]string{})
	result := tool.Execute(nil, nil)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "no built-in tools")
}

func Test_ToolListBuiltin_ReturnsSortedNames(t *testing.T) {
	tool := ToolListBuiltin([]string{"zebra", "apple", "mango"})
	result := tool.Execute(nil, nil)
	require.False(t, result.IsError, "unexpected error: %s", result.Content)

	for _, name := range []string{"apple", "mango", "zebra"} {
		assert.Contains(t, result.Content, name)
	}

	// Verify sort order: apple before mango before zebra
	ai := indexOf(result.Content, "apple")
	mi := indexOf(result.Content, "mango")
	zi := indexOf(result.Content, "zebra")
	assert.Less(t, ai, mi, "apple should appear before mango")
	assert.Less(t, mi, zi, "mango should appear before zebra")
}

func Test_ToolListBuiltin_DetailsMatchNames(t *testing.T) {
	names := []string{"read", "write", "bash"}
	tool := ToolListBuiltin(names)
	result := tool.Execute(nil, nil)
	require.False(t, result.IsError)

	details, ok := result.Details.([]string)
	require.True(t, ok, "Details should be []string")
	assert.Len(t, details, 3)
}

func Test_ToolListBuiltin_Name(t *testing.T) {
	tool := ToolListBuiltin([]string{})
	assert.Equal(t, "tool_list_builtin", tool.Name())
}

// indexOf returns the position of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := range s {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

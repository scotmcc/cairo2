package tools

import "testing"

func TestParseStringList(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"valid_list", "[bash, read, edit]", []string{"bash", "read", "edit"}},
		{"empty_list", "[]", []string{}},
		{"single_item", "[bash]", []string{"bash"}},
		{"spaces", "[ bash , read ]", []string{"bash", "read"}},
		{"no_brackets", "bash,read", []string{"bash", "read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStringList(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("parseStringList(%q) = %v, want %v", tc.input, got, tc.want)
			}
			for i, v := range tc.want {
				if got[i] != v {
					t.Errorf("[%d] got %q, want %q", i, got[i], v)
				}
			}
		})
	}
}

func TestParseSkillAllowedTools(t *testing.T) {
	t.Run("with_allowed_tools", func(t *testing.T) {
		content := "---\nallowed_tools: [bash, read]\n---\nSkill body.\n"
		got := ParseSkillAllowedTools(content)
		if len(got) != 2 || got[0] != "bash" || got[1] != "read" {
			t.Errorf("expected [bash read], got %v", got)
		}
	})

	t.Run("no_frontmatter", func(t *testing.T) {
		got := ParseSkillAllowedTools("Plain markdown without any frontmatter.\n")
		if got != nil {
			t.Errorf("expected nil for no frontmatter, got %v", got)
		}
	})

	t.Run("malformed_frontmatter", func(t *testing.T) {
		// Unclosed --- block → parseSkillFrontmatter returns nil meta.
		got := ParseSkillAllowedTools("---\nallowed_tools: [bash]\nno closing delimiter")
		if got != nil {
			t.Errorf("expected nil for malformed frontmatter, got %v", got)
		}
	})

	t.Run("no_allowed_tools_key", func(t *testing.T) {
		content := "---\ndescription: A skill\ntags: foo\n---\nBody.\n"
		got := ParseSkillAllowedTools(content)
		if got != nil {
			t.Errorf("expected nil when no allowed_tools key, got %v", got)
		}
	})
}

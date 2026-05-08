package tools

import (
	"fmt"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
)

// skillTool is the consolidated skill tool — replaces skill_list, skill_create,
// skill_read, skill_update, skill_delete.
type skillTool struct {
	db    *db.DB
	embed *EmbedClient
}

func Skill(database *db.DB, embed *EmbedClient) agent.Tool {
	return skillTool{db: database, embed: embed}
}

func (skillTool) Name() string { return "skill" }
func (skillTool) Description() string {
	return `Manage skills — reusable prompt patterns, workflows, and reference material keyed by name.
Actions:
- list: return all skills (name + one-line description).
- read: return the full body of a skill. Args: name (required).
- create: save a new skill. Fails if a skill with the same name already exists — use update instead. Args: name, description, content (all required); tags (optional).
- update: replace a skill's content. Args: name, content (both required).
- delete: remove a skill. Args: name (required).
- search: search skills. Args: query (required), limit (optional, default 5), mode (optional: "semantic" default, "exact" for FTS keyword/phrase, "hybrid" for both).`
}
func (skillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "read", "create", "update", "delete", "search"},
				"description": "Operation to perform. Required.",
			},
			"name":        prop("string", "Skill name. Required for read, create, update, delete."),
			"description": prop("string", "Short summary. Required for create."),
			"content":     prop("string", "Skill body. Required for create, update."),
			"tags":        propOptional("string", "Comma-separated tags", "none"),
			"query":       prop("string", "Natural-language query. Required for search."),
			"limit":       propOptional("integer", "Max results for search", "5"),
			"mode":        propOptional("string", `Search mode: "semantic" (default, cosine similarity), "exact" (FTS5 keyword/phrase), "hybrid" (both, deduplicated)`, "semantic"),
		},
		"required": []string{"action"},
	}
}

func (t skillTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	// Per-action discipline tiers:
	//   list, read, search → tier 1 (readonly): observable, no state change
	//   create, update, delete → tier 3 (full): modifies the reusable prompt library (identity layer)
	action := strArg(args, "action")
	switch action {
	case "create", "update", "delete":
		if r, refused := checkDiscipline(ctx, "skill", action, 3); refused {
			return r
		}
	}
	return DispatchAction(args, "skill", map[string]func() agent.ToolResult{
		"list":   func() agent.ToolResult { return t.doList() },
		"read":   func() agent.ToolResult { return t.doRead(args) },
		"create": func() agent.ToolResult { return t.doCreate(args) },
		"update": func() agent.ToolResult { return t.doUpdate(args) },
		"delete": func() agent.ToolResult { return t.doDelete(args) },
		"search": func() agent.ToolResult { return t.doSearch(args) },
	})
}

func (t skillTool) doList() agent.ToolResult {
	skills, err := t.db.Skills.List()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: list: %v", err), IsError: true}
	}
	if len(skills) == 0 {
		return agent.ToolResult{Content: "no skills defined"}
	}
	var b strings.Builder
	for _, s := range skills {
		fmt.Fprintf(&b, "%s — %s\n", s.Name, s.Description)
	}
	return agent.ToolResult{Content: strings.TrimSpace(b.String()), Details: skills}
}

func (t skillTool) doRead(args map[string]any) agent.ToolResult {
	name := strArg(args, "name")
	if name == "" {
		return agent.ToolResult{Content: "error: name is required for skill/read", IsError: true}
	}
	s, err := t.db.Skills.Get(name)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: skill %q not found", name), IsError: true}
	}
	return agent.ToolResult{
		Content: fmt.Sprintf("# %s\n\n%s\n\n%s", s.Name, s.Description, SkillBody(s.Content)),
		Details: s,
	}
}

func (t skillTool) doCreate(args map[string]any) agent.ToolResult {
	name := strArg(args, "name")
	description := strArg(args, "description")
	content := strArg(args, "content")
	if name == "" || content == "" {
		return agent.ToolResult{Content: "error: name, description, and content are all required", IsError: true}
	}

	meta, _ := parseSkillFrontmatter(content)
	if meta != nil {
		if description == "" {
			description = meta["description"]
		}
		tags := strArg(args, "tags")
		if tags == "" {
			if t, ok := meta["tags"]; ok {
				args["tags"] = t
			}
		}
		// raw content preserved — frontmatter stored in DB, stripped at injection sites
	}

	if description == "" {
		return agent.ToolResult{Content: "error: name, description, and content are all required", IsError: true}
	}

	var embedding []float32
	text := name + "\n\n" + description + "\n\n" + content
	if vec, err := t.embed.Embed(text); err == nil && vec != nil {
		embedding = vec
	}

	embedModel := ""
	if t.embed != nil {
		embedModel = t.embed.Model
	}

	if err := t.db.Skills.Create(name, description, content, formatTags(strArg(args, "tags")), embedModel, embedding); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return agent.ToolResult{Content: fmt.Sprintf("error: skill %q already exists; use action=\"update\" to modify it", name), IsError: true}
		}
		return agent.ToolResult{Content: fmt.Sprintf("error: create: %v", err), IsError: true}
	}
	suffix := ""
	if len(embedding) > 0 {
		suffix = fmt.Sprintf(" (%d-dim embedding)", len(embedding))
	}
	return agent.ToolResult{Content: fmt.Sprintf("skill %q saved%s", name, suffix)}
}

func (t skillTool) doUpdate(args map[string]any) agent.ToolResult {
	name := strArg(args, "name")
	content := strArg(args, "content")
	if name == "" {
		return agent.ToolResult{Content: "error: name is required for skill/update", IsError: true}
	}
	if content == "" {
		return agent.ToolResult{Content: "error: content is required for skill/update", IsError: true}
	}

	// raw content preserved — frontmatter stored in DB, stripped at injection sites

	var embedding []float32
	if existing, err := t.db.Skills.Get(name); err == nil {
		text := name + "\n\n" + existing.Description + "\n\n" + content
		if vec, err2 := t.embed.Embed(text); err2 == nil && vec != nil {
			embedding = vec
		}
	}

	embedModel := ""
	if t.embed != nil {
		embedModel = t.embed.Model
	}

	if err := t.db.Skills.Update(name, content, embedModel, embedding); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: update: %v", err), IsError: true}
	}
	suffix := ""
	if len(embedding) > 0 {
		suffix = fmt.Sprintf(" (%d-dim embedding)", len(embedding))
	}
	return agent.ToolResult{Content: fmt.Sprintf("skill %q updated%s", name, suffix)}
}

func (t skillTool) doDelete(args map[string]any) agent.ToolResult {
	name := strArg(args, "name")
	if name == "" {
		return agent.ToolResult{Content: "error: name is required for skill/delete", IsError: true}
	}
	if err := t.db.Skills.Delete(name); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: delete: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("skill %q deleted", name)}
}

// SkillBody strips YAML frontmatter from skill content before LLM injection.
// If no frontmatter is present, the content is returned unchanged.
func SkillBody(content string) string {
	_, body := parseSkillFrontmatter(content)
	return body
}

// parseStringList parses an inline YAML sequence: "[bash, read]" → ["bash","read"].
// Also handles bare comma-separated values without brackets.
func parseStringList(v string) []string {
	v = strings.Trim(v, "[] ")
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ParseSkillAllowedTools extracts the allowed_tools list from skill content frontmatter.
// Returns nil if no frontmatter or no allowed_tools key.
func ParseSkillAllowedTools(content string) []string {
	meta, _ := parseSkillFrontmatter(content)
	if meta == nil {
		return nil
	}
	raw, found := meta["allowed_tools"]
	if !found || raw == "" {
		return nil
	}
	return parseStringList(raw)
}

// parseSkillFrontmatter splits YAML-style frontmatter from markdown content.
// If content starts with "---\n", returns parsed key/value pairs and the body.
// Otherwise returns nil map and content unchanged.
func parseSkillFrontmatter(content string) (meta map[string]string, body string) {
	if !strings.HasPrefix(content, "---\n") {
		return nil, content
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		return nil, content
	}
	header := rest[:end]
	body = strings.TrimPrefix(rest[end+5:], "\n")
	meta = make(map[string]string)
	for _, line := range strings.Split(header, "\n") {
		if i := strings.IndexByte(line, ':'); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			meta[k] = v
		}
	}
	return meta, body
}

func (t skillTool) doSearch(args map[string]any) agent.ToolResult {
	query := strArg(args, "query")
	if query == "" {
		return agent.ToolResult{Content: "error: query is required for skill/search", IsError: true}
	}
	limit := intArg(args, "limit", 5)
	mode := strArg(args, "mode")
	if mode == "" {
		mode = "semantic"
	}

	var skills []*db.Skill

	switch mode {
	case "exact":
		var err error
		skills, err = t.db.Skills.SearchFTS(query, limit)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("error: fts search: %v", err), IsError: true}
		}
	case "hybrid":
		vec, err := t.embed.Embed(query)
		if err != nil {
			return agent.ToolResult{Content: "failed to embed query — is the embed model running?", IsError: true}
		}
		var semantic []*db.Skill
		if vec != nil {
			semantic, err = t.db.Skills.Search(vec, t.embed.Model, limit)
			if err != nil {
				return agent.ToolResult{Content: fmt.Sprintf("error: semantic search: %v", err), IsError: true}
			}
		}
		exact, err := t.db.Skills.SearchFTS(query, limit)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("error: fts search: %v", err), IsError: true}
		}
		seen := make(map[int64]bool)
		for _, s := range semantic {
			if !seen[s.ID] {
				seen[s.ID] = true
				skills = append(skills, s)
			}
		}
		for _, s := range exact {
			if !seen[s.ID] {
				seen[s.ID] = true
				skills = append(skills, s)
			}
		}
	default: // "semantic"
		vec, err := t.embed.Embed(query)
		if err != nil {
			return agent.ToolResult{Content: "failed to embed query — is the embed model running?", IsError: true}
		}
		if vec == nil {
			return agent.ToolResult{Content: "embed unavailable: no model configured"}
		}
		skills, err = t.db.Skills.Search(vec, t.embed.Model, limit)
		if err != nil {
			return agent.ToolResult{Content: fmt.Sprintf("error: search: %v", err), IsError: true}
		}
	}

	if len(skills) == 0 {
		return agent.ToolResult{Content: "no matching skills found"}
	}

	var b strings.Builder
	for _, s := range skills {
		fmt.Fprintf(&b, "%s — %s\n", s.Name, s.Description)
	}
	return agent.ToolResult{Content: strings.TrimSpace(b.String()), Details: skills}
}

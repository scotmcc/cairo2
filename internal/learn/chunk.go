package learn

import (
	"fmt"
	"regexp"
	"strings"
)

// Chunk is one semantic unit within a file's content.
type Chunk struct {
	StartLine int    // 1-based line number of the chunk's first line
	Length    int    // number of lines in the chunk
	Content   string // raw text of the chunk
	Label     string // "method", "type", "paragraph", "section-h2"
	Name      string // method/class/heading name (may be empty)
}

// MaxChunkTokens is the upper bound on estimated tokens per chunk. Token
// estimate: len(text)/4. Chunks exceeding this are split at line boundaries.
// Default: 400 — a safety margin under nomic-embed-text's 512-token window.
// Override via Config.DB.Config.Get(db.KeyLearnMaxChunkTokens) before indexing.
var MaxChunkTokens = 400

var (
	goFuncRe          = regexp.MustCompile(`(?m)^(\s*func\s+)(\w+)\s*\(`)
	goTypeRe          = regexp.MustCompile(`(?m)^type\s+(\w+)\s+(?:struct|interface)\b`)
	pythonDefRe       = regexp.MustCompile(`(?m)^(\s*def\s+)(\w+)\s*\(`)
	tsVarRe           = regexp.MustCompile(`(?m)^(?:\s*(?:function|const|let|var)\s+)(\w+)\s*[=(]`)
	tsInterfaceTypeRe = regexp.MustCompile(`(?m)^(\s*)(\w+)\s*:\s*(?:interface|type)\s*=`)
	markdownH2Re      = regexp.MustCompile(`(?m)^##\s+(.+)$`)
	blankLineRe       = regexp.MustCompile(`(?m)^[ \t]*$`)
)

// ChunkContent splits content into semantic units based on fileType. After
// per-language chunking, any chunk whose estimated token count exceeds
// MaxChunkTokens is further split at line boundaries.
func ChunkContent(content string, fileType string) []Chunk {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	lines := strings.Split(content, "\n")

	var raw []Chunk
	switch fileType {
	case "go":
		raw = chunkGoSymbols(lines)
	case "c", "cpp", "h", "hpp", "rs", "java", "cs", "ts", "tsx", "jsx", "javascript":
		raw = chunkBySignatures(lines, "method")
	case "py":
		raw = chunkBySignatures(lines, "method", pythonDefRe)
	case "md", "markdown":
		raw = chunkByHeadings2(lines)
	default:
		raw = chunkByParagraphs(lines)
	}

	return splitOversizedChunks(raw, MaxChunkTokens)
}

// chunkGoSymbols extracts Go symbols: top-level func declarations and
// type declarations (struct and interface only). Each symbol starts a new
// chunk; content runs until the next symbol or end of file.
// v1 uses regex heuristics; tree-sitter multi-language support is deferred.
func chunkGoSymbols(lines []string) []Chunk {
	type sym struct {
		lineIndex int
		name      string
		label     string // "method" for func, "type" for struct/interface
	}
	var syms []sym

	for i, line := range lines {
		if m := goFuncRe.FindStringSubmatch(line); m != nil {
			syms = append(syms, sym{i, m[len(m)-1], "method"})
		} else if m := goTypeRe.FindStringSubmatch(line); m != nil {
			syms = append(syms, sym{i, m[1], "type"})
		}
	}

	if len(syms) == 0 {
		return []Chunk{{
			StartLine: 1,
			Length:    len(lines),
			Content:   contentFromLines(lines, 0, len(lines)),
			Label:     "method",
			Name:      "",
		}}
	}

	chunks := make([]Chunk, len(syms))
	for i, s := range syms {
		end := len(lines)
		if i+1 < len(syms) {
			end = syms[i+1].lineIndex
		}
		chunks[i] = Chunk{
			StartLine: s.lineIndex + 1, // 1-based
			Length:    end - s.lineIndex,
			Content:   contentFromLines(lines, s.lineIndex, end),
			Label:     s.label,
			Name:      s.name,
		}
	}
	return chunks
}

func chunkBySignatures(lines []string, defaultLabel string, re ...*regexp.Regexp) []Chunk {
	var pattern *regexp.Regexp
	if len(re) > 0 {
		pattern = re[0]
	} else {
		pattern = goFuncRe
	}

	// Find all signature matches.
	type sig struct {
		lineIndex int // 0-based line index
		name      string
	}
	var sigs []sig

	for i, line := range lines {
		match := pattern.FindStringSubmatch(line)
		if match != nil {
			name := match[len(match)-1] // last capture group = identifier
			sigs = append(sigs, sig{i, name})
		}
	}

	if len(sigs) == 0 {
		// No signatures found — return the whole file as one chunk
		return []Chunk{{
			StartLine: 1,
			Length:    len(lines),
			Content:   contentFromLines(lines, 0, len(lines)),
			Label:     defaultLabel,
			Name:      "",
		}}
	}

	var chunks []Chunk
	for i, sig := range sigs {
		start := sig.lineIndex
		end := len(lines)
		if i+1 < len(sigs) {
			end = sigs[i+1].lineIndex
		}
		chunks = append(chunks, Chunk{
			StartLine: start + 1, // 1-based
			Length:    end - start,
			Content:   contentFromLines(lines, start, end),
			Label:     defaultLabel,
			Name:      sig.name,
		})
	}
	return chunks
}

func chunkByHeadings2(lines []string) []Chunk {
	type heading struct {
		lineIndex int
		text      string
	}
	var headings []heading

	for i, line := range lines {
		match := markdownH2Re.FindStringSubmatch(line)
		if match != nil {
			headings = append(headings, heading{i, match[1]})
		}
	}

	if len(headings) == 0 {
		return []Chunk{{
			StartLine: 1,
			Length:    len(lines),
			Content:   contentFromLines(lines, 0, len(lines)),
			Label:     "section-h2",
			Name:      "",
		}}
	}

	var chunks []Chunk
	for i, h := range headings {
		start := h.lineIndex
		end := len(lines)
		if i+1 < len(headings) {
			end = headings[i+1].lineIndex
		}
		chunks = append(chunks, Chunk{
			StartLine: start + 1,
			Length:    end - start,
			Content:   contentFromLines(lines, start, end),
			Label:     "section-h2",
			Name:      h.text,
		})
	}
	return chunks
}

func chunkByParagraphs(lines []string) []Chunk {
	if len(lines) == 0 {
		return nil
	}

	// Find blank line boundaries.
	type block struct {
		start int
		end   int
	}
	var blocks []block

	start := 0
	for i, line := range lines {
		if blankLineRe.MatchString(line) {
			if i > start {
				blocks = append(blocks, block{start, i})
			}
			start = i + 1
		}
	}
	if start < len(lines) {
		blocks = append(blocks, block{start, len(lines)})
	}

	if len(blocks) == 0 {
		return []Chunk{{
			StartLine: 1,
			Length:    len(lines),
			Content:   contentFromLines(lines, 0, len(lines)),
			Label:     "paragraph",
			Name:      "",
		}}
	}

	var chunks []Chunk
	for _, b := range blocks {
		chunks = append(chunks, Chunk{
			StartLine: b.start + 1,
			Length:    b.end - b.start,
			Content:   contentFromLines(lines, b.start, b.end),
			Label:     "paragraph",
			Name:      "",
		})
	}
	return chunks
}

// splitOversizedChunks partitions any chunk whose estimated token count
// (len(content)/4) exceeds maxTokens into sub-chunks split at line boundaries.
// Sub-chunks inherit the parent's Label; the Name gets a _part1/_part2 suffix
// when the parent had a non-empty name. StartLine and Length are adjusted to
// reflect the actual lines covered by each sub-chunk.
//
// The algorithm walks lines and emits a new sub-chunk whenever the running
// token estimate would exceed maxTokens. It always splits at a line boundary
// so no content is discarded — only reorganised.
func splitOversizedChunks(chunks []Chunk, maxTokens int) []Chunk {
	if maxTokens <= 0 {
		return chunks
	}
	maxChars := maxTokens * 4 // token estimate: 1 token ≈ 4 chars

	out := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		if len(c.Content) <= maxChars {
			out = append(out, c)
			continue
		}

		// Split this oversized chunk at line boundaries.
		lines := strings.Split(c.Content, "\n")
		partNum := 0
		partStart := 0 // index into lines

		for partStart < len(lines) {
			// Accumulate lines until we'd exceed maxChars.
			end := partStart
			size := 0
			for end < len(lines) {
				lineLen := len(lines[end]) + 1 // +1 for the newline
				if size+lineLen > maxChars && end > partStart {
					break
				}
				size += lineLen
				end++
			}
			if end == partStart {
				// Single line exceeds cap — emit it anyway to avoid infinite loop.
				end = partStart + 1
			}

			partNum++
			name := c.Name
			if name != "" {
				name = fmt.Sprintf("%s_part%d", c.Name, partNum)
			}
			out = append(out, Chunk{
				StartLine: c.StartLine + partStart,
				Length:    end - partStart,
				Content:   contentFromLines(lines, partStart, end),
				Label:     c.Label,
				Name:      name,
			})
			partStart = end
		}
	}
	return out
}

func contentFromLines(lines []string, start, end int) string {
	return strings.Join(lines[start:end], "\n")
}

// AugmentedText returns a formatted string suitable for embedding inputs.
// Format: project=NAME file=PATH line=N func=NAME · content (first 200 chars)
func AugmentedText(project, relPath string, chunk Chunk) string {
	fn := ""
	if chunk.Name != "" {
		fn = " func=" + chunk.Name
	}
	content := chunk.Content
	if len(content) > 200 {
		content = content[:200] + "…"
	}
	return fmt.Sprintf("project=%s file=%s line=%d%s · %s", project, relPath, chunk.StartLine, fn, content)
}

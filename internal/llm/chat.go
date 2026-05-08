package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ChatCallbacks receives streaming events during SSE chat completion.
type ChatCallbacks struct {
	Thinking func(token string)
	Content  func(token string)
}

type chatRequest struct {
	Model              string         `json:"model"`
	Messages           []Message      `json:"messages"`
	IsStreaming        bool           `json:"stream"`
	Tools              []ToolDef      `json:"tools,omitempty"`
	ResponseFormat     any            `json:"response_format,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type completionResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// serializeMessages returns a copy of msgs with the [tool error] prefix
// applied to any message where IsError is true. OpenAI has no native error
// role, so we annotate the content body so the model can recognise failures.
// Also defaults any tool_calls with empty Type to "function" (OpenAI spec requirement).
// Demotes any system message not at position 0 to user role with "[harness] " prefix
// (vLLM/OpenAI enforces system role only at position 0).
func serializeMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		if m.IsError && !hasToolErrorPrefix(m.Content) {
			m.Content = "[tool error] " + m.Content
		}
		for j := range m.ToolCalls {
			if m.ToolCalls[j].Type == "" {
				m.ToolCalls[j].Type = "function"
			}
			// OpenAI spec requires arguments as a JSON-encoded string, not an object.
			// After a DB round-trip, Arguments arrives as map[string]any — re-encode it.
			if _, ok := m.ToolCalls[j].Function.Arguments.(string); !ok && m.ToolCalls[j].Function.Arguments != nil {
				if b, err := json.Marshal(m.ToolCalls[j].Function.Arguments); err == nil {
					m.ToolCalls[j].Function.Arguments = string(b)
				}
			}
		}
		out[i] = m
	}

	// Demote any system message not at position 0 to user role with marker.
	// vLLM/OpenAI spec requires system role only at position 0.
	sawSystemAtZero := false
	for i := range out {
		if out[i].Role != "system" {
			continue
		}
		if i == 0 && !sawSystemAtZero {
			sawSystemAtZero = true
			continue
		}
		// Out-of-position system → user with marker
		out[i].Role = "user"
		out[i].Content = "[harness] " + out[i].Content
	}

	return out
}

func hasToolErrorPrefix(s string) bool {
	return len(s) >= 13 && s[:13] == "[tool error] "
}

// stripThinkBlocks removes <think>...</think> blocks from content.
// Used for non-streaming Complete() to prevent reasoning tokens from
// contaminating stored content (consider, summaries, etc.).
func stripThinkBlocks(s string) string {
	const open = "<think>"
	const close = "</think>"
	var b strings.Builder
	for {
		start := strings.Index(s, open)
		if start == -1 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		s = s[start+len(open):]
		end := strings.Index(s, close)
		if end == -1 {
			// No closing tag — discard the rest (truncated thinking block)
			return b.String()
		}
		s = s[end+len(close):]
	}
}

// buildTemplateKwargs returns the chat_template_kwargs map when thinking should
// be disabled, nil otherwise (omitted from the JSON body via omitempty).
func buildTemplateKwargs(opts ChatOptions) map[string]any {
	if opts.DisableThinking {
		return map[string]any{"enable_thinking": false}
	}
	return nil
}

// buildResponseFormat translates ChatOptions.Format to OpenAI response_format.
func buildResponseFormat(format any) any {
	if format == nil {
		return nil
	}
	if s, ok := format.(string); ok && s == "json" {
		return map[string]any{"type": "json_object"}
	}
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "out",
			"schema": format,
		},
	}
}

// Complete makes a non-streaming one-shot completion against /v1/chat/completions.
// Intended for background tasks (consider inner-dialogue, summarizer) where
// streaming UI feedback is not needed. Returns the assistant text with any
// <think>...</think> blocks stripped.
// No tools, no callbacks.
func (c *Client) Complete(ctx context.Context, model string, messages []Message, opts ChatOptions) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutChat)
	defer cancel()
	body, err := json.Marshal(chatRequest{
		Model:              model,
		Messages:           serializeMessages(messages),
		IsStreaming:        false,
		ResponseFormat:     buildResponseFormat(opts.Format),
		ChatTemplateKwargs: buildTemplateKwargs(opts),
	})
	if err != nil {
		return "", err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", parseOpenAIError(resp.StatusCode, respBody)
	}

	var cr completionResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return "", fmt.Errorf("llm decode: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm: empty choices in response")
	}

	return stripThinkBlocks(cr.Choices[0].Message.Content), nil
}

// sseChunk is the SSE delta shape from /v1/chat/completions stream events.
type sseChunk struct {
	Choices []sseChoice `json:"choices"`
}

type sseChoice struct {
	Delta        sseDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type sseDelta struct {
	Content          string         `json:"content"`
	ToolCalls        []sseDeltaCall `json:"tool_calls"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
}

type sseDeltaCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// toolCallAssembler reassembles streaming tool call deltas into complete ToolCalls.
// Deltas arrive per-index; each carries a fragment of the arguments JSON string.
type toolCallAssembler struct {
	byIndex map[int]*ToolCall
	args    map[int]*bytes.Buffer
	order   []int
}

func (a *toolCallAssembler) apply(deltas []sseDeltaCall) {
	if a.byIndex == nil {
		a.byIndex = make(map[int]*ToolCall)
		a.args = make(map[int]*bytes.Buffer)
	}
	for _, d := range deltas {
		if _, exists := a.byIndex[d.Index]; !exists {
			tc := &ToolCall{ID: d.ID, Type: d.Type}
			tc.Function.Name = d.Function.Name
			a.byIndex[d.Index] = tc
			a.args[d.Index] = &bytes.Buffer{}
			a.order = append(a.order, d.Index)
		} else {
			tc := a.byIndex[d.Index]
			if d.ID != "" && tc.ID == "" {
				tc.ID = d.ID
			}
			if d.Type != "" && tc.Type == "" {
				tc.Type = d.Type
			}
			if d.Function.Name != "" && tc.Function.Name == "" {
				tc.Function.Name = d.Function.Name
			}
		}
		if d.Function.Arguments != "" {
			a.args[d.Index].WriteString(d.Function.Arguments)
		}
	}
}

func (a *toolCallAssembler) finalize() []ToolCall {
	if a.byIndex == nil {
		return nil
	}
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		tc := *a.byIndex[idx]
		tc.Function.Arguments = a.args[idx].String()
		if tc.Type == "" {
			tc.Type = "function"
		}
		out = append(out, tc)
	}
	return out
}

// thinkSplitter is a 2-state streaming parser for <think>...</think> blocks.
// Outside bytes go to cbContent; inside bytes are counted for budget (not emitted).
// An N-1 byte lookahead handles tags split across SSE deltas.
type thinkSplitter struct {
	inside, budgetHit bool
	pending           []byte
	thinkBytes        int
}

const thinkOpen = "<think>"
const thinkClose = "</think>"

// tagPrefixLen returns the length of the longest prefix of tag that is a suffix of s.
func tagPrefixLen(s []byte, tag string) int {
	for i := min(len(s), len(tag)-1); i > 0; i-- {
		if bytes.HasSuffix(s, []byte(tag[:i])) {
			return i
		}
	}
	return 0
}

func (s *thinkSplitter) countThink(n, budget int) {
	if !s.budgetHit {
		s.thinkBytes += n
		s.budgetHit = budget > 0 && s.thinkBytes > budget
	}
}

func (s *thinkSplitter) feed(data []byte, budget int, cbContent, cbThinking func(string)) {
	s.pending = append(s.pending, data...)
	for {
		tag := thinkOpen
		if s.inside {
			tag = thinkClose
		}
		idx := bytes.Index(s.pending, []byte(tag))
		if idx < 0 {
			keep := tagPrefixLen(s.pending, tag)
			if flush := s.pending[:len(s.pending)-keep]; len(flush) > 0 {
				if s.inside {
					s.countThink(len(flush), budget)
				} else {
					cbContent(string(flush))
				}
			}
			s.pending = s.pending[len(s.pending)-keep:]
			return
		}
		before := s.pending[:idx]
		s.pending = s.pending[idx+len(tag):]
		if s.inside {
			s.countThink(len(before), budget)
			s.inside = false
		} else {
			if len(before) > 0 {
				cbContent(string(before))
			}
			cbThinking("")
			s.inside, s.thinkBytes = true, 0
		}
	}
}

// flush emits any remaining outside-block content at stream end.
func (s *thinkSplitter) flush(cbContent func(string)) {
	if len(s.pending) > 0 && !s.inside {
		cbContent(string(s.pending))
	}
	s.pending = nil
}

// filterValidToolCalls discards tool calls whose assembled arguments are not valid JSON.
func filterValidToolCalls(tcs []ToolCall) []ToolCall {
	var out []ToolCall
	for _, tc := range tcs {
		if s, ok := tc.Function.Arguments.(string); ok && json.Valid([]byte(s)) {
			out = append(out, tc)
		}
	}
	return out
}

// StreamOnce streams a chat completion from /v1/chat/completions via SSE.
// Delta content is routed through the <think> splitter; delta tool_calls are
// reassembled by index into complete ToolCalls.
func (c *Client) StreamOnce(ctx context.Context, model string, messages []Message, tools []ToolDef, opts ChatOptions, cb ChatCallbacks) (text string, toolCalls []ToolCall, budgetExceeded bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutChat)
	defer cancel()
	body, err := json.Marshal(chatRequest{
		Model:              model,
		Messages:           serializeMessages(messages),
		IsStreaming:        true,
		Tools:              tools,
		ResponseFormat:     buildResponseFormat(opts.Format),
		ChatTemplateKwargs: buildTemplateKwargs(opts),
	})
	if err != nil {
		return "", nil, false, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", nil, false, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, false, ctx.Err()
		}
		return "", nil, false, fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", nil, false, parseOpenAIError(resp.StatusCode, errBody)
	}

	const maxBuf = 4 * 1024 * 1024
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, maxBuf), maxBuf)

	var (
		textBuf       strings.Builder
		assembler     toolCallAssembler
		splitter      thinkSplitter
		thinkingFired bool
		done          bool
	)

	cbContent := func(s string) {
		textBuf.WriteString(s)
		if cb.Content != nil {
			cb.Content(s)
		}
	}
	cbThinking := func(s string) {
		thinkingFired = true
		if cb.Thinking != nil {
			cb.Thinking(s)
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue // blank lines are SSE event separators; we dispatch per data: line
		}

		var payload string
		if strings.HasPrefix(line, "data: ") {
			payload = line[6:]
		} else if strings.HasPrefix(line, "data:") {
			payload = line[5:]
		} else {
			continue // ignore event:, id:, comment lines
		}

		if payload == "[DONE]" {
			done = true
			break
		}

		var chunk sseChunk
		if json.Unmarshal([]byte(payload), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		// Server-side qwen3 reasoning parser strips <think> and emits the trace
		// as reasoning_content. Fire the thinking indicator once on first arrival.
		if delta.ReasoningContent != "" && !thinkingFired {
			cbThinking("")
		}
		if delta.Content != "" {
			splitter.feed([]byte(delta.Content), opts.ThinkBudget, cbContent, cbThinking)
		}
		if len(delta.ToolCalls) > 0 {
			assembler.apply(delta.ToolCalls)
		}
	}

	splitter.flush(cbContent)
	budgetExceeded = splitter.budgetHit

	if scanErr := scanner.Err(); scanErr != nil {
		if ctx.Err() != nil {
			return textBuf.String(), nil, budgetExceeded, ctx.Err()
		}
		return "", nil, false, fmt.Errorf("llm stream: %w", scanErr)
	}

	if !done {
		return textBuf.String(), filterValidToolCalls(assembler.finalize()), budgetExceeded,
			fmt.Errorf("llm: stream ended without completion marker")
	}

	return textBuf.String(), assembler.finalize(), budgetExceeded, nil
}

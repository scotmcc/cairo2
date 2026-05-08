package tui

// panel_config_picker.go — model selection picker for the config panel.
// When the user hits Enter on `model`, `embed_model`, `summary_model`, or any
// role:<name>:model row, we fetch the list from Ollama and render a centered
// modal with the options. Esc cancels and returns focus to the field row.

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
)

// keys that trigger the picker instead of free-text edit.
var configPickerKeys = map[string]bool{
	"model":                  true,
	"embed_model":            true,
	"summary_model":          true,
	"consider.model":         true,
	"consider.summary_model": true,
}

// configKeyUsesPicker reports whether a row key should open the model picker.
// Includes the static keys above plus any role:<name>:model row.
func configKeyUsesPicker(key string) bool {
	if configPickerKeys[key] {
		return true
	}
	if _, field, ok := roleRowKey(key); ok && field == "model" {
		return true
	}
	return false
}

// configEnterPicker fetches the model list and switches to picker mode.
// Always reads the current ollama_url from config so changes take effect
// immediately. Returns false if Ollama is unreachable; caller falls back
// to free-text edit.
func configEnterPicker(m *model, configKey, currentValue string) bool {
	url, _ := m.db.Config.Get(config.KeyOllamaURL)
	if url == "" {
		return false
	}
	apiKey, _ := m.db.Config.Get(config.KeyLLMAPIKey)
	if envKey := os.Getenv("LLM_API_KEY"); envKey != "" {
		apiKey = envKey
	}
	client := llm.New(url, apiKey)
	models, err := client.ListModels()
	if err != nil || len(models) == 0 {
		return false
	}
	m.config.picking = true
	m.config.editKey = configKey
	m.config.pickerOptions = models
	m.config.pickerSelected = 0
	for i, name := range models {
		if name == currentValue {
			m.config.pickerSelected = i
			break
		}
	}
	m.config.pickerWindowTop = 0
	pickerScrollIntoView(m)
	return true
}

// pickerVisibleRows is how many option rows the modal shows at once. Long
// model lists scroll inside this window. Sized to fit comfortably on most
// terminal heights without dominating the screen.
const pickerVisibleRows = 14

// pickerScrollIntoView nudges pickerWindowTop so pickerSelected is visible.
func pickerScrollIntoView(m *model) {
	n := len(m.config.pickerOptions)
	if n <= pickerVisibleRows {
		m.config.pickerWindowTop = 0
		return
	}
	if m.config.pickerSelected < m.config.pickerWindowTop {
		m.config.pickerWindowTop = m.config.pickerSelected
	} else if m.config.pickerSelected >= m.config.pickerWindowTop+pickerVisibleRows {
		m.config.pickerWindowTop = m.config.pickerSelected - pickerVisibleRows + 1
	}
	if m.config.pickerWindowTop < 0 {
		m.config.pickerWindowTop = 0
	}
	if maxTop := n - pickerVisibleRows; m.config.pickerWindowTop > maxTop {
		m.config.pickerWindowTop = maxTop
	}
}

// configUpdatePicking handles keys while the modal picker is active.
func configUpdatePicking(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.config.picking = false
		m.config.pickerOptions = nil
		m.config.editKey = ""
		return true, nil

	case "enter":
		if m.config.pickerSelected >= 0 && m.config.pickerSelected < len(m.config.pickerOptions) {
			chosen := m.config.pickerOptions[m.config.pickerSelected]
			if _, _, ok := roleRowKey(m.config.editKey); ok {
				_, _ = roleRowSet(m, m.config.editKey, chosen)
			} else {
				_ = m.db.Config.Set(m.config.editKey, chosen)
			}
		}
		m.config.picking = false
		m.config.editKey = ""
		m.config.pickerOptions = nil
		return true, nil

	case "up", "k":
		if m.config.pickerSelected > 0 {
			m.config.pickerSelected--
		}
		pickerScrollIntoView(m)
		return true, nil

	case "down", "j":
		if m.config.pickerSelected < len(m.config.pickerOptions)-1 {
			m.config.pickerSelected++
		}
		pickerScrollIntoView(m)
		return true, nil

	case "pgup":
		m.config.pickerSelected -= pickerVisibleRows
		if m.config.pickerSelected < 0 {
			m.config.pickerSelected = 0
		}
		pickerScrollIntoView(m)
		return true, nil

	case "pgdown":
		m.config.pickerSelected += pickerVisibleRows
		if last := len(m.config.pickerOptions) - 1; m.config.pickerSelected > last {
			m.config.pickerSelected = last
		}
		pickerScrollIntoView(m)
		return true, nil

	case "home", "g":
		m.config.pickerSelected = 0
		pickerScrollIntoView(m)
		return true, nil

	case "end", "G":
		m.config.pickerSelected = len(m.config.pickerOptions) - 1
		if m.config.pickerSelected < 0 {
			m.config.pickerSelected = 0
		}
		pickerScrollIntoView(m)
		return true, nil
	}
	return true, nil
}

// configRenderPickerModal draws a centered bordered box with the picker
// over the supplied base view. Returns a string sized to the full content
// area (width × height) so it cleanly replaces the underlying region.
func configRenderPickerModal(m *model, width, height int, _ string) string {
	if !m.config.picking {
		return ""
	}

	titleText := "Choose a model"
	if _, _, ok := roleRowKey(m.config.editKey); ok {
		titleText = fmt.Sprintf("Choose a model — %s", m.config.editKey)
	} else if m.config.editKey != "" {
		titleText = fmt.Sprintf("Choose a model — %s", m.config.editKey)
	}

	// Box sizing.
	maxOptW := lipgloss.Width(titleText)
	for _, opt := range m.config.pickerOptions {
		if w := lipgloss.Width(opt); w > maxOptW {
			maxOptW = w
		}
	}
	innerW := maxOptW + 6
	if innerW < 32 {
		innerW = 32
	}
	if innerW > width-8 {
		innerW = width - 8
	}
	if innerW < 16 {
		innerW = 16
	}

	titleStyle := lipgloss.NewStyle().Foreground(colTool).Bold(true).Background(colSurfaceElev)
	hintStyle := lipgloss.NewStyle().Foreground(colTextDim).Background(colSurfaceElev)
	rowStyle := lipgloss.NewStyle().Foreground(colText).Background(colSurfaceElev)
	selStyle := lipgloss.NewStyle().Foreground(colFocus).Background(colSurfaceHi).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim).Background(colSurfaceElev)

	n := len(m.config.pickerOptions)
	start := m.config.pickerWindowTop
	end := start + pickerVisibleRows
	if end > n {
		end = n
	}

	var lines []string
	lines = append(lines, titleStyle.Width(innerW).Padding(0, 1).Render(titleText))
	lines = append(lines, lipgloss.NewStyle().Foreground(colBorderThin).Background(colSurfaceElev).
		Render(strings.Repeat("─", innerW)))

	if start > 0 {
		lines = append(lines, dim.Width(innerW).Padding(0, 2).Render(fmt.Sprintf("↑ %d more above", start)))
	}
	for i := start; i < end; i++ {
		opt := m.config.pickerOptions[i]
		display := opt
		if lipgloss.Width(display) > innerW-4 {
			display = truncate(display, innerW-4)
		}
		if i == m.config.pickerSelected {
			lines = append(lines, selStyle.Width(innerW).Padding(0, 1).Render("▸ "+display))
		} else {
			lines = append(lines, rowStyle.Width(innerW).Padding(0, 1).Render("  "+display))
		}
	}
	if end < n {
		lines = append(lines, dim.Width(innerW).Padding(0, 2).Render(fmt.Sprintf("↓ %d more below", n-end)))
	}

	footHint := "↑↓ select · enter confirm · esc cancel"
	if n > pickerVisibleRows {
		footHint += " · PgUp/PgDn page"
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(colBorderThin).Background(colSurfaceElev).
		Render(strings.Repeat("─", innerW)))
	lines = append(lines, hintStyle.Width(innerW).Padding(0, 1).Render(footHint))

	body := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorderElev).
		Background(colSurfaceElev).
		Render(body)

	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceBackground(colBg),
	)
}

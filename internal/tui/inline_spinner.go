package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InlineSpinner renders an animated spinner + text on the last line of the
// output viewport. Use it for any async operation that needs a loading state.
//
// Usage:
//  1. Call StartInlineSpinner(text) — appends the initial spinner line and
//     returns a tea.Cmd that ticks the animation.
//  2. Handle inlineSpinnerTickMsg in Update — calls TickInlineSpinner which
//     updates the last output line with the next frame.
//  3. When the async result arrives, call StopInlineSpinner() to clear the
//     active spinner, then ReplaceSpinnerLine with your final output.
//  4. While still active, call TransitionSpinnerText(text) to change the
//     text in-place — the tick chain continues uninterrupted on the same line.

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// inlineSpinnerTickMsg is sent on each animation frame.
type inlineSpinnerTickMsg struct {
	id int // matches the active spinner ID to avoid stale ticks
}

// InlineSpinnerState tracks the active inline spinner.
type InlineSpinnerState struct {
	active           bool
	id               int
	frame            int
	text             string
	spinnerLineOwned bool // true if the last output line belongs to the spinner
}

// StartInlineSpinner begins an animated spinner on the last output line.
// Returns a tea.Cmd to start ticking.
func (m *Model) StartInlineSpinner(text string) tea.Cmd {
	m.inlineSpinner.active = true
	m.inlineSpinner.id++
	m.inlineSpinner.frame = 0
	m.inlineSpinner.text = text
	m.inlineSpinner.spinnerLineOwned = true

	theme := m.getTheme()
	line := renderSpinnerLine(theme.Gradient1, spinnerFrames[0], text)
	// Append directly without wrapping — spinner lines must be a single output entry
	// so TickInlineSpinner and ReplaceSpinnerLine can reliably target the last line.
	m.output = append(m.output, line)
	m.viewport.SetContent(joinLines(m.output))
	m.viewport.GotoBottom()

	id := m.inlineSpinner.id
	return tickInlineSpinner(id)
}

// TickInlineSpinner advances the spinner animation. Call from Update when
// receiving inlineSpinnerTickMsg.
func (m *Model) TickInlineSpinner(msg inlineSpinnerTickMsg) tea.Cmd {
	if !m.inlineSpinner.active || msg.id != m.inlineSpinner.id {
		return nil // stale tick, ignore
	}

	m.inlineSpinner.frame = (m.inlineSpinner.frame + 1) % len(spinnerFrames)
	theme := m.getTheme()
	line := renderSpinnerLine(theme.Gradient1, spinnerFrames[m.inlineSpinner.frame], m.inlineSpinner.text)

	// Replace the last output line with the updated frame.
	if len(m.output) > 0 && m.inlineSpinner.spinnerLineOwned {
		m.output[len(m.output)-1] = line
		m.viewport.SetContent(joinLines(m.output))
		m.viewport.GotoBottom()
	}

	return tickInlineSpinner(m.inlineSpinner.id)
}

// StopInlineSpinner cancels the active spinner. The last spinner line
// remains in the output — the caller should replace or append below it.
func (m *Model) StopInlineSpinner() {
	m.inlineSpinner.active = false
}

// TransitionSpinnerText changes the text of the active spinner in-place,
// updating the last output line without creating a new line or restarting
// the tick chain. If the spinner is inactive, restarts it with the new text.
func (m *Model) TransitionSpinnerText(text string) {
	if !m.inlineSpinner.active {
		// Spinner was stopped (e.g. by a stale event) — restart it.
		// The caller still expects animation to continue.
		m.inlineSpinner.active = true
		m.inlineSpinner.id++
		m.inlineSpinner.frame = 0
		m.inlineSpinner.text = text
		// Don't append a new line — the last line should still be the spinner's.
		if m.inlineSpinner.spinnerLineOwned && len(m.output) > 0 {
			theme := m.getTheme()
			line := renderSpinnerLine(theme.Gradient1, spinnerFrames[0], text)
			m.output[len(m.output)-1] = line
			m.viewport.SetContent(joinLines(m.output))
			m.viewport.GotoBottom()
		}
		return
	}
	m.inlineSpinner.text = text
	theme := m.getTheme()
	line := renderSpinnerLine(theme.Gradient1, spinnerFrames[m.inlineSpinner.frame], text)
	if len(m.output) > 0 && m.inlineSpinner.spinnerLineOwned {
		m.output[len(m.output)-1] = line
		m.viewport.SetContent(joinLines(m.output))
		m.viewport.GotoBottom()
	}
}

// ReplaceSpinnerLine replaces the spinner line with a new string.
// Useful for swapping the spinner with a final "✓ Done" result.
// If the spinner doesn't own the last line, appends instead.
func (m *Model) ReplaceSpinnerLine(line string) {
	if len(m.output) > 0 && m.inlineSpinner.spinnerLineOwned {
		m.output = m.output[:len(m.output)-1]
		m.output = append(m.output, m.wrapOutputLine(line)...)
		m.inlineSpinner.spinnerLineOwned = false
		m.viewport.SetContent(joinLines(m.output))
		m.viewport.GotoBottom()
	} else {
		// Spinner line was already replaced or never started — just append.
		m.appendOutput(line)
	}
}

func tickInlineSpinner(id int) tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return inlineSpinnerTickMsg{id: id}
	})
}

func renderSpinnerLine(color lipgloss.Color, frame, text string) string {
	s := lipgloss.NewStyle().Foreground(color)
	return s.Render("  " + frame + " " + text)
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

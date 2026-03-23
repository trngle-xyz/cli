package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	// Minimum size guard — show a simple message instead of a broken layout.
	if m.width < minTermWidth || m.height < minTermHeight {
		msg := lipgloss.NewStyle().Foreground(muted).Render(
			fmt.Sprintf("Terminal too small (%d×%d)\nResize to at least %d×%d", m.width, m.height, minTermWidth, minTermHeight))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
	}

	if m.setupMode {
		return m.setup.View(m.theme)
	}

	theme := m.getTheme()

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtle).
		Padding(0, 1)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.Gradient1)

	statusStyle := lipgloss.NewStyle().
		Foreground(muted).
		Padding(0, 1)

	title := titleStyle.Render("▲ TRNGLE")

	var statusIndicator string
	if m.loading {
		statusIndicator = m.spinner.View() + " " + m.t("connecting")
	} else if m.connected {
		statusIndicator = lipgloss.NewStyle().Foreground(special).Render("●") + " " + truncateAddress(m.walletAddr)
	} else {
		statusIndicator = lipgloss.NewStyle().Foreground(errorC).Render("○") + " " + m.t("disconnected")
	}
	status := statusStyle.Render(statusIndicator)

	spacerWidth := m.width - lipgloss.Width(title) - lipgloss.Width(status) - 2
	if spacerWidth < 1 {
		spacerWidth = 1
	}

	headerContent := lipgloss.JoinHorizontal(
		lipgloss.Center,
		title,
		lipgloss.NewStyle().Width(spacerWidth).Render(""),
		status,
	)

	headerW := m.width - 2
	header := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(subtle).
		Width(headerW).
		MaxWidth(headerW).
		Render(headerContent)

	var scrollIndicator string
	if m.viewportReady && m.viewport.ScrollPercent() < 1.0 {
		scrollIndicator = lipgloss.NewStyle().Foreground(theme.Gradient1).Render(fmt.Sprintf(" ↑%.0f%% ", m.viewport.ScrollPercent()*100))
	}

	var output string
	if m.viewportReady {
		output = m.renderViewportClipped()
	} else {
		outputHeight := m.height - 7
		if outputHeight < 5 {
			outputHeight = 5
		}
		output = strings.Repeat("\n", outputHeight)
	}

	inputBoxW := m.width - 4
	inputBox := borderStyle.
		Width(inputBoxW).
		MaxWidth(inputBoxW).
		Render(m.input.View())

	timeStr := m.currentTime.Format("15:04:05")
	dateStr := m.currentTime.Format("Mon, Jan 2 2006")

	rightFooter := lipgloss.NewStyle().Foreground(muted).Render(dateStr) + "  " + lipgloss.NewStyle().Foreground(theme.Gradient1).Render(timeStr)

	// Build left footer, dropping segments if the terminal is narrow.
	footerAvail := m.width - lipgloss.Width(rightFooter) - lipgloss.Width(scrollIndicator) - 6
	fullLeft := m.t("footerQuit") + " • " + m.t("footerClear") + " • " + m.t("footerHistory") + " • " + m.t("footerScroll")
	shortLeft := m.t("footerQuit") + " • " + m.t("footerClear")
	leftText := fullLeft
	if footerAvail < len(fullLeft) {
		leftText = shortLeft
	}
	if footerAvail < len(shortLeft) {
		leftText = m.t("footerQuit")
	}
	leftFooter := lipgloss.NewStyle().Foreground(muted).Render(leftText)

	gap := m.width - lipgloss.Width(leftFooter) - lipgloss.Width(scrollIndicator) - lipgloss.Width(rightFooter) - 4
	if gap < 1 {
		gap = 1
	}

	footerW := m.width - 2
	footer := lipgloss.NewStyle().
		Width(footerW).
		MaxWidth(footerW).
		Padding(0, 1).
		Render(leftFooter + lipgloss.NewStyle().Width(gap/2).Render("") + scrollIndicator + lipgloss.NewStyle().Width(gap/2).Render("") + rightFooter)

	mainView := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		output,
		inputBox,
		footer,
	)

	if m.confirmQuit {
		border := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(warning).
			Padding(1, 2)
		title := lipgloss.NewStyle().Bold(true).Foreground(warning).Render(m.t("confirmQuitTitle"))
		msg := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(m.t("confirmQuitMessage"))
		hint := lipgloss.NewStyle().Foreground(muted).Render(m.t("confirmQuitHint"))
		card := border.Render(strings.Join([]string{title, "", msg, "", hint}, "\n"))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
	}

	if m.settings.IsVisible() {
		mainLines := strings.Split(mainView, "\n")
		popupView := m.settings.View()
		popupLines := strings.Split(popupView, "\n")

		for len(mainLines) < m.height {
			mainLines = append(mainLines, strings.Repeat(" ", m.width))
		}

		inPopup := false
		for i, line := range popupLines {
			if strings.TrimSpace(line) != "" {
				inPopup = true
			}
			if inPopup && i < len(mainLines) {
				mainLines[i] = line
			}
		}

		if len(mainLines) > m.height {
			mainLines = mainLines[:m.height]
		}

		return strings.Join(mainLines, "\n")
	}

	return mainView
}

func truncateAddress(addr string) string {
	if len(addr) <= 16 {
		return addr
	}
	return addr[:8] + "..." + addr[len(addr)-6:]
}

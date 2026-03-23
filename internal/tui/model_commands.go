package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/trngle-xyz/cli/internal/history"
)

func (m *Model) executeCommand(cmd string) tea.Cmd {
	m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render("❯ " + cmd))

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	theme := m.getTheme()

	switch strings.ToLower(parts[0]) {
	case "help", "h", "?":
		m.appendOutput(
			"",
			lipgloss.NewStyle().Bold(true).Foreground(theme.Gradient1).Render(m.t("availableCommands")),
			"",
			"  help, h, ?        "+m.t("helpShowHelp"),
			"  status            "+m.t("helpStatus"),
			"  balance, bal      "+m.t("helpBalance"),
			"  address, addr     "+m.t("helpAddress"),
			"  quote <amt> <from> to <to>  Swap assets (e.g., quote 100 CC to CBTC)",
			"  trades            "+m.t("helpTrades")+" (--failed, --settled, --pending)",
			"  trades <id>       Show trade detail with event timeline",
			"  settings          "+m.t("helpSettings"),
			"  theme [name]      "+m.t("helpTheme"),
			"  lang [code]       "+m.t("helpLang"),
			"  clear, cls        "+m.t("helpClear"),
			"  quit, exit        "+m.t("helpQuit"),
			"",
			lipgloss.NewStyle().Foreground(muted).Render("  "+m.t("footerClear")+" • "+m.t("footerQuit")+" • "+m.t("footerHistory")),
			"",
		)

	case "status":
		m.output = []string{}
		m.showBanner()

	case "balance", "bal":
		if !m.connected {
			m.appendOutput(lipgloss.NewStyle().Foreground(warning).Render(m.t("notConnected")))
			break
		}
		balancePartyHint := ""
		if m.quote.Quote != nil && m.quote.Quote.AcceptContext != nil {
			balancePartyHint = strings.TrimSpace(m.quote.Quote.AcceptContext.TakerParty)
		}
		m.appendOutput("")
		spinnerCmd := m.StartInlineSpinner("Fetching...")
		return tea.Batch(spinnerCmd, fetchBalancesCmd(m.cfg, balancePartyHint))

	case "address", "addr":
		if m.walletAddr != "" {
			m.appendOutput(fmt.Sprintf("%s: %s", m.t("address"), m.walletAddr))
		} else {
			m.appendOutput(lipgloss.NewStyle().Foreground(warning).Render(m.t("notConnected")))
		}

	case "quote", "swap":
		amount, fromAsset, toAsset, parseErr := ParseQuoteCommand(parts)
		if parseErr != "" {
			for _, line := range strings.Split(parseErr, "\n") {
				m.appendOutput(lipgloss.NewStyle().Foreground(warning).Render(line))
			}
			return nil
		}

		valid, validErr := ValidateSwapAssets(fromAsset, toAsset)
		if !valid {
			m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render(validErr))
			return nil
		}

		m.quote.SetTheme(m.theme)
		m.quote.SetLang(m.lang)
		m.appendOutput("")
		spinnerCmd := m.StartInlineSpinner(m.t("generatingQuote"))
		return tea.Batch(spinnerCmd, prefetchQuoteCmd(m.cfg, fromAsset, toAsset, amount, m.walletAddr))

	case "trades":
		m.renderTradesCommand(parts)

	case "theme":
		if len(parts) < 2 {
			m.appendOutput("")
			m.appendOutput(lipgloss.NewStyle().Bold(true).Foreground(theme.Gradient1).Render(m.t("availableThemes") + ":"))
			m.appendOutput("")
			for name, t := range themes {
				indicator := "  "
				if name == m.theme {
					indicator = lipgloss.NewStyle().Foreground(special).Render("● ")
				}
				m.appendOutput(indicator + lipgloss.NewStyle().Foreground(t.Gradient1).Render(name) + " - " + t.Name)
			}
			m.appendOutput("")
			m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render(m.t("currentTheme") + ": " + themes[m.theme].Name))
			m.appendOutput("")
		} else {
			themeName := strings.ToLower(parts[1])
			if _, ok := themes[themeName]; ok {
				m.theme = themeName
				m.input.PromptStyle = lipgloss.NewStyle().Foreground(themes[themeName].Gradient1).Bold(true)
				m.spinner.Style = lipgloss.NewStyle().Foreground(themes[themeName].Gradient1)
				m.appendOutput(lipgloss.NewStyle().Foreground(special).Render("✓ " + m.t("themeChanged") + " " + themes[themeName].Name))
				m.output = []string{}
				m.showBanner()
			} else {
				m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render(m.t("unknownTheme") + ": " + themeName))
				m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render(m.t("useThemeCmd")))
			}
		}

	case "lang", "language":
		allLangs := GetAllLanguages()
		if len(parts) < 2 {
			m.appendOutput("")
			m.appendOutput(lipgloss.NewStyle().Bold(true).Foreground(theme.Gradient1).Render(m.t("availableLangs") + ":"))
			m.appendOutput("")
			for code, lang := range allLangs {
				indicator := "  "
				if code == m.lang {
					indicator = lipgloss.NewStyle().Foreground(special).Render("● ")
				}
				m.appendOutput(indicator + lipgloss.NewStyle().Foreground(theme.Gradient2).Render(code) + " - " + lang.Name)
			}
			m.appendOutput("")
			m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render(m.t("currentLang") + ": " + GetLanguage(m.lang).Name))
			m.appendOutput("")
		} else {
			langCode := strings.ToLower(parts[1])
			if _, ok := allLangs[langCode]; ok {
				m.lang = langCode
				m.appendOutput(lipgloss.NewStyle().Foreground(special).Render("✓ " + m.t("langChanged") + " " + GetLanguage(langCode).Name))
				m.output = []string{}
				m.showBanner()
			} else {
				m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render(m.t("unknownLang") + ": " + langCode))
				m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render(m.t("useLangCmd")))
			}
		}

	case "settings":
		m.settings.SetTheme(m.theme)
		m.settings.SetLang(m.lang)
		m.settings.SetSize(m.width, m.height)
		m.settings.Show()
		m.input.Blur()

	case "clear", "cls":
		m.output = []string{}
		m.showBanner()

	case "quit", "exit":
		m.appendOutput(m.t("useCtrlCToExit"))

	default:
		m.appendOutput(
			lipgloss.NewStyle().Foreground(errorC).Render(m.t("unknownCommand")+": "+parts[0]),
			lipgloss.NewStyle().Foreground(muted).Render(m.t("typeHelp")),
		)
	}

	return nil
}

// renderTradesCommand handles the "trades" command with optional filters and detail view.
//
//	trades              — last 20 trades, numbered
//	trades --failed     — only failed/sign_failed/context_failed
//	trades --settled    — only settled
//	trades --pending    — only pending/submitted (in-flight)
//	trades <number>     — detail view by index from last listing
//	trades <id>         — detail view by full ID
func (m *Model) renderTradesCommand(parts []string) {
	if m.historyDB == nil {
		m.appendOutput(
			"",
			lipgloss.NewStyle().Foreground(warning).Render("  Trade history database not available"),
			"",
		)
		return
	}

	theme := m.getTheme()
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Gradient1)
	dimStyle := lipgloss.NewStyle().Foreground(muted)
	sepStyle := lipgloss.NewStyle().Foreground(subtle)
	numStyle := lipgloss.NewStyle().Foreground(theme.Gradient2).Bold(true)

	// Detail view: trades <number> or trades <id>
	if len(parts) >= 2 && !strings.HasPrefix(parts[1], "-") {
		arg := parts[1]
		// Check if it's a number referencing the last listing.
		if idx, err := parseTradeIndex(arg); err == nil {
			if idx >= 1 && idx <= len(m.lastTradeIDs) {
				m.renderTradeDetail(m.lastTradeIDs[idx-1])
				return
			}
			m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render(
				fmt.Sprintf("  No trade #%d — run trades first to see the list", idx)))
			return
		}
		m.renderTradeDetail(arg)
		return
	}

	// Parse filter flags.
	statusFilter := ""
	title := m.t("recentTrades")
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "--failed", "-f":
			statusFilter = "failed"
			title = "Failed Trades"
		case "--settled", "-s":
			statusFilter = "settled"
			title = "Settled Trades"
		case "--pending", "-p":
			statusFilter = "pending"
			title = "In-Flight Trades"
		}
	}

	// Query the DB.
	var txType, asset string
	limit := 20
	txs, total, err := m.historyDB.List(limit, 0, txType, asset)
	if err != nil {
		m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  Error reading trade history: "+err.Error()))
		return
	}

	// Apply status filter.
	if statusFilter != "" {
		var filtered []history.Tx
		for _, tx := range txs {
			if matchesStatusFilter(tx.Status, statusFilter) {
				filtered = append(filtered, tx)
			}
		}
		txs = filtered
	}

	// Store IDs for numbered lookup.
	m.lastTradeIDs = make([]string, len(txs))
	for i, tx := range txs {
		m.lastTradeIDs[i] = tx.ID
	}

	m.appendOutput("")
	m.appendOutput(headerStyle.Render("  " + title))
	m.appendOutput("")

	if len(txs) == 0 {
		m.appendOutput(dimStyle.Render("  " + m.t("noTradesFound")))
		m.appendOutput("")
		return
	}

	for i, tx := range txs {
		statusIcon, statusStyle := tradeStatusStyle(tx.Status)
		from := deref(tx.FromAsset, "?")
		to := deref(tx.ToAsset, "?")
		fromAmt := deref(tx.FromAmount, "")
		toAmt := deref(tx.ToAmount, "")
		pair := truncateAmount(fromAmt) + " " + from + " → " + truncateAmount(toAmt) + " " + to
		age := formatAge(tx.CreatedAt)

		num := numStyle.Render(fmt.Sprintf("%2d)", i+1))

		line := fmt.Sprintf("  %s %s  %s  %s",
			num,
			statusStyle.Render(statusIcon+" "+padRight(tx.Status, 14)),
			pair,
			dimStyle.Render(age),
		)
		m.appendOutput(line)

		// Show error inline for failed trades.
		if tx.ErrorMessage != nil && *tx.ErrorMessage != "" {
			errSnippet := *tx.ErrorMessage
			if len(errSnippet) > 60 {
				errSnippet = errSnippet[:60] + "…"
			}
			m.appendOutput("      " + lipgloss.NewStyle().Foreground(errorC).Render(errSnippet))
		}

		if i < len(txs)-1 {
			m.appendOutput("")
		}
	}

	m.appendOutput("")
	m.appendOutput(sepStyle.Render("  " + strings.Repeat("─", 50)))
	countInfo := fmt.Sprintf("  Showing %d", len(txs))
	if total > len(txs) {
		countInfo += fmt.Sprintf(" of %d", total)
	}
	countInfo += " trades"
	m.appendOutput(dimStyle.Render(countInfo))
	m.appendOutput(dimStyle.Render("  Use: trades <number> for details"))
	m.appendOutput("")
}

func parseTradeIndex(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return 0, fmt.Errorf("not a number")
	}
	return n, nil
}

// renderTradeDetail shows full details for a single trade including event timeline.
func (m *Model) renderTradeDetail(id string) {
	theme := m.getTheme()
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Gradient1)
	labelStyle := lipgloss.NewStyle().Foreground(muted)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	sepStyle := lipgloss.NewStyle().Foreground(subtle)
	dimStyle := lipgloss.NewStyle().Foreground(muted)

	// Try exact match first, then prefix match.
	tx, err := m.historyDB.Get(id)
	if err != nil {
		m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  Error: "+err.Error()))
		return
	}
	if tx == nil {
		// Try prefix search — user might type partial ID.
		txs, _, _ := m.historyDB.List(100, 0, "", "")
		for i := range txs {
			if strings.HasPrefix(txs[i].ID, id) {
				tx = &txs[i]
				break
			}
		}
	}
	if tx == nil {
		m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  Trade not found: "+id))
		return
	}

	statusIcon, statusStyle := tradeStatusStyle(tx.Status)

	m.appendOutput("")
	m.appendOutput(headerStyle.Render("  Trade Detail"))
	m.appendOutput(sepStyle.Render("  " + strings.Repeat("─", 50)))
	m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("ID", 14)), valueStyle.Render(tx.ID)))
	m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Status", 14)), statusStyle.Render(statusIcon+" "+tx.Status)))
	m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Created", 14)), valueStyle.Render(formatTimestamp(tx.CreatedAt))))

	if tx.QuoteID != nil {
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Quote ID", 14)), valueStyle.Render(*tx.QuoteID)))
	}
	if tx.TradeID != nil {
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Trade ID", 14)), valueStyle.Render(*tx.TradeID)))
	}
	if tx.TradeCID != nil {
		cid := *tx.TradeCID
		if len(cid) > 40 {
			cid = cid[:40] + "…"
		}
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Trade CID", 14)), dimStyle.Render(cid)))
	}

	m.appendOutput(sepStyle.Render("  " + strings.Repeat("─", 50)))

	from := deref(tx.FromAsset, "?")
	to := deref(tx.ToAsset, "?")
	fromAmt := deref(tx.FromAmount, "?")
	toAmt := deref(tx.ToAmount, "?")
	rate := deref(tx.Rate, "?")

	m.appendOutput(fmt.Sprintf("  %s  %s %s", labelStyle.Render(padRight("Send", 14)), valueStyle.Render(fromAmt), valueStyle.Render(from)))
	m.appendOutput(fmt.Sprintf("  %s  %s %s", labelStyle.Render(padRight("Receive", 14)), lipgloss.NewStyle().Foreground(theme.Gradient1).Render(toAmt), lipgloss.NewStyle().Foreground(theme.Gradient2).Render(to)))
	m.appendOutput(fmt.Sprintf("  %s  1 %s = %s %s", labelStyle.Render(padRight("Rate", 14)), from, rate, to))

	if tx.Counterparty != nil {
		cp := *tx.Counterparty
		if len(cp) > 40 {
			cp = cp[:40] + "…"
		}
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Counterparty", 14)), dimStyle.Render(cp)))
	}

	if tx.SettledAt != nil {
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Settled At", 14)), valueStyle.Render(formatTimestamp(*tx.SettledAt))))
	}

	// Error details.
	if tx.ErrorMessage != nil && *tx.ErrorMessage != "" {
		m.appendOutput(sepStyle.Render("  " + strings.Repeat("─", 50)))
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Error", 14)), lipgloss.NewStyle().Foreground(errorC).Render(*tx.ErrorMessage)))
		if tx.ErrorPhase != nil {
			m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Phase", 14)), valueStyle.Render(*tx.ErrorPhase)))
		}
	}

	// Event timeline.
	events, err := m.historyDB.GetEvents(tx.ID)
	if err == nil && len(events) > 0 {
		m.appendOutput(sepStyle.Render("  " + strings.Repeat("─", 50)))
		m.appendOutput(labelStyle.Render("  Event Timeline:"))
		m.appendOutput("")
		for _, evt := range events {
			evtIcon := "•"
			evtStyle := dimStyle
			switch evt.EventType {
			case "trade_settled":
				evtIcon = "✓"
				evtStyle = lipgloss.NewStyle().Foreground(special)
			case "trade_cleanup_complete", "trade_monitor_cleanup_complete":
				evtIcon = "↩"
				evtStyle = lipgloss.NewStyle().Foreground(warning)
			case "confirm_verification_failed", "trade_cleanup_failed":
				evtIcon = "✗"
				evtStyle = lipgloss.NewStyle().Foreground(errorC)
			case "maker_confirmed":
				evtIcon = "✓"
			case "taker_confirmed":
				evtIcon = "✓"
			case "trade_expired":
				evtIcon = "⏱"
				evtStyle = lipgloss.NewStyle().Foreground(warning)
			}
			age := formatAge(evt.CreatedAt)
			m.appendOutput(fmt.Sprintf("  %s %s  %s",
				evtStyle.Render(evtIcon),
				evtStyle.Render(padRight(evt.EventType, 32)),
				dimStyle.Render(age),
			))
		}
	}

	if tx.FinalEvent != nil {
		m.appendOutput(fmt.Sprintf("  %s  %s", labelStyle.Render(padRight("Final Event", 14)), valueStyle.Render(*tx.FinalEvent)))
	}
	m.appendOutput(fmt.Sprintf("  %s  %d", labelStyle.Render(padRight("Event Count", 14)), tx.EventCount))

	m.appendOutput(sepStyle.Render("  " + strings.Repeat("─", 50)))
	m.appendOutput("")
}

// --- formatting helpers ---

func tradeStatusStyle(status string) (string, lipgloss.Style) {
	switch status {
	case "settled":
		return "✓", lipgloss.NewStyle().Foreground(special)
	case "failed", "sign_failed", "context_failed":
		return "✗", lipgloss.NewStyle().Foreground(errorC)
	case "refunded":
		return "↩", lipgloss.NewStyle().Foreground(warning)
	case "expired":
		return "⏱", lipgloss.NewStyle().Foreground(warning)
	case "pending", "submitted":
		return "◐", lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	default:
		return "?", lipgloss.NewStyle().Foreground(muted)
	}
}

func matchesStatusFilter(status, filter string) bool {
	switch filter {
	case "failed":
		return status == "failed" || status == "sign_failed" || status == "context_failed"
	case "settled":
		return status == "settled"
	case "pending":
		return status == "pending" || status == "submitted"
	default:
		return true
	}
}

func deref(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func truncateAmount(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// parseTimestamp parses an RFC3339 or RFC3339Nano timestamp string.
func parseTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	return t, err
}

func formatAge(timestamp string) string {
	t, err := parseTimestamp(timestamp)
	if err != nil {
		return timestamp
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("Jan 02 15:04")
	}
}

func formatTimestamp(ts string) string {
	t, err := parseTimestamp(ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02 15:04:05")
}

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/trngle-xyz/cli/internal/core"
)

type QuoteState int

const (
	QuoteStateNone QuoteState = iota
	QuoteStateAwaitingConfirm
	QuoteStateExecuting
)

// SupportedAssets is the canonical list of tradeable assets.
var SupportedAssets = []string{"CC", "CBTC", "USDXLR"}

// supportedAssetSet is built from SupportedAssets for O(1) lookup.
var supportedAssetSet = func() map[string]bool {
	m := make(map[string]bool, len(SupportedAssets))
	for _, a := range SupportedAssets {
		m[a] = true
	}
	return m
}()

// supportedAssetsStr is the comma-separated list for user-facing messages.
var supportedAssetsStr = strings.Join(SupportedAssets, ", ")

const smallRateThreshold = 0.0001

type ActiveQuote struct {
	ID            string
	FromAsset     string
	ToAsset       string
	FromAmount    string
	ToAmount      string
	Price         string
	ExpiresAt     time.Time
	TTLSeconds    int
	AcceptContext *core.AcceptContext
}

type QuoteManager struct {
	State            QuoteState
	Quote            *ActiveQuote
	StatusStep       int
	StatusTickCount  int
	StatusFrameIndex int
	theme            string
	lang             string

	// Real-time operator notifications
	notifyClient    *core.NotifyClient
	eventCh         chan core.TradeEvent
	pendingEvents   []core.TradeEvent // events received, waiting for next tick to process
	waitingForEvent bool              // true = spinner is waiting for a real event to advance
	activeTradeID   string            // trade ID of the current in-flight trade (for event filtering)
}

type quoteCountdownTickMsg struct{}

type quoteExpiredMsg struct{}

type quoteStatusTickMsg struct{}

type quoteCompleteMsg struct{}

type quoteCancelledMsg struct{}

// tradeEventMsg wraps a real-time notification from the operator.
type tradeEventMsg struct {
	event core.TradeEvent
}

type flowStep struct {
	pending string
	success string
	failure string
}

func NewQuoteManager() QuoteManager {
	return QuoteManager{
		State: QuoteStateNone,
		theme: "cyan",
		lang:  "en",
	}
}

func (q *QuoteManager) SetTheme(theme string) {
	q.theme = theme
}

func (q *QuoteManager) SetLang(lang string) {
	q.lang = lang
}

func (q *QuoteManager) t(key string) string {
	lang := GetLanguage(q.lang)
	if text, ok := lang.Text[key]; ok {
		return text
	}
	return GetLanguage("en").Text[key]
}

func (q *QuoteManager) getTheme() ColorTheme {
	if theme, ok := themes[q.theme]; ok {
		return theme
	}
	return themes["cyan"]
}

func (q *QuoteManager) IsAwaitingInput() bool {
	return q.State == QuoteStateAwaitingConfirm
}

func (q *QuoteManager) IsExecuting() bool {
	return q.State == QuoteStateExecuting
}

func (q *QuoteManager) Cancel() {
	q.State = QuoteStateNone
	q.Quote = nil
	q.activeTradeID = ""
	q.pendingEvents = nil
}

func (q *QuoteManager) HasExecutablePayload() bool {
	// AcceptContext is fetched lazily when the user presses Y; we only
	// need a valid quote ID to proceed.
	return q.Quote != nil && strings.TrimSpace(q.Quote.ID) != ""
}

func (q *QuoteManager) SetFromPrefetch(
	quote *core.Quote,
	acceptCtx *core.AcceptContext,
) {
	if quote == nil {
		q.Quote = nil
		return
	}
	q.Quote = &ActiveQuote{
		ID:            quote.ID,
		FromAsset:     quote.FromAsset,
		ToAsset:       quote.ToAsset,
		FromAmount:    quote.FromAmount,
		ToAmount:      quote.ToAmount,
		Price:         quote.Rate,
		ExpiresAt:     quote.ExpiresAt,
		TTLSeconds:    quote.TTLSeconds,
		AcceptContext: acceptCtx,
	}
}

func (q *QuoteManager) GetRemainingSeconds() int {
	if q.Quote == nil {
		return 0
	}
	remaining := int(time.Until(q.Quote.ExpiresAt).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (q *QuoteManager) IsExpired() bool {
	if q.Quote == nil {
		return true
	}
	return time.Now().After(q.Quote.ExpiresAt)
}

func (q *QuoteManager) StartCountdown() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return quoteCountdownTickMsg{}
	})
}

func (q *QuoteManager) HandleCountdownTick() (bool, tea.Cmd) {
	if q.State != QuoteStateAwaitingConfirm || q.Quote == nil {
		return false, nil
	}

	if q.IsExpired() {
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		return true, func() tea.Msg { return quoteExpiredMsg{} }
	}

	return false, q.StartCountdown()
}

// formatRate formats a price rate for display, using more decimal places for small values.
func formatRate(rate float64) string {
	if rate < smallRateThreshold {
		return fmt.Sprintf("%.8f", rate)
	}
	return fmt.Sprintf("%.4f", rate)
}

func (q *QuoteManager) AcceptQuote() tea.Cmd {
	q.State = QuoteStateExecuting
	q.StatusStep = 0
	q.StatusTickCount = 0
	q.StatusFrameIndex = 0
	return q.tickStatus()
}

func (q *QuoteManager) StartPostSignFlow() tea.Cmd {
	q.State = QuoteStateExecuting
	q.StatusStep = 1
	q.StatusTickCount = 0
	q.StatusFrameIndex = 0
	q.waitingForEvent = true
	q.pendingEvents = nil
	if q.Quote != nil && q.Quote.AcceptContext != nil {
		q.activeTradeID = q.Quote.AcceptContext.TradeID
	}

	// Listen for operator events — the Model's inline spinner handles animation.
	// If no WebSocket, fall back to tick-based animation.
	if q.eventCh == nil {
		return q.tickStatus()
	}
	return q.listenForEvent()
}

// HasNotifications returns true if a WebSocket event channel is available.
func (q *QuoteManager) HasNotifications() bool {
	return q.eventCh != nil
}

// ConsumeEvent pops the next buffered event, if any.
func (q *QuoteManager) ConsumeEvent() (core.TradeEvent, bool) {
	if len(q.pendingEvents) == 0 {
		return core.TradeEvent{}, false
	}
	evt := q.pendingEvents[0]
	q.pendingEvents = q.pendingEvents[1:]
	return evt, true
}

// ConnectNotifications establishes a WebSocket to the operator for real-time trade events.
// Should be called once when the TUI starts, before any trade flow.
func (q *QuoteManager) ConnectNotifications(operatorURL, partyID string) {
	if operatorURL == "" || partyID == "" || strings.ToLower(operatorURL) == "mock" {
		return
	}
	q.eventCh = make(chan core.TradeEvent, 16)
	q.notifyClient = core.NewNotifyClient(operatorURL, "taker", partyID, func(evt core.TradeEvent) {
		select {
		case q.eventCh <- evt:
		default:
			// Drop if channel is full — UI will catch up
		}
	})
	q.notifyClient.Connect()
}

// CloseNotifications shuts down the WebSocket client.
func (q *QuoteManager) CloseNotifications() {
	if q.notifyClient != nil {
		q.notifyClient.Close()
		q.notifyClient = nil
	}
}

// listenForEvent returns a bubbletea Cmd that blocks until an event arrives on the channel.
func (q *QuoteManager) listenForEvent() tea.Cmd {
	if q.eventCh == nil {
		return nil
	}
	ch := q.eventCh
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return tradeEventMsg{event: evt}
	}
}

func (q *QuoteManager) tickStatus() tea.Cmd {
	return tea.Tick(time.Millisecond*120, func(t time.Time) tea.Msg {
		return quoteStatusTickMsg{}
	})
}

func (q *QuoteManager) HandleStatusTick() ([]string, string, bool, tea.Cmd) {
	theme := q.getTheme()

	pendingStyle := lipgloss.NewStyle().Foreground(theme.Gradient1)
	intermediateSuccessStyle := lipgloss.NewStyle().Foreground(muted)
	finalSuccessStyle := lipgloss.NewStyle().Foreground(special)
	failStyle := lipgloss.NewStyle().Foreground(errorC)

	steps := []flowStep{
		{pending: q.t("signingSettlementPending"), success: q.t("settlementSigned"), failure: q.t("failedToSignSettlement")},
		{pending: q.t("makerConfirmingPending"), success: q.t("makerConfirmed"), failure: q.t("solverTimeout")},
		{pending: q.t("awaitingSettlementPending"), success: q.t("settled"), failure: q.t("failedToSettle")},
		{pending: q.t("pendingRefund"), success: q.t("refunded"), failure: q.t("cleanupFailed")},
	}

	if q.StatusStep >= len(steps) {
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		return nil, "", false, func() tea.Msg { return quoteCompleteMsg{} }
	}

	step := steps[q.StatusStep]
	isFinal := q.StatusStep == len(steps)-1

	frame := spinnerFrames[q.StatusFrameIndex%len(spinnerFrames)]
	pendingLine := pendingStyle.Render("  " + frame + " " + step.pending)
	q.StatusFrameIndex++
	q.StatusTickCount++

	// For steps 1+ (maker confirm, settlement), wait for real operator events.
	if q.StatusStep >= 1 && q.waitingForEvent {
		// Check if a real event has arrived.
		if len(q.pendingEvents) > 0 {
			evt := q.pendingEvents[0]
			q.pendingEvents = q.pendingEvents[1:]
			return q.processTradeEvent(evt, steps, failStyle, intermediateSuccessStyle, finalSuccessStyle)
		}
		// If no notification channel, fall back to old fake animation.
		if q.eventCh == nil {
			if q.StatusTickCount >= 8 {
				q.StatusTickCount = 0
				successStyle := intermediateSuccessStyle
				if isFinal {
					successStyle = finalSuccessStyle
				}
				successLine := successStyle.Render("  ✓ " + step.success)
				if !isFinal {
					q.StatusStep++
					return nil, successLine, true, q.tickStatus()
				}
				var output []string
				if q.Quote != nil {
					summaryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
					output = append(output, "")
					output = append(output, summaryStyle.Render(fmt.Sprintf("  %s %s %s → %s %s",
						q.t("swappedSummary"),
						q.Quote.FromAmount, q.Quote.FromAsset,
						q.Quote.ToAmount, q.Quote.ToAsset)))
				}
				q.State = QuoteStateNone
				q.Quote = nil
				return output, successLine, true, func() tea.Msg { return quoteCompleteMsg{} }
			}
			if q.StatusTickCount == 1 {
				return []string{pendingLine}, "", false, q.tickStatus()
			}
			return nil, pendingLine, true, q.tickStatus()
		}
		// No event yet — keep spinning and waiting.
		if q.StatusTickCount == 1 {
			return []string{pendingLine}, "", false, q.tickStatus()
		}
		return nil, pendingLine, true, q.tickStatus()
	}

	// Step 0 (signing) uses the original tick-based animation.
	if q.StatusTickCount < 8 {
		if q.StatusTickCount == 1 {
			return []string{pendingLine}, "", false, q.tickStatus()
		}
		return nil, pendingLine, true, q.tickStatus()
	}
	q.StatusTickCount = 0

	successStyle := intermediateSuccessStyle
	if isFinal {
		successStyle = finalSuccessStyle
	}
	successLine := successStyle.Render("  ✓ " + step.success)

	if !isFinal {
		q.StatusStep++
		return nil, successLine, true, q.tickStatus()
	}

	q.State = QuoteStateNone
	q.Quote = nil
	return nil, successLine, true, func() tea.Msg { return quoteCompleteMsg{} }
}

// HandleTradeEvent processes a real-time event from the operator and returns
// a bubbletea Cmd to continue listening for more events.
func (q *QuoteManager) HandleTradeEvent(evt core.TradeEvent) tea.Cmd {
	if q.State != QuoteStateExecuting {
		// Not in a trade flow — ignore (but keep listening).
		return q.listenForEvent()
	}
	// Only process events for the current active trade.
	if q.activeTradeID != "" && evt.TradeID != "" && evt.TradeID != q.activeTradeID {
		return q.listenForEvent()
	}
	// Buffer the event for the next tick to pick up.
	q.pendingEvents = append(q.pendingEvents, evt)
	return q.listenForEvent()
}

func (q *QuoteManager) processTradeEvent(
	evt core.TradeEvent,
	steps []flowStep,
	failStyle, intermediateSuccessStyle, finalSuccessStyle lipgloss.Style,
) ([]string, string, bool, tea.Cmd) {
	step := steps[q.StatusStep]
	isFinal := q.StatusStep == len(steps)-1

	switch evt.Type {
	// Maker confirmed → advance past step 1
	case "taker_confirmed":
		// taker_confirmed means taker's on-chain confirm was verified by the operator.
		// The trade is now in PendingMaker — waiting for maker.
		// Keep waiting on the same step.
		return nil, "", false, q.tickStatus()

	case "maker_confirmed":
		if q.StatusStep == 1 {
			successLine := intermediateSuccessStyle.Render("  ✓ " + step.success)
			q.StatusStep = 2
			q.StatusTickCount = 0
			q.waitingForEvent = true
			return nil, successLine, true, q.tickStatus()
		}

	case "trade_settled", "settlement_completed":
		// Jump straight to settled, completing the flow.
		q.waitingForEvent = false
		var output []string
		// If we're still on maker step, show that too.
		if q.StatusStep == 1 {
			output = append(output, intermediateSuccessStyle.Render("  ✓ "+steps[1].success))
		}
		settledLine := finalSuccessStyle.Render("  ✓ " + steps[2].success)
		if q.Quote != nil {
			summaryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
			output = append(output, "")
			output = append(output, summaryStyle.Render(fmt.Sprintf("  %s %s %s → %s %s",
				q.t("swappedSummary"),
				q.Quote.FromAmount, q.Quote.FromAsset,
				q.Quote.ToAmount, q.Quote.ToAsset)))
		}
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		return output, settledLine, true, func() tea.Msg { return quoteCompleteMsg{} }

	case "settlement_failed":
		// Settlement failed on the ledger — cleanup will follow.
		q.waitingForEvent = false
		var output []string
		if q.StatusStep == 1 {
			output = append(output, intermediateSuccessStyle.Render("  ✓ "+steps[1].success))
		}
		failLine := failStyle.Render("  ✗ " + q.t("failedToSettle"))
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		return output, failLine, true, func() tea.Msg { return quoteCompleteMsg{} }

	case "maker_error":
		// Maker reported an error — cleanup is in progress.
		q.waitingForEvent = false
		errMsg := evt.Error
		if errMsg == "" {
			errMsg = "maker error"
		}
		failLine := failStyle.Render("  ✗ " + errMsg)
		// Don't end the flow yet — wait for trade_cleanup_complete/failed.
		q.StatusStep = 3
		q.StatusTickCount = 0
		q.waitingForEvent = true
		return nil, failLine, true, q.tickStatus()

	case "confirm_verification_failed":
		// Operator couldn't verify our taker confirm on the ledger.
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		failLine := failStyle.Render("  ✗ " + q.t("verificationFailed") + ": " + evt.Reason)
		return nil, failLine, true, func() tea.Msg { return quoteCompleteMsg{} }

	case "trade_expired":
		// Trade expired (solver didn't respond in time).
		// Resolve step 1 as failure ("Solver failed to respond"), advance to step 3 (refund spinner).
		timeoutLine := failStyle.Render("  ✗ " + q.t("solverTimeout"))
		q.StatusStep = 3 // refund step
		q.StatusTickCount = 0
		q.waitingForEvent = true
		return nil, timeoutLine, true, q.tickStatus()

	case "trade_cleanup_complete", "trade_monitor_cleanup_complete":
		// Trade was cleaned up — tokens refunded.
		// The operator sends "expire_pending_taker" / "expire_pending_maker" (underscore-separated).
		// Handle both forms defensively in case of legacy or alternate operator implementations.
		isTimeout := evt.Action == "expire_pending_maker" || evt.Action == "expire_pending_taker" ||
			evt.Action == "expire_pendingtaker" || evt.Action == "expire_pendingmaker"
		tryAgain := lipgloss.NewStyle().Foreground(muted).Render("  try again")

		if q.StatusStep == 3 {
			// Already on refund step — resolve the spinner in-place.
			q.State = QuoteStateNone
			q.Quote = nil
			q.activeTradeID = ""
			refundLine := finalSuccessStyle.Render("  ✓ "+q.t("refunded")) + tryAgain
			return nil, refundLine, true, func() tea.Msg { return quoteCompleteMsg{} }
		}

		if isTimeout {
			// Got cleanup without a prior trade_expired event — show timeout, then refund resolves spinner.
			q.State = QuoteStateNone
			q.Quote = nil
			q.activeTradeID = ""
			timeoutLine := failStyle.Render("  ✗ " + q.t("solverTimeout"))
			refundLine := finalSuccessStyle.Render("  ✓ "+q.t("refunded")) + tryAgain
			return []string{timeoutLine}, refundLine, true, func() tea.Msg { return quoteCompleteMsg{} }
		}

		// Non-timeout cleanup
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		failLine := failStyle.Render("  ✗ " + q.t("cleanupFailed") + " (" + evt.Action + ")")
		return nil, failLine, true, func() tea.Msg { return quoteCompleteMsg{} }

	case "trade_cleanup_failed", "trade_monitor_cleanup_failed":
		q.State = QuoteStateNone
		q.Quote = nil
		q.activeTradeID = ""
		failLine := failStyle.Render("  ✗ " + q.t("cleanupFailed") + ": " + evt.Error)
		return nil, failLine, true, func() tea.Msg { return quoteCompleteMsg{} }
	}

	// Unknown event type or event for a different step — keep spinning.
	_ = isFinal
	return nil, "", false, q.tickStatus()
}

func (q *QuoteManager) RenderFetchingQuote() []string {
	theme := q.getTheme()
	spinnerStyle := lipgloss.NewStyle().Foreground(theme.Gradient1)

	return []string{
		"",
		spinnerStyle.Render("  ⟳ " + q.t("generatingQuote")),
	}
}

func (q *QuoteManager) RenderQuote() []string {
	if q.Quote == nil {
		return []string{}
	}

	theme := q.getTheme()

	labelStyle := lipgloss.NewStyle().Foreground(muted)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	sendAssetStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	sendAmountStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	receiveAssetStyle := lipgloss.NewStyle().Foreground(theme.Gradient2).Bold(true)
	receiveAmountStyle := lipgloss.NewStyle().Foreground(theme.Gradient1).Bold(true)
	promptStyle := lipgloss.NewStyle().Foreground(warning).Bold(true)

	separator := lipgloss.NewStyle().Foreground(subtle).Render("  " + strings.Repeat("─", 40))

	remaining := q.GetRemainingSeconds()
	expiresStr := fmt.Sprintf("%ds", remaining)
	if remaining <= 5 {
		expiresStr = lipgloss.NewStyle().Foreground(errorC).Bold(true).Render(expiresStr)
	} else if remaining <= 10 {
		expiresStr = lipgloss.NewStyle().Foreground(warning).Render(expiresStr)
	} else {
		expiresStr = valueStyle.Render(expiresStr)
	}

	youSendLabel := padLabel(q.t("youSend"), 14)
	youReceiveLabel := padLabel(q.t("youReceive"), 14)
	rateLabel := padLabel(q.t("rate"), 14)
	expiresLabel := padLabel(q.t("expiresIn"), 14)
	quoteIdLabel := padLabel(q.t("quoteId"), 14)

	lines := []string{
		"",
		separator,
		"",
		labelStyle.Render("  "+youSendLabel) + sendAmountStyle.Render(q.Quote.FromAmount) + " " + sendAssetStyle.Render(q.Quote.FromAsset),
		labelStyle.Render("  "+youReceiveLabel) + receiveAmountStyle.Render(q.Quote.ToAmount) + " " + receiveAssetStyle.Render(q.Quote.ToAsset),
		"",
		labelStyle.Render("  "+rateLabel) + valueStyle.Render("1 "+q.Quote.FromAsset+" = "+q.Quote.Price+" "+q.Quote.ToAsset),
		labelStyle.Render("  "+expiresLabel) + expiresStr,
		labelStyle.Render("  "+quoteIdLabel) + valueStyle.Render(q.Quote.ID),
		"",
		separator,
		"",
		promptStyle.Render("  " + q.t("tradePrompt") + " "),
	}

	return lines
}

func (q *QuoteManager) RenderCountdown() string {
	if q.Quote == nil {
		return ""
	}

	theme := q.getTheme()
	remaining := q.GetRemainingSeconds()

	var style lipgloss.Style
	if remaining <= 5 {
		style = lipgloss.NewStyle().Foreground(errorC).Bold(true)
	} else if remaining <= 10 {
		style = lipgloss.NewStyle().Foreground(warning)
	} else {
		style = lipgloss.NewStyle().Foreground(theme.Gradient1)
	}

	return style.Render(fmt.Sprintf("%ds", remaining))
}

func padLabel(label string, width int) string {
	if len(label) >= width {
		return label
	}
	return label + strings.Repeat(" ", width-len(label))
}

func ValidateSwapAssets(fromAsset, toAsset string) (bool, string) {
	if !supportedAssetSet[fromAsset] {
		return false, fmt.Sprintf("Invalid asset: %s. Valid assets: %s", fromAsset, supportedAssetsStr)
	}
	if !supportedAssetSet[toAsset] {
		return false, fmt.Sprintf("Invalid asset: %s. Valid assets: %s", toAsset, supportedAssetsStr)
	}
	if fromAsset == toAsset {
		return false, "Cannot swap same asset"
	}

	return true, ""
}

func ParseQuoteCommand(parts []string) (amount, fromAsset, toAsset string, err string) {
	usage := fmt.Sprintf("Usage: quote <amount> <from> [to] <to>\n  Example: quote 100 CC to CBTC\n  Assets: %s", supportedAssetsStr)

	if len(parts) < 4 {
		return "", "", "", usage
	}

	// Amount must be the first argument.
	if _, parseErr := strconv.ParseFloat(parts[1], 64); parseErr != nil {
		return "", "", "", fmt.Sprintf("First argument must be a number (got %q).\n  %s", parts[1], usage)
	}
	amount = parts[1]

	// Remaining tokens: expect <from> [to] <to>, skip the word "to" if present.
	var assets []string
	for _, tok := range parts[2:] {
		if strings.ToLower(tok) == "to" {
			continue
		}
		assets = append(assets, strings.ToUpper(tok))
	}

	if len(assets) < 2 {
		return "", "", "", fmt.Sprintf("Need two assets (from and to).\n  %s", usage)
	}
	if len(assets) > 2 {
		return "", "", "", fmt.Sprintf("Too many assets specified.\n  %s", usage)
	}

	return amount, assets[0], assets[1], ""
}



package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/history"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

// Version is set at build time via -ldflags "-X github.com/trngle-xyz/cli/internal/tui.Version=x.y.z".
var Version = "dev"

// UI timing constants for shutdown animation sequence.
const (
	shutdownInitialDelay = 250 * time.Millisecond
	shutdownStepDelay    = 200 * time.Millisecond
	shutdownFinalDelay   = 150 * time.Millisecond
	maxOutputLines       = 500
)

const (
	minTermWidth  = 60
	minTermHeight = 15
)

var (
	subtle  = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#3A3A3A"}
	special = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	muted   = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5A5A5A"}
	warning = lipgloss.AdaptiveColor{Light: "#FFA500", Dark: "#FFB347"}
	errorC  = lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6B6B"}
)

type ColorTheme struct {
	Name      string
	Gradient1 lipgloss.Color
	Gradient2 lipgloss.Color
	Gradient3 lipgloss.Color
	Gradient4 lipgloss.Color
	Gradient5 lipgloss.Color
}

var themes = map[string]ColorTheme{
	"cyan": {
		Name:      "Cyan",
		Gradient1: lipgloss.Color("#00D4FF"),
		Gradient2: lipgloss.Color("#00B4D8"),
		Gradient3: lipgloss.Color("#0096C7"),
		Gradient4: lipgloss.Color("#0077B6"),
		Gradient5: lipgloss.Color("#023E8A"),
	},
	"purple": {
		Name:      "Purple",
		Gradient1: lipgloss.Color("#E040FB"),
		Gradient2: lipgloss.Color("#D500F9"),
		Gradient3: lipgloss.Color("#AA00FF"),
		Gradient4: lipgloss.Color("#7C4DFF"),
		Gradient5: lipgloss.Color("#651FFF"),
	},
	"green": {
		Name:      "Green",
		Gradient1: lipgloss.Color("#00E676"),
		Gradient2: lipgloss.Color("#00C853"),
		Gradient3: lipgloss.Color("#00BFA5"),
		Gradient4: lipgloss.Color("#1DE9B6"),
		Gradient5: lipgloss.Color("#64FFDA"),
	},
	"orange": {
		Name:      "Orange",
		Gradient1: lipgloss.Color("#FF9100"),
		Gradient2: lipgloss.Color("#FF6D00"),
		Gradient3: lipgloss.Color("#FF3D00"),
		Gradient4: lipgloss.Color("#DD2C00"),
		Gradient5: lipgloss.Color("#BF360C"),
	},
	"pink": {
		Name:      "Pink",
		Gradient1: lipgloss.Color("#FF4081"),
		Gradient2: lipgloss.Color("#F50057"),
		Gradient3: lipgloss.Color("#C51162"),
		Gradient4: lipgloss.Color("#AD1457"),
		Gradient5: lipgloss.Color("#880E4F"),
	},
	"gold": {
		Name:      "Gold",
		Gradient1: lipgloss.Color("#FFD700"),
		Gradient2: lipgloss.Color("#FFC107"),
		Gradient3: lipgloss.Color("#FFB300"),
		Gradient4: lipgloss.Color("#FFA000"),
		Gradient5: lipgloss.Color("#FF8F00"),
	},
}

var asciiLogo = `
                 ______
                /     /\
               /     /##\
              /     /####\
             /     /######\
            /     /########\
           /     /##########\
          /     /#####/\#####\
         /     /#####/++\#####\
        /     /#####/++++\#####\
       /     /#####/\+++++\#####\
      /     /#####/  \+++++\#####\
     /     /#####/    \+++++\#####\
    /     /#####/      \+++++\#####\
   /     /#####/        \+++++\#####\
  /     /#####/__________\+++++\#####\
 /                        \+++++\#####\
/__________________________\+++++\####/
\+++++++++++++++++++++++++++++++++\##/
 \+++++++++++++++++++++++++++++++++\/
   ''''''''''''''''''''''''''''''''`

var asciiTitle = `
 ████████╗██████╗ ███╗   ██╗ ██████╗ ██╗     ███████╗
 ╚══██╔══╝██╔══██╗████╗  ██║██╔════╝ ██║     ██╔════╝
    ██║   ██████╔╝██╔██╗ ██║██║  ███╗██║     █████╗  
    ██║   ██╔══██╗██║╚██╗██║██║   ██║██║     ██╔══╝  
    ██║   ██║  ██║██║ ╚████║╚██████╔╝███████╗███████╗
    ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═══╝ ╚═════╝ ╚══════╝╚══════╝`

type (
	tickMsg              time.Time
	shutdownFinalizeMsg  struct{}
	shutdownPrintNextMsg struct{}
)

type Model struct {
	width       int
	height      int
	spinner     spinner.Model
	loading     bool
	err         error
	walletAddr  string
	connected   bool
	apiHealthy  bool
	apiChecking bool
	apiEndpoint string
	apiToken    string // auto-token displayed to user
	network     string
	version     string
	currentTime time.Time
	theme       string
	lang        string
	cfg         config.AppConfig

	input         textinput.Model
	history       []string
	historyIndex  int
	output        []string
	viewport      viewport.Model
	showedBanner  bool
	viewportReady bool

	settings                SettingsPopup
	quote                   QuoteManager
	setup                   SetupWizard
	historyDB               *history.Store
	activeTxID              string
	lastTradeIDs            []string
	setupMode               bool
	configPath              string
	inlineSpinner           InlineSpinnerState
	shuttingDown            bool
	confirmQuit             bool
	shutdownQueue           []string
	shutdownQueueActive     bool
	shutdownFinalizePending bool

	gasPoller *loop.Adapter // persistent adapter for background gas polling
}

func (m Model) t(key string) string {
	lang := GetLanguage(m.lang)
	if text, ok := lang.Text[key]; ok {
		return text
	}
	return GetLanguage("en").Text[key]
}

func (m Model) getTheme() ColorTheme {
	if theme, ok := themes[m.theme]; ok {
		return theme
	}
	return themes["cyan"]
}

func NewModel() Model {
	defaultTheme := themes["cyan"]

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(defaultTheme.Gradient1)

	ti := textinput.New()
	ti.Placeholder = "type a command..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 60
	ti.PromptStyle = lipgloss.NewStyle().Foreground(defaultTheme.Gradient1).Bold(true)
	ti.Prompt = "▶ "

	return Model{
		spinner:      s,
		loading:      true,
		apiChecking:  true,
		input:        ti,
		output:       []string{},
		history:      []string{},
		historyIndex: -1,
		version:      Version,
		network:      "Canton Testnet",
		currentTime:  time.Now(),
		theme:        "cyan",
		lang:         "en",
		settings:     NewSettingsPopup(),
		quote:        NewQuoteManager(),
	}
}

func NewModelFromConfig(cfg config.AppConfig, configPath string, forceSetup bool) Model {
	m := NewModel()
	m.cfg = cfg
	m.configPath = configPath
	if cfg.Theme != "" {
		m.theme = cfg.Theme
	}
	if cfg.Language != "" {
		m.lang = cfg.Language
	}
	if cfg.Network != "" {
		m.network = cfg.Network
	}
	if cfg.WalletPartyID != "" {
		m.walletAddr = cfg.WalletPartyID
	}
	m.apiEndpoint = fmt.Sprintf("http://%s:%d", cfg.LocalAPI.Bind, cfg.LocalAPI.Port)
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(m.getTheme().Gradient1).Bold(true)
	m.spinner.Style = lipgloss.NewStyle().Foreground(m.getTheme().Gradient1)
	m.setup = NewSetupWizard(cfg, configPath)
	m.setupMode = forceSetup
	if forceSetup {
		m.loading = false
	}
	// Connect to operator notification WebSocket for real-time trade events.
	m.quote.ConnectNotifications(cfg.TrngleAPIURL, cfg.WalletPartyID)
	return m
}

// SetHistoryDB sets the local history database on the model.
// Called from main.go after opening the DB.
func (m *Model) SetHistoryDB(db *history.Store) {
	m.historyDB = db
}

func doTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) showBanner() {
	theme := m.getTheme()
	labelStyle := lipgloss.NewStyle().Foreground(muted)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	okStyle := lipgloss.NewStyle().Foreground(special)
	errStyle := lipgloss.NewStyle().Foreground(errorC)
	warnStyle := lipgloss.NewStyle().Foreground(warning)

	logoLines := strings.Split(asciiLogo, "\n")
	titleLines := strings.Split(asciiTitle, "\n")

	gradientColors := []lipgloss.Color{theme.Gradient1, theme.Gradient1, theme.Gradient2, theme.Gradient2, theme.Gradient3, theme.Gradient3, theme.Gradient4, theme.Gradient4, theme.Gradient5}

	walletStatus := errStyle.Render("○ " + m.t("disconnected"))
	if m.connected {
		walletStatus = okStyle.Render("● " + m.t("connected"))
	} else if m.loading {
		walletStatus = warnStyle.Render("◐ " + m.t("connecting"))
	}

	apiStatus := errStyle.Render("○ " + m.t("offline"))
	if m.apiHealthy {
		apiStatus = okStyle.Render("● " + m.t("healthy"))
	} else if m.apiChecking {
		apiStatus = warnStyle.Render("◐ " + m.t("apiChecking"))
	}

	rightContent := []string{}
	for i, line := range titleLines {
		if strings.TrimSpace(line) != "" {
			colorIdx := i
			if colorIdx >= len(gradientColors) {
				colorIdx = len(gradientColors) - 1
			}
			rightContent = append(rightContent, lipgloss.NewStyle().Foreground(gradientColors[colorIdx]).Bold(true).Render(line))
		}
	}
	rightContent = append(rightContent, lipgloss.NewStyle().Foreground(theme.Gradient3).Render("  v"+m.version))
	rightContent = append(rightContent, "")
	rightContent = append(rightContent, labelStyle.Render("  "+m.t("wallet")+"    ")+walletStatus)
	rightContent = append(rightContent, labelStyle.Render("  "+m.t("address")+"   ")+valueStyle.Render(m.formatAddress()))
	rightContent = append(rightContent, labelStyle.Render("  "+m.t("network")+"   ")+valueStyle.Render(m.network))
	rightContent = append(rightContent, "")
	rightContent = append(rightContent, labelStyle.Render("  "+m.t("localApi")+" ")+apiStatus)
	rightContent = append(rightContent, labelStyle.Render("  "+m.t("endpoint")+"  ")+valueStyle.Render(m.apiEndpoint))
	if m.apiToken != "" {
		rightContent = append(rightContent, labelStyle.Render("  Token     ")+lipgloss.NewStyle().Foreground(warning).Render(m.apiToken))
	}

	maxLogoWidth := 0
	for _, line := range logoLines {
		if len(line) > maxLogoWidth {
			maxLogoWidth = len(line)
		}
	}

	titleStartLine := 5

	rightIdx := 0
	for i := 0; i < len(logoLines) || rightIdx < len(rightContent); i++ {
		var logoLine string
		var rightLine string

		if i < len(logoLines) {
			logoLine = lipgloss.NewStyle().Foreground(theme.Gradient1).Render(logoLines[i])
		}
		paddedLogo := lipgloss.NewStyle().Width(maxLogoWidth + 4).Render(logoLine)

		if i >= titleStartLine && rightIdx < len(rightContent) {
			rightLine = rightContent[rightIdx]
			rightIdx++
		}

		m.output = append(m.output, paddedLogo+rightLine)
	}

	for rightIdx < len(rightContent) {
		m.output = append(m.output, lipgloss.NewStyle().Width(maxLogoWidth+4).Render("")+rightContent[rightIdx])
		rightIdx++
	}

	m.output = append(m.output, "")
	m.output = append(m.output, lipgloss.NewStyle().Foreground(muted).Italic(true).Render("  "+m.t("welcome")))
	m.output = append(m.output, "")

	if m.viewportReady {
		m.viewport.SetContent(strings.Join(m.output, "\n"))
		m.viewport.GotoBottom()
	}
}

func (m Model) formatAddress() string {
	if m.walletAddr == "" {
		return lipgloss.NewStyle().Foreground(muted).Render(m.t("notConnected"))
	}
	return truncateAddress(m.walletAddr)
}

func (m Model) Init() tea.Cmd {
	if m.setupMode {
		return tea.Batch(
			textinput.Blink,
			doTick(),
		)
	}
	return tea.Batch(
		m.spinner.Tick,
		textinput.Blink,
		initializeWalletCmd(m.cfg),
		startAPIAfterSetupCmd(m.cfg),
		doTick(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	enqueueShutdownLines := func(lines ...string) tea.Cmd {
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			m.shutdownQueue = append(m.shutdownQueue, line)
		}
		if !m.shutdownQueueActive && len(m.shutdownQueue) > 0 {
			m.shutdownQueueActive = true
			return tea.Tick(shutdownInitialDelay, func(time.Time) tea.Msg { return shutdownPrintNextMsg{} })
		}
		return nil
	}

	if m.setupMode {
		switch msg := msg.(type) {
		case tickMsg:
			m.currentTime = time.Time(msg)
			return m, doTick()
		case tea.KeyMsg:
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
		case tea.WindowSizeMsg:
			m.width = msg.Width
			m.height = msg.Height
			m.setup.SetSize(msg.Width, msg.Height)
			return m, nil
		case setupCompleteMsg:
			m.setupMode = false
			m.cfg = msg.cfg
			m.theme = msg.cfg.Theme
			m.lang = msg.cfg.Language
			m.walletAddr = msg.cfg.WalletPartyID
			m.network = msg.cfg.Network
			m.apiEndpoint = fmt.Sprintf("http://%s:%d", msg.cfg.LocalAPI.Bind, msg.cfg.LocalAPI.Port)

			// Reset main TUI state for a clean transition from wizard.
			m.loading = true
			m.apiChecking = true
			m.showedBanner = false
			m.output = []string{}
			m.input.PromptStyle = lipgloss.NewStyle().Foreground(m.getTheme().Gradient1).Bold(true)
			m.spinner.Style = lipgloss.NewStyle().Foreground(m.getTheme().Gradient1)
			m.input.Focus()

			// Reinitialize viewport with current terminal dimensions.
			if m.width > 0 && m.height > 0 {
				m.input.Width = m.width - 10
				viewportHeight := m.height - 7
				if viewportHeight < 5 {
					viewportHeight = 5
				}
				m.viewport = viewport.New(m.width-2, viewportHeight)
				m.viewport.SetContent("")
				m.viewportReady = true
			}

			// Connect WebSocket notifications now that config has party ID.
			m.quote.ConnectNotifications(msg.cfg.TrngleAPIURL, msg.cfg.WalletPartyID)

			return m, tea.Batch(
				m.spinner.Tick,
				textinput.Blink,
				initializeWalletCmd(m.cfg),
				startAPIAfterSetupCmd(m.cfg), // starts server, then health check follows
				doTick(),
			)
		}

		// All other messages (including walletVerifiedMsg / walletVerifyErrMsg
		// from the async verify command) are forwarded to the wizard.
		var cmd tea.Cmd
		m.setup, cmd = m.setup.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case shutdownFinalizeMsg:
		m.quote.CloseNotifications()
		return m, tea.Quit
	case shutdownPrintNextMsg:
		if len(m.shutdownQueue) == 0 {
			m.shutdownQueueActive = false
			if m.shutdownFinalizePending {
				m.shutdownFinalizePending = false
				return m, tea.Tick(shutdownFinalDelay, func(time.Time) tea.Msg { return shutdownFinalizeMsg{} })
			}
			return m, nil
		}
		next := m.shutdownQueue[0]
		m.shutdownQueue = m.shutdownQueue[1:]
		m.appendOutput(next)
		if len(m.shutdownQueue) > 0 {
			return m, tea.Tick(shutdownStepDelay, func(time.Time) tea.Msg { return shutdownPrintNextMsg{} })
		}
		m.shutdownQueueActive = false
		if m.shutdownFinalizePending {
			m.shutdownFinalizePending = false
			return m, tea.Tick(shutdownFinalDelay, func(time.Time) tea.Msg { return shutdownFinalizeMsg{} })
		}
		return m, nil
	case settingsCloseMsg:
		m.settings.Hide()
		m.input.Focus()
		return m, nil
	case settingsBuildMsg:
		m.appendOutput(lipgloss.NewStyle().Foreground(special).Render("✓ " + m.t("settingsBuildStarted")))
		m.appendOutput(fmt.Sprintf("  Port: %s", msg.config.APIPort))
		m.appendOutput(fmt.Sprintf("  Wallet: %s", truncateAddress(msg.config.WalletAddress)))
		m.settings.Hide()
		m.input.Focus()
		return m, nil
	case quoteCountdownTickMsg:
		q := m.quote.Quote // snapshot to avoid TOCTOU race
		if m.quote.State == QuoteStateAwaitingConfirm && q != nil {
			expired, cmd := m.quote.HandleCountdownTick()
			if expired {
				return m, cmd
			}
			m.updateLastLineWithCountdown()
			return m, cmd
		}
		return m, nil
	case quoteExpiredMsg:
		m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  ✗ " + m.t("quoteExpired")))
		m.appendOutput("")
		return m, nil
	case tradeEventMsg:
		listenCmd := m.quote.HandleTradeEvent(msg.event)
		// Process any buffered events now.
		return m, tea.Batch(listenCmd, m.processTradeEvents())
	case quoteStatusTickMsg:
		// Check for buffered events if inline spinner is driving animation.
		if m.quote.IsExecuting() && m.quote.StatusStep >= 1 {
			cmd := m.processTradeEvents()
			if cmd != nil {
				return m, cmd
			}
			// No events processed — if WebSocket connected, wait for next event.
			if m.quote.HasNotifications() {
				return m, nil
			}
			// No WebSocket — fall through to tick-based animation.
			// Stop inline spinner first to avoid dual-animation conflict.
			if m.inlineSpinner.active {
				m.StopInlineSpinner()
			}
		}
		output, replacement, shouldReplace, cmd := m.quote.HandleStatusTick()
		if shouldReplace && len(m.output) > 0 {
			// Directly update the last line instead of ReplaceSpinnerLine,
			// which relies on spinnerLineOwned and breaks after the first call.
			m.output[len(m.output)-1] = replacement
			m.viewport.SetContent(joinLines(m.output))
			m.viewport.GotoBottom()
		}
		for _, line := range output {
			m.appendOutput(line)
		}
		return m, cmd
	case quoteCompleteMsg:
		m.appendOutput("")
		return m, nil
	case quoteCancelledMsg:
		m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render("  " + m.t("quoteCancelled")))
		m.appendOutput("")
		return m, nil
	case quotePrefetchResultMsg:
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		if msg.err != nil {
			m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(errorC).Render("  ✗ " + msg.err.Error()))
			m.appendOutput("")
			return m, nil
		}
		m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(muted).Render("  ✓ " + m.t("quoteGenerated")))
		m.quote.SetFromPrefetch(msg.quote, msg.acceptCtx)
		m.quote.State = QuoteStateAwaitingConfirm
		for _, line := range m.quote.RenderQuote() {
			m.appendOutput(line)
		}
		return m, m.quote.StartCountdown()
	case quoteAcceptContextReadyMsg:
		if msg.err != nil {
			if m.inlineSpinner.active {
				m.StopInlineSpinner()
			}
			m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(errorC).Render("  ✗ " + m.t("failedToSignSettlement")))
			m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  " + msg.err.Error()))
			m.appendOutput("")
			// Record context fetch failure.
			m.historyUpdateStatus("context_failed",
				history.WithError(msg.err.Error(), "accept_context"),
				history.WithFinalEvent("context_failed"),
			)
			m.activeTxID = ""
			m.quote.Cancel()
			return m, nil
		}
		if m.quote.Quote != nil {
			m.quote.Quote.AcceptContext = msg.acceptCtx
		}
		// Update the DB row with trade IDs from the AcceptContext.
		if msg.acceptCtx != nil {
			m.historyUpdateFields(
				history.WithTradeID(msg.acceptCtx.TradeID),
				history.WithTradeCID(msg.acceptCtx.TradeCID),
				history.WithCounterparty(msg.acceptCtx.MakerParty),
			)
		}
		m.TransitionSpinnerText(m.t("checkingHoldingsPending"))
		return m, submitOnChainCmd(m.cfg, m.quote.Quote)
	case quoteHoldingsValidMsg:
		m.TransitionSpinnerText(m.t("signingSettlementPending"))
		return m, submitWithHoldingCmd(m.cfg, m.quote.Quote, msg.single)
	case quoteMergeNeededMsg:
		m.TransitionSpinnerText(fmt.Sprintf(m.t("mergingHoldings"), len(msg.holdings)))
		return m, mergeHoldingsCmd(m.cfg, m.quote.Quote, msg.holdings)
	case quoteMergeCompleteMsg:
		m.TransitionSpinnerText(m.t("signingSettlementPending"))
		return m, submitWithHoldingCmd(m.cfg, m.quote.Quote, msg.single)
	case quoteSubmitResultMsg:
		if msg.submitErr != nil {
			if m.inlineSpinner.active {
				m.StopInlineSpinner()
			}
			m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(errorC).Render("  ✗ " + m.t("failedToSignSettlement")))
			m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  " + msg.submitErr.Error()))
			m.appendOutput("")
			// Record sign failure.
			m.historyUpdateStatus("sign_failed",
				history.WithError(msg.submitErr.Error(), "sign"),
				history.WithFinalEvent("sign_failed"),
			)
			m.activeTxID = ""
			m.quote.Cancel()
			return m, nil
		}
		// Sign succeeded — trade is now on the ledger.
		m.historyUpdateStatus("submitted")
		if msg.confirmErr != nil {
			m.appendOutput(lipgloss.NewStyle().Foreground(warning).Render("  confirm warning: " + msg.confirmErr.Error()))
		}
		// Transition the existing spinner directly to "Solver confirming..." — no intermediate line.
		m.TransitionSpinnerText(m.t("makerConfirmingPending"))
		listenCmd := m.quote.StartPostSignFlow()
		return m, listenCmd
	case shutdownResultMsg:
		m.shuttingDown = false
		if msg.apiAttempted {
			if msg.apiStopped {
				cmd := enqueueShutdownLines(lipgloss.NewStyle().Foreground(special).Render("  ✓ " + m.t("shutdownApiStopped")))
				m.shutdownFinalizePending = true
				cmd2 := enqueueShutdownLines(lipgloss.NewStyle().Foreground(muted).Render("  " + m.t("shutdownExitNow")))
				return m, tea.Batch(cmd, cmd2)
			} else {
				cmd := enqueueShutdownLines(lipgloss.NewStyle().Foreground(errorC).Render("  ✗ " + m.t("shutdownApiStopFailed") + ": " + msg.err.Error()))
				m.shutdownFinalizePending = true
				cmd2 := enqueueShutdownLines(lipgloss.NewStyle().Foreground(muted).Render("  " + m.t("shutdownExitNow")))
				return m, tea.Batch(cmd, cmd2)
			}
		} else {
			cmd := enqueueShutdownLines(lipgloss.NewStyle().Foreground(muted).Render("  • " + m.t("shutdownApiSkipped")))
			m.shutdownFinalizePending = true
			cmd2 := enqueueShutdownLines(lipgloss.NewStyle().Foreground(muted).Render("  " + m.t("shutdownExitNow")))
			return m, tea.Batch(cmd, cmd2)
		}

	case balancesResultMsg:
		m.StopInlineSpinner()
		theme := m.getTheme()
		if msg.err != nil {
			m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(errorC).Render("  ✗ " + msg.err.Error()))
			m.appendOutput("")
			return m, nil
		}
		m.ReplaceSpinnerLine(lipgloss.NewStyle().Bold(true).Foreground(theme.Gradient1).Render("  " + m.t("walletBalances")))
		m.appendOutput("")
		m.appendOutput(fmt.Sprintf("  %-10s %20s", m.t("asset"), m.t("balance")))
		m.appendOutput(lipgloss.NewStyle().Foreground(subtle).Render("  " + strings.Repeat("─", 32)))
		sort.Slice(msg.balances, func(i, j int) bool {
			return msg.balances[i].Symbol < msg.balances[j].Symbol
		})
		for _, b := range msg.balances {
			m.appendOutput(fmt.Sprintf("  %-10s %20s", b.Symbol, b.Amount))
		}
		if len(msg.balances) == 0 {
			m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render("  " + m.t("noHoldingsFound")))
		}
		m.appendOutput(lipgloss.NewStyle().Foreground(subtle).Render("  " + strings.Repeat("─", 32)))
		m.appendOutput("")
		return m, nil
	}

	if m.settings.IsVisible() {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.String() == "ctrl+c" {
				m.confirmQuit = true
				return m, nil
			}
			if msg.String() == "ctrl+d" {
				return m, tea.Quit
			}
		case tea.WindowSizeMsg:
			m.width = msg.Width
			m.height = msg.Height
			m.settings.SetSize(msg.Width, msg.Height)
			return m, nil
		}

		var cmd tea.Cmd
		m.settings, cmd = m.settings.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case tickMsg:
		m.currentTime = time.Time(msg)
		return m, doTick()

	case tea.KeyMsg:
		keyStr := msg.String()

		if m.confirmQuit {
			if IsConfirmKey(m.lang, keyStr) || keyStr == "enter" {
				m.confirmQuit = false
				m.shuttingDown = true
				if m.gasPoller != nil {
					m.gasPoller.StopGasPoller()
					m.gasPoller.StopAuthRefresh()
				}
				m.appendOutput("")
				m.appendOutput(lipgloss.NewStyle().Foreground(warning).Render("  ◐ " + m.t("shutdownStarting")))
				cmd := tea.Cmd(nil)
				if m.cfg.LocalAPI.Enabled {
					cmd = enqueueShutdownLines(lipgloss.NewStyle().Foreground(warning).Render("  ◐ " + m.t("shutdownApiStopping")))
				}
				return m, tea.Batch(shutdownAppCmd(m.cfg), cmd)
			}
			if IsDenyKey(m.lang, keyStr) || keyStr == "esc" {
				m.confirmQuit = false
				return m, nil
			}
			return m, nil
		}

		if m.shuttingDown {
			if keyStr == "ctrl+c" {
				return m, nil
			}
			return m, nil
		}

		if m.quote.IsAwaitingInput() {
			if IsConfirmKey(m.lang, keyStr) {
				if !m.quote.HasExecutablePayload() {
					m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("  " + m.t("missingTradePayload")))
					m.appendOutput("")
					return m, nil
				}
				// Record trade attempt as pending in local DB.
				txID := history.GenerateID()
				m.activeTxID = txID
				q := m.quote.Quote
				m.historyInsert(history.Tx{
					ID:        txID,
					Type:      "trade",
					Status:    "pending",
					FromAsset: strPtr(q.FromAsset),
					FromAmount: strPtr(q.FromAmount),
					ToAsset:   strPtr(q.ToAsset),
					ToAmount:  strPtr(q.ToAmount),
					Rate:      strPtr(q.Price),
					QuoteID:   strPtr(q.ID),
				})
				m.quote.State = QuoteStateExecuting
				m.appendOutput("")
				spinnerCmd := m.StartInlineSpinner(m.t("generatingContractPending"))
				return m, tea.Batch(spinnerCmd, fetchAcceptContextCmd(m.cfg, m.quote.Quote.ID))
			}
			if IsDenyKey(m.lang, keyStr) || keyStr == "esc" {
				m.quote.Cancel()
				m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render("  ✗ " + m.t("tradeCancelled")))
				m.appendOutput("")
				return m, nil
			}
			if keyStr == "ctrl+c" {
				m.confirmQuit = true
				return m, nil
			}
			return m, nil
		}

		if m.quote.IsExecuting() {
			if keyStr == "ctrl+c" {
				m.confirmQuit = true
				return m, nil
			}
			return m, nil
		}

		switch keyStr {
		case "ctrl+c":
			m.confirmQuit = true
			return m, nil

		case "enter":
			if !m.loading {
				cmdStr := strings.TrimSpace(m.input.Value())
				if cmdStr != "" {
					swapCmd := m.executeCommand(cmdStr)
					m.history = append(m.history, cmdStr)
					m.historyIndex = len(m.history)
					m.input.Reset()
					if swapCmd != nil {
						return m, swapCmd
					}
				}
			}
			return m, nil

		case "up":
			if len(m.history) > 0 && m.historyIndex > 0 {
				m.historyIndex--
				m.input.SetValue(m.history[m.historyIndex])
				m.input.CursorEnd()
			}
			return m, nil

		case "down":
			if m.historyIndex < len(m.history)-1 {
				m.historyIndex++
				m.input.SetValue(m.history[m.historyIndex])
				m.input.CursorEnd()
			} else {
				m.historyIndex = len(m.history)
				m.input.Reset()
			}
			return m, nil

		case "ctrl+l":
			m.output = []string{}
			m.viewport.SetContent("")
			m.showBanner()
			return m, nil

		case "pgup", "ctrl+b":
			m.viewport.LineUp(3)
			return m, nil

		case "pgdown", "ctrl+f":
			m.viewport.LineDown(3)
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = m.width - 10

		viewportHeight := m.height - 7
		if viewportHeight < 5 {
			viewportHeight = 5
		}

		if !m.viewportReady {
			m.viewport = viewport.New(m.width-2, viewportHeight)
			m.viewport.SetContent(strings.Join(m.output, "\n"))
			m.viewportReady = true
		} else {
			m.viewport.Width = m.width - 2
			m.viewport.Height = viewportHeight
		}

		m.settings.SetSize(msg.Width, msg.Height)
		if !m.showedBanner && !m.setupMode {
			m.showBanner()
			m.showedBanner = true
		}
		return m, nil

	case inlineSpinnerTickMsg:
		cmd := m.TickInlineSpinner(msg)
		if cmd != nil {
			return m, cmd
		}
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		// When not loading, don't propagate — stops the tick chain.

	case walletInitMsg:
		m.loading = false
		m.walletAddr = msg.address
		m.connected = true
		m.output = []string{}
		m.showBanner()
		m.showedBanner = true
		// Initialize WebSocket notifications now that we have a party ID.
		// ConnectNotifications is a no-op if already connected.
		if !m.quote.HasNotifications() {
			m.quote.ConnectNotifications(m.cfg.TrngleAPIURL, msg.address)
		}
		// Start background gas poller once wallet is connected
		return m, startGasPollerCmd(m.cfg)

	case gasPollerReadyMsg:
		if msg.poller != nil {
			m.gasPoller = msg.poller
		}
		return m, nil

	case apiStartedMsg:
		// API server was just started after wizard — now check if it's healthy.
		return m, checkAPIHealthCmd(m.cfg)

	case apiHealthMsg:
		m.apiChecking = false
		m.apiHealthy = msg.healthy
		if msg.endpoint != "" {
			m.apiEndpoint = msg.endpoint
		}
		if msg.token != "" {
			m.apiToken = msg.token
		}
		m.output = []string{}
		m.showBanner()
		m.showedBanner = true
		return m, nil

	case errMsg:
		m.loading = false
		m.apiChecking = false
		m.err = msg.err
		m.output = []string{}
		m.showBanner()
		m.showedBanner = true
		m.appendOutput(lipgloss.NewStyle().Foreground(errorC).Render("✗ " + m.t("error") + ": " + msg.err.Error()))
		return m, nil
	}

	if !m.loading {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// processTradeEvents consumes buffered trade events from the QuoteManager
// and drives the inline spinner through the post-sign flow steps.
func (m *Model) processTradeEvents() tea.Cmd {
	evt, ok := m.quote.ConsumeEvent()
	if !ok {
		return nil
	}

	// Serialize event for audit log.
	evtJSON, err := json.Marshal(evt)
	if err != nil {
		evtJSON = []byte("{}")
	}
	m.historyRecordEvent(evt.Type, string(evtJSON))

	failStyle := lipgloss.NewStyle().Foreground(errorC)
	successStyle := lipgloss.NewStyle().Foreground(special)
	hintStyle := lipgloss.NewStyle().Foreground(muted)

	switch evt.Type {
	case "taker_confirmed":
		// Still waiting for solver — no change.
		return nil

	case "maker_confirmed":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(muted).Render("  ✓ " + m.t("makerConfirmed")))
		m.quote.StatusStep = 2
		return m.StartInlineSpinner(m.t("awaitingSettlementPending"))

	case "trade_settled", "settlement_completed":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		// If still on solver step, resolve that first.
		if m.quote.StatusStep == 1 {
			m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(muted).Render("  ✓ " + m.t("makerConfirmed")))
			m.appendOutput(lipgloss.NewStyle().Foreground(muted).Render("  ✓ " + m.t("awaitingSettlementPending")))
		}
		m.ReplaceSpinnerLine(successStyle.Render("  ✓ " + m.t("settled")))
		if q := m.quote.Quote; q != nil {
			summaryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
			m.appendOutput("")
			m.appendOutput(summaryStyle.Render(fmt.Sprintf("  %s %s %s → %s %s",
				m.t("swappedSummary"),
				q.FromAmount, q.FromAsset,
				q.ToAmount, q.ToAsset)))
		}
		m.historyUpdateStatus("settled",
			history.WithSettledAt(time.Now()),
			history.WithFinalEvent("trade_settled"),
			history.WithRawResult(string(evtJSON)),
		)
		m.resetTradeState()
		return nil

	case "settlement_failed":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		if m.quote.StatusStep == 1 {
			m.ReplaceSpinnerLine(lipgloss.NewStyle().Foreground(muted).Render("  ✓ " + m.t("makerConfirmed")))
			m.appendOutput(failStyle.Render("  ✗ " + m.t("failedToSettle")))
		} else {
			m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + m.t("failedToSettle")))
		}
		m.historyUpdateStatus("failed",
			history.WithError(evt.Error, "settlement"),
			history.WithFinalEvent("settlement_failed"),
		)
		m.resetTradeState()
		return nil

	case "maker_error":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		errMsg := evt.Error
		if errMsg == "" {
			errMsg = "maker error"
		}
		m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + errMsg))
		m.historyRecordEvent("maker_error", string(evtJSON))
		// Cleanup is in progress — start refund spinner.
		m.quote.StatusStep = 3
		return m.StartInlineSpinner(m.t("pendingRefund"))

	case "confirm_verification_failed":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + m.t("verificationFailed") + ": " + evt.Reason))
		m.historyUpdateStatus("failed",
			history.WithError(evt.Reason, "verification"),
			history.WithFinalEvent("confirm_verification_failed"),
		)
		m.resetTradeState()
		return nil

	case "trade_expired":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + m.t("solverTimeout")))
		m.quote.StatusStep = 3
		return m.StartInlineSpinner(m.t("pendingRefund"))

	case "trade_cleanup_complete", "trade_monitor_cleanup_complete":
		isTimeout := evt.Action == "expire_pending_maker" || evt.Action == "expire_pending_taker" ||
			evt.Action == "expire_pendingtaker" || evt.Action == "expire_pendingmaker"
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}

		if m.quote.StatusStep == 3 {
			// Refund step — resolve spinner.
			refundLine := successStyle.Render("  ✓ "+m.t("refunded")) + hintStyle.Render("  try again")
			m.ReplaceSpinnerLine(refundLine)
		} else if isTimeout {
			// Got cleanup without prior trade_expired — show timeout then refund.
			m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + m.t("solverTimeout")))
			refundLine := successStyle.Render("  ✓ "+m.t("refunded")) + hintStyle.Render("  try again")
			m.appendOutput(refundLine)
		} else {
			m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + m.t("cleanupFailed") + " (" + evt.Action + ")"))
		}
		m.historyUpdateStatus("refunded",
			history.WithFinalEvent(evt.Type),
			history.WithRawResult(string(evtJSON)),
		)
		m.resetTradeState()
		return nil

	case "trade_cleanup_failed", "trade_monitor_cleanup_failed":
		if m.inlineSpinner.active {
			m.StopInlineSpinner()
		}
		m.ReplaceSpinnerLine(failStyle.Render("  ✗ " + m.t("cleanupFailed") + ": " + evt.Error))
		m.historyUpdateStatus("failed",
			history.WithError(evt.Error, "cleanup"),
			history.WithFinalEvent("trade_cleanup_failed"),
		)
		m.resetTradeState()
		return nil
	}

	return nil
}

// resetTradeState clears all active trade/quote tracking fields and appends
// a blank line to visually separate the completed trade from subsequent output.
func (m *Model) resetTradeState() {
	m.activeTxID = ""
	m.quote.State = QuoteStateNone
	m.quote.Quote = nil
	m.quote.activeTradeID = ""
	m.appendOutput("")
}

// --- History DB helpers (fire-and-forget, never block the trade flow) ---

func (m *Model) historyInsert(tx history.Tx) {
	if m.historyDB == nil {
		return
	}
	_ = m.historyDB.Insert(tx)
}

func (m *Model) historyUpdateStatus(newStatus string, opts ...history.UpdateOption) {
	if m.historyDB == nil || m.activeTxID == "" {
		return
	}
	_ = m.historyDB.UpdateStatus(m.activeTxID, newStatus, opts...)
}

func (m *Model) historyUpdateFields(opts ...history.FieldOption) {
	if m.historyDB == nil || m.activeTxID == "" {
		return
	}
	_ = m.historyDB.UpdateFields(m.activeTxID, opts...)
}

func (m *Model) historyRecordEvent(eventType string, eventJSON string) {
	if m.historyDB == nil || m.activeTxID == "" {
		return
	}
	_ = m.historyDB.RecordEvent(m.activeTxID, eventType, eventJSON)
}

func strPtr(s string) *string { return &s }

func (m *Model) appendOutput(lines ...string) {
	for _, line := range lines {
		m.output = append(m.output, m.wrapOutputLine(line)...)
	}
	if len(m.output) > maxOutputLines {
		m.output = m.output[len(m.output)-maxOutputLines:]
	}
	m.viewport.SetContent(strings.Join(m.output, "\n"))
	m.viewport.GotoBottom()
}

func (m *Model) updateLastLineWithCountdown() {
	if len(m.output) == 0 || m.quote.Quote == nil {
		return
	}

	promptStyle := lipgloss.NewStyle().Foreground(warning).Bold(true)
	countdown := m.quote.RenderCountdown()

	newPrompt := promptStyle.Render("  "+m.t("tradePrompt")+" ") + countdown
	m.output[len(m.output)-1] = newPrompt

	m.viewport.SetContent(strings.Join(m.output, "\n"))
	m.viewport.GotoBottom()
}

func (m Model) renderViewportClipped() string {
	if !m.viewportReady || m.viewport.Height <= 0 || m.viewport.Width <= 0 {
		return ""
	}

	start := m.viewport.YOffset
	if start < 0 {
		start = 0
	}
	if start > len(m.output) {
		start = len(m.output)
	}

	end := start + m.viewport.Height
	if end > len(m.output) {
		end = len(m.output)
	}

	lines := make([]string, 0, m.viewport.Height)
	for _, line := range m.output[start:end] {
		lines = append(lines, ansi.Truncate(line, m.viewport.Width, ""))
	}

	for len(lines) < m.viewport.Height {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func (m Model) outputWrapWidth() int {
	if m.viewportReady && m.viewport.Width > 0 {
		return m.viewport.Width
	}
	if m.width > 2 {
		return m.width - 2
	}
	return 0
}

func (m Model) wrapOutputLine(line string) []string {
	width := m.outputWrapWidth()
	if width <= 0 {
		return strings.Split(line, "\n")
	}
	wrapped := ansi.Wrap(line, width, "")
	return strings.Split(wrapped, "\n")
}

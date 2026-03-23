package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/trngle-xyz/cli/internal/config"
	loopwallet "github.com/trngle-xyz/cli/internal/wallet/loop"
)

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

type setupCompleteMsg struct {
	cfg config.AppConfig
}

// walletVerifiedMsg is sent when async Loop auth + account fetch succeeds.
type walletVerifiedMsg struct {
	partyID string
}

// walletVerifyErrMsg is sent when async Loop auth + account fetch fails.
type walletVerifyErrMsg struct {
	err error
}

// ---------------------------------------------------------------------------
// Steps
// ---------------------------------------------------------------------------

type setupStep int

const (
	stepWallet setupStep = iota
	stepAPI
	stepTheme
)

// ---------------------------------------------------------------------------
// SetupWizard
// ---------------------------------------------------------------------------

type SetupWizard struct {
	configPath string
	baseConfig config.AppConfig
	width      int
	height     int
	step       setupStep
	focus      int

	// wallet step
	partyIDIn    textinput.Model
	privateKey   textinput.Model
	trngleAPIKey textinput.Model
	networks     []string
	networkIdx   int
	verifying    bool
	resolvedPID  string

	// api step
	apiEnabled  bool
	port        textinput.Model
	bind        textinput.Model
	authModes   []string
	authModeIdx int
	fixedToken  textinput.Model

	// theme step
	languages   []string
	themes      []string
	languageIdx int
	themeIdx    int

	// status
	successMessage string
	errorMessage   string
}

func NewSetupWizard(cfg config.AppConfig, configPath string) SetupWizard {
	// --- wallet inputs ---
	partyIDIn := textinput.New()
	partyIDIn.Placeholder = "paste from your Loop wallet"
	partyIDIn.Width = 72
	partyIDIn.CharLimit = 256

	privateKey := textinput.New()
	privateKey.Placeholder = "your private signing key (hex)"
	privateKey.EchoMode = textinput.EchoPassword
	privateKey.EchoCharacter = '•'
	privateKey.Width = 72
	privateKey.CharLimit = 256

	trngleAPIKey := textinput.New()
	trngleAPIKey.Placeholder = "your TRNGLE API key"
	trngleAPIKey.EchoMode = textinput.EchoPassword
	trngleAPIKey.EchoCharacter = '•'
	trngleAPIKey.Width = 72
	trngleAPIKey.CharLimit = 256

	// --- api inputs ---
	port := textinput.New()
	port.Placeholder = "8080"
	port.SetValue(fmt.Sprintf("%d", cfg.LocalAPI.Port))
	port.Width = 12
	port.CharLimit = 6

	bind := textinput.New()
	bind.Placeholder = "127.0.0.1"
	bind.SetValue(cfg.LocalAPI.Bind)
	bind.Width = 20
	bind.CharLimit = 32

	fixedToken := textinput.New()
	fixedToken.Placeholder = "API authentication token"
	fixedToken.SetValue(cfg.LocalAPI.FixedToken)
	fixedToken.Width = 36
	fixedToken.CharLimit = 256

	// --- lang / theme lists ---
	langs := make([]string, 0, len(GetAllLanguages()))
	for code := range GetAllLanguages() {
		langs = append(langs, code)
	}
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	sort.Strings(langs)

	themeKeys := make([]string, 0, len(themes))
	for key := range themes {
		themeKeys = append(themeKeys, key)
	}
	if len(themeKeys) == 0 {
		themeKeys = []string{"cyan"}
	}
	sort.Strings(themeKeys)

	if cfg.WalletPartyID != "" {
		partyIDIn.SetValue(cfg.WalletPartyID)
	}
	if cfg.PrivateKeyHex != "" {
		privateKey.SetValue(cfg.PrivateKeyHex)
	}
	if cfg.TrngleAPIKey != "" {
		trngleAPIKey.SetValue(cfg.TrngleAPIKey)
	}
	if bind.Value() == "" {
		bind.SetValue("127.0.0.1")
	}

	networks := loopwallet.Networks // ["mainnet", "testnet", "devnet"]

	w := SetupWizard{
		configPath:   configPath,
		baseConfig:   cfg,
		authModes:    []string{"none", "auto-token", "fixed-token"},
		networks:     networks,
		languages:    langs,
		themes:       themeKeys,
		apiEnabled:   cfg.LocalAPI.Enabled,
		partyIDIn:    partyIDIn,
		privateKey:   privateKey,
		trngleAPIKey: trngleAPIKey,
		port:         port,
		bind:         bind,
		fixedToken:   fixedToken,
		resolvedPID:  cfg.WalletPartyID,
	}
	w.authModeIdx = indexOf(w.authModes, cfg.LocalAPI.AuthMode)
	w.networkIdx = indexOf(w.networks, cfg.Network)
	if w.networkIdx < 0 {
		w.networkIdx = 0 // default to mainnet
	}
	w.languageIdx = indexOf(w.languages, cfg.Language)
	w.themeIdx = indexOf(w.themes, cfg.Theme)
	return w
}

func (s *SetupWizard) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s SetupWizard) Update(msg tea.Msg) (SetupWizard, tea.Cmd) {
	// Handle async verification results.
	switch msg := msg.(type) {
	case walletVerifiedMsg:
		s.verifying = false
		s.resolvedPID = msg.partyID
		s.successMessage = "Wallet connected — " + truncateAddress(msg.partyID)
		s.errorMessage = ""
		s.step = stepAPI
		s.focus = 0
		s.blurInputs()
		return s, nil

	case walletVerifyErrMsg:
		s.verifying = false
		s.errorMessage = msg.err.Error()
		s.successMessage = ""
		return s, nil
	}

	// Block input while verifying (except quit).
	if s.verifying {
		if km, ok := msg.(tea.KeyMsg); ok && km.String() == "ctrl+c" {
			return s, tea.Quit
		}
		return s, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			s.focusNext()
			return s, nil
		case "shift+tab", "up":
			s.focusPrev()
			return s, nil
		case "left":
			s.cycleCurrent(-1)
			return s, nil
		case "right":
			s.cycleCurrent(1)
			return s, nil
		case "enter":
			// Handle reset defaults button in API step.
			if s.step == stepAPI && s.showResetDefaults() && s.focus == resetDefaultsFocus {
				s.port.SetValue("8080")
				s.bind.SetValue("127.0.0.1")
				s.successMessage = "Reset to defaults"
				return s, nil
			}

			done, cfg, cmd, err := s.submitStep()
			if err != nil {
				s.errorMessage = err.Error()
				s.successMessage = ""
				return s, nil
			}
			if done {
				return s, func() tea.Msg { return setupCompleteMsg{cfg: cfg} }
			}
			if cmd != nil {
				return s, cmd
			}
			return s, nil
		}
	}

	return s.routeInputUpdate(msg)
}

func (s SetupWizard) routeInputUpdate(msg tea.Msg) (SetupWizard, tea.Cmd) {
	switch s.step {
	case stepWallet:
		// focus 0 = network (choice row, handled by left/right keys)
		if s.focus == 1 {
			var cmd tea.Cmd
			s.partyIDIn, cmd = s.partyIDIn.Update(msg)
			return s, cmd
		}
		if s.focus == 2 {
			var cmd tea.Cmd
			s.privateKey, cmd = s.privateKey.Update(msg)
			return s, cmd
		}
		if s.focus == 3 {
			var cmd tea.Cmd
			s.trngleAPIKey, cmd = s.trngleAPIKey.Update(msg)
			return s, cmd
		}
	case stepAPI:
		if s.focus == 1 {
			var cmd tea.Cmd
			s.port, cmd = s.port.Update(msg)
			return s, cmd
		}
		if s.focus == 2 {
			var cmd tea.Cmd
			s.bind, cmd = s.bind.Update(msg)
			return s, cmd
		}
		if s.focus == s.fixedTokenFocus() && s.authModes[s.authModeIdx] == "fixed-token" {
			var cmd tea.Cmd
			s.fixedToken, cmd = s.fixedToken.Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s SetupWizard) View(themeName string) string {
	t := themes["cyan"]
	if selected, ok := themes[themeName]; ok {
		t = selected
	}
	if selected, ok := themes[s.themes[s.themeIdx]]; ok {
		t = selected
	}

	// Each step builds its own self-contained widget with its own width.
	// The outer shell just centers the widget on screen.
	widget := s.renderStepWidget(t)

	return lipgloss.Place(s.width, s.height, lipgloss.Center, lipgloss.Center, widget)
}

// renderStepWidget builds the complete bordered box for the current step.
func (s SetupWizard) renderStepWidget(t ColorTheme) string {
	// Step-specific content and width.
	stepContent, widgetWidth := s.renderStepContent(t)

	innerWidth := widgetWidth - 6 // border + padding

	// Shared header: logo, title, subtitle, section title.
	logo := lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center,
		lipgloss.NewStyle().Foreground(t.Gradient2).Render(asciiLogo))
	title := lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center,
		lipgloss.NewStyle().Bold(true).Foreground(t.Gradient1).Render("TRNGLE — First-Time Setup"))
	subtitle := lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center,
		lipgloss.NewStyle().Foreground(muted).Render("Tab navigate • ←→ cycle • Enter confirm • Ctrl+C quit"))
	section := lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center, s.sectionTitle(t))

	// Action button.
	actionLabel, actionFocused := s.actionLabel()
	actionStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("252")).Background(lipgloss.Color("238")).Bold(true)
	if actionFocused {
		actionStyle = actionStyle.Foreground(lipgloss.Color("0")).Background(t.Gradient1)
	}
	action := lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center, actionStyle.Render("  "+actionLabel+"  "))

	// Status.
	var statusParts []string
	if s.verifying {
		statusParts = append(statusParts,
			lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center,
				lipgloss.NewStyle().Foreground(t.Gradient1).Bold(true).Render("⟳  Connecting to Loop...")))
	}
	if s.successMessage != "" {
		statusParts = append(statusParts,
			lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center,
				lipgloss.NewStyle().Foreground(special).Render("✓ "+s.successMessage)))
	}
	if s.errorMessage != "" {
		statusParts = append(statusParts,
			lipgloss.PlaceHorizontal(innerWidth, lipgloss.Center,
				lipgloss.NewStyle().Foreground(errorC).Render("✗ "+s.errorMessage)))
	}
	statusBlock := strings.Join(statusParts, "\n")

	// Assemble.
	body := lipgloss.JoinVertical(lipgloss.Left,
		logo, "", title, subtitle, "",
		section, "",
		stepContent, "",
		action, "",
		statusBlock,
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Gradient2).
		Padding(1, 2).
		Width(widgetWidth).
		Render(body)
}

// renderStepContent returns the field area string and the desired widget width.
func (s SetupWizard) renderStepContent(t ColorTheme) (string, int) {
	rows := s.stepRows(t)

	switch s.step {
	case stepWallet:
		// Wallet: wide widget, each row centered individually.
		w := s.width - 6
		if w < 82 {
			w = 82
		}
		inner := w - 6
		lines := make([]string, len(rows))
		for i, row := range rows {
			lines[i] = lipgloss.PlaceHorizontal(inner, lipgloss.Center, row)
		}
		return strings.Join(lines, "\n"), w

	default:
		// API / Theme: same wide widget as wallet, rows left-aligned inside.
		w := s.width - 6
		if w < 82 {
			w = 82
		}
		inner := w - 6
		// Left-align the rows block, then center the block within the widget.
		block := strings.Join(rows, "\n")
		return lipgloss.PlaceHorizontal(inner, lipgloss.Center,
			lipgloss.NewStyle().Width(60).Render(block)), w
	}
}

// ---------------------------------------------------------------------------
// Step rendering
// ---------------------------------------------------------------------------

func (s SetupWizard) sectionTitle(t ColorTheme) string {
	titles := map[setupStep]string{
		stepWallet: "── 1/3  Wallet Setup ──",
		stepAPI:    "── 2/3  Local API Setup ──",
		stepTheme:  "── 3/3  Preferences ──",
	}
	return lipgloss.NewStyle().Bold(true).Foreground(t.Gradient1).Render(titles[s.step])
}

func (s SetupWizard) stepRows(t ColorTheme) []string {
	switch s.step {
	case stepWallet:
		rows := []string{
			s.choiceRow("Network", s.networks[s.networkIdx], 0, t),
			s.inputRow("Party ID", s.partyIDIn.View(), 1, t),
			s.inputRow("Private Key", s.privateKey.View(), 2, t),
			s.inputRow("TRNGLE API Key", s.trngleAPIKey.View(), 3, t),
		}
		if s.resolvedPID != "" {
			display := s.resolvedPID
			if len(display) > 48 {
				display = display[:24] + "..." + display[len(display)-24:]
			}
			rows = append(rows, lipgloss.NewStyle().Foreground(special).PaddingLeft(18).Render("✓ "+display))
		}
		return rows

	case stepAPI:
		descStyle := lipgloss.NewStyle().Foreground(muted)
		defStyle := lipgloss.NewStyle().Foreground(muted).Italic(true)

		rows := []string{
			s.choiceRow("Enable API", yesNo(s.apiEnabled), 0, t),
		}

		// Port with default indicator.
		portView := s.inputRow("Port", s.port.View(), 1, t)
		if s.isDefaultPort() {
			portView += defStyle.Render("(default)")
		}
		rows = append(rows, portView)

		// Bind with default indicator.
		bindView := s.inputRow("Bind Address", s.bind.View(), 2, t)
		if s.isDefaultBind() {
			bindView += defStyle.Render("(default)")
		}
		rows = append(rows, bindView)

		// Network-exposed warning (inline, no hardcoded padding).
		if s.apiEnabled && strings.TrimSpace(s.bind.Value()) == "0.0.0.0" {
			rows = append(rows, lipgloss.NewStyle().Foreground(warning).Render("  ⚠ Network-exposed — anyone on your network can reach this"))
		}

		// Reset defaults hint when port or bind have been changed.
		if !s.isDefaultPort() || !s.isDefaultBind() {
			resetLabel := "Reset Defaults"
			if s.focus == resetDefaultsFocus {
				rows = append(rows, "  "+lipgloss.NewStyle().Foreground(t.Gradient1).Bold(true).Render("[ "+resetLabel+" ]"))
			} else {
				rows = append(rows, "  "+lipgloss.NewStyle().Foreground(muted).Render("[ "+resetLabel+" ]"))
			}
		}

		rows = append(rows, s.choiceRow("Auth Mode", s.authModes[s.authModeIdx], s.authModeFocus(), t))

		switch s.authModes[s.authModeIdx] {
		case "auto-token":
			rows = append(rows, "  "+descStyle.Render("Randomly generate a token"))
		case "fixed-token":
			rows = append(rows, "  "+descStyle.Render("Enter your own token manually"))
			rows = append(rows, s.inputRow("Fixed Token", s.fixedToken.View(), s.fixedTokenFocus(), t))
		case "none":
			rows = append(rows, "  "+descStyle.Render("No auth (suitable for single user machines)"))
		}
		return rows

	default:
		return []string{
			s.choiceRow("Language", s.langDisplayName(), 0, t),
			s.choiceRow("Theme", s.themeDisplayName(), 1, t),
		}
	}
}

func (s SetupWizard) actionLabel() (string, bool) {
	mf := s.maxFocus()
	switch s.step {
	case stepWallet:
		return "▶ Verify Wallet & Continue", s.focus == mf
	case stepAPI:
		return "▶ Continue", s.focus == mf
	default:
		return "▶ Launch TRNGLE", s.focus == mf
	}
}

// ---------------------------------------------------------------------------
// Step submission
// ---------------------------------------------------------------------------

func (s *SetupWizard) submitStep() (bool, config.AppConfig, tea.Cmd, error) {
	if s.focus != s.maxFocus() {
		s.focusNext()
		return false, config.AppConfig{}, nil, nil
	}

	switch s.step {
	case stepWallet:
		partyID := strings.TrimSpace(s.partyIDIn.Value())
		if partyID == "" {
			return false, config.AppConfig{}, nil, fmt.Errorf("party ID is required — find it in your Loop wallet")
		}
		pk := strings.TrimSpace(s.privateKey.Value())
		if pk == "" {
			return false, config.AppConfig{}, nil, fmt.Errorf("private key is required")
		}

		adapter := loopwallet.NewAdapter()
		if err := adapter.Initialize(pk); err != nil {
			return false, config.AppConfig{}, nil, fmt.Errorf("invalid private key: %w", err)
		}

		s.verifying = true
		s.errorMessage = ""
		s.successMessage = ""
		return false, config.AppConfig{}, verifyWalletCmd(pk, partyID, s.networks[s.networkIdx]), nil

	case stepAPI:
		portStr := strings.TrimSpace(s.port.Value())
		portNum, err := strconv.Atoi(portStr)
		if err != nil || portNum < 1024 || portNum > 65535 {
			return false, config.AppConfig{}, nil, fmt.Errorf("port must be 1024-65535 (lower ports require admin privileges)")
		}
		bindAddr := strings.TrimSpace(s.bind.Value())
		if bindAddr == "" {
			return false, config.AppConfig{}, nil, fmt.Errorf("bind address is required")
		}
		if net.ParseIP(bindAddr) == nil {
			return false, config.AppConfig{}, nil, fmt.Errorf("bind must be a valid IP address")
		}
		mode := s.authModes[s.authModeIdx]
		if mode == "fixed-token" && strings.TrimSpace(s.fixedToken.Value()) == "" {
			return false, config.AppConfig{}, nil, fmt.Errorf("fixed token is required when auth mode is fixed-token")
		}
		if s.apiEnabled && bindAddr != "127.0.0.1" && mode == "none" {
			return false, config.AppConfig{}, nil, fmt.Errorf("authentication is required when API is exposed beyond localhost (bind=%s); select auto-token or fixed-token auth mode", bindAddr)
		}
		if s.apiEnabled && bindAddr == "0.0.0.0" {
			s.successMessage = "⚠ API exposed on all interfaces — ensure firewall is configured"
		} else {
			s.successMessage = "API settings saved"
		}
		s.errorMessage = ""
		s.step = stepTheme
		s.focus = 0
		s.blurInputs()
		return false, config.AppConfig{}, nil, nil

	default:
		cfg, err := s.buildConfig()
		if err != nil {
			return false, config.AppConfig{}, nil, err
		}
		return true, cfg, nil, nil
	}
}

// verifyWalletCmd runs Loop auth + account fetch in a background goroutine.
func verifyWalletCmd(privateKeyHex, partyID, network string) tea.Cmd {
	return func() tea.Msg {
		adapter := loopwallet.NewAdapter()
		if err := adapter.Initialize(privateKeyHex); err != nil {
			return walletVerifyErrMsg{err: fmt.Errorf("key error: %w", err)}
		}

		net := strings.ToLower(strings.TrimSpace(network))
		if net == "" {
			net = "mainnet"
		}
		apiURL := loopwallet.NetworkAPIURLs[net]
		if apiURL == "" {
			apiURL = loopwallet.NetworkAPIURLs["mainnet"]
		}

		if err := adapter.Authenticate(partyID, apiURL); err != nil {
			return walletVerifyErrMsg{err: fmt.Errorf("auth failed: %w", err)}
		}

		account, err := adapter.FetchAccount()
		if err != nil {
			return walletVerifyErrMsg{err: fmt.Errorf("account fetch failed: %w", err)}
		}

		return walletVerifiedMsg{partyID: account.PartyID}
	}
}

// ---------------------------------------------------------------------------
// Build final config
// ---------------------------------------------------------------------------

func (s *SetupWizard) buildConfig() (config.AppConfig, error) {
	cfg := s.baseConfig
	cfg.WalletProvider = "loop"
	cfg.WalletPartyID = s.resolvedPID
	cfg.PrivateKeyHex = strings.TrimSpace(s.privateKey.Value())
	cfg.TrngleAPIKey = strings.TrimSpace(s.trngleAPIKey.Value())
	cfg.Network = s.networks[s.networkIdx]
	// Set the operator API URL based on the selected network
	cfg.TrngleAPIURL = config.APIURLForNetwork(cfg.Network)
	cfg.LocalAPI.Enabled = s.apiEnabled
	cfg.LocalAPI.Port = mustParseInt(strings.TrimSpace(s.port.Value()), cfg.LocalAPI.Port)
	cfg.LocalAPI.Bind = strings.TrimSpace(s.bind.Value())
	cfg.LocalAPI.AuthMode = s.authModes[s.authModeIdx]
	cfg.LocalAPI.FixedToken = strings.TrimSpace(s.fixedToken.Value())
	cfg.Language = s.languages[s.languageIdx]
	cfg.Theme = s.themes[s.themeIdx]

	if cfg.LocalAPI.AuthMode == "auto-token" && cfg.LocalAPI.FixedToken == "" {
		cfg.LocalAPI.FixedToken = generateToken()
	}

	if err := config.Save(s.configPath, cfg); err != nil {
		return config.AppConfig{}, fmt.Errorf("save config: %w", err)
	}
	loaded, err := config.Load(s.configPath)
	if err != nil {
		return config.AppConfig{}, fmt.Errorf("verify config: %w", err)
	}
	if loaded.PrivateKeyHex != cfg.PrivateKeyHex ||
		loaded.WalletPartyID != cfg.WalletPartyID ||
		loaded.LocalAPI.Port != cfg.LocalAPI.Port ||
		loaded.Theme != cfg.Theme ||
		loaded.LocalAPI.AuthMode != cfg.LocalAPI.AuthMode {
		return config.AppConfig{}, fmt.Errorf("config verification failed after save")
	}

	return cfg, nil
}

// ---------------------------------------------------------------------------
// Focus management
// ---------------------------------------------------------------------------

// resetDefaultsFocus is the focus index for the reset button (only present when non-default).
const resetDefaultsFocus = 3

func (s *SetupWizard) showResetDefaults() bool {
	return strings.TrimSpace(s.port.Value()) != "8080" || strings.TrimSpace(s.bind.Value()) != "127.0.0.1"
}

func (s *SetupWizard) authModeFocus() int {
	if s.showResetDefaults() {
		return 4 // enable, port, bind, reset, authmode
	}
	return 3 // enable, port, bind, authmode
}

func (s *SetupWizard) fixedTokenFocus() int {
	return s.authModeFocus() + 1
}

func (s *SetupWizard) maxFocus() int {
	switch s.step {
	case stepWallet:
		return 4 // network, partyid, pk, trngle-api-key, [verify]
	case stepAPI:
		base := s.authModeFocus() + 1 // authmode + [continue]
		if s.authModes[s.authModeIdx] == "fixed-token" {
			base++ // + fixedtoken
		}
		return base
	default:
		return 2 // language, theme, [launch]
	}
}

func (s *SetupWizard) focusNext() {
	s.errorMessage = ""
	s.blurInputs()
	s.focus++
	if s.focus > s.maxFocus() {
		s.focus = 0
	}
	s.focusInput()
}

func (s *SetupWizard) focusPrev() {
	s.errorMessage = ""
	s.blurInputs()
	s.focus--
	if s.focus < 0 {
		s.focus = s.maxFocus()
	}
	s.focusInput()
}

func (s *SetupWizard) focusInput() {
	switch s.step {
	case stepWallet:
		// focus 0 = network (choice row, no text input)
		if s.focus == 1 {
			s.partyIDIn.Focus()
		}
		if s.focus == 2 {
			s.privateKey.Focus()
		}
		if s.focus == 3 {
			s.trngleAPIKey.Focus()
		}
	case stepAPI:
		if s.focus == 1 {
			s.port.Focus()
		}
		if s.focus == 2 {
			s.bind.Focus()
		}
		if s.focus == s.fixedTokenFocus() && s.authModes[s.authModeIdx] == "fixed-token" {
			s.fixedToken.Focus()
		}
	}
}

func (s *SetupWizard) blurInputs() {
	s.partyIDIn.Blur()
	s.privateKey.Blur()
	s.trngleAPIKey.Blur()
	s.port.Blur()
	s.bind.Blur()
	s.fixedToken.Blur()
}

func (s *SetupWizard) cycleCurrent(direction int) {
	switch s.step {
	case stepWallet:
		if s.focus == 0 {
			s.networkIdx = cycle(s.networkIdx, len(s.networks), direction)
		}
	case stepAPI:
		if s.focus == 0 {
			s.apiEnabled = !s.apiEnabled
		}
		if s.focus == s.authModeFocus() {
			s.authModeIdx = cycle(s.authModeIdx, len(s.authModes), direction)
		}
	case stepTheme:
		if s.focus == 0 {
			s.languageIdx = cycle(s.languageIdx, len(s.languages), direction)
		}
		if s.focus == 1 {
			s.themeIdx = cycle(s.themeIdx, len(s.themes), direction)
		}
	}
}

// ---------------------------------------------------------------------------
// Row renderers
// ---------------------------------------------------------------------------

func (s SetupWizard) choiceRow(label, value string, field int, t ColorTheme) string {
	lbl := lipgloss.NewStyle().Foreground(muted).Width(16)
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	if s.focus == field {
		lbl = lbl.Foreground(t.Gradient1).Bold(true)
		val = val.Foreground(t.Gradient1).Bold(true)
	}
	arrows := ""
	if s.focus == field {
		arrows = lipgloss.NewStyle().Foreground(muted).Render("  ← →")
	}
	return "  " + lbl.Render(label) + val.Render("[ "+value+" ]") + arrows
}

func (s SetupWizard) inputRow(label, value string, field int, t ColorTheme) string {
	lbl := lipgloss.NewStyle().Foreground(muted).Width(16)
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	if s.focus == field {
		lbl = lbl.Foreground(t.Gradient1).Bold(true)
		val = val.Foreground(t.Gradient1)
	}
	return "  " + lbl.Render(label) + val.Render(value)
}

// ---------------------------------------------------------------------------
// Display helpers
// ---------------------------------------------------------------------------

func (s SetupWizard) langDisplayName() string {
	code := s.languages[s.languageIdx]
	lang := GetLanguage(code)
	return fmt.Sprintf("%s (%s)", lang.Name, code)
}

func (s SetupWizard) themeDisplayName() string {
	key := s.themes[s.themeIdx]
	if t, ok := themes[key]; ok {
		return t.Name
	}
	return key
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

func cycle(idx, size, direction int) int {
	if size == 0 {
		return 0
	}
	next := idx + direction
	if next >= size {
		return 0
	}
	if next < 0 {
		return size - 1
	}
	return next
}

func indexOf(values []string, target string) int {
	for i, v := range values {
		if strings.EqualFold(v, target) {
			return i
		}
	}
	return 0
}

func mustParseInt(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func generateToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure indicates a serious system problem.
		// Fall back to time-based entropy rather than a static placeholder.
		buf = []byte(fmt.Sprintf("%016x", time.Now().UnixNano()))
	}
	return hex.EncodeToString(buf)
}

const (
	defaultPort = "8080"
	defaultBind = "127.0.0.1"
)

func (s *SetupWizard) isDefaultPort() bool {
	return strings.TrimSpace(s.port.Value()) == defaultPort
}

func (s *SetupWizard) isDefaultBind() bool {
	return strings.TrimSpace(s.bind.Value()) == defaultBind
}

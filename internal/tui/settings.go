package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type SettingsField int

const (
	FieldNetwork SettingsField = iota
	FieldAPIPort
	FieldWalletAddress
	FieldWalletProviderKey
	FieldTrngleAPIKey
	FieldBuildButton
)

var settingsNetworks = []string{"mainnet", "testnet", "devnet"}

type SettingsPopup struct {
	visible           bool
	width             int
	height            int
	theme             string
	lang              string
	focusedField      SettingsField
	inputs            []textinput.Model
	networkIdx        int
	buildButtonFocus  bool
	errorMessage      string
	successMessage    string
}

type SettingsConfig struct {
	Network           string
	APIPort           string
	WalletAddress     string
	WalletProviderKey string
	TrngleAPIKey      string
}

type settingsBuildMsg struct {
	config SettingsConfig
}

type settingsCloseMsg struct{}

func NewSettingsPopup() SettingsPopup {
	inputs := make([]textinput.Model, 4)

	portInput := textinput.New()
	portInput.Placeholder = "8080"
	portInput.CharLimit = 6
	portInput.Width = 25
	portInput.Prompt = ""
	inputs[0] = portInput

	walletInput := textinput.New()
	walletInput.Placeholder = "0x..."
	walletInput.CharLimit = 256
	walletInput.Width = 25
	walletInput.Prompt = ""
	inputs[1] = walletInput

	providerKeyInput := textinput.New()
	providerKeyInput.Placeholder = "api-key"
	providerKeyInput.CharLimit = 256
	providerKeyInput.Width = 25
	providerKeyInput.Prompt = ""
	providerKeyInput.EchoMode = textinput.EchoPassword
	providerKeyInput.EchoCharacter = '•'
	inputs[2] = providerKeyInput

	trngleKeyInput := textinput.New()
	trngleKeyInput.Placeholder = "api-key"
	trngleKeyInput.CharLimit = 256
	trngleKeyInput.Width = 25
	trngleKeyInput.Prompt = ""
	trngleKeyInput.EchoMode = textinput.EchoPassword
	trngleKeyInput.EchoCharacter = '•'
	inputs[3] = trngleKeyInput

	return SettingsPopup{
		visible:      false,
		inputs:       inputs,
		networkIdx:   0,
		focusedField: FieldNetwork,
		theme:        "cyan",
		lang:         "en",
	}
}

func (s *SettingsPopup) SetTheme(theme string) {
	s.theme = theme
}

func (s *SettingsPopup) SetLang(lang string) {
	s.lang = lang
}

func (s *SettingsPopup) SetSize(width, height int) {
	s.width = width
	s.height = height
}

func (s *SettingsPopup) Show() {
	s.visible = true
	s.errorMessage = ""
	s.successMessage = ""
	s.focusedField = FieldNetwork
	s.buildButtonFocus = false
	for i := range s.inputs {
		s.inputs[i].Blur()
	}
}

func (s *SettingsPopup) Hide() {
	s.visible = false
}

func (s *SettingsPopup) IsVisible() bool {
	return s.visible
}

func (s *SettingsPopup) t(key string) string {
	lang := GetLanguage(s.lang)
	if text, ok := lang.Text[key]; ok {
		return text
	}
	return GetLanguage("en").Text[key]
}

func (s *SettingsPopup) getTheme() ColorTheme {
	if theme, ok := themes[s.theme]; ok {
		return theme
	}
	return themes["cyan"]
}

func (s *SettingsPopup) LoadConfig(config SettingsConfig) {
	// Set network index.
	s.networkIdx = 0
	for i, n := range settingsNetworks {
		if n == config.Network {
			s.networkIdx = i
			break
		}
	}
	s.inputs[0].SetValue(config.APIPort)
	s.inputs[1].SetValue(config.WalletAddress)
	s.inputs[2].SetValue(config.WalletProviderKey)
	s.inputs[3].SetValue(config.TrngleAPIKey)
}

func (s *SettingsPopup) GetConfig() SettingsConfig {
	return SettingsConfig{
		Network:           settingsNetworks[s.networkIdx],
		APIPort:           s.inputs[0].Value(),
		WalletAddress:     s.inputs[1].Value(),
		WalletProviderKey: s.inputs[2].Value(),
		TrngleAPIKey:      s.inputs[3].Value(),
	}
}

// inputIdx returns the index into s.inputs for a text-input field,
// or -1 for choice/button fields (FieldNetwork, FieldBuildButton).
func (s *SettingsPopup) inputIdx() int {
	switch s.focusedField {
	case FieldAPIPort:
		return 0
	case FieldWalletAddress:
		return 1
	case FieldWalletProviderKey:
		return 2
	case FieldTrngleAPIKey:
		return 3
	}
	return -1
}

func (s *SettingsPopup) focusNext() {
	if s.buildButtonFocus {
		s.buildButtonFocus = false
		s.focusedField = FieldNetwork
		return
	}
	if idx := s.inputIdx(); idx >= 0 {
		s.inputs[idx].Blur()
	}
	if s.focusedField == FieldTrngleAPIKey {
		s.buildButtonFocus = true
	} else {
		s.focusedField++
		if idx := s.inputIdx(); idx >= 0 {
			s.inputs[idx].Focus()
		}
	}
}

func (s *SettingsPopup) focusPrev() {
	if s.buildButtonFocus {
		s.buildButtonFocus = false
		s.focusedField = FieldTrngleAPIKey
		s.inputs[3].Focus()
		return
	}
	if idx := s.inputIdx(); idx >= 0 {
		s.inputs[idx].Blur()
	}
	if s.focusedField == FieldNetwork {
		s.buildButtonFocus = true
	} else {
		s.focusedField--
		if idx := s.inputIdx(); idx >= 0 {
			s.inputs[idx].Focus()
		}
	}
}

func (s SettingsPopup) Update(msg tea.Msg) (SettingsPopup, tea.Cmd) {
	if !s.visible {
		return s, nil
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			s.Hide()
			return s, func() tea.Msg { return settingsCloseMsg{} }

		case "tab", "down":
			s.focusNext()
			return s, nil

		case "shift+tab", "up":
			s.focusPrev()
			return s, nil

		case "left":
			if s.focusedField == FieldNetwork {
				s.networkIdx = (s.networkIdx - 1 + len(settingsNetworks)) % len(settingsNetworks)
				return s, nil
			}

		case "right":
			if s.focusedField == FieldNetwork {
				s.networkIdx = (s.networkIdx + 1) % len(settingsNetworks)
				return s, nil
			}

		case "enter":
			if s.buildButtonFocus {
				config := s.GetConfig()
				if config.APIPort == "" {
					s.errorMessage = s.t("settingsPortRequired")
					return s, nil
				}
				if config.WalletAddress == "" {
					s.errorMessage = s.t("settingsWalletRequired")
					return s, nil
				}
				s.errorMessage = ""
				s.successMessage = s.t("settingsBuildStarted")
				return s, func() tea.Msg { return settingsBuildMsg{config: config} }
			}
			s.focusNext()
			return s, nil
		}
	}

	if idx := s.inputIdx(); idx >= 0 && !s.buildButtonFocus {
		var cmd tea.Cmd
		s.inputs[idx], cmd = s.inputs[idx].Update(msg)
		cmds = append(cmds, cmd)
	}

	return s, tea.Batch(cmds...)
}

func (s SettingsPopup) View() string {
	if !s.visible {
		return ""
	}

	theme := s.getTheme()

	popupWidth := 50

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.Gradient1)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Width(18).
		Align(lipgloss.Right).
		PaddingRight(1)

	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	focusedInputStyle := lipgloss.NewStyle().
		Foreground(theme.Gradient1)

	buttonStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Background(lipgloss.Color("238")).
		Foreground(lipgloss.Color("252"))

	focusedButtonStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Background(theme.Gradient1).
		Foreground(lipgloss.Color("0")).
		Bold(true)

	hintStyle := lipgloss.NewStyle().
		Foreground(muted).
		Italic(true)

	title := titleStyle.Render("⚙  " + s.t("settingsTitle"))

	// Network choice row.
	networkLabel := labelStyle.Render(s.t("settingsNetwork"))
	networkVal := settingsNetworks[s.networkIdx]
	networkFocused := s.focusedField == FieldNetwork && !s.buildButtonFocus
	var networkRow string
	if networkFocused {
		networkRow = networkLabel + focusedInputStyle.Render("◄ "+networkVal+" ►")
	} else {
		networkRow = networkLabel + inputStyle.Render("  "+networkVal)
	}

	fields := []struct {
		label    string
		field    SettingsField
		inputIdx int
	}{
		{s.t("settingsAPIPort"), FieldAPIPort, 0},
		{s.t("settingsWalletAddress"), FieldWalletAddress, 1},
		{s.t("settingsProviderKey"), FieldWalletProviderKey, 2},
		{s.t("settingsTrngleKey"), FieldTrngleAPIKey, 3},
	}

	var rows []string
	rows = append(rows, networkRow)
	for _, f := range fields {
		label := labelStyle.Render(f.label)
		isFocused := s.focusedField == f.field && !s.buildButtonFocus

		inputView := s.inputs[f.inputIdx].View()
		inputLines := strings.Split(inputView, "\n")
		inputValue := ""
		if len(inputLines) > 0 {
			inputValue = inputLines[0]
		}

		if isFocused {
			row := label + focusedInputStyle.Render("> ") + inputValue
			rows = append(rows, row)
		} else {
			row := label + inputStyle.Render("  ") + inputValue
			rows = append(rows, row)
		}
	}

	var buildBtn string
	if s.buildButtonFocus {
		buildBtn = focusedButtonStyle.Render("▶ " + s.t("settingsBuild"))
	} else {
		buildBtn = buttonStyle.Render("  " + s.t("settingsBuild"))
	}

	var statusRow string
	if s.errorMessage != "" {
		statusRow = "\n" + lipgloss.NewStyle().Foreground(errorC).Render("  ✗ "+s.errorMessage)
	} else if s.successMessage != "" {
		statusRow = "\n" + lipgloss.NewStyle().Foreground(special).Render("  ✓ "+s.successMessage)
	}

	hint := hintStyle.Render(s.t("settingsHint"))

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		strings.Join(rows, "\n"),
		"",
		"  "+buildBtn,
		statusRow,
		"",
		hint,
	)

	popupStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.Gradient2).
		Padding(1, 3).
		Width(popupWidth)

	popup := popupStyle.Render(content)
	popupHeight := lipgloss.Height(popup)
	popupW := lipgloss.Width(popup)

	topPadding := 0
	leftPadding := 0

	if s.height > 0 {
		topPadding = (s.height - popupHeight) / 2
		if topPadding < 0 {
			topPadding = 0
		}
	}
	if s.width > 0 {
		leftPadding = (s.width - popupW) / 2
		if leftPadding < 0 {
			leftPadding = 0
		}
	}

	popupLines := strings.Split(popup, "\n")
	leftPad := strings.Repeat(" ", leftPadding)
	for i, line := range popupLines {
		popupLines[i] = leftPad + line
	}

	var result []string
	for i := 0; i < topPadding; i++ {
		result = append(result, "")
	}
	result = append(result, popupLines...)

	return strings.Join(result, "\n")
}


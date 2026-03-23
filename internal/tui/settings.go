package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type SettingsField int

const (
	FieldAPIPort SettingsField = iota
	FieldWalletAddress
	FieldWalletProviderKey
	FieldTrngleAPIKey
	FieldBuildButton
)

type SettingsPopup struct {
	visible           bool
	width             int
	height            int
	theme             string
	lang              string
	focusedField      SettingsField
	inputs            []textinput.Model
	buildButtonFocus  bool
	errorMessage      string
	successMessage    string
}

type SettingsConfig struct {
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
	inputs[FieldAPIPort] = portInput

	walletInput := textinput.New()
	walletInput.Placeholder = "0x..."
	walletInput.CharLimit = 256
	walletInput.Width = 25
	walletInput.Prompt = ""
	inputs[FieldWalletAddress] = walletInput

	providerKeyInput := textinput.New()
	providerKeyInput.Placeholder = "api-key"
	providerKeyInput.CharLimit = 256
	providerKeyInput.Width = 25
	providerKeyInput.Prompt = ""
	providerKeyInput.EchoMode = textinput.EchoPassword
	providerKeyInput.EchoCharacter = '•'
	inputs[FieldWalletProviderKey] = providerKeyInput

	trngleKeyInput := textinput.New()
	trngleKeyInput.Placeholder = "api-key"
	trngleKeyInput.CharLimit = 256
	trngleKeyInput.Width = 25
	trngleKeyInput.Prompt = ""
	trngleKeyInput.EchoMode = textinput.EchoPassword
	trngleKeyInput.EchoCharacter = '•'
	inputs[FieldTrngleAPIKey] = trngleKeyInput

	inputs[FieldAPIPort].Focus()

	return SettingsPopup{
		visible:      false,
		inputs:       inputs,
		focusedField: FieldAPIPort,
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
	s.focusedField = FieldAPIPort
	s.buildButtonFocus = false
	for i := range s.inputs {
		s.inputs[i].Blur()
	}
	s.inputs[FieldAPIPort].Focus()
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
	s.inputs[FieldAPIPort].SetValue(config.APIPort)
	s.inputs[FieldWalletAddress].SetValue(config.WalletAddress)
	s.inputs[FieldWalletProviderKey].SetValue(config.WalletProviderKey)
	s.inputs[FieldTrngleAPIKey].SetValue(config.TrngleAPIKey)
}

func (s *SettingsPopup) GetConfig() SettingsConfig {
	return SettingsConfig{
		APIPort:           s.inputs[FieldAPIPort].Value(),
		WalletAddress:     s.inputs[FieldWalletAddress].Value(),
		WalletProviderKey: s.inputs[FieldWalletProviderKey].Value(),
		TrngleAPIKey:      s.inputs[FieldTrngleAPIKey].Value(),
	}
}

func (s *SettingsPopup) focusNext() {
	if s.buildButtonFocus {
		s.buildButtonFocus = false
		s.focusedField = FieldAPIPort
		s.inputs[s.focusedField].Focus()
		return
	}

	s.inputs[s.focusedField].Blur()

	if s.focusedField == FieldTrngleAPIKey {
		s.buildButtonFocus = true
	} else {
		s.focusedField++
		s.inputs[s.focusedField].Focus()
	}
}

func (s *SettingsPopup) focusPrev() {
	if s.buildButtonFocus {
		s.buildButtonFocus = false
		s.focusedField = FieldTrngleAPIKey
		s.inputs[s.focusedField].Focus()
		return
	}

	s.inputs[s.focusedField].Blur()

	if s.focusedField == FieldAPIPort {
		s.buildButtonFocus = true
	} else {
		s.focusedField--
		s.inputs[s.focusedField].Focus()
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

	if !s.buildButtonFocus {
		var cmd tea.Cmd
		s.inputs[s.focusedField], cmd = s.inputs[s.focusedField].Update(msg)
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

	fields := []struct {
		label string
		field SettingsField
	}{
		{s.t("settingsAPIPort"), FieldAPIPort},
		{s.t("settingsWalletAddress"), FieldWalletAddress},
		{s.t("settingsProviderKey"), FieldWalletProviderKey},
		{s.t("settingsTrngleKey"), FieldTrngleAPIKey},
	}

	var rows []string
	for _, f := range fields {
		label := labelStyle.Render(f.label)
		isFocused := s.focusedField == f.field && !s.buildButtonFocus

		inputView := s.inputs[f.field].View()
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


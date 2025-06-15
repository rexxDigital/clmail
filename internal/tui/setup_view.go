package tui

// huge thanks to the examples at bubble teas GitHub!

import (
	"database/sql"
	"fmt"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rexxDigital/clmail/internal/accounts"
	"github.com/rexxDigital/clmail/internal/db"
	"strconv"
	"strings"
)

type keyMap struct {
	Up   key.Binding
	Down key.Binding
	Next key.Binding
	Quit key.Binding
	Help key.Binding
}

// ShortHelp returns keybindings to be shown in the mini help view.
// This is part of the key.Map interface.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

// FullHelp returns keybindings for the expanded help view.
// This is part of the key.Map interface.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Next}, // first column
		{k.Help, k.Quit},       // second column
	}
}

// Initialize our keymap
var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "ctrl+k"),
		key.WithHelp("↑/ctrl+k", "move up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "ctrl+j", "tab"),
		key.WithHelp("↓/ctrl+j/tab", "move down"),
	),
	Next: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "next"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("esc", "ctrl+c"),
		key.WithHelp("esc", "quit"),
	),
}

type accountCreatedMsg struct {
	success bool
	err     error
}

type SetupView struct {
	inputs     []textinput.Model
	paginator  paginator.Model
	keys       keyMap
	help       help.Model
	showHelp   bool
	focusIndex int
	email      string
	password   string
	imap       bool
	mapServer  string
	mapPort    string
	smtpServer string
	smtpPort   string
	active     bool
	cursorMode cursor.Mode
	width      int
	height     int
	errorMsg   string
	dbClient   *db.Client
}

func NewSetupView(width, height int, dbClient *db.Client) *SetupView {
	// Create text inputs
	inputs := make([]textinput.Model, 6)

	// Email
	inputs[0] = textinput.New()
	inputs[0].Placeholder = "Email"
	inputs[0].Focus()
	inputs[0].Width = 30

	// Password input
	inputs[1] = textinput.New()
	inputs[1].Placeholder = "Password"
	inputs[1].EchoMode = textinput.EchoPassword
	inputs[1].Width = 30

	// *map server input
	inputs[2] = textinput.New()
	inputs[2].Placeholder = "*map Server"
	inputs[2].Width = 30

	// *map port input
	inputs[3] = textinput.New()
	inputs[3].Placeholder = "*map Port"
	inputs[3].Width = 30

	// *map port input
	inputs[4] = textinput.New()
	inputs[4].Placeholder = "SMTP Server"
	inputs[4].Width = 30

	// *map port input
	inputs[5] = textinput.New()
	inputs[5].Placeholder = "SMTP Port"
	inputs[5].Width = 30

	p := paginator.New()
	p.Type = paginator.Dots
	p.PerPage = 2
	p.ActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
	p.SetTotalPages(len(inputs))

	// Disable the built-in keybindings by setting them to no-op bindings
	p.KeyMap.NextPage = key.NewBinding()
	p.KeyMap.PrevPage = key.NewBinding()

	return &SetupView{
		keys:       keys,
		help:       help.New(),
		showHelp:   true,
		inputs:     inputs,
		paginator:  p,
		focusIndex: 0,
		width:      width,
		height:     height,
		dbClient:   dbClient,
	}
}

func (m *SetupView) Init() tea.Cmd {
	return textinput.Blink
}

func (m *SetupView) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.HandleWindowSizeMsg(msg)
		return m, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		// Set focus to next input
		case key.Matches(msg, m.keys.Up) || key.Matches(msg, m.keys.Down) || key.Matches(msg, m.keys.Next):
			s := msg.String()

			// We want to get the page we are on and how many inputs we have per page
			// So we can select the button instead of an invisible input field
			currentPage := m.paginator.Page
			inputsPerPage := m.paginator.PerPage

			startIdx := currentPage * inputsPerPage
			endIdx := min(startIdx+inputsPerPage, len(m.inputs))

			if s == "enter" && m.focusIndex == endIdx {
				// If we're not on the last page, move to the next page
				if currentPage < m.paginator.TotalPages-1 {
					m.paginator.NextPage()
					// Focus the first input on the new page
					m.focusIndex = (currentPage + 1) * inputsPerPage
				} else if currentPage == m.paginator.TotalPages-1 && m.focusIndex == len(m.inputs) {
					// This is the final submit button on the last page
					return m, m.createAccount()
				}

				cmds := make([]tea.Cmd, len(m.inputs))
				for i := 0; i < len(m.inputs); i++ {
					if i == m.focusIndex {
						cmds[i] = m.inputs[i].Focus()
						m.inputs[i].PromptStyle = focusedStyle
						m.inputs[i].TextStyle = focusedStyle
					} else {
						m.inputs[i].Blur()
						m.inputs[i].PromptStyle = noStyle
						m.inputs[i].TextStyle = noStyle
					}
				}

				return m, tea.Batch(cmds...)
			}

			// Cycle indexes
			if s == "ctrl+k" || s == "shift+tab" || s == "up" {
				m.focusIndex--
			} else {
				m.focusIndex++
			}

			if m.focusIndex > len(m.inputs) {
				m.focusIndex = 0
			} else if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs)
			}

			// Handle focus boundaries for the current page
			if m.focusIndex > endIdx {
				m.focusIndex = startIdx
			} else if m.focusIndex < startIdx {
				m.focusIndex = endIdx
			}

			cmds := make([]tea.Cmd, len(m.inputs))
			for i := 0; i <= len(m.inputs)-1; i++ {
				if i == m.focusIndex {
					// Set focused state
					cmds[i] = m.inputs[i].Focus()
					m.inputs[i].PromptStyle = focusedStyle
					m.inputs[i].TextStyle = focusedStyle
					continue
				}
				// Remove focused state
				m.inputs[i].Blur()
				m.inputs[i].PromptStyle = noStyle
				m.inputs[i].TextStyle = noStyle
			}

			return m, tea.Batch(cmds...)

		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			// This is the crucial part - set the help mode based on showHelp
			if m.showHelp {
				m.help.ShowAll = true // Show full help
			} else {
				m.help.ShowAll = false // Show short help
			}

			return m, nil
		}
	case accountCreatedMsg:
		if msg.success {
			return m, func() tea.Msg {
				return SwitchViewMsg{ViewName: "home"}
			}
		} else {
			m.errorMsg = msg.err.Error()
			return m, nil
		}
	}

	// Handle character input and blinking
	cmd := m.updateInputs(message)

	m.paginator, _ = m.paginator.Update(message)

	return m, cmd
}

func (m *SetupView) View() string {
	modalStyle := lipgloss.NewStyle().
		BorderForeground(subtleColor).
		Width(m.width).
		Height(m.height - 40).
		Align(lipgloss.Center)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(subtleColor).
		MarginBottom(1)

	footerStyle := lipgloss.NewStyle().
		Foreground(subtleColor).
		MarginTop(1).
		Faint(true)

	titleStr := titleStyle.Render("Cagi Setup")

	start, end := m.paginator.GetSliceBounds(len(m.inputs))

	form := "Please enter your login information:\n\n"

	for i, input := range m.inputs[start:end] {
		form += input.View() + "\n"
		if i == len(m.inputs)-1 {
			form += "\n"
		}
	}

	currentPage := m.paginator.Page
	isLastPage := currentPage == m.paginator.TotalPages-1
	inputsPerPage := m.paginator.PerPage
	startIdx := currentPage * inputsPerPage
	endIdx := min(startIdx+inputsPerPage, len(m.inputs))

	var btn strings.Builder

	var button *string

	if isLastPage {
		button = &blurredSubmitButton
		if m.focusIndex == len(m.inputs) {
			button = &focusedSubmitButton
		}
	} else {
		button = &blurredNextButton
		if m.focusIndex == endIdx {
			button = &focusedNextButton
		}
	}
	fmt.Fprintf(&btn, "\n\n%s\n\n", *button)

	var errorMsgBuilder strings.Builder

	if m.errorMsg != "" {
		errorMsg := fmt.Sprintf("%s\n\n", errorStyle.Render(m.errorMsg))
		fmt.Fprintf(&errorMsgBuilder, "\n\n%s\n\n", errorMsg)
	}

	var footerStr string
	if m.showHelp {
		footerStr = footerStyle.Render(m.help.View(m.keys))
	} else {
		footerStr = footerStyle.Render("Press ? for help\n")
	}

	return modalStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Center,
			titleStr,
			form,
			btn.String(),
			errorMsgBuilder.String(),
			m.paginator.View(),
			footerStr,
		),
	)
}

func (m *SetupView) HandleWindowSizeMsg(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
}

func (m *SetupView) getFormData() *db.CreateAccountParams {
	return &db.CreateAccountParams{
		Name:                   m.inputs[0].Value(),
		DisplayName:            m.inputs[0].Value(),
		Email:                  m.inputs[0].Value(),
		ImapServer:             m.inputs[2].Value(),
		ImapPort:               parsePort(m.inputs[3].Value(), 993),
		ImapUsername:           strings.Split(m.inputs[0].Value(), "@")[0],
		ImapUseSsl:             true,
		ImapAuthMethod:         "plain",
		SmtpServer:             m.inputs[4].Value(),
		SmtpPort:               parsePort(m.inputs[5].Value(), 587),
		SmtpUsername:           strings.Split(m.inputs[0].Value(), "@")[0],
		SmtpUseTls:             true,
		SmtpAuthMethod:         "plain",
		RefreshIntervalMinutes: 5,
		Signature:              sql.NullString{},
		IsDefault:              true,
	}
}

func parsePort(port string, defaultPort int64) int64 {
	if port == "" {
		return defaultPort
	}
	p, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		return defaultPort
	}
	return p
}

func (m *SetupView) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *SetupView) createAccount() tea.Cmd {
	return func() tea.Msg {
		params := *m.getFormData()
		pwd := m.inputs[1].Value()

		err := accounts.CreateAccount(params, pwd, m.dbClient)
		if err != nil {
			return accountCreatedMsg{success: false, err: err}
		}

		return accountCreatedMsg{success: true, err: nil}
	}
}

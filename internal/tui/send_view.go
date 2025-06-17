package tui

import (
	"fmt"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rexxDigital/clmail/internal/accounts"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/internal/smtp"
	"github.com/rexxDigital/clmail/types"
	"math/rand"
	"strings"
	"time"
)

const (
	fieldSubject = iota
	fieldFrom
	fieldTo
	fieldCC
	fieldBody
	maxField = fieldBody
)

type SendView struct {
	types.Mail
	account       *db.Account
	dbClient      *db.Client
	subjectArea   textarea.Model
	fromArea      textarea.Model
	toArea        textarea.Model
	ccArea        textarea.Model
	bodyArea      textarea.Model
	isSending     bool
	selectedInput int

	width  int
	height int
}

type mailSendMsg struct {
	success bool
	err     error
}

func NewSendView(width, height int, account *db.Account, mail types.Mail, dbClient *db.Client) *SendView {
	subjectArea := textarea.New()
	subjectArea.SetHeight(1)
	subjectArea.CharLimit = 200
	subjectArea.ShowLineNumbers = false

	fromArea := textarea.New()
	fromArea.SetHeight(1)
	fromArea.CharLimit = 100
	fromArea.ShowLineNumbers = false
	if account != nil {
		fromArea.SetValue(account.Email)
	}

	toArea := textarea.New()
	toArea.SetHeight(1)
	toArea.CharLimit = 200
	toArea.ShowLineNumbers = false

	ccArea := textarea.New()
	ccArea.SetHeight(1)
	ccArea.CharLimit = 200
	ccArea.ShowLineNumbers = false

	bodyArea := textarea.New()
	bodyArea.SetHeight(10)
	bodyArea.ShowLineNumbers = false

	if mail.InReplyTo != "" {
		subjectArea.SetValue("Re: " + mail.Subject)
		toArea.SetValue(mail.To)
		bodyArea.SetValue(formatReply(mail))
	} else {
		subjectArea.Placeholder = "Enter subject..."
		toArea.Placeholder = "recipient@example.com"
		ccArea.Placeholder = "cc@example.com (optional)"
		bodyArea.Placeholder = "Type your message here..."
	}

	sendView := &SendView{
		account:       account,
		Mail:          mail,
		dbClient:      dbClient,
		subjectArea:   subjectArea,
		fromArea:      fromArea,
		toArea:        toArea,
		ccArea:        ccArea,
		bodyArea:      bodyArea,
		isSending:     false,
		selectedInput: fieldSubject,
		width:         width,
		height:        height,
	}

	sendView.focusField(fieldSubject)

	return sendView

}

func (m *SendView) focusField(field int) {
	m.subjectArea.Blur()
	m.fromArea.Blur()
	m.toArea.Blur()
	m.ccArea.Blur()
	m.bodyArea.Blur()

	switch field {
	case fieldSubject:
		m.subjectArea.Focus()
	case fieldFrom:
		m.fromArea.Focus()
	case fieldTo:
		m.toArea.Focus()
	case fieldCC:
		m.ccArea.Focus()
	case fieldBody:
		m.bodyArea.Focus()
	}
}

func (m *SendView) HandleWindowSizeMsg(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
}

func (m *SendView) Init() tea.Cmd {
	return textarea.Blink
}

func (m *SendView) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.HandleWindowSizeMsg(msg)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg {
				return SwitchViewMsg{ViewName: "home"}
			}
		case "ctrl+s":
			if !m.isSending {
				m.isSending = true
				return m, m.sendMail()
			}
		case "tab":
			m.selectedInput++
			if m.selectedInput > maxField {
				m.selectedInput = fieldSubject
			}
			m.focusField(m.selectedInput)
			return m, nil
		case "shift+tab":
			m.selectedInput--
			if m.selectedInput < fieldSubject {
				m.selectedInput = maxField
			}
			m.focusField(m.selectedInput)
			return m, nil
		}

	case mailSendMsg:
		m.isSending = false
		if msg.success {
			return m, func() tea.Msg {
				return SwitchViewMsg{ViewName: "home"}
			}
		}

		return m, nil
	}

	var cmd tea.Cmd
	switch m.selectedInput {
	case fieldSubject:
		m.subjectArea, cmd = m.subjectArea.Update(message)
	case fieldFrom:
		m.fromArea, cmd = m.fromArea.Update(message)
	case fieldTo:
		m.toArea, cmd = m.toArea.Update(message)
	case fieldCC:
		m.ccArea, cmd = m.ccArea.Update(message)
	case fieldBody:
		m.bodyArea, cmd = m.bodyArea.Update(message)
	}
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *SendView) View() string {
	headerStyle := lipgloss.NewStyle().
		Background(backgroundColor).
		Foreground(subtleColor).
		Padding(0, 1).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(subtleColor).
		MarginRight(1)

	selectedLabelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(highlightColor).
		Padding(0, 1).
		MarginRight(1)

	fieldStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		MarginBottom(1)

	selectedFieldStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		MarginBottom(1)

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		MarginTop(1)

	var header string
	if m.Mail.InReplyTo != "" {
		header = fmt.Sprintf("📧 CLMAIL - Replying to: %s", m.Mail.Subject)
	} else {
		header = "📧 CLMAIL - Compose New Email"
	}
	headerView := headerStyle.Width(m.width).Render(header)

	var content strings.Builder

	subjectLabel := labelStyle.Render("Subject:")
	if m.selectedInput == fieldSubject {
		subjectLabel = selectedLabelStyle.Render("Subject:")
	}
	subjectField := m.subjectArea.View()
	if m.selectedInput == fieldSubject {
		subjectField = selectedFieldStyle.Render(subjectField)
	} else {
		subjectField = fieldStyle.Render(subjectField)
	}
	content.WriteString(subjectLabel + "\n" + subjectField + "\n")

	fromLabel := labelStyle.Render("From:")
	if m.selectedInput == fieldFrom {
		fromLabel = selectedLabelStyle.Render("From:")
	}
	fromField := m.fromArea.View()
	if m.selectedInput == fieldFrom {
		fromField = selectedFieldStyle.Render(fromField)
	} else {
		fromField = fieldStyle.Render(fromField)
	}
	content.WriteString(fromLabel + "\n" + fromField + "\n")

	toLabel := labelStyle.Render("To:")
	if m.selectedInput == fieldTo {
		toLabel = selectedLabelStyle.Render("To:")
	}
	toField := m.toArea.View()
	if m.selectedInput == fieldTo {
		toField = selectedFieldStyle.Render(toField)
	} else {
		toField = fieldStyle.Render(toField)
	}
	content.WriteString(toLabel + "\n" + toField + "\n")

	ccLabel := labelStyle.Render("CC:")
	if m.selectedInput == fieldCC {
		ccLabel = selectedLabelStyle.Render("CC:")
	}
	ccField := m.ccArea.View()
	if m.selectedInput == fieldCC {
		ccField = selectedFieldStyle.Render(ccField)
	} else {
		ccField = fieldStyle.Render(ccField)
	}
	content.WriteString(ccLabel + "\n" + ccField + "\n")

	bodyLabel := labelStyle.Render("Message:")
	if m.selectedInput == fieldBody {
		bodyLabel = selectedLabelStyle.Render("Message:")
	}
	bodyField := m.bodyArea.View()
	if m.selectedInput == fieldBody {
		bodyField = selectedFieldStyle.Render(bodyField)
	} else {
		bodyField = fieldStyle.Render(bodyField)
	}
	content.WriteString(bodyLabel + "\n" + bodyField + "\n")

	var helpText string
	if m.isSending {
		helpText = "Sending email..."
	} else {
		helpText = "Tab/Shift+Tab: Navigate • Ctrl+S: Send • Esc: Cancel"
	}
	help := helpStyle.Render(helpText)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		headerView,
		content.String(),
		help,
	)

}

func (m *SendView) sendMail() tea.Cmd {
	return func() tea.Msg {
		if err := m.validateForm(); err != nil {
			return mailSendMsg{
				success: false,
				err:     err,
			}
		}

		password, _ := accounts.GetPassword(m.account.Email)

		domain := strings.Split(m.account.Email, "@")
		if len(domain) != 2 {
			domain[1] = "localhost"
		}

		date := time.Now()

		messageID := fmt.Sprintf("<%d.%d@%s>", date.Unix(), rand.Int63(), domain[1])
		m.Mail.MessageID = messageID

		referenceList := m.createReferenceList()

		err := smtp.SendMail(types.Mail{
			MessageID:  messageID,
			References: referenceList,
			To:         m.toArea.Value(),
			From:       m.fromArea.Value(),
			Subject:    m.subjectArea.Value(),
			Body:       m.bodyArea.Value(),
			Date:       date,
			InReplyTo:  m.InReplyTo,
		}, m.account, password, m.dbClient)
		if err != nil {
			return mailSendMsg{
				success: false,
				err:     err,
			}
		}

		return mailSendMsg{
			success: true,
			err:     nil,
		}
	}
}

func (m *SendView) createReferenceList() string {
	references := ""

	if m.Mail.References != "" {
		existing := strings.Split(m.Mail.References, ",")
		for i, ref := range existing {
			if i != len(existing)-1 {
				references = references + "<" + ref + "> "
			} else {
				references = references + "<" + ref + ">"
			}
		}
	}

	if m.Mail.MessageID != "" {
		references = references + m.Mail.MessageID
	}

	return references
}

func (m *SendView) validateForm() error {
	if m.subjectArea.Value() == "" {
		return fmt.Errorf("subject is required")
	}
	if m.toArea.Value() == "" {
		return fmt.Errorf("to is required")
	}
	if m.bodyArea.Value() == "" {
		return fmt.Errorf("body is required")
	}

	return nil
}

func formatReply(mail types.Mail) string {
	var reply strings.Builder

	reply.WriteString("\r\n")

	reply.WriteString(fmt.Sprintf("On %s, %s wrote:\n",
		mail.Date.Format("Jan 2, 2006 at 3:04 PM"),
		mail.To))

	quotedBody := quoteText(mail.Body)
	reply.WriteString(quotedBody)

	return reply.String()
}

func quoteText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	var quoted strings.Builder

	for _, line := range lines {
		quoted.WriteString("> " + line + "\n")
	}

	return quoted.String()
}

package tui

import (
	"context"
	"database/sql/driver"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/types"
	"log"
	"maps"
	"slices"
	"strings"
	"time"
)

type HomeView struct {
	dbClient          *db.Client
	accounts          []db.Account
	currentAccount    *db.Account
	threads           []db.GetThreadsInFolderRow
	selectedThread    []db.Email
	selectedFolder    int
	selectedThreadInt int
	selectedEmail     int
	folders           map[int]db.Folder
	activePanel       int // 0: folders, 1: email list, 2: email content
	width             int
	height            int
	loading           bool
	threadsViewport   viewport.Model
	contentViewport   viewport.Model
}

const (
	FolderPanel = iota
	EmailListPanel
	ContentPanel
)

func NewHomeView(width, height int, dbClient *db.Client) *HomeView {
	homeView := &HomeView{
		loading:           true,
		selectedFolder:    0,
		selectedThreadInt: 0,
		activePanel:       FolderPanel,
		width:             width,
		height:            height,
		dbClient:          dbClient,
		folders:           make(map[int]db.Folder),
	}

	homeView.threadsViewport = viewport.New(0, 0)
	homeView.contentViewport = viewport.New(0, 0)

	// load initial data from db
	homeView.loadAccounts()
	homeView.loadFolders()
	homeView.loadThreads()

	return homeView

}

func (m *HomeView) HandleWindowSizeMsg(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height

	// update viewport sizes
	folderWidth := min(25, m.width/5)
	emailListWidth := min(60, m.width/3)
	contentWidth := m.width - folderWidth - emailListWidth - 6

	// calculate available height and viewport height
	availableHeight := m.height - 4
	viewportHeight := availableHeight - 2

	m.threadsViewport.Width = emailListWidth - 4
	m.threadsViewport.Height = viewportHeight
	m.contentViewport.Width = contentWidth - 4
	m.contentViewport.Height = viewportHeight
}

func (m *HomeView) Init() tea.Cmd {
	return m.tickDatabase()
}

func (m *HomeView) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := message.(type) {
	case tickMsg:
		return m, m.tickDatabase()
	case tea.WindowSizeMsg:
		m.HandleWindowSizeMsg(msg)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "l":
			// cycle through panels
			m.activePanel = (m.activePanel + 1) % 3
		case "h":
			m.activePanel = (m.activePanel - 1 + 3) % 3
		case "up", "k":
			switch m.activePanel {
			case FolderPanel:
				if m.selectedFolder > 0 {
					m.selectedFolder--
					m.SelectFolder(m.selectedFolder)
					if len(m.threads) > 0 {
						m.selectedEmail = 0
						m.loadThreadEmails(m.threads[m.selectedThreadInt].ID)
					}
				}
			case EmailListPanel:
				if m.selectedThreadInt > 0 {
					m.selectedThreadInt--
					if len(m.threads) > 0 {
						m.selectedEmail = 0
						m.loadThreadEmails(m.threads[m.selectedThreadInt].ID)
					}
					m.updateThreadsViewport()
				} else {
					m.threadsViewport, cmd = m.threadsViewport.Update(msg)
					cmds = append(cmds, cmd)
				}
			case ContentPanel:
				if m.selectedEmail > 0 {
					m.selectedEmail--
					m.updateContentViewport()
				} else {
					m.contentViewport, cmd = m.contentViewport.Update(msg)
					cmds = append(cmds, cmd)
				}
			}
		case "down", "j":
			switch m.activePanel {
			case FolderPanel:
				if m.selectedFolder < len(m.folders)-1 {
					m.selectedFolder++
					m.SelectFolder(m.selectedFolder)
					if len(m.threads) > 0 {
						m.selectedEmail = 0
						m.loadThreadEmails(m.threads[m.selectedThreadInt].ID)
					}
				}
			case EmailListPanel:
				if m.selectedThreadInt < len(m.threads)-1 {
					m.selectedThreadInt++
					if len(m.threads) > 0 {
						m.selectedEmail = 0
						m.loadThreadEmails(m.threads[m.selectedThreadInt].ID)
					}
					m.updateThreadsViewport()
				} else {
					m.threadsViewport, cmd = m.threadsViewport.Update(msg)
					cmds = append(cmds, cmd)
				}
			case ContentPanel:
				if m.selectedEmail < len(m.selectedThread)-1 {
					m.selectedEmail++
					m.updateContentViewport()
				} else {
					m.contentViewport, cmd = m.contentViewport.Update(msg)
					cmds = append(cmds, cmd)
				}
			}
		case "r":
			return m, func() tea.Msg {
				return SwitchViewMsg{ViewName: "send", Account: m.currentAccount, Mail: m.GetSelectedMail()}
			}
		default:
			// pass other keys to active viewport for scrolling
			switch m.activePanel {
			case EmailListPanel:
				m.threadsViewport, cmd = m.threadsViewport.Update(msg)
				cmds = append(cmds, cmd)
			case ContentPanel:
				m.contentViewport, cmd = m.contentViewport.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *HomeView) updateThreadsViewport() {
	emailListContent := m.buildThreadsContent()
	m.threadsViewport.SetContent(emailListContent)
}

func (m *HomeView) updateContentViewport() {
	contentData := m.buildContentData()
	m.contentViewport.SetContent(contentData)
}

func (m *HomeView) buildThreadsContent() string {
	emailListContent := strings.Builder{}
	emailListContent.WriteString(lipgloss.NewStyle().Bold(true).Render("Threads") + "\n\n")

	for i, thread := range m.threads {
		unreadCount := 0
		folderCount := 0
		unread, _ := thread.FolderUnreadCount.Value()
		folderCount += int(thread.FolderCount)
		unreadCount += convertToInt(unread)

		sender := thread.LatestFolderSender

		emailItem := fmt.Sprintf("%v %-20s\n%s\n%s",
			thread.LatestFolderSenderName,
			"<"+truncateString(sender, 20)+">",
			truncateString(thread.Subject, m.threadsViewport.Width-6),
			thread.LatestMessageDate.Format("2006-01-02 15:04"))

		if i == m.selectedThreadInt && m.activePanel == EmailListPanel {
			emailListContent.WriteString(lipgloss.NewStyle().Foreground(highlightColor).Bold(true).BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(m.threadsViewport.Width).Render("> "+emailItem) + "\n\n")
		} else if i == m.selectedThreadInt {
			emailListContent.WriteString(lipgloss.NewStyle().Foreground(specialColor).Bold(true).BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(m.threadsViewport.Width).Render("> "+emailItem) + "\n\n")
		} else {
			emailListContent.WriteString(lipgloss.NewStyle().Foreground(subtleColor).Bold(true).BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(m.threadsViewport.Width).Render(emailItem) + "\n\n")
		}
	}

	return emailListContent.String()
}

func (m *HomeView) buildContentData() string {
	if m.selectedThread == nil || len(m.selectedThread) == 0 {
		return "No email selected"
	}

	email := m.selectedThread[m.selectedEmail]

	var bodyText string
	if !email.BodyText.Valid {
		bodyText = "No body text available"
	} else {
		bodyText = email.BodyText.String
	}

	headerLines := []string{
		fmt.Sprintf("From: %s", email.FromAddress),
		fmt.Sprintf("To: %s", email.ToAddresses),
		fmt.Sprintf("Date: %s", email.ReceivedDate.Format("2006-01-02 15:04")),
	}

	header := strings.Join(headerLines, "\n")

	content := strings.Builder{}

	subjectText := email.Subject
	content.WriteString(lipgloss.NewStyle().
		Bold(true).
		Foreground(highlightColor).
		Render(subjectText))
	content.WriteString("\n\n")

	content.WriteString(lipgloss.NewStyle().
		Foreground(subtleColor).
		Render(header))
	content.WriteString("\n\n")

	content.WriteString(strings.Repeat("─", min(m.contentViewport.Width-6, 50)))
	content.WriteString("\n\n")

	content.WriteString(bodyText)

	if len(m.selectedThread) > 1 {
		content.WriteString("\n\n")
		content.WriteString(strings.Repeat("─", min(m.contentViewport.Width-6, 50)))
		navText := fmt.Sprintf("\nEmail %d of %d in thread (use j/k to navigate)",
			m.selectedEmail+1, len(m.selectedThread))
		content.WriteString(lipgloss.NewStyle().
			Foreground(subtleColor).
			Italic(true).
			Render(navText))
	}

	return content.String()
}

func (m *HomeView) View() string {
	// get width for the different parts
	folderWidth := min(25, m.width/5)
	emailListWidth := min(60, m.width/3)
	contentWidth := m.width - folderWidth - emailListWidth - 6

	headerStyle := lipgloss.NewStyle().
		Background(backgroundColor).
		Foreground(subtleColor).
		Padding(0, 1).
		Bold(true)

	header := ""
	if m.currentAccount != nil {
		header = fmt.Sprintf("📧 CLMAIL - %s (%s)", m.currentAccount.Name, m.currentAccount.Email)
	} else {
		header = "📧 CLMAIL - No Account Selected"
	}

	headerView := headerStyle.Width(m.width).Render(header)

	// pretty status bar :)
	statusStyle := lipgloss.NewStyle().
		Background(backgroundColor).
		Foreground(highlightColor).
		Padding(0, 1)

	status := ""
	if m.loading {
		status = "⏳ Loading..."
	} else if m.currentAccount != nil {
		unreadCount := 0
		folderCount := 0
		for _, thread := range m.threads {
			unread, err := thread.FolderUnreadCount.Value()
			if err != nil {
				continue
			}
			folderCount += int(thread.FolderCount)
			unreadCount += convertToInt(unread)
		}
		// TODO: change to threads instead of mails, and fix reading/unread in mail
		status = fmt.Sprintf("📊 %d threads • %d unread • h/l: panels • j/k: navigate • r: reply • q: quit",
			folderCount, unreadCount)
	} else {
		status = "ℹ️ No accounts configured. Press Esc to go back and add an account."
	}

	statusView := statusStyle.Width(m.width).Render(status)

	// we need - 4 for our beautiful top panel!
	availableHeight := m.height - 4

	viewportHeight := availableHeight - 2

	// update viewport sizes if needed
	if m.threadsViewport.Width != emailListWidth-4 || m.threadsViewport.Height != viewportHeight {
		m.threadsViewport.Width = emailListWidth - 4
		m.threadsViewport.Height = viewportHeight
		m.updateThreadsViewport()
	}

	if m.contentViewport.Width != contentWidth-4 || m.contentViewport.Height != viewportHeight {
		m.contentViewport.Width = contentWidth - 4
		m.contentViewport.Height = viewportHeight
		m.updateContentViewport()
	}

	folderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Height(availableHeight).
		Width(folderWidth)

	if m.activePanel == FolderPanel {
		folderStyle = folderStyle.BorderForeground(highlightColor).Border(lipgloss.RoundedBorder())
	}

	folderContent := strings.Builder{}
	folderContent.WriteString(lipgloss.NewStyle().Bold(true).Render("Folders") + "\n\n")

	keys := slices.Sorted(maps.Keys(m.folders))

	for i, folder := range keys {
		if i == m.selectedFolder && m.activePanel == FolderPanel {
			folderContent.WriteString(lipgloss.NewStyle().Foreground(highlightColor).Bold(true).Render("> "+m.folders[folder].Name) + "\n")
		} else if i == m.selectedFolder {
			folderContent.WriteString(lipgloss.NewStyle().Foreground(specialColor).Bold(true).Render("> "+m.folders[folder].Name) + "\n")
		} else {
			folderContent.WriteString("  " + m.folders[folder].Name + "\n")
		}
	}

	foldersView := folderStyle.Render(folderContent.String())

	// email list panel with viewport
	emailListStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Height(availableHeight).
		Width(emailListWidth)

	if m.activePanel == EmailListPanel {
		emailListStyle = emailListStyle.BorderForeground(highlightColor).Border(lipgloss.RoundedBorder())
	}

	emailsView := emailListStyle.Render(m.threadsViewport.View())

	// all the beautiful mail content!
	contentStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Height(availableHeight).
		Width(contentWidth)

	if m.activePanel == ContentPanel {
		contentStyle = contentStyle.BorderForeground(highlightColor).Border(lipgloss.RoundedBorder())
	}

	contentView := contentStyle.Render(m.contentViewport.View())

	mainView := lipgloss.JoinHorizontal(
		lipgloss.Top,
		foldersView,
		emailsView,
		contentView,
	)

	appStyle := lipgloss.NewStyle()

	return appStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		headerView,
		mainView,
		statusView,
	))
}

func (m *HomeView) tickDatabase() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		m.loadThreads()
		return tickMsg{}
	})
}

type tickMsg struct{}

func (m *HomeView) loadAccounts() {
	accounts, err := m.dbClient.ListAccounts(context.Background())
	if err != nil {
		m.loading = false
		return
	}

	m.accounts = accounts
	if len(accounts) > 0 {
		m.currentAccount = &accounts[0]
	}
	m.loading = false
}

func (m *HomeView) loadFolders() {
	folders, err := m.dbClient.ListFolders(context.Background(), m.accounts[0].ID)
	if err != nil {
		return
	}

	for _, folder := range folders {
		f := strings.ToLower(folder.Name)
		switch {
		case strings.Contains(f, "inbox"):
			m.folders[0] = folder
		case strings.Contains(f, "drafts"):
			m.folders[1] = folder
		case strings.Contains(f, "sent"):
			m.folders[2] = folder
		case strings.Contains(f, "junk"), strings.Contains(f, "spam"):
			m.folders[3] = folder
		case strings.Contains(f, "trash"), strings.Contains(f, "deleted"):
			m.folders[4] = folder
		default:
			i := len(m.folders)
			m.folders[i] = folder
		}
	}
}

func (m *HomeView) loadThreads() {
	threads, err := m.dbClient.GetThreadsInFolder(context.Background(), db.GetThreadsInFolderParams{
		FolderID:  m.folders[m.selectedFolder].ID,
		AccountID: m.currentAccount.ID,
		// TODO: Pagination
		Limit: 10,
	})

	if err != nil {
		m.loading = false
		log.Printf("Failed to get threads: %v", err)
		return
	}

	m.threads = threads
	m.loading = false
	m.updateThreadsViewport()
}

func (m *HomeView) loadThreadEmails(threadID int64) {
	emails, err := m.dbClient.ListEmailsByThread(context.Background(), threadID)
	if err != nil {
		m.loading = false
		return
	}
	m.selectedThread = emails
	m.updateContentViewport()
}

func (m *HomeView) SelectFolder(folderID int) {
	m.selectedFolder = folderID
	m.selectedThreadInt = 0
	m.selectedEmail = 0
	m.selectedThread = nil
	m.loadThreads()
}

func (m *HomeView) SelectAccount(account *db.Account) {
	m.currentAccount = account
}

func (m *HomeView) GetSelectedMail() *types.Mail {
	mail := m.selectedThread[m.selectedEmail]
	return &types.Mail{
		MessageID:  mail.MessageID,
		References: mail.ReferenceID.String,
		To:         mail.FromAddress,
		From:       m.currentAccount.Email,
		Subject:    mail.Subject,
		Body:       mail.BodyText.String,
		CC:         nil,
		Date:       mail.ReceivedDate,
		InReplyTo:  mail.MessageID,
	}
}

// helper
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// just in case they are huge
func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}

	if len(s) <= maxLen {
		return s
	}

	if maxLen <= 3 {
		return s[:maxLen]
	}

	return s[:maxLen-3] + "..."
}

func convertToInt(value driver.Value) int {
	switch v := value.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v) // Convert float64 to int
	case int:
		return v
	case nil:
		return 0
	default:
		return 0
	}
}

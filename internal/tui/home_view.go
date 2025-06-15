package tui

import (
	"context"
	"database/sql/driver"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rexxDigital/clmail/internal/db"
	"log"
	"maps"
	"slices"
	"strings"
	"time"
)

type HomeView struct {
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
	dbClient          *db.Client
	accounts          []db.Account
	currentAccount    *db.Account
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

	// load initial data from db
	homeView.loadAccounts()
	homeView.loadFolders()
	homeView.loadThreads()

	return homeView

}

func (m *HomeView) HandleWindowSizeMsg(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
}

func (m *HomeView) Init() tea.Cmd {
	return m.tickDatabase()
}

func (m *HomeView) Update(message tea.Msg) (tea.Model, tea.Cmd) {
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
					m.selectedEmail = 0
				}
			case EmailListPanel:
				if m.selectedThreadInt > 0 {
					m.selectedThreadInt--
					if len(m.threads) > 0 {
						m.selectedEmail = 0
						m.loadThreadEmails(m.threads[m.selectedThreadInt].ID)
					}
				}
			case ContentPanel:
				if m.selectedEmail > 0 {
					m.selectedEmail--
				}
			}
		case "down", "j":
			switch m.activePanel {
			case FolderPanel:
				if m.selectedFolder < len(m.folders)-1 {
					m.selectedFolder++
					m.SelectFolder(m.selectedFolder)
					m.selectedEmail = 0
				}
			case EmailListPanel:
				if m.selectedThreadInt < len(m.threads)-1 {
					m.selectedThreadInt++
					if len(m.threads) > 0 {
						m.selectedEmail = 0
						m.loadThreadEmails(m.threads[m.selectedThreadInt].ID)
					}
				}
			case ContentPanel:
				if m.selectedEmail < len(m.selectedThread)-1 {
					m.selectedEmail++
				}
			}
		case "ctrl+a":

		}
	}

	return m, nil
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
		header = fmt.Sprintf("📧 CAGI - %s (%s)", m.currentAccount.Name, m.currentAccount.Email)
	} else {
		header = "📧 CAGI - No Account Selected"
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
		// TODO: USE GetEmailsStats FROM DB
		status = fmt.Sprintf("📊 %d threads • %d unread • h/j/k/l: navigate • r: reply • q: quit", folderCount, unreadCount)
	} else {
		status = "ℹ️ No accounts configured. Press Esc to go back and add an account."
	}

	statusView := statusStyle.Width(m.width).Render(status)

	// We need - 4 for out beautiful top panel!
	contentHeight := m.height - 4

	appStyle := lipgloss.NewStyle()

	folderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Height(contentHeight).
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

	emailListStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Height(contentHeight).
		Width(emailListWidth)

	if m.activePanel == EmailListPanel {
		emailListStyle = emailListStyle.BorderForeground(highlightColor).Border(lipgloss.RoundedBorder())
	}

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
			truncateString(thread.Subject, emailListWidth-6),
			thread.LatestMessageDate.Format("2006-01-02 15:04"))

		if i == m.selectedThreadInt && m.activePanel == EmailListPanel {
			emailListContent.WriteString(lipgloss.NewStyle().Foreground(highlightColor).Bold(true).BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(emailListWidth-4).Render("> "+emailItem) + "\n\n")
		} else if i == m.selectedThreadInt {
			emailListContent.WriteString(lipgloss.NewStyle().Foreground(specialColor).Bold(true).BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(emailListWidth-4).Render("> "+emailItem) + "\n\n")
		} else {
			emailListContent.WriteString(lipgloss.NewStyle().Foreground(subtleColor).Bold(true).BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(emailListWidth-4).Render(emailItem) + "\n\n")
		}
	}

	emailsView := emailListStyle.Render(emailListContent.String())

	// all the beautiful mail content!
	contentStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Height(contentHeight).
		Width(contentWidth)

	if m.activePanel == ContentPanel {
		contentStyle = contentStyle.BorderForeground(highlightColor).Border(lipgloss.RoundedBorder())
	}

	threadContent := strings.Builder{}
	if m.selectedThread != nil && len(m.selectedThread) > 0 {
		for i, email := range m.selectedThread {
			var bodyText string
			if !email.BodyText.Valid {
				bodyText = "No body text available"
			} else {
				bodyText = email.BodyText.String
			}

			contentData := lipgloss.NewStyle().BorderBottom(true).BorderStyle(lipgloss.MarkdownBorder()).Width(contentWidth - 4).Render(fmt.Sprintf(
				"From: %s\nTo: %s\nDate: %s\n\n\n%s",
				email.FromAddress,
				email.ToAddresses,
				email.ReceivedDate.Format("2006-01-02 15:04"),
				bodyText,
			))

			if m.selectedEmail == i && m.activePanel == ContentPanel {
				threadContent.WriteString(lipgloss.NewStyle().Bold(true).Foreground(highlightColor).Render(email.Subject) + "\n\n" + contentData + "\n\n")
			} else if m.selectedEmail == i {
				threadContent.WriteString(lipgloss.NewStyle().Bold(true).Foreground(specialColor).Render(email.Subject) + "\n\n" + contentData + "\n\n")
			} else {
				threadContent.WriteString(lipgloss.NewStyle().Bold(true).Render(email.Subject) + "\n\n" + contentData + "\n\n")
			}
		}
	} else {
		threadContent.WriteString("No email selected")
	}

	contentView := contentStyle.Render(threadContent.String())

	mainView := lipgloss.JoinHorizontal(
		lipgloss.Top,
		foldersView,
		emailsView,
		contentView,
	)

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
}

func (m *HomeView) loadThreadEmails(threadID int64) {
	emails, err := m.dbClient.ListEmailsByThread(context.Background(), threadID)
	if err != nil {
		m.loading = false
		return
	}
	m.selectedThread = emails
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

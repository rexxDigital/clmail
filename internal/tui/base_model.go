package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/types"
)

type SwitchViewMsg struct {
	ViewName string
	Account  *db.Account
	Mail     *types.Mail
}

type BaseModel struct {
	currentView tea.Model
	width       int
	height      int
	hasAccount  bool
	dbClient    *db.Client
}

func NewBaseModel(dbClient *db.Client) *BaseModel {
	return &BaseModel{
		currentView: NewSetupView(0, 0, dbClient),
		width:       0,
		height:      0,
		dbClient:    dbClient,
	}
}

func (m *BaseModel) HandleWindowSizeMsg(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
}

func (m *BaseModel) Init() tea.Cmd {
	return m.checkConfig
}

func (m *BaseModel) checkConfig() tea.Msg {
	ctx := context.Background()
	accounts, err := m.dbClient.ListAccounts(ctx)
	if err != nil {
		return accountExists(false)
	}

	return accountExists(len(accounts) > 0)
}

type accountExists bool

func (m *BaseModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.HandleWindowSizeMsg(msg)
	case SwitchViewMsg:
		switch msg.ViewName {
		case "home":
			m.currentView = NewHomeView(m.width, m.height, m.dbClient)
			return m, m.currentView.Init()
		case "setup":
			m.currentView = NewSetupView(m.width, m.height, m.dbClient)
			return m, m.currentView.Init()
		case "send":
			mail := &types.Mail{}
			if msg.Mail != nil {
				mail = msg.Mail
			}
			m.currentView = NewSendView(m.width, m.height, msg.Account, *mail, m.dbClient)
			return m, m.currentView.Init()
		}
	case accountExists:
		m.hasAccount = bool(msg)
		if m.hasAccount {
			m.currentView = NewHomeView(m.width, m.height, m.dbClient)
			return m, m.currentView.Init()
		} else {
			m.currentView = NewSetupView(m.width, m.height, m.dbClient)
			return m, m.currentView.Init()
		}
	}

	newView, cmd := m.currentView.Update(message)
	if newModel, ok := newView.(tea.Model); ok && newModel != m.currentView {
		m.currentView = newModel
	}

	return m, cmd
}

func (m *BaseModel) View() string {
	return m.currentView.View()
}

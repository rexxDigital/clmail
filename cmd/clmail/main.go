package main

import (
	_ "embed"
	"github.com/rexxDigital/clmail/internal/accounts"
	"github.com/rexxDigital/clmail/internal/db"
	"github.com/rexxDigital/clmail/internal/imap"
	"github.com/rexxDigital/clmail/internal/tui"
	"log"
	_ "modernc.org/sqlite"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	dbClient, err := db.NewClient()
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer dbClient.Close()

	password, _ := accounts.GetPassword("kagi@copland.se")

	imapcl, err := imap.NewImapClient(db.Account{
		Email:      "kagi",
		ImapServer: "mail.copland.se",
		ImapPort:   993,
		ID:         1,
	}, password, dbClient)
	if err != nil {
		log.Fatalf("Failed to init imap client: %v", err)
	}
	defer imapcl.Close()

	go imapcl.Idle("INBOX")

	if _, err := tea.NewProgram(tui.NewBaseModel(dbClient), tea.WithAltScreen()).Run(); err != nil {
		os.Exit(1)
	}
}

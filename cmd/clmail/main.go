package main

import (
	_ "embed"
	"github.com/rexxDigital/clmail/internal/db"
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

	baseModel := tui.NewBaseModel(dbClient)

	defer baseModel.Close()

	if _, err := tea.NewProgram(baseModel, tea.WithAltScreen()).Run(); err != nil {
		os.Exit(1)
	}
}

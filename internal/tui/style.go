package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
)

var (
	subtleColor     = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#DDC7A1"}
	highlightColor  = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#E78A4E"}
	specialColor    = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#D8A657"}
	errorColor      = lipgloss.AdaptiveColor{Light: "#C14A4A", Dark: "#EA6962"}
	backgroundColor = lipgloss.AdaptiveColor{Light: "#eee0b7", Dark: "#504945"}
	focusedStyle    = lipgloss.NewStyle().Foreground(highlightColor)
	blurredStyle    = lipgloss.NewStyle().Foreground(subtleColor)
	errorStyle      = lipgloss.NewStyle().Foreground(errorColor)
	noStyle         = lipgloss.NewStyle()

	focusedSubmitButton = focusedStyle.Render("[ Submit ]")
	blurredSubmitButton = fmt.Sprintf("[ %s ]", blurredStyle.Render("Submit"))
	focusedNextButton   = focusedStyle.Render("[ Next ]")
	blurredNextButton   = fmt.Sprintf("[ %s ]", blurredStyle.Render("Next"))
)

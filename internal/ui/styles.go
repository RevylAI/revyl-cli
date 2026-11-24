// Package ui provides terminal styling, output formatting, progress, and interactive prompt helpers.
package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

var (
	isInputTTY  = isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	isOutputTTY = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	isStderrTTY = isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())
)

var isTTY = isStderrTTY

// IsInputTTY reports the cached initialization-time TTY classification of os.Stdin.
func IsInputTTY() bool {
	return isInputTTY
}

func clearLine() {
	if isTTY {
		fmt.Fprint(os.Stderr, "\r\033[K")
		statusLineOpen = false
	}
}

func ClearLine() {
	clearLine()
}

var (
	Purple  = lipgloss.Color("#9D61FF")
	Teal    = lipgloss.Color("#14B8A6")
	Red     = lipgloss.Color("#EF4444")
	Amber   = lipgloss.Color("#F59E0B")
	Green   = lipgloss.Color("#22C55E")
	Gray    = lipgloss.Color("#6B7280")
	DimGray = lipgloss.Color("#9CA3AF")
)

var (
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Purple)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(Green).
			Bold(true)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(Red).
			Bold(true)

	WarningStyle = lipgloss.NewStyle().
			Foreground(Amber)

	InfoStyle = lipgloss.NewStyle()

	DimStyle = lipgloss.NewStyle().
			Foreground(DimGray)

	LinkStyle = lipgloss.NewStyle().
			Foreground(Purple).
			Underline(true)

	AccentStyle = lipgloss.NewStyle().
			Foreground(Purple)

	BoldInfoStyle = lipgloss.NewStyle().
			Bold(true)
)

var (
	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Purple).
			Padding(0, 1)

	BoxTitleStyle = lipgloss.NewStyle().
			Foreground(Purple).
			Bold(true)

	ResultBoxPassedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Green).
				Padding(0, 1)

	ResultBoxFailedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Red).
				Padding(0, 1)
)

var (
	TableHeaderStyle = lipgloss.NewStyle().
				Foreground(DimGray).
				Bold(true).
				Padding(0, 2)

	TableCellStyle = lipgloss.NewStyle().
			Padding(0, 2)
)

var (
	StatusPassedStyle = lipgloss.NewStyle().
				Foreground(Green)

	StatusFailedStyle = lipgloss.NewStyle().
				Foreground(Red)

	StatusRunningStyle = lipgloss.NewStyle().
				Foreground(Teal)
)

var (
	ProgressBarStyle = lipgloss.NewStyle().
		Foreground(Purple)
)

var (
	DiffAddStyle = lipgloss.NewStyle().
			Foreground(Green)

	DiffRemoveStyle = lipgloss.NewStyle().
			Foreground(Red)

	DiffContextStyle = lipgloss.NewStyle().
				Foreground(DimGray)
)

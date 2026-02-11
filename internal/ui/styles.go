// Package ui provides terminal UI components using Charm libraries.
//
// This package contains all the styling, rendering, and interactive
// components for the Revyl CLI's beautiful terminal interface.
package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// isTTY reports whether stdout is connected to a terminal.
// When false, ANSI escape codes should be avoided since output may be piped.
var isTTY = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())

// clearLine emits an ANSI escape sequence to clear the current line.
// If stdout is not a TTY, this is a no-op to avoid garbage output in pipes.
func clearLine() {
	if isTTY {
		fmt.Print("\r\033[K")
	}
}

// Brand colors for Revyl.
var (
	// Primary brand color - Revyl purple
	Purple = lipgloss.Color("#9D61FF")

	// Secondary colors
	Teal    = lipgloss.Color("#14B8A6")
	Red     = lipgloss.Color("#EF4444")
	Amber   = lipgloss.Color("#F59E0B")
	Green   = lipgloss.Color("#22C55E")
	Gray    = lipgloss.Color("#6B7280")
	DimGray = lipgloss.Color("#9CA3AF")
)

// Text styles.
var (
	// TitleStyle for main headings
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Purple)

	// SuccessStyle for success messages
	SuccessStyle = lipgloss.NewStyle().
			Foreground(Green).
			Bold(true)

	// ErrorStyle for error messages
	ErrorStyle = lipgloss.NewStyle().
			Foreground(Red).
			Bold(true)

	// WarningStyle for warning messages
	WarningStyle = lipgloss.NewStyle().
			Foreground(Amber)

	// InfoStyle for informational messages
	InfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB"))

	// DimStyle for less important text
	DimStyle = lipgloss.NewStyle().
			Foreground(DimGray)

	// LinkStyle for URLs
	LinkStyle = lipgloss.NewStyle().
			Foreground(Purple).
			Underline(true)

	// AccentStyle for purple-highlighted elements (numbers, indicators)
	AccentStyle = lipgloss.NewStyle().
			Foreground(Purple)

	// BoldInfoStyle for highlighted option labels
	BoldInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB")).
			Bold(true)
)

// Box styles.
var (
	// BoxStyle for content boxes
	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Purple).
			Padding(0, 1)

	// BoxTitleStyle for box titles
	BoxTitleStyle = lipgloss.NewStyle().
			Foreground(Purple).
			Bold(true)

	// ResultBoxPassedStyle for passed test results
	ResultBoxPassedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Green).
				Padding(0, 1)

	// ResultBoxFailedStyle for failed test results
	ResultBoxFailedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Red).
				Padding(0, 1)
)

// Table styles.
var (
	// TableHeaderStyle for table headers
	TableHeaderStyle = lipgloss.NewStyle().
				Foreground(DimGray).
				Bold(true).
				Padding(0, 2)

	// TableCellStyle for table cells
	TableCellStyle = lipgloss.NewStyle().
			Padding(0, 2)
)

// Status indicator styles.
var (
	// StatusPassedStyle for passed status
	StatusPassedStyle = lipgloss.NewStyle().
				Foreground(Green)

	// StatusFailedStyle for failed status
	StatusFailedStyle = lipgloss.NewStyle().
				Foreground(Red)

	// StatusRunningStyle for running status
	StatusRunningStyle = lipgloss.NewStyle().
				Foreground(Teal)
)

// Progress bar styles.
var (
	// ProgressBarStyle for the progress bar container
	ProgressBarStyle = lipgloss.NewStyle().
		Foreground(Purple)
)

// Diff styles.
var (
	// DiffAddStyle for added lines
	DiffAddStyle = lipgloss.NewStyle().
			Foreground(Green)

	// DiffRemoveStyle for removed lines
	DiffRemoveStyle = lipgloss.NewStyle().
			Foreground(Red)

	// DiffContextStyle for context lines
	DiffContextStyle = lipgloss.NewStyle().
				Foreground(DimGray)
)

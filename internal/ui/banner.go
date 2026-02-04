// Package ui provides the ASCII banner for the Revyl CLI.
package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Banner is the ASCII art logo for Revyl CLI.
const banner = `
  ██████╗ ███████╗██╗   ██╗██╗   ██╗██╗     
  ██╔══██╗██╔════╝██║   ██║╚██╗ ██╔╝██║     
  ██████╔╝█████╗  ██║   ██║ ╚████╔╝ ██║     
  ██╔══██╗██╔══╝  ╚██╗ ██╔╝  ╚██╔╝  ██║     
  ██║  ██║███████╗ ╚████╔╝    ██║   ███████╗
  ╚═╝  ╚═╝╚══════╝  ╚═══╝     ╚═╝   ╚══════╝`

// tagline is the product tagline.
const tagline = "Proactive Reliability for Mobile Apps"

// PrintBanner prints the Revyl banner with version info.
//
// Parameters:
//   - version: The CLI version string to display
func PrintBanner(version string) {
	// Style the banner with purple color
	styledBanner := lipgloss.NewStyle().
		Foreground(Purple).
		Bold(true).
		Render(banner)

	fmt.Println(styledBanner)
	fmt.Println()

	// Tagline
	taglineStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Italic(true).
		PaddingLeft(2)
	fmt.Println(taglineStyle.Render(tagline))
	fmt.Println()

	// Version and info
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		PaddingLeft(2)

	fmt.Println(infoStyle.Render(fmt.Sprintf("Version: %s", version)))
	fmt.Println(infoStyle.Render("Docs:    https://docs.revyl.com"))
	fmt.Println()
}

// PrintMiniBanner prints a smaller banner for commands.
func PrintMiniBanner() {
	styledBanner := lipgloss.NewStyle().
		Foreground(Purple).
		Bold(true).
		Render("revyl")
	fmt.Println(styledBanner)
}

// GetHelpText returns the formatted help text for the CLI.
func GetHelpText() string {
	purple := lipgloss.NewStyle().Foreground(Purple).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	return fmt.Sprintf(`%s

  %s

%s
  %s            Authenticate with your Revyl account
  %s                Initialize project configuration
  %s      Create a new test
  %s  Create a new workflow
  %s         Open a test in browser
  %s       Build, upload, and run a test
  %s    Run a workflow (collection of tests)

%s
  %s         Start MCP server for AI agent integration
  %s              Output machine-readable CLI schema

%s
  %s              List and manage test definitions
  %s   Validate YAML test files (dry-run)
  %s            List and manage app builds

%s  https://docs.revyl.com
%s  support@revyl.ai`,
		purple.Render(banner),
		dim.Render(tagline+". Catch bugs before your users do."),
		purple.Render("Quick Start:"),
		purple.Render("revyl auth login"),
		purple.Render("revyl init"),
		purple.Render("revyl create test <name>"),
		purple.Render("revyl create workflow <name>"),
		purple.Render("revyl open test <name>"),
		purple.Render("revyl test <name>"),
		purple.Render("revyl run workflow <name>"),
		purple.Render("AI/LLM Integration:"),
		purple.Render("revyl mcp serve"),
		purple.Render("revyl schema"),
		purple.Render("Management:"),
		purple.Render("revyl tests"),
		purple.Render("revyl tests validate"),
		purple.Render("revyl build"),
		purple.Render("Docs: "),
		purple.Render("Help: "),
	)
}

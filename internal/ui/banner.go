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
	if quietMode {
		return
	}

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

// GetCondensedHelp returns a compact cheat-sheet for the 80/20 user journey.
// Shown when the user runs `revyl` with no arguments. No ASCII banner,
// no Cobra auto-generated command list -- just the essentials.
func GetCondensedHelp() string {
	purple := lipgloss.NewStyle().Foreground(Purple).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)

	return fmt.Sprintf(`%s

%s
  %s            Authenticate with your Revyl account
  %s                Initialize project configuration
  %s     Run a test
  %s  Run a workflow

%s
  %s                    List and manage tests
  %s                List and manage workflows
  %s                   List and manage app builds

%s
  %s              Machine-readable CLI schema for LLM agents
  %s           Start MCP server for AI integration

%s  https://docs.revyl.com
%s  support@revyl.ai

%s
`,
		purple.Render("Revyl")+" - "+dim.Render(tagline),
		purple.Render("Getting Started:"),
		purple.Render("revyl auth login"),
		purple.Render("revyl init"),
		purple.Render("revyl test run <name>"),
		purple.Render("revyl workflow run <name>"),
		purple.Render("Manage:"),
		purple.Render("revyl test"),
		purple.Render("revyl workflow"),
		purple.Render("revyl build"),
		purple.Render("AI/Tooling:"),
		purple.Render("revyl schema"),
		purple.Render("revyl mcp serve"),
		purple.Render("Docs: "),
		purple.Render("Help: "),
		hint.Render(`Use "revyl --help" for a full list of commands.`),
	)
}

// GetHelpText returns the verbose help text for the CLI, used by `revyl --help`.
// Contains the full curated command reference without the ASCII banner.
func GetHelpText() string {
	purple := lipgloss.NewStyle().Foreground(Purple).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	return fmt.Sprintf(`%s

%s
  %s            Authenticate with your Revyl account
  %s                Initialize project configuration
  %s      Run a test
  %s   Run a test with build
  %s  Run a workflow

%s
  %s      Create a new test
  %s  Create a new workflow
  %s         Open a test in browser
  %s              List and manage test definitions
  %s            List and manage app builds

%s
  %s         Start MCP server for AI agent integration
  %s              Output machine-readable CLI schema

%s  https://docs.revyl.com
%s  support@revyl.ai`,
		dim.Render(tagline+". Catch bugs before your users do."),
		purple.Render("Quick Start:"),
		purple.Render("revyl auth login"),
		purple.Render("revyl init"),
		purple.Render("revyl test run <name>"),
		purple.Render("revyl test run <name> --build"),
		purple.Render("revyl workflow run <name>"),
		purple.Render("More:"),
		purple.Render("revyl test create <name>"),
		purple.Render("revyl workflow create <name>"),
		purple.Render("revyl test open <name>"),
		purple.Render("revyl test"),
		purple.Render("revyl build"),
		purple.Render("AI/LLM:"),
		purple.Render("revyl mcp serve"),
		purple.Render("revyl schema"),
		purple.Render("Docs: "),
		purple.Render("Help: "),
	)
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/ui"
)

// AGENTS.md is the tool-agnostic entrypoint agents read natively (Cursor,
// Codex, Claude Code). Revyl owns only the content between its markers so a
// re-install can update the flow without touching the rest of the file.
const (
	agentsMDFileName    = "AGENTS.md"
	agentsMDStartMarker = "<!-- revyl:agents:start -->"
	agentsMDEndMarker   = "<!-- revyl:agents:end -->"
)

const agentsMDBlockIntro = agentsMDStartMarker + `
## Revyl — run this app on a cloud device

Use the Revyl CLI to build, run, and verify app changes on a cloud device.
Revyl viewer URLs are live device streams — share them with the user as soon
as you have one.

`

const agentsMDBlockBody = `On a local machine the CLI opens the live viewer in the user's browser
automatically when the session is ready (check "opened_browser" in the
handshake; --no-open disables it). ALWAYS also post viewer_url as a clickable
markdown link — that is the fallback on cloud VMs — and never try to open a
browser yourself.

One-time setup (ephemeral shells may lack the CLI):

` + "```bash" + `
if ! command -v revyl >/dev/null 2>&1; then
  REVYL_NO_MODIFY_PATH=1 sh -c 'curl -fsSL https://revyl.com/install.sh | sh'
  export PATH="$HOME/.revyl/bin:$PATH"
fi
revyl auth status || revyl auth login --token "$REVYL_API_KEY"
` + "```" + `

Dev loop (run from the app directory containing .revyl/config.yaml):

` + "```bash" + `
# Start in the background. Returns JSON as soon as the simulator is watchable;
# the build keeps running behind it. Share viewer_url with the user right away.
revyl dev --remote --detach --json

# Watch the build until the app is installed and launched.
revyl dev status            # state: building -> idle; last_rebuild.status: running -> success
revyl dev logs --build --follow

# After each code change:
revyl dev rebuild --wait --json
` + "```" + `

Verify like a user (separate short-lived commands; never in the loop terminal):

` + "```bash" + `
revyl device screenshot --out screen.png
revyl device validation -s 0 "<expected user-visible outcome>" --json
revyl device report --session-id <session-id> --json
` + "```" + `

Auth: when .revyl/config.yaml has an auth_bypass section, sessions launch
authenticated automatically (launch vars + deep link are applied for you). If
the app ever shows a logged-out state mid-session (expired token), re-mint the
launch vars with this repo's own mint script (if it has one), then re-fire the
auth deep link:

` + "```bash" + `
revyl dev auth refresh
` + "```" + `

Stop with ` + "`revyl dev stop`" + ` when done. Never paste launch-var values or
tokens into code, logs, screenshots, or PRs — reference key names only.
` + agentsMDEndMarker + "\n"

// renderAgentsMDBlock assembles the block, naming the app project's location
// when the install root is a repo root above it (monorepo layout).
func renderAgentsMDBlock(installRoot string) string {
	return agentsMDBlockIntro + agentsMDProjectLocationNote(installRoot) + agentsMDBlockBody
}

// printProjectNotInitialized reports a missing project config with
// self-correcting guidance for monorepo layouts (agents at a repo root with
// the app in a subdirectory).
func printProjectNotInitialized() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "the current directory"
	}
	ui.PrintError("Project not initialized in %s", dir)
	if nested := nestedProjectDirs(dir); len(nested) > 0 {
		ui.PrintInfo("Found a Revyl project in %s/ — run from there (cd %s) or pass -C %s.", nested[0], nested[0], nested[0])
		return
	}
	ui.PrintInfo("If the app lives in a subdirectory, run from there (e.g. cd ios) or pass -C <dir>. Otherwise run 'revyl init'.")
}

// nestedProjectHint returns a short " (found a Revyl project in ios/ — ...)"
// suffix for error messages when the app project lives in a subdirectory of
// the invocation directory. Empty when there is nothing to point at.
func nestedProjectHint(dir string) string {
	nested := nestedProjectDirs(dir)
	if len(nested) == 0 {
		return ""
	}
	return fmt.Sprintf(" (found a Revyl project in %s/ — run from there or pass -C %s)", nested[0], nested[0])
}

// nestedProjectDirs returns direct child directories of root that contain a
// Revyl project (.revyl/config.yaml) — the monorepo layout where the agent
// works at the repo root and the app lives in a subdir (e.g. ios/).
func nestedProjectDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), ".revyl", "config.yaml")); err == nil {
			dirs = append(dirs, entry.Name())
		}
	}
	return dirs
}

// agentsMDProjectLocationNote names where the app project lives relative to
// the AGENTS.md install root, so agents at a monorepo root know where to run
// revyl commands. Empty when the install root is itself the project.
func agentsMDProjectLocationNote(installRoot string) string {
	if _, err := os.Stat(filepath.Join(installRoot, ".revyl", "config.yaml")); err == nil {
		return ""
	}
	nested := nestedProjectDirs(installRoot)
	switch len(nested) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("The Revyl project lives in `%s/` — run all revyl commands from that directory (`cd %s`), or pass `-C %s`.\n\n", nested[0], nested[0], nested[0])
	default:
		return fmt.Sprintf("Revyl projects live in: %s — run revyl commands from the app's directory, or pass `-C <dir>`.\n\n", "`"+strings.Join(nested, "/`, `")+"/`")
	}
}

// installAgentsMDBlock creates AGENTS.md with the Revyl block, or updates the
// marked block in an existing file (content outside the markers is preserved).
// Without force, an existing block is left untouched.
func installAgentsMDBlock(projectDir string, force bool) (string, bool, error) {
	path := filepath.Join(projectDir, agentsMDFileName)
	block := renderAgentsMDBlock(projectDir)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		content := "# Agent Instructions\n\n" + block
		if writeErr := os.WriteFile(path, []byte(content), 0644); writeErr != nil {
			return path, false, fmt.Errorf("failed to write %s: %w", path, writeErr)
		}
		return path, true, nil
	}
	if err != nil {
		return path, false, fmt.Errorf("failed to read %s: %w", path, err)
	}

	existing := string(data)
	startIdx := strings.Index(existing, agentsMDStartMarker)
	endIdx := strings.Index(existing, agentsMDEndMarker)

	if startIdx >= 0 && endIdx > startIdx {
		if !force {
			return path, false, nil
		}
		updated := existing[:startIdx] + block + existing[endIdx+len(agentsMDEndMarker):]
		updated = strings.TrimSuffix(updated, "\n") + "\n"
		if writeErr := os.WriteFile(path, []byte(updated), 0644); writeErr != nil {
			return path, false, fmt.Errorf("failed to update %s: %w", path, writeErr)
		}
		return path, true, nil
	}

	updated := strings.TrimRight(existing, "\n") + "\n\n" + block
	if writeErr := os.WriteFile(path, []byte(updated), 0644); writeErr != nil {
		return path, false, fmt.Errorf("failed to update %s: %w", path, writeErr)
	}
	return path, true, nil
}

// installAgentsMDForTarget writes the AGENTS.md block for project-scoped skill
// installs. Global installs have no project directory to anchor to.
func installAgentsMDForTarget(target skillInstallTarget, force bool) (string, bool, error) {
	if target.global {
		return "", false, nil
	}
	// target.path is <projectDir>/.<tool>/skills.
	projectDir := filepath.Dir(filepath.Dir(target.path))
	if projectDir == "" {
		projectDir = "."
	}
	return installAgentsMDBlock(projectDir, force)
}

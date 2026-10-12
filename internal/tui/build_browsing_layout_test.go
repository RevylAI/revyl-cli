package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/revyl/cli/internal/api"
)

func TestAppBuildBrowserFitsTerminal(t *testing.T) {
	for _, width := range []int{72, 80, 120} {
		for _, height := range []int{24, 42, 60} {
			for _, cursor := range []int{0, 52, 104} {
				for _, state := range []string{"loaded", "next_page", "search", "search_editing", "loading", "page_error", "delete_confirmation"} {
					t.Run(fmt.Sprintf("width_%d_height_%d_cursor_%d_%s", width, height, cursor, state), func(t *testing.T) {
						m := newHubModel("dev", false)
						m.width, m.height, m.appBuildCursor = width, height, cursor
						m.selectedAppName = "App"
						for index := range 105 {
							branch := "base"
							if index%2 == 1 {
								branch = "feature"
							}
							m.appBuilds = append(m.appBuilds, api.BuildVersion{Version: fmt.Sprintf("build_%03d", index), BranchName: branch})
						}
						switch state {
						case "next_page":
							m.appBuildHasNext = true
						case "search":
							m.appBuildScope.Search = "build"
							m.appBuildHasNext = true
						case "search_editing":
							m.appBuildSearching = true
							m.appBuildSearchInput.SetValue("build")
						case "loading":
							m.appsLoading, m.appBuildHasNext = true, true
						case "page_error":
							m.appBuildHasNext = true
							m.err = fmt.Errorf("page temporarily unavailable")
						case "delete_confirmation":
							m.confirmDelete, m.appBuildHasNext = true, true
						}
						view := m.renderAppDetail()
						if actual := lipgloss.Height(view); actual > height {
							t.Fatalf("build browser renders %d rows into %d-row terminal: %s", actual, height, view)
						}
						for lineNumber, line := range strings.Split(view, "\n") {
							if actual := lipgloss.Width(line); actual > width {
								t.Errorf("line %d renders %d columns into %d-column terminal: %q", lineNumber+1, actual, width, line)
							}
						}
						for _, label := range []string{"REVYL", "Build Versions", "All branches", "u upload", "b branch", "/ search", "m load more", "d delete", "R refresh", "esc back", "q quit", fmt.Sprintf("build_%03d", cursor)} {
							if !strings.Contains(view, label) {
								t.Errorf("missing scope, navigation, or selected build %q", label)
							}
						}
						if m.appBuildHasNext && !strings.Contains(view, "More builds available. Press m to load more.") {
							t.Error("missing pagination hint")
						}
						if m.appBuildScope.Search != "" && !strings.Contains(view, "Search: build") {
							t.Error("missing submitted search")
						}
						if width == 120 && !strings.Contains(view, "u upload  b branch  / search  m load more  d delete  R refresh  esc back  q quit") {
							t.Error("wide terminal no longer shows the original one-line help")
						}
					})
				}
			}
		}
	}
}

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
)

type buildPickerStep int

const (
	buildPickerBranch buildPickerStep = iota
	buildPickerMode
	buildPickerExact
	buildPickerName
)

const defaultBranchLabel = "Default branch"

type buildPickerModel struct {
	id, appID, title string
	client           *api.Client
	step             buildPickerStep
	branches         []api.BuildBranch
	builds           []api.BuildVersion
	// defaultBranch is the app's default branch read by ID, which carries no
	// latest_build; its listed copy in branches does.
	defaultBranch *api.BuildBranch
	// currentBranchID is the branch the selection being replaced names. Its
	// row stays listed even when it is the default branch.
	currentBranchID                   string
	selection                         api.BuildSelection
	label                             string
	cursor                            int
	loading, done, cancelled          bool
	branchOnly, allowAll, allowCreate bool
	workflow                          bool
	err                               error
	nameInput                         textinput.Model
	searchInput                       textinput.Model
	searching                         bool
	branchSearch, buildSearch         string
	branchPage, buildPage             int
	moreBranches, moreBuilds          bool
}

type buildPickerDataMsg struct {
	id                   string
	page                 int
	hasNext, reloaded    bool
	branches             []api.BuildBranch
	builds               []api.BuildVersion
	defaultBranch        *api.BuildBranch
	branchID, branchName string
	err                  error
}

func newBuildPicker(client *api.Client, appID, title string) buildPickerModel {
	input := textinput.New()
	input.CharLimit = 255
	input.Placeholder = "Exact Revyl branch name"
	search := textinput.New()
	search.CharLimit = 255
	search.Placeholder = "Search, then Enter (empty clears)"
	return buildPickerModel{id: uuid.NewString(), appID: appID, title: title, client: client, loading: true, nameInput: input, searchInput: search}
}

func (m buildPickerModel) Init() tea.Cmd {
	return m.fetchBranches(1)
}

func (m buildPickerModel) fetchBranches(page int) tea.Cmd {
	return func() tea.Msg {
		return m.readBranchPage(page)
	}
}

func (m buildPickerModel) reloadBranches(through int) tea.Cmd {
	return func() tea.Msg {
		reload := buildPickerDataMsg{id: m.id, reloaded: true}
		for page := 1; ; page++ {
			data := m.readBranchPage(page)
			if data.err != nil {
				return data
			}
			if page == 1 {
				reload.defaultBranch = data.defaultBranch
			}
			reload.branches = append(reload.branches, data.branches...)
			reload.page, reload.hasNext = page, data.hasNext
			if page >= through || !data.hasNext {
				return reload
			}
		}
	}
}

func (m buildPickerModel) readBranchPage(page int) buildPickerDataMsg {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	msg := buildPickerDataMsg{id: m.id, page: page, defaultBranch: m.defaultBranch}
	if page == 1 && !m.allowAll {
		branch, err := m.client.ResolveBranch(ctx, m.appID, "", "")
		if err != nil {
			msg.err = err
			return msg
		}
		msg.defaultBranch = branch
	}
	result, err := m.client.ListBuildBranches(ctx, m.appID, api.BranchListOptions{Page: page, PageSize: 100, Search: m.branchSearch})
	if err != nil {
		msg.err = err
		return msg
	}
	msg.branches, msg.hasNext = result.Branches, page < result.TotalPages
	return msg
}

func (m buildPickerModel) fetchBuilds() tea.Cmd {
	return m.fetchBuildPage(1)
}

func (m buildPickerModel) fetchBuildPage(page int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		msg := buildPickerDataMsg{id: m.id, page: page, branchID: m.selection.BranchID}
		if page == 1 {
			selection := m.selection
			branch, err := m.client.ResolveBranchWithContext(ctx, m.appID, selection.BranchID, selection.BranchName, selection.ContextBranchName)
			if err != nil {
				msg.err = err
				return msg
			}
			msg.branchID, msg.branchName = branch.ID, branch.Name
		}
		result, err := m.client.ListBuildVersionsWithOptions(ctx, m.appID, api.BuildListOptions{BranchID: msg.branchID, Page: page, PageSize: 100, Search: m.buildSearch})
		if err != nil {
			msg.err = err
			return msg
		}
		msg.builds, msg.hasNext = result.Items, result.HasNext
		return msg
	}
}

func (m buildPickerModel) options() []string {
	switch m.step {
	case buildPickerMode:
		if m.workflow {
			return []string{"Automatic", "Latest build"}
		}
		return []string{"Automatic", "Latest build", "Exact build"}
	case buildPickerExact:
		options := make([]string, len(m.builds))
		for i, build := range m.builds {
			options[i] = build.Version + " · " + build.ID
		}
		return options
	default:
		options := []string{m.defaultBranchOption()}
		if m.allowAll {
			options[0] = "All branches"
		}
		for _, branch := range m.branchRows() {
			label := branch.Name
			if m.isDefaultBranch(branch) {
				label += " (default)"
			}
			label += " · " + buildselection.BranchDefaultBuildLabel(branch)
			options = append(options, label)
		}
		return options
	}
}

func mergeBranchPage(listed, page []api.BuildBranch) (merged []api.BuildBranch, repeated bool) {
	listedIDs := make(map[string]struct{}, len(listed)+len(page))
	for _, branch := range listed {
		listedIDs[branch.ID] = struct{}{}
	}
	for _, branch := range page {
		if _, ok := listedIDs[branch.ID]; ok {
			repeated = true
			continue
		}
		listedIDs[branch.ID] = struct{}{}
		listed = append(listed, branch)
	}
	return listed, repeated
}

func (m buildPickerModel) isDefaultBranch(branch api.BuildBranch) bool {
	return m.defaultBranch != nil && branch.ID == m.defaultBranch.ID || branch.IsDefault != nil && *branch.IsDefault
}

// defaultBranchOption is the no-branch choice, named after the app's default
// branch and its default build once they are known.
func (m buildPickerModel) defaultBranchOption() string {
	for _, branch := range m.branches {
		if m.isDefaultBranch(branch) {
			return defaultBranchLabel + " (" + branch.Name + ") · " + buildselection.BranchDefaultBuildLabel(branch)
		}
	}
	if m.defaultBranch != nil {
		return defaultBranchLabel + " (" + m.defaultBranch.Name + ") · " + branchDefaultBuildLabel(*m.defaultBranch)
	}
	return defaultBranchLabel
}

// branchRows are the branches listed after the first choice. The default
// branch already is that choice outside the browse filter, so its own row
// appears only while the current selection names it.
func (m buildPickerModel) branchRows() []api.BuildBranch {
	if m.allowAll {
		return m.branches
	}
	rows := make([]api.BuildBranch, 0, len(m.branches))
	for _, branch := range m.branches {
		if !m.isDefaultBranch(branch) || branch.ID == m.currentBranchID {
			rows = append(rows, branch)
		}
	}
	return rows
}

func (m buildPickerModel) cursorBranchID() string {
	if m.cursor == 0 {
		return ""
	}
	return m.branchRows()[m.cursor-1].ID
}

func (m buildPickerModel) cursorForBranch(branchID string) int {
	for i, branch := range m.branchRows() {
		if branch.ID == branchID {
			return i + 1
		}
	}
	return 0
}

func (m buildPickerModel) Update(msg tea.Msg) (buildPickerModel, tea.Cmd) {
	if data, ok := msg.(buildPickerDataMsg); ok {
		if data.id != m.id {
			return m, nil
		}
		m.loading = false
		m.err = data.err
		if data.err != nil {
			return m, nil
		}
		if m.step == buildPickerExact {
			if data.page <= 1 {
				m.builds, m.cursor = nil, 0
			}
			m.builds = append(m.builds, data.builds...)
			m.buildPage, m.moreBuilds = max(1, data.page), data.hasNext
			m.selection.BranchID = data.branchID
			m.selection.BranchName = ""
			if data.branchName != "" {
				m.label = data.branchName
			}
			return m, nil
		}
		listed := m.branches
		if data.page <= 1 || data.reloaded {
			listed = nil
		}
		merged, repeated := mergeBranchPage(listed, data.branches)
		// Activity only moves branches up, so a repeat means rows crossed a page boundary.
		if repeated && data.page > 1 && !data.reloaded {
			m.loading = true
			return m, m.reloadBranches(data.page)
		}
		previous := m
		m.branches, m.defaultBranch = merged, data.defaultBranch
		m.branchPage, m.moreBranches = max(1, data.page), data.hasNext
		if data.reloaded {
			m.cursor = m.cursorForBranch(previous.cursorBranchID())
		} else if data.page <= 1 {
			m.cursor = 0
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.searching && key.String() != "ctrl+c" {
		switch key.String() {
		case "esc":
			m.searching = false
			m.searchInput.Blur()
			return m, nil
		case "enter":
			m.searching, m.loading, m.err, m.cursor = false, true, nil, 0
			m.searchInput.Blur()
			query := strings.TrimSpace(m.searchInput.Value())
			if m.step == buildPickerExact {
				if query != m.buildSearch {
					m.builds, m.buildPage, m.moreBuilds = nil, 0, false
				}
				m.buildSearch = query
				return m, m.fetchBuilds()
			}
			if query != m.branchSearch {
				m.branches, m.branchPage, m.moreBranches = nil, 0, false
			}
			m.branchSearch = query
			return m, m.fetchBranches(1)
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
	}
	if key.String() == "esc" {
		m.done, m.cancelled = true, true
		return m, nil
	}
	if key.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.loading {
		return m, nil
	}
	if m.step == buildPickerName {
		if key.String() == "enter" {
			selection := api.BuildSelection{BranchName: m.nameInput.Value()}
			if m.workflow && selection.BranchName == "" {
				m.selection, m.done = selection, true
				return m, nil
			}
			if selection.BranchName == "" {
				m.err = fmt.Errorf("enter a branch name")
				return m, nil
			}
			if err := selection.Validate(); err != nil {
				m.err = err
				return m, nil
			}
			m.selection, m.label, m.done = selection, selection.BranchName, !m.workflow || m.branchOnly
			if m.workflow && !m.branchOnly {
				m.step, m.cursor = buildPickerMode, 0
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "/":
		if m.step == buildPickerBranch || m.step == buildPickerExact {
			query := m.branchSearch
			if m.step == buildPickerExact {
				query = m.buildSearch
			}
			m.searchInput.SetValue(query)
			m.searchInput.Focus()
			m.searching = true
			return m, textinput.Blink
		}
	case "m":
		if m.step == buildPickerExact && m.moreBuilds {
			m.loading, m.err = true, nil
			return m, m.fetchBuildPage(m.buildPage + 1)
		}
		if m.step == buildPickerBranch && m.moreBranches {
			m.loading, m.err = true, nil
			return m, m.fetchBranches(m.branchPage + 1)
		}
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.options())-1 {
			m.cursor++
		}
	case "n":
		if m.allowCreate && m.step == buildPickerBranch {
			m.step = buildPickerName
			m.nameInput.Focus()
			return m, textinput.Blink
		}
	case "R":
		if m.workflow {
			m.step = buildPickerName
			m.nameInput.Focus()
			return m, textinput.Blink
		}
		m.loading, m.err = true, nil
		if m.step == buildPickerExact {
			return m, m.fetchBuilds()
		}
		m.step, m.cursor = buildPickerBranch, 0
		return m, m.Init()
	case "backspace":
		if m.workflow {
			m.step = buildPickerName
			m.nameInput.Focus()
			return m, textinput.Blink
		}
		m.step, m.cursor, m.err = buildPickerBranch, 0, nil
	case "enter":
		switch m.step {
		case buildPickerBranch:
			m.selection = api.BuildSelection{}
			m.builds, m.buildPage, m.moreBuilds, m.buildSearch = nil, 0, false, ""
			m.label = defaultBranchLabel
			if m.allowAll && m.cursor == 0 {
				m.done, m.label = true, "All branches"
				return m, nil
			}
			if m.cursor > 0 {
				branch := m.branchRows()[m.cursor-1]
				m.selection.BranchID, m.label = branch.ID, branch.Name
			}
			if m.branchOnly {
				m.done = true
				return m, nil
			}
			m.step, m.cursor = buildPickerMode, 0
		case buildPickerMode:
			switch m.cursor {
			case 0:
				m.selection.Mode, m.done = api.BuildSelectionModeInherit, true
			case 1:
				m.selection.Mode, m.done = api.BuildSelectionModeLatest, true
				m.label += " · Latest build"
			case 2:
				m.step, m.cursor, m.loading = buildPickerExact, 0, true
				return m, m.fetchBuilds()
			}
		case buildPickerExact:
			if len(m.builds) == 0 {
				return m, nil
			}
			build := m.builds[m.cursor]
			m.selection.Mode, m.selection.BuildID = api.BuildSelectionModePinned, build.ID
			m.label += " · Exact build " + build.Version
			m.done = true
		}
	}
	return m, nil
}

func (m buildPickerModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" REVYL") + "  " + m.title + "\n\n")
	if m.err != nil {
		b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n\n")
	}
	query := m.branchSearch
	if m.step == buildPickerExact {
		query = m.buildSearch
	}
	if m.searching {
		b.WriteString("  / " + m.searchInput.View() + "\n\n")
	} else if query != "" && m.step != buildPickerMode && m.step != buildPickerName {
		b.WriteString("  Search: " + query + "\n\n")
	}
	if m.loading {
		b.WriteString("  Loading branch/build choices...\n")
	}
	if m.step == buildPickerName {
		b.WriteString("  " + m.nameInput.View() + "\n")
	} else {
		options := m.options()
		if len(options) == 0 && !m.loading {
			if m.buildSearch != "" {
				b.WriteString("  No builds match this search. Press / to change or clear it.\n")
			} else {
				b.WriteString("  No builds on this branch. Upload a build before selecting a pin.\n")
			}
		}
		start, end := scrollWindow(m.cursor, len(options), 12)
		for i := start; i < end; i++ {
			prefix := "    "
			if i == m.cursor {
				prefix = "  ▸ "
			}
			b.WriteString(prefix + options[i] + "\n")
		}
	}
	if m.step == buildPickerBranch || m.step == buildPickerExact {
		if m.step == buildPickerBranch && m.moreBranches || m.step == buildPickerExact && m.moreBuilds {
			b.WriteString("\n  More choices available. Press m to load more.\n")
		}
		b.WriteString("\n  / search  m load more")
	}
	b.WriteString("\n  enter select  ↑/↓ move  backspace branches  R retry  esc cancel")
	if m.allowCreate && m.step == buildPickerBranch {
		b.WriteString("  n new branch")
	}
	return b.String()
}

func buildSelectionLabel(selection api.BuildSelection) string {
	branch := selection.BranchName
	if branch == "" {
		branch = selection.BranchID
	}
	if branch == "" {
		branch = defaultBranchLabel
	}
	switch selection.Mode {
	case api.BuildSelectionModeLatest:
		return branch + " · Latest build"
	case api.BuildSelectionModePinned:
		return branch + " · Exact build " + selection.BuildID
	default:
		return branch + " · Automatic"
	}
}

// branchDefaultBuildLabel names the default build of a branch read by ID.
// Such a branch carries no latest_build, so Latest build goes unversioned.
func branchDefaultBuildLabel(branch api.BuildBranch) string {
	if branch.PinnedBuildID == nil {
		return "Latest build"
	}
	return buildselection.BranchDefaultBuildLabel(branch)
}

// Package pinnedskills renders the shared CLI skills that the Cursor, Codex and
// Claude Code plugins bundle, and checks plugin text against the CLI release a
// plugin pins. Those plugins run a pinned Revyl CLI release that can be older
// than the skills, so each list item that relies on a newer command is replaced
// with one every pinned runtime can follow.
//
// commands/<release tag>.txt records the command tree of each release a plugin
// pins. make -C revyl-cli record-pinned-commands records a newly pinned release
// from its checksum-verified binary and removes the trees no plugin pins; the
// targets that write a pin run it.
package pinnedskills

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed overrides/*.md
var overrides embed.FS

type listItemOverride struct {
	// item is the exact start of the list item's first line.
	item string
	// replacement names a file in overrides/; empty drops the item.
	replacement string
}

// listItemOverrides lists, per skill file relative to the skills directory, the
// list items a pinned runtime cannot follow.
var listItemOverrides = map[string][]listItemOverride{
	"revyl-cli-dev-loop/SKILL.md": append(agentRuleOverrides,
		listItemOverride{item: "1. Find the app with `revyl setup detect", replacement: "web-find-app.md"},
		listItemOverride{item: "2. Pick an address", replacement: "web-pick-address.md"},
		listItemOverride{item: "3. Check it with `revyl setup check-url", replacement: "web-check-address.md"},
		listItemOverride{item: "- **OS permission prompts:**", replacement: "os-permission-prompts.md"},
	),
	"revyl-cli-auth-bypass/SKILL.md": agentRuleOverrides,
	"revyl-cli-auth-bypass/references/web.md": {
		{item: "- Check an address with `revyl setup check-url", replacement: "web-public-address-check.md"},
	},
}

// agentRuleOverrides covers the agent rules every bundled skill shares.
var agentRuleOverrides = []listItemOverride{
	{item: "- Pass `--intent", replacement: "pinned-cli.md"},
	{item: "- Report problems with Revyl yourself", replacement: "report-problems.md"},
	{item: "- When a `revyl` command says a newer Revyl CLI is available"},
}

// Render returns one skill file, named relative to the skills directory, as a
// pinned plugin ships it.
func Render(skillPath string, content []byte) ([]byte, error) {
	text := string(content)
	for _, override := range listItemOverrides[skillPath] {
		start, end, found := findListItem(text, override.item)
		if !found {
			return nil, fmt.Errorf("%s has no list item starting %q for pinned plugins to replace", skillPath, override.item)
		}
		if override.replacement == "" {
			text = text[:start] + text[end:]
			continue
		}
		replacement, err := overrides.ReadFile("overrides/" + override.replacement)
		if err != nil {
			return nil, err
		}
		text = text[:start] + strings.TrimRight(string(replacement), "\n") + "\n" + text[end:]
	}
	return []byte(text), nil
}

// File is one file of a skill as a pinned plugin ships it.
type File struct {
	Content []byte
	Mode    fs.FileMode
}

// RenderSkillFiles returns every file of one skill directory in skills, keyed
// by its slash-separated path inside the skill, with Markdown rendered for
// pinned plugins and each source file's permission bits.
func RenderSkillFiles(skills fs.FS, name string) (map[string]File, error) {
	files := map[string]File{}
	err := fs.WalkDir(skills, name, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content, err := fs.ReadFile(skills, path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".md") {
			if content, err = Render(path, content); err != nil {
				return err
			}
		}
		files[strings.TrimPrefix(path, name+"/")] = File{Content: content, Mode: info.Mode().Perm()}
		return nil
	})
	return files, err
}

// findListItem returns the byte range of the top-level list item whose first
// line starts with item, through its indented continuation lines, ignoring
// lines inside fenced code blocks.
func findListItem(text, item string) (start, end int, found bool) {
	inFence := false
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		lineStart := offset
		offset += len(line)
		if found {
			if strings.HasPrefix(line, "  ") {
				continue
			}
			return start, lineStart, true
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence && strings.HasPrefix(line, item) {
			start, found = lineStart, true
		}
	}
	return start, len(text), found
}

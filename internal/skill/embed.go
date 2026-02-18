// Package skill provides embedded agent skill content for the Revyl CLI.
//
// The SKILL.md file is embedded at compile time via go:embed so that every
// distribution channel (Homebrew, direct download, npm, pip) can extract
// the agent skill without requiring network access or extra files.
package skill

import (
	_ "embed"
)

// SkillContent holds the raw bytes of the agent skill Markdown file.
// This is the canonical copy; the file at skills/revyl-device/SKILL.md
// in the source tree is kept in sync for browsing convenience.
//
//go:embed SKILL.md
var SkillContent string

// SkillName is the directory name used when installing the skill.
const SkillName = "revyl-device"

// SkillFileName is the expected file name within the skill directory.
const SkillFileName = "SKILL.md"

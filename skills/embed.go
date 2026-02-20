package skills

import (
	_ "embed"
)

const SkillFileName = "SKILL.md"

const (
	RevylCLIName        = "revyl-cli"
	RevylCLICreateName  = "revyl-cli-create"
	RevylCLIAnalyzeName = "revyl-cli-analyze"
	RevylCLIDevLoopName = "revyl-cli-dev-loop"
	RevylMCPName        = "revyl-mcp"
	RevylMCPCreateName  = "revyl-mcp-create"
	RevylMCPAnalyzeName = "revyl-mcp-analyze"
	RevylMCPDevLoopName = "revyl-mcp-dev-loop"
)

//go:embed revyl-cli/SKILL.md
var RevylCLIContent string

//go:embed revyl-cli-create/SKILL.md
var RevylCLICreateContent string

//go:embed revyl-cli-analyze/SKILL.md
var RevylCLIAnalyzeContent string

//go:embed revyl-cli-dev-loop/SKILL.md
var RevylCLIDevLoopContent string

//go:embed revyl-mcp/SKILL.md
var RevylMCPContent string

//go:embed revyl-mcp-create/SKILL.md
var RevylMCPCreateContent string

//go:embed revyl-mcp-analyze/SKILL.md
var RevylMCPAnalyzeContent string

//go:embed revyl-mcp-dev-loop/SKILL.md
var RevylMCPDevLoopContent string

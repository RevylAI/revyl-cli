package skills

import (
	_ "embed"
)

const SkillFileName = "SKILL.md"

const (
	RevylCLIName           = "revyl-cli"
	RevylCLICreateName     = "revyl-cli-create"
	RevylCLIAnalyzeName    = "revyl-cli-analyze"
	RevylCLIOptimizeName   = "revyl-cli-optimize-tests"
	RevylCLIDevLoopName    = "revyl-cli-dev-loop"
	RevylCLIAtlasName      = "revyl-cli-atlas"
	RevylCLIAuthBypassName = "revyl-cli-auth-bypass"
	RevylMCPName           = "revyl-mcp"
	RevylMCPCreateName     = "revyl-mcp-create"
	RevylMCPAnalyzeName    = "revyl-mcp-analyze"
	RevylMCPDevLoopName    = "revyl-mcp-dev-loop"
)

//go:embed revyl-cli/SKILL.md
var RevylCLIContent string

//go:embed revyl-cli-create/SKILL.md
var RevylCLICreateContent string

//go:embed revyl-cli-analyze/SKILL.md
var RevylCLIAnalyzeContent string

//go:embed revyl-cli-optimize-tests/SKILL.md
var RevylCLIOptimizeContent string

//go:embed revyl-cli-dev-loop/SKILL.md
var RevylCLIDevLoopContent string

//go:embed revyl-cli-atlas/SKILL.md
var RevylCLIAtlasContent string

//go:embed revyl-cli-auth-bypass/SKILL.md
var RevylCLIAuthBypassContent string

//go:embed revyl-mcp/SKILL.md
var RevylMCPContent string

//go:embed revyl-mcp-create/SKILL.md
var RevylMCPCreateContent string

//go:embed revyl-mcp-analyze/SKILL.md
var RevylMCPAnalyzeContent string

//go:embed revyl-mcp-dev-loop/SKILL.md
var RevylMCPDevLoopContent string

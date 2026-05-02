package skills

import (
	_ "embed"
)

const SkillFileName = "SKILL.md"

const (
	RevylCLIName                  = "revyl-cli"
	RevylCLICreateName            = "revyl-cli-create"
	RevylCLIAnalyzeName           = "revyl-cli-analyze"
	RevylCLIDevLoopName           = "revyl-cli-dev-loop"
	RevylCLIAuthBypassName        = "revyl-cli-auth-bypass"
	RevylCLIAuthBypassExpoName    = "revyl-cli-auth-bypass-expo"
	RevylCLIAuthBypassRNName      = "revyl-cli-auth-bypass-react-native"
	RevylCLIAuthBypassIOSName     = "revyl-cli-auth-bypass-ios"
	RevylCLIAuthBypassAndroidName = "revyl-cli-auth-bypass-android"
	RevylCLIAuthBypassFlutterName = "revyl-cli-auth-bypass-flutter"
	RevylMCPName                  = "revyl-mcp"
	RevylMCPCreateName            = "revyl-mcp-create"
	RevylMCPAnalyzeName           = "revyl-mcp-analyze"
	RevylMCPDevLoopName           = "revyl-mcp-dev-loop"
)

//go:embed revyl-cli/SKILL.md
var RevylCLIContent string

//go:embed revyl-cli-create/SKILL.md
var RevylCLICreateContent string

//go:embed revyl-cli-analyze/SKILL.md
var RevylCLIAnalyzeContent string

//go:embed revyl-cli-dev-loop/SKILL.md
var RevylCLIDevLoopContent string

//go:embed revyl-cli-auth-bypass/SKILL.md
var RevylCLIAuthBypassContent string

//go:embed revyl-cli-auth-bypass-expo/SKILL.md
var RevylCLIAuthBypassExpoContent string

//go:embed revyl-cli-auth-bypass-react-native/SKILL.md
var RevylCLIAuthBypassRNContent string

//go:embed revyl-cli-auth-bypass-ios/SKILL.md
var RevylCLIAuthBypassIOSContent string

//go:embed revyl-cli-auth-bypass-android/SKILL.md
var RevylCLIAuthBypassAndroidContent string

//go:embed revyl-cli-auth-bypass-flutter/SKILL.md
var RevylCLIAuthBypassFlutterContent string

//go:embed revyl-mcp/SKILL.md
var RevylMCPContent string

//go:embed revyl-mcp-create/SKILL.md
var RevylMCPCreateContent string

//go:embed revyl-mcp-analyze/SKILL.md
var RevylMCPAnalyzeContent string

//go:embed revyl-mcp-dev-loop/SKILL.md
var RevylMCPDevLoopContent string

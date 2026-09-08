package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

const SkillFileName = "SKILL.md"

const (
	RevylCLIName            = "revyl-cli"
	RevylCLICreateName      = "revyl-cli-create"
	RevylCLIAnalyzeName     = "revyl-cli-analyze"
	RevylCLIOptimizeName    = "revyl-cli-optimize-tests"
	RevylCLIDevLoopName     = "revyl-cli-dev-loop"
	RevylCLIAtlasName       = "revyl-cli-atlas"
	RevylCLIAtlasReviewName = "revyl-cli-atlas-review"
	RevylCLIAuthBypassName  = "revyl-cli-auth-bypass"
	RevylMCPName            = "revyl-mcp"
	RevylMCPCreateName      = "revyl-mcp-create"
	RevylMCPAnalyzeName     = "revyl-mcp-analyze"
	RevylMCPDevLoopName     = "revyl-mcp-dev-loop"
)

type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

//go:embed all:revyl-cli all:revyl-cli-* all:revyl-mcp all:revyl-mcp-*
var packages embed.FS

func Files(name string) ([]File, error) {
	switch name {
	case RevylCLIName, RevylCLICreateName, RevylCLIAnalyzeName, RevylCLIOptimizeName,
		RevylCLIDevLoopName, RevylCLIAtlasName, RevylCLIAtlasReviewName, RevylCLIAuthBypassName,
		RevylMCPName, RevylMCPCreateName, RevylMCPAnalyzeName, RevylMCPDevLoopName:
	default:
		return nil, fmt.Errorf("unknown skill package %q", name)
	}
	return packageFiles(packages, name)
}

func packageFiles(source fs.FS, name string) ([]File, error) {
	files := make([]File, 0)
	err := fs.WalkDir(source, name, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		relativePath := strings.TrimPrefix(path, name+"/")
		mode := fs.FileMode(0o644)
		if strings.HasPrefix(relativePath, "scripts/") {
			mode = 0o755
		}
		files = append(files, File{Path: relativePath, Content: content, Mode: mode})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read skill package %q: %w", name, err)
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

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

//go:embed revyl-cli-atlas-review/SKILL.md
var RevylCLIAtlasReviewContent string

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

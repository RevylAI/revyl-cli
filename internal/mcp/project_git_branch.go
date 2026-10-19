package mcp

import (
	"context"
	"path/filepath"

	"github.com/revyl/cli/internal/buildselection"
)

func (s *Server) toolProjectDirectory(projectDirectory string) string {
	if projectDirectory == "" {
		return s.workDir
	}
	if !filepath.IsAbs(projectDirectory) {
		return filepath.Join(s.workDir, projectDirectory)
	}
	return filepath.Clean(projectDirectory)
}

func (s *Server) detectGitBranch(ctx context.Context, explicitProjectDir string) (string, error) {
	projectDirectory := explicitProjectDir
	if projectDirectory == "" {
		projectDirectory = s.explicitProjectDir
	}
	if projectDirectory == "" {
		return "", ctx.Err()
	}
	branch, err := buildselection.DetectGitBranch(ctx, buildselection.GitBranchOptions{
		WorkDir: s.toolProjectDirectory(projectDirectory),
	})
	if err != nil {
		return "", err
	}
	return branch.Name, nil
}

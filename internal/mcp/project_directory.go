package mcp

import "path/filepath"

func (s *Server) toolProjectDirectory(projectDirectory string) string {
	if projectDirectory == "" {
		return s.workDir
	}
	if !filepath.IsAbs(projectDirectory) {
		return filepath.Join(s.workDir, projectDirectory)
	}
	return filepath.Clean(projectDirectory)
}

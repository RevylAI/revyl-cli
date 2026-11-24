package mcp

import (
	"context"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/sessionprofile"
)

func (s *Server) sessionProfileStart(ctx context.Context, explicitPick string) (sessionprofile.Start, error) {
	options := sessionprofile.StartOptions{
		Pick:         explicitPick,
		FromCheckout: true,
		Asked:        explicitPick != "" && explicitPick != config.NoSessionProfile,
	}
	if s.project != nil {
		options.ProjectRoot, options.ConfigPath = s.project.ProjectRoot, s.project.ConfigPath
		if s.project.Aggregate != nil {
			options.Session = s.project.Aggregate.Session
		}
	}
	return sessionprofile.ResolveStart(ctx, options)
}

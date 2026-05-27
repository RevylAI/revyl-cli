// Package workflowref resolves workflow command arguments to workflow IDs.
package workflowref

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
)

// Client is the subset of the API client required for workflow reference resolution.
type Client interface {
	GetWorkflow(ctx context.Context, workflowID string) (*api.Workflow, error)
	ListAllWorkflows(ctx context.Context, pageSize int) ([]api.SimpleWorkflow, error)
}

// Resolution is the resolved workflow reference.
type Resolution struct {
	ID           string
	Name         string
	Input        string
	InputWasUUID bool
}

// IsUUID reports whether ref is a syntactically valid UUID.
func IsUUID(ref string) bool {
	ref = strings.TrimSpace(ref)
	parsed, err := uuid.Parse(ref)
	return err == nil && strings.EqualFold(ref, parsed.String())
}

// Resolve resolves a workflow UUID or exact workflow name.
func Resolve(ctx context.Context, client Client, ref string) (*Resolution, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("workflow name or UUID is required")
	}
	if client == nil {
		return nil, fmt.Errorf("workflow resolver requires an API client")
	}

	if parsed, err := uuid.Parse(ref); err == nil && strings.EqualFold(ref, parsed.String()) {
		canonicalID := parsed.String()
		workflow, getErr := client.GetWorkflow(ctx, canonicalID)
		if getErr == nil {
			name := ""
			if workflow != nil {
				name = workflow.Name
				if strings.TrimSpace(workflow.ID) != "" {
					canonicalID = workflow.ID
				}
			}
			return &Resolution{
				ID:           canonicalID,
				Name:         name,
				Input:        ref,
				InputWasUUID: true,
			}, nil
		}
		if !isNotFound(getErr) {
			return nil, fmt.Errorf("workflow UUID %q could not be resolved: %w", ref, getErr)
		}
		return resolveByExactName(ctx, client, ref, true)
	}

	return resolveByExactName(ctx, client, ref, false)
}

func resolveByExactName(ctx context.Context, client Client, ref string, inputWasValidUUID bool) (*Resolution, error) {
	workflows, err := client.ListAllWorkflows(ctx, 200)
	if err != nil {
		return nil, fmt.Errorf("failed to search for workflow %q: %w", ref, err)
	}

	matches := make([]api.SimpleWorkflow, 0, 2)
	for _, workflow := range workflows {
		if workflow.Name == ref {
			matches = append(matches, workflow)
		}
	}

	if len(matches) == 1 {
		return &Resolution{
			ID:           matches[0].ID,
			Name:         matches[0].Name,
			Input:        ref,
			InputWasUUID: inputWasValidUUID,
		}, nil
	}

	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].ID < matches[j].ID
		})
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.ID)
		}
		return nil, fmt.Errorf("multiple workflows named %q found -- use UUID to disambiguate:\n  %s", ref, strings.Join(ids, "\n  "))
	}

	if inputWasValidUUID {
		return nil, fmt.Errorf("%q is a valid UUID, but no workflow with that ID or exact name was found", ref)
	}
	if looksUUIDLike(ref) {
		return nil, fmt.Errorf("%q is not a valid UUID and no workflow with that exact name was found", ref)
	}
	return nil, fmt.Errorf("workflow %q not found\n\nHint: Run 'revyl workflow list' to see all available workflows.", ref)
}

func isNotFound(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func looksUUIDLike(ref string) bool {
	ref = strings.TrimSpace(ref)
	if len(ref) != 36 {
		return false
	}
	for _, idx := range []int{8, 13, 18, 23} {
		if ref[idx] != '-' {
			return false
		}
	}
	return true
}

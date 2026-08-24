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

// BoundedCatalogClient is the API surface required to resolve multiple exact
// workflow names without accepting a partial organization catalog.
type BoundedCatalogClient interface {
	ListWorkflowsBounded(ctx context.Context, pageSize, maxWorkflows int) ([]api.SimpleWorkflow, error)
}

const (
	exactNameCatalogPageSize     = 200
	exactNameCatalogMaxWorkflows = 5000
)

// ExactNameResolutionErrorKind classifies a batch exact-name resolution
// failure without requiring callers to inspect or record its human-facing text.
type ExactNameResolutionErrorKind string

const (
	ExactNameInvalidInput       ExactNameResolutionErrorKind = "invalid_input"
	ExactNameCatalogUnavailable ExactNameResolutionErrorKind = "catalog_unavailable"
	ExactNameCatalogLimit       ExactNameResolutionErrorKind = "catalog_limit_exceeded"
	ExactNameNotFound           ExactNameResolutionErrorKind = "not_found"
	ExactNameAmbiguous          ExactNameResolutionErrorKind = "ambiguous"
	ExactNameInvalidWorkflowID  ExactNameResolutionErrorKind = "invalid_workflow_id"
)

// ExactNameResolutionError is an actionable, classifiable batch-resolution
// failure. Name and WorkflowIDs are for direct CLI output; analytics should use
// Kind rather than recording these organization-specific values.
type ExactNameResolutionError struct {
	Kind        ExactNameResolutionErrorKind
	Name        string
	WorkflowIDs []string
	Err         error
}

func (e *ExactNameResolutionError) Error() string {
	switch e.Kind {
	case ExactNameInvalidInput:
		return "workflow names must not be blank"
	case ExactNameCatalogUnavailable, ExactNameCatalogLimit:
		return fmt.Sprintf("failed to load the complete workflow catalog: %v", e.Err)
	case ExactNameNotFound:
		return fmt.Sprintf("workflow %q not found\n\nHint: Run 'revyl workflow list' to see all available workflows.", e.Name)
	case ExactNameAmbiguous:
		return fmt.Sprintf("multiple workflows named %q found -- use UUID to disambiguate:\n  %s", e.Name, strings.Join(e.WorkflowIDs, "\n  "))
	case ExactNameInvalidWorkflowID:
		return fmt.Sprintf("workflow %q has an invalid server ID; contact Revyl support", e.Name)
	default:
		return "workflow name resolution failed"
	}
}

func (e *ExactNameResolutionError) Unwrap() error {
	return e.Err
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

// ResolveExactNames resolves a batch of exact, case-sensitive workflow names
// after enumerating the complete organization catalog once. It fails closed if
// the catalog exceeds the resolver's explicit safety bound.
func ResolveExactNames(ctx context.Context, client BoundedCatalogClient, refs []string) (map[string]Resolution, error) {
	resolved, issues, err := ResolveExactNamesBestEffort(ctx, client, refs)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if issue := issues[strings.TrimSpace(ref)]; issue != nil {
			return nil, issue
		}
	}
	return resolved, nil
}

// ResolveExactNamesBestEffort resolves every unique exact name from one
// bounded catalog read while returning name-local failures separately.
func ResolveExactNamesBestEffort(
	ctx context.Context,
	client BoundedCatalogClient,
	refs []string,
) (map[string]Resolution, map[string]*ExactNameResolutionError, error) {
	if client == nil {
		return nil, nil, &ExactNameResolutionError{Kind: ExactNameInvalidInput}
	}

	names := make([]string, 0, len(refs))
	requested := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		name := strings.TrimSpace(ref)
		if name == "" {
			return nil, nil, &ExactNameResolutionError{Kind: ExactNameInvalidInput}
		}
		if _, seen := requested[name]; seen {
			continue
		}
		requested[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return map[string]Resolution{}, map[string]*ExactNameResolutionError{}, nil
	}

	workflows, err := client.ListWorkflowsBounded(ctx, exactNameCatalogPageSize, exactNameCatalogMaxWorkflows)
	if err != nil {
		kind := ExactNameCatalogUnavailable
		var limitErr *api.WorkflowCatalogLimitError
		if errors.As(err, &limitErr) {
			kind = ExactNameCatalogLimit
		}
		return nil, nil, &ExactNameResolutionError{Kind: kind, Err: err}
	}

	matchesByName := make(map[string][]api.SimpleWorkflow, len(names))
	for _, workflow := range workflows {
		name := strings.TrimSpace(workflow.Name)
		if _, wanted := requested[name]; wanted {
			matchesByName[name] = append(matchesByName[name], workflow)
		}
	}

	resolved := make(map[string]Resolution, len(names))
	issues := make(map[string]*ExactNameResolutionError)
	for _, name := range names {
		matches := matchesByName[name]
		canonicalIDs := make([]string, 0, len(matches))
		invalidID := false
		for _, match := range matches {
			parsedID, parseErr := uuid.Parse(strings.TrimSpace(match.ID))
			if parseErr != nil {
				invalidID = true
				break
			}
			canonicalIDs = append(canonicalIDs, parsedID.String())
		}
		if invalidID {
			issues[name] = &ExactNameResolutionError{Kind: ExactNameInvalidWorkflowID, Name: name}
			continue
		}
		switch len(matches) {
		case 0:
			issues[name] = &ExactNameResolutionError{Kind: ExactNameNotFound, Name: name}
		case 1:
			resolved[name] = Resolution{
				ID:    canonicalIDs[0],
				Name:  matches[0].Name,
				Input: name,
			}
		default:
			sort.Strings(canonicalIDs)
			issues[name] = &ExactNameResolutionError{
				Kind:        ExactNameAmbiguous,
				Name:        name,
				WorkflowIDs: canonicalIDs,
			}
		}
	}

	return resolved, issues, nil
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

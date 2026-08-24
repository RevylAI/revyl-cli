package projectpublication

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

type fakeClient struct {
	readResult    *api.ProjectConfigurationReadResponse
	readErr       error
	replaceResult *api.ProjectConfigurationReplaceResponse
	replaceErr    error
	replaceCalls  int
	replaceID     string
	replace       api.ProjectConfigurationReplaceRequest
}

func (f *fakeClient) ReadProjectConfiguration(
	context.Context,
	string,
	api.ProjectConfigurationReadRequest,
) (*api.ProjectConfigurationReadResponse, error) {
	return f.readResult, f.readErr
}

func (f *fakeClient) ReplaceProjectConfiguration(
	_ context.Context,
	projectID string,
	request api.ProjectConfigurationReplaceRequest,
) (*api.ProjectConfigurationReplaceResponse, error) {
	f.replaceCalls++
	f.replaceID = projectID
	f.replace = request
	return f.replaceResult, f.replaceErr
}

func testCandidate() Candidate {
	return Candidate{
		ProjectID: "77c1943b-9c5e-4b66-bf65-40f719da5f6e",
		Locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     "revyl",
			RepositoryName:                "mobile",
			RepositoryRelativeProjectRoot: "apps/mobile",
		},
		Configuration: api.AuthoredRevylConfig{},
	}
}

func TestResolveCandidateUsesNearestCanonicalConfigAndGitHubRemote(t *testing.T) {
	worktree := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", worktree},
		{"-C", worktree, "remote", "add", "origin", "git@github.com:Acme/Mobile.git"},
	} {
		command := exec.Command("git", args...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	projectRoot := filepath.Join(worktree, "apps", "mobile")
	configDir := filepath.Join(projectRoot, ".revyl")
	if err := os.MkdirAll(filepath.Join(projectRoot, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	authored := config.AuthoredConfig{Project: config.AuthoredProject{ID: testCandidate().ProjectID}}
	contents, err := config.MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), contents, 0o600); err != nil {
		t.Fatal(err)
	}

	candidate, err := ResolveCandidate(filepath.Join(projectRoot, "src"))
	if err != nil {
		t.Fatalf("ResolveCandidate() error = %v", err)
	}
	if candidate.ProjectID != authored.Project.ID {
		t.Fatalf("project ID = %q", candidate.ProjectID)
	}
	if candidate.Locator.Namespace != "Acme" || candidate.Locator.RepositoryName != "Mobile" || candidate.Locator.RepositoryRelativeProjectRoot != "apps/mobile" {
		t.Fatalf("locator = %#v", candidate.Locator)
	}
}

func TestPublishUsesExplicitAbsentPrecondition(t *testing.T) {
	client := &fakeClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStateAbsent,
		},
		replaceResult: &api.ProjectConfigurationReplaceResponse{
			Outcome: api.ProjectConfigurationReplaceResponseOutcomeApplied,
		},
	}
	candidate := testCandidate()

	result, err := Publish(context.Background(), client, candidate)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Outcome != api.ProjectConfigurationReplaceResponseOutcomeApplied {
		t.Fatalf("Publish() outcome = %q", result.Outcome)
	}
	if client.replaceCalls != 1 || client.replaceID != candidate.ProjectID {
		t.Fatalf("replace calls = %d, project ID = %q", client.replaceCalls, client.replaceID)
	}
	if client.replace.Force != nil {
		t.Fatalf("force = %#v, want omitted", client.replace.Force)
	}
	absent, err := client.replace.Precondition.AsProjectConfigurationAbsentPrecondition()
	if err != nil || absent.State != "absent" {
		t.Fatalf("absent precondition = %#v, error = %v", absent, err)
	}
}

func TestPublishUsesObservedHashForPresentConfiguration(t *testing.T) {
	client := &fakeClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Authority:                api.ConfigurationAuthorityManual,
				ProjectConfigurationHash: "observed-hash",
			},
		},
		replaceResult: &api.ProjectConfigurationReplaceResponse{
			Outcome: api.ProjectConfigurationReplaceResponseOutcomeUnchanged,
		},
	}

	result, err := Publish(context.Background(), client, testCandidate())
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Outcome != api.ProjectConfigurationReplaceResponseOutcomeUnchanged {
		t.Fatalf("Publish() outcome = %q", result.Outcome)
	}
	present, err := client.replace.Precondition.AsProjectConfigurationPresentPrecondition()
	if err != nil || present.ProjectConfigurationHash != "observed-hash" {
		t.Fatalf("present precondition = %#v, error = %v", present, err)
	}
}

func TestPublishRefusesGitAuthorityBeforeReplace(t *testing.T) {
	client := &fakeClient{readResult: &api.ProjectConfigurationReadResponse{
		State: api.ProjectConfigurationReadResponseStatePresent,
		Resource: &api.ProjectConfigurationResource{
			Authority: api.ConfigurationAuthorityGitDefaultBranch,
		},
	}}

	_, err := Publish(context.Background(), client, testCandidate())
	var publicationErr *Error
	if !errors.As(err, &publicationErr) || publicationErr.Code != "git_authority_rejects_manual_write" {
		t.Fatalf("Publish() error = %v", err)
	}
	if client.replaceCalls != 0 {
		t.Fatalf("replace calls = %d, want 0", client.replaceCalls)
	}
}

func TestPublishForceReplacesGitAuthorityWithObservedHash(t *testing.T) {
	client := &fakeClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStatePresent,
			Resource: &api.ProjectConfigurationResource{
				Authority:                api.ConfigurationAuthorityGitDefaultBranch,
				ProjectConfigurationHash: "observed-hash",
			},
		},
		replaceResult: &api.ProjectConfigurationReplaceResponse{
			Outcome: api.ProjectConfigurationReplaceResponseOutcomeApplied,
			Resource: api.ProjectConfigurationResource{
				Authority: api.ConfigurationAuthorityGitDefaultBranch,
			},
		},
	}
	candidate := testCandidate()
	candidate.AllowGitAuthorityOverride = true

	result, err := Publish(context.Background(), client, candidate)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Resource.Authority != api.ConfigurationAuthorityGitDefaultBranch {
		t.Fatalf("authority = %q", result.Resource.Authority)
	}
	if client.replace.Force == nil || !*client.replace.Force {
		t.Fatalf("force = %#v", client.replace.Force)
	}
	present, err := client.replace.Precondition.AsProjectConfigurationPresentPrecondition()
	if err != nil || present.ProjectConfigurationHash != "observed-hash" {
		t.Fatalf("present precondition = %#v, error = %v", present, err)
	}
}

func TestPublishMapsObservedStateConflicts(t *testing.T) {
	for _, code := range []string{
		"observed_configuration_changed",
		"observed_configuration_now_present",
		"observed_configuration_no_longer_present",
	} {
		t.Run(code, func(t *testing.T) {
			client := &fakeClient{
				readResult: &api.ProjectConfigurationReadResponse{
					State: api.ProjectConfigurationReadResponseStateAbsent,
				},
				replaceErr: &api.APIError{StatusCode: 409, Code: code},
			}

			_, err := Publish(context.Background(), client, testCandidate())
			var publicationErr *Error
			if !errors.As(err, &publicationErr) || publicationErr.Code != "observed_configuration_changed" {
				t.Fatalf("Publish() error = %v", err)
			}
		})
	}
}

func TestPublishMapsAuthorityConflict(t *testing.T) {
	client := &fakeClient{
		readResult: &api.ProjectConfigurationReadResponse{
			State: api.ProjectConfigurationReadResponseStateAbsent,
		},
		replaceErr: &api.APIError{StatusCode: 409, Code: "git_authority_rejects_manual_write"},
	}

	_, err := Publish(context.Background(), client, testCandidate())
	var publicationErr *Error
	if !errors.As(err, &publicationErr) || publicationErr.Code != "git_authority_rejects_manual_write" {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestPublishPreservesUnclassifiedErrors(t *testing.T) {
	want := errors.New("network unavailable")
	client := &fakeClient{readErr: want}

	_, err := Publish(context.Background(), client, testCandidate())
	if !errors.Is(err, want) {
		t.Fatalf("Publish() error = %v, want %v", err, want)
	}
}

func TestPublishMapsRepositoryAccessPrerequisite(t *testing.T) {
	client := &fakeClient{readErr: &api.APIError{
		StatusCode: 404,
		Code:       "project_configuration_inaccessible",
	}}

	_, err := Publish(context.Background(), client, testCandidate())
	var publicationErr *Error
	if !errors.As(err, &publicationErr) || publicationErr.Code != "project_configuration_inaccessible" {
		t.Fatalf("Publish() error = %v", err)
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "project_configuration_inaccessible" {
		t.Fatalf("Publish() error lost API classification: %v", err)
	}
}

func TestPublishMapsRepositoryProviderOutage(t *testing.T) {
	client := &fakeClient{readErr: &api.APIError{
		StatusCode: 502,
		Code:       "repository_provider_unavailable",
	}}

	_, err := Publish(context.Background(), client, testCandidate())
	var publicationErr *Error
	if !errors.As(err, &publicationErr) || publicationErr.Code != "repository_provider_unavailable" {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestPublishPreservesRemovedProjectFromReadAndWrite(t *testing.T) {
	for _, test := range []struct {
		name   string
		client *fakeClient
	}{
		{
			name: "read",
			client: &fakeClient{readErr: &api.APIError{
				StatusCode: 409,
				Code:       "project_removed",
			}},
		},
		{
			name: "write race",
			client: &fakeClient{
				readResult: &api.ProjectConfigurationReadResponse{
					State: api.ProjectConfigurationReadResponseStateAbsent,
				},
				replaceErr: &api.APIError{
					StatusCode: 409,
					Code:       "project_removed",
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Publish(context.Background(), test.client, testCandidate())
			var publicationErr *Error
			if !errors.As(err, &publicationErr) || publicationErr.Code != "project_removed" {
				t.Fatalf("Publish() error = %v", err)
			}
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "project_removed" {
				t.Fatalf("Publish() error lost API classification: %v", err)
			}
		})
	}
}

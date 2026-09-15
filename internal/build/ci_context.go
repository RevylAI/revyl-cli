package build

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// CIContext contains provider-neutral CI and SCM values detected from the
// current process environment.
type CIContext struct {
	System       string
	CommitSHA    string
	Branch       string
	RunID        string
	RunURL       string
	Repository   string
	Actor        string
	ActorURL     string
	SCMProvider  string
	SCMNamespace string
	SCMProject   string
	ReviewNumber int
	ReviewURL    string
	BaseSHA      string
}

type uploadSCMContextKey struct{}

// WithUploadSCMContext validates explicit upload identity independently of the runner.
func WithUploadSCMContext(ctx context.Context, repository, commitSHA string, reviewNumber int) (context.Context, error) {
	repository = strings.TrimSpace(repository)
	commitSHA = strings.TrimSpace(commitSHA)
	namespace, project, ok := parseGitHubRepository("https://github.com/" + repository)
	if !ok || repository != namespace+"/"+project {
		return nil, fmt.Errorf("invalid --repo: use the PR's base GitHub repository in owner/name format")
	}
	if len(commitSHA) != 40 && len(commitSHA) != 64 {
		return nil, fmt.Errorf("invalid --commit: supply the full PR head commit SHA (40 or 64 hexadecimal characters)")
	}
	if _, err := hex.DecodeString(commitSHA); err != nil {
		return nil, fmt.Errorf("invalid --commit: supply the full PR head commit SHA in hexadecimal")
	}
	if reviewNumber < 0 {
		return nil, fmt.Errorf("invalid --pr: supply a positive PR number or omit the flag to match by commit")
	}
	detected, detectedCI := DetectCIContext()
	system := "external"
	if detectedCI {
		system = detected.System
	}
	ci := CIContext{
		System: system, CommitSHA: strings.ToLower(commitSHA),
		Repository: repository, SCMProvider: "github", SCMNamespace: namespace, SCMProject: project,
		ReviewNumber: reviewNumber, RunID: detected.RunID, RunURL: detected.RunURL,
		Actor: detected.Actor, ActorURL: detected.ActorURL,
	}
	if reviewNumber > 0 {
		ci.ReviewURL = "https://github.com/" + repository + "/pull/" + strconv.Itoa(reviewNumber)
	}
	return context.WithValue(ctx, uploadSCMContextKey{}, ci), nil
}

// CIContextFromContext gives explicit upload identity precedence over runner detection.
func CIContextFromContext(ctx context.Context) (CIContext, bool) {
	if ci, ok := ctx.Value(uploadSCMContextKey{}).(CIContext); ok {
		return ci, true
	}
	return DetectCIContext()
}

// DetectCIContext reads the CI metadata shared by uploads and request headers.
func DetectCIContext() (CIContext, bool) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return detectGitHubActions(), true
	}
	if os.Getenv("BUILDKITE") == "true" {
		return detectBuildkite(), true
	}
	return CIContext{}, false
}

type githubActionsEvent struct {
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
		Head    struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
}

func detectGitHubActions() CIContext {
	event := readGitHubActionsEvent(os.Getenv("GITHUB_EVENT_PATH"))
	repository := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY"))
	namespace, project := splitRepository(repository)
	serverURL := strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL")), "/")
	runID := strings.TrimSpace(os.Getenv("GITHUB_RUN_ID"))
	actor := strings.TrimSpace(os.Getenv("GITHUB_ACTOR"))

	context := CIContext{
		System:       "github-actions",
		CommitSHA:    firstNonEmpty(event.PullRequest.Head.SHA, os.Getenv("REVYL_PR_HEAD_SHA"), os.Getenv("GITHUB_SHA")),
		Branch:       strings.TrimSpace(os.Getenv("GITHUB_REF_NAME")),
		RunID:        runID,
		Repository:   repository,
		Actor:        actor,
		SCMNamespace: namespace,
		SCMProject:   project,
		ReviewNumber: event.PullRequest.Number,
		ReviewURL:    strings.TrimSpace(event.PullRequest.HTMLURL),
		BaseSHA:      strings.TrimSpace(event.PullRequest.Base.SHA),
	}
	if repository != "" {
		context.SCMProvider = "github"
	}
	if serverURL != "" && actor != "" {
		context.ActorURL = serverURL + "/" + actor
	}
	if serverURL != "" && repository != "" && runID != "" {
		context.RunURL = serverURL + "/" + repository + "/actions/runs/" + runID
	}
	return context
}

func detectBuildkite() CIContext {
	remote := strings.TrimSpace(os.Getenv("BUILDKITE_REPO"))
	namespace, project, isGitHub := parseGitHubRepository(remote)
	repository := ""
	provider := ""
	if isGitHub {
		repository = namespace + "/" + project
		provider = "github"
	}

	reviewNumber := parsePositiveInt(os.Getenv("BUILDKITE_PULL_REQUEST"))
	reviewURL := ""
	if repository != "" && reviewNumber > 0 {
		reviewURL = "https://github.com/" + repository + "/pull/" + strconv.Itoa(reviewNumber)
	}

	return CIContext{
		System:       "buildkite",
		CommitSHA:    firstNonEmpty(os.Getenv("BUILDKITE_PULL_REQUEST_HEAD_COMMIT"), os.Getenv("BUILDKITE_COMMIT")),
		Branch:       strings.TrimSpace(os.Getenv("BUILDKITE_BRANCH")),
		RunID:        strings.TrimSpace(os.Getenv("BUILDKITE_BUILD_ID")),
		RunURL:       strings.TrimSpace(os.Getenv("BUILDKITE_BUILD_URL")),
		Repository:   repository,
		Actor:        strings.TrimSpace(os.Getenv("BUILDKITE_BUILD_CREATOR")),
		SCMProvider:  provider,
		SCMNamespace: namespace,
		SCMProject:   project,
		ReviewNumber: reviewNumber,
		ReviewURL:    reviewURL,
	}
}

func parseGitHubRepository(remote string) (string, string, bool) {
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = "ssh://git@github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	parsed, err := url.Parse(remote)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", "", false
	}
	switch parsed.Scheme {
	case "https", "http", "ssh", "git":
	default:
		return "", "", false
	}
	path := strings.TrimSuffix(strings.TrimSuffix(parsed.Path, "/"), ".git")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
		for _, character := range part {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.", character)) {
				return "", "", false
			}
		}
	}
	return parts[0], parts[1], true
}

func readGitHubActionsEvent(eventPath string) githubActionsEvent {
	var event githubActionsEvent
	if strings.TrimSpace(eventPath) == "" {
		return event
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return event
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return githubActionsEvent{}
	}
	return event
}

func splitRepository(repository string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(repository), "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	namespace := strings.TrimSpace(parts[0])
	project := strings.TrimSpace(parts[1])
	if namespace == "" || project == "" {
		return "", ""
	}
	return namespace, project
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

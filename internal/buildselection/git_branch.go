package buildselection

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/revyl/cli/internal/analytics"
)

type GitBranchOrigin string

const (
	GitBranchOriginNone      GitBranchOrigin = "none"
	GitBranchOriginLocalGit  GitBranchOrigin = "local_git"
	GitBranchOriginGitHub    GitBranchOrigin = "github_actions"
	GitBranchOriginBuildkite GitBranchOrigin = "buildkite"
	GitBranchOriginSubmitted GitBranchOrigin = "submitted_git"
	branchDetectionTimeout                   = 5 * time.Second
	gitOutputLimit                           = 64 * 1024
	gitBranchNameLimit                       = 4096
	githubEventLimit                         = 1024 * 1024
)

type GitBranch struct {
	Name   string
	Origin GitBranchOrigin
}

type GitBranchOptions struct {
	WorkDir          string
	SourceGit        *GitSourceContext
	ArtifactIdentity *ArtifactSourceIdentity
}

type GitSourceContext struct {
	RepositoryURL string
	Ref           string
}

type ArtifactSourceIdentity struct {
	Repository   string
	CommitSHA    string
	ReviewNumber int
}

type checkoutState struct {
	branch, head, repository string
	parents                  []string
}

// DetectGitBranch uses only the customer source supplied by its caller.
// CI pull request context names the pull request's own branch, never the
// branch it merges into. Branch-name semantics are validated when the server
// selects the hint, so an irrelevant unsupported name cannot invalidate a saved
// branch or exact build.
func DetectGitBranch(ctx context.Context, opts GitBranchOptions) (GitBranch, error) {
	if err := ctx.Err(); err != nil {
		return GitBranch{}, err
	}
	lookup, cancel := context.WithTimeout(ctx, branchDetectionTimeout)
	defer cancel()
	result := GitBranch{Origin: GitBranchOriginNone}
	if opts.SourceGit != nil {
		result = detectSubmittedPullRequest(lookup, *opts.SourceGit)
		if result.Origin == GitBranchOriginNone {
			result = detectSubmittedGit(lookup, *opts.SourceGit)
		}
	} else if opts.WorkDir != "" {
		checkout, ok := readCheckout(lookup, opts.WorkDir, opts.ArtifactIdentity != nil)
		if ok {
			result = detectCIBranch(&checkout, opts.ArtifactIdentity)
			identityMatches := opts.ArtifactIdentity == nil || artifactMatchesCheckout(*opts.ArtifactIdentity, checkout)
			if result.Origin == GitBranchOriginNone && identityMatches && checkout.branch != "" && !syntheticGitHubBranch(checkout.branch) {
				result = branchCandidate(checkout.branch, GitBranchOriginLocalGit)
			}
		} else if opts.ArtifactIdentity != nil {
			result = detectCIBranch(nil, opts.ArtifactIdentity)
		} else if lookup.Err() == nil {
			// No checkout leaves no local code to contradict the CI's own branch.
			// A timed-out Git lookup proves nothing, so it does not count.
			result = detectCIBranch(nil, nil)
		}
	} else if opts.ArtifactIdentity != nil {
		result = detectCIBranch(nil, opts.ArtifactIdentity)
	}
	if err := ctx.Err(); err != nil {
		return GitBranch{}, err
	}
	analytics.SetGitBranchOrigin(ctx, string(result.Origin))
	return result, nil
}

func branchCandidate(name string, origin GitBranchOrigin) GitBranch {
	if name == "" {
		return GitBranch{Origin: GitBranchOriginNone}
	}
	return GitBranch{Name: boundedUnsupportedCandidate(name), Origin: origin}
}

// Unserializable and oversized candidates remain longer than the valid name
// limit, so their deferred error representation cannot select another branch.
func boundedUnsupportedCandidate(name string) string {
	if !utf8.ValidString(name) {
		return strings.Repeat("invalid-utf8", 24)
	}
	if utf8.RuneCountInString(name) <= gitBranchNameLimit {
		return name
	}
	return string([]rune(name)[:gitBranchNameLimit])
}

func readCheckout(ctx context.Context, dir string, needIdentity bool) (checkoutState, bool) {
	inside, ok, _ := runDetectionGit(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if !ok || strings.TrimSpace(inside) != "true" {
		return checkoutState{}, false
	}
	branchRef, branchOK, _ := runDetectionGit(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	checkout := checkoutState{}
	if branchOK && strings.HasPrefix(branchRef, "refs/heads/") {
		checkout.branch = strings.TrimRight(strings.TrimPrefix(branchRef, "refs/heads/"), "\r\n")
	}
	if !needIdentity && checkout.branch != "" && os.Getenv("GITHUB_ACTIONS") != "true" && os.Getenv("BUILDKITE") != "true" {
		return checkout, true
	}
	head, _, _ := runDetectionGit(ctx, dir, "rev-parse", "--verify", "HEAD")
	checkout.head = strings.TrimSpace(head)
	remote, _, _ := runDetectionGit(ctx, dir, "remote", "get-url", "origin")
	checkout.repository = normalizeRepository(remote)
	if os.Getenv("GITHUB_ACTIONS") == "true" && strings.HasPrefix(os.Getenv("GITHUB_REF"), "refs/pull/") {
		commit, _, _ := runDetectionGit(ctx, dir, "cat-file", "-p", "HEAD")
		for _, line := range strings.Split(commit, "\n") {
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "parent ") {
				checkout.parents = append(checkout.parents, strings.TrimPrefix(line, "parent "))
			}
		}
	}
	return checkout, true
}

func artifactMatchesCheckout(identity ArtifactSourceIdentity, checkout checkoutState) bool {
	return normalizeRepository(identity.Repository) != "" &&
		normalizeRepository(identity.Repository) == checkout.repository &&
		sameCommit(identity.CommitSHA, checkout.head)
}

func syntheticGitHubBranch(branch string) bool {
	if os.Getenv("GITHUB_ACTIONS") != "true" || !strings.HasPrefix(os.Getenv("GITHUB_REF"), "refs/pull/") {
		return false
	}
	return branch == os.Getenv("GITHUB_REF_NAME") || branch == os.Getenv("GITHUB_REF")
}

func detectSubmittedGit(ctx context.Context, source GitSourceContext) GitBranch {
	absent := GitBranch{Origin: GitBranchOriginNone}
	ref := source.Ref
	if source.RepositoryURL == "" || ref == "" {
		return absent
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		return branchCandidate(strings.TrimPrefix(ref, "refs/heads/"), GitBranchOriginSubmitted)
	}
	if ref == "HEAD" || strings.HasPrefix(ref, "refs/") || looksLikeCommit(ref) || strings.HasPrefix(ref, "-") {
		return absent
	}
	headRef, tagRef := "refs/heads/"+ref, "refs/tags/"+ref
	output, ok, overflow := runDetectionGit(ctx, "", "ls-remote", "--heads", "--tags", "--", source.RepositoryURL, headRef, tagRef)
	if !ok || overflow {
		return absent
	}
	hasBranch, hasTag := false, false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 2)
		if len(fields) != 2 {
			continue
		}
		hasBranch = hasBranch || fields[1] == headRef
		hasTag = hasTag || fields[1] == tagRef || fields[1] == tagRef+"^{}"
	}
	if hasBranch && !hasTag {
		return branchCandidate(ref, GitBranchOriginSubmitted)
	}
	return absent
}

type githubBranchEvent struct {
	Number     int    `json:"number"`
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	} `json:"pull_request"`
}

// detectCIBranch accepts the CI's branch only when the given checkout or
// artifact is the code CI built. With neither, nothing local can contradict the
// CI, so its branch is used as reported.
func detectCIBranch(checkout *checkoutState, identity *ArtifactSourceIdentity) GitBranch {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return detectGitHubBranch(checkout, identity)
	}
	if os.Getenv("BUILDKITE") == "true" {
		return detectBuildkiteBranch(checkout, identity)
	}
	return GitBranch{Origin: GitBranchOriginNone}
}

func detectGitHubBranch(checkout *checkoutState, identity *ArtifactSourceIdentity) GitBranch {
	absent := GitBranch{Origin: GitBranchOriginNone}
	event := readBranchEvent(os.Getenv("GITHUB_EVENT_PATH"))
	repository := normalizeRepository(os.Getenv("GITHUB_REPOSITORY"))
	if repository == "" {
		return absent
	}
	pr := event.PullRequest
	if pr.Head.Ref != "" && pr.Head.SHA != "" {
		baseRepository := normalizeRepository(pr.Base.Repo.FullName)
		headRepository := normalizeRepository(pr.Head.Repo.FullName)
		if baseRepository != repository || headRepository == "" {
			return absent
		}
		if identity != nil {
			identityRepository := normalizeRepository(identity.Repository)
			if (identityRepository != baseRepository && identityRepository != headRepository) || !sameCommit(identity.CommitSHA, pr.Head.SHA) ||
				(identity.ReviewNumber > 0 && identity.ReviewNumber != pr.Number && identity.ReviewNumber != event.Number) {
				return absent
			}
		} else if checkout != nil {
			if checkout.branch != "" && checkout.branch != pr.Head.Ref && !syntheticGitHubBranch(checkout.branch) {
				return absent
			}
			if checkout.repository != baseRepository && checkout.repository != headRepository {
				return absent
			}
			mergeCheckout := strings.HasPrefix(os.Getenv("GITHUB_REF"), "refs/pull/") && strings.HasSuffix(os.Getenv("GITHUB_REF"), "/merge") &&
				sameCommit(checkout.head, os.Getenv("GITHUB_SHA")) && containsCommit(checkout.parents, pr.Head.SHA)
			if !sameCommit(checkout.head, pr.Head.SHA) && !mergeCheckout {
				return absent
			}
		}
		return pullRequestHeadBranch(pr.Head.Ref, pr.Head.Repo.FullName, headRepository != baseRepository, GitBranchOriginGitHub)
	}
	if checkout != nil && checkout.branch != "" && (identity == nil || artifactMatchesCheckout(*identity, *checkout)) {
		return absent
	}
	ref := os.Getenv("GITHUB_REF")
	// Only pull request runs set GITHUB_HEAD_REF. When their event is unusable,
	// GITHUB_REF can name the branch they merge into (pull_request_target).
	if os.Getenv("GITHUB_HEAD_REF") != "" || os.Getenv("GITHUB_REF_TYPE") == "tag" || !strings.HasPrefix(ref, "refs/heads/") {
		return absent
	}
	commit := os.Getenv("GITHUB_SHA")
	if identity != nil {
		if normalizeRepository(identity.Repository) != repository || !sameCommit(identity.CommitSHA, commit) {
			return absent
		}
	} else if checkout != nil && (checkout.repository != repository || !sameCommit(checkout.head, commit)) {
		return absent
	}
	return branchCandidate(strings.TrimPrefix(ref, "refs/heads/"), GitBranchOriginGitHub)
}

type buildkiteSource struct {
	reviewNumber   int
	repositoryURL  string
	repository     string
	fork           bool
	commit, branch string
}

// readBuildkiteSource describes the code a non-tag Buildkite build checks out:
// the pull request's head for pull request builds, otherwise the pipeline's.
func readBuildkiteSource() (buildkiteSource, bool) {
	if os.Getenv("BUILDKITE_TAG") != "" {
		return buildkiteSource{}, false
	}
	source := buildkiteSource{repositoryURL: os.Getenv("BUILDKITE_REPO")}
	pipelineRepository := normalizeRepository(source.repositoryURL)
	if pipelineRepository == "" {
		return buildkiteSource{}, false
	}
	source.repository = pipelineRepository
	if review, err := strconv.Atoi(os.Getenv("BUILDKITE_PULL_REQUEST")); err == nil && review > 0 {
		source.reviewNumber = review
		if headRepositoryURL := os.Getenv("BUILDKITE_PULL_REQUEST_REPO"); headRepositoryURL != "" {
			source.repositoryURL, source.repository = headRepositoryURL, normalizeRepository(headRepositoryURL)
			if source.repository == "" {
				return buildkiteSource{}, false
			}
		}
	}
	source.fork = source.repository != pipelineRepository
	source.commit = os.Getenv("BUILDKITE_PULL_REQUEST_HEAD_COMMIT")
	if source.commit == "" {
		source.commit = os.Getenv("BUILDKITE_COMMIT")
	}
	source.branch = os.Getenv("BUILDKITE_BRANCH")
	if source.fork {
		// Pipelines can prefix a fork's branch with its owner, as in owner:main.
		// Git branch names cannot contain ':', so the prefix is never the branch.
		if _, branch, prefixed := strings.Cut(source.branch, ":"); prefixed {
			source.branch = branch
		}
	}
	return source, true
}

func detectBuildkiteBranch(checkout *checkoutState, identity *ArtifactSourceIdentity) GitBranch {
	absent := GitBranch{Origin: GitBranchOriginNone}
	source, ok := readBuildkiteSource()
	if !ok {
		return absent
	}
	if identity != nil {
		if normalizeRepository(identity.Repository) != source.repository || !sameCommit(identity.CommitSHA, source.commit) {
			return absent
		}
	} else if checkout != nil {
		if checkout.repository != source.repository || !sameCommit(checkout.head, source.commit) {
			return absent
		}
		if checkout.branch != "" && checkout.branch != source.branch {
			return absent
		}
	}
	if source.reviewNumber > 0 {
		if identity != nil && identity.ReviewNumber > 0 && identity.ReviewNumber != source.reviewNumber {
			return absent
		}
		return pullRequestHeadBranch(source.branch, source.repositoryURL, source.fork, GitBranchOriginBuildkite)
	}
	if checkout != nil && checkout.branch != "" && (identity == nil || artifactMatchesCheckout(*identity, *checkout)) {
		return absent
	}
	return branchCandidate(source.branch, GitBranchOriginBuildkite)
}

// pullRequestHeadBranch names the pull request's own branch. A fork's branch
// takes GitHub's label form, owner:branch, so it can never name the base
// repository's branch of the same name: Git branch names cannot contain ':'.
// A verified pull request without a usable name still reports its origin, which
// keeps the bare local checkout branch from standing in for it.
func pullRequestHeadBranch(branch, headRepository string, fork bool, origin GitBranchOrigin) GitBranch {
	if branch == "" {
		return GitBranch{Origin: origin}
	}
	if !fork {
		return branchCandidate(branch, origin)
	}
	owner := repositoryNamespace(headRepository)
	if owner == "" {
		return GitBranch{Origin: origin}
	}
	return branchCandidate(owner+":"+branch, origin)
}

func detectSubmittedPullRequest(ctx context.Context, source GitSourceContext) GitBranch {
	absent := GitBranch{Origin: GitBranchOriginNone}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		event := readBranchEvent(os.Getenv("GITHUB_EVENT_PATH"))
		pr := event.PullRequest
		if !submittedSourceMatchesPullRequest(ctx, source, pr.Head.Repo.FullName, pr.Head.Ref, pr.Head.SHA) {
			return absent
		}
		return detectGitHubBranch(nil, &ArtifactSourceIdentity{Repository: source.RepositoryURL, CommitSHA: pr.Head.SHA, ReviewNumber: pr.Number})
	}
	if os.Getenv("BUILDKITE") != "true" {
		return absent
	}
	buildkite, ok := readBuildkiteSource()
	if !ok || buildkite.reviewNumber <= 0 || !submittedSourceMatchesPullRequest(ctx, source, buildkite.repositoryURL, buildkite.branch, buildkite.commit) {
		return absent
	}
	return pullRequestHeadBranch(buildkite.branch, buildkite.repositoryURL, buildkite.fork, GitBranchOriginBuildkite)
}

func submittedSourceMatchesPullRequest(ctx context.Context, source GitSourceContext, repository, headBranch, headCommit string) bool {
	if normalizeRepository(source.RepositoryURL) == "" || normalizeRepository(source.RepositoryURL) != normalizeRepository(repository) || headBranch == "" {
		return false
	}
	if sameCommit(source.Ref, headCommit) {
		return true
	}
	if source.Ref != headBranch && source.Ref != "refs/heads/"+headBranch {
		return false
	}
	args := []string{"ls-remote", "--heads", "--tags", "--", source.RepositoryURL, "refs/heads/" + headBranch}
	if source.Ref == headBranch {
		args = append(args, "refs/tags/"+headBranch)
	}
	output, ok, overflow := runDetectionGit(ctx, "", args...)
	fields := strings.Fields(output)
	return ok && !overflow && len(fields) == 2 && sameCommit(fields[0], headCommit) && fields[1] == "refs/heads/"+headBranch
}

func readBranchEvent(path string) githubBranchEvent {
	var event githubBranchEvent
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > githubEventLimit {
		return event
	}
	file, err := os.Open(path) // #nosec G304 -- GITHUB_EVENT_PATH is a bounded CI event file.
	if err != nil {
		return event
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, githubEventLimit+1))
	if err != nil || len(data) > githubEventLimit || json.Unmarshal(data, &event) != nil {
		return githubBranchEvent{}
	}
	return event
}

// parseRepository splits a repository reference, such as owner/name, an HTTPS
// URL or an SSH remote, into its lowercase host and case-preserving path.
func parseRepository(value string) (host, path string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if strings.HasPrefix(value, "git@") && strings.Contains(value, ":") {
		value = "ssh://" + strings.Replace(value, ":", "/", 1)
	}
	if !strings.Contains(value, "://") {
		if strings.Count(value, "/") != 1 || strings.HasPrefix(value, ".") {
			return "", ""
		}
		value = "https://github.com/" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ""
	}
	path = strings.TrimSuffix(strings.TrimSuffix(parsed.Path, "/"), ".git")
	if path == "" {
		return "", ""
	}
	return strings.ToLower(parsed.Host), path
}

func normalizeRepository(value string) string {
	host, path := parseRepository(value)
	if path == "" {
		return ""
	}
	if host == "github.com" {
		path = strings.ToLower(path)
	}
	return host + path
}

// repositoryNamespace returns the path above a repository's name, the owner on
// GitHub, in the provider's case.
func repositoryNamespace(value string) string {
	_, path := parseRepository(value)
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 2 {
		return ""
	}
	for _, segment := range segments {
		if segment == "" {
			return ""
		}
	}
	return strings.Join(segments[:len(segments)-1], "/")
}

func looksLikeCommit(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func sameCommit(a, b string) bool {
	if (len(a) != 40 && len(a) != 64) || len(a) != len(b) {
		return false
	}
	if _, err := hex.DecodeString(a); err != nil {
		return false
	}
	return strings.EqualFold(a, b)
}

func containsCommit(commits []string, target string) bool {
	for _, commit := range commits {
		if sameCommit(commit, target) {
			return true
		}
	}
	return false
}

type boundedGitOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedGitOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := gitOutputLimit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func runDetectionGit(ctx context.Context, dir string, args ...string) (string, bool, bool) {
	if dir != "" {
		args = append([]string{"-C", filepath.Clean(dir)}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_OPTIONAL_LOCKS", "GIT_SSH", "GIT_SSH_COMMAND":
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_OPTIONAL_LOCKS=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oConnectTimeout=3 -oConnectionAttempts=1")
	cmd.WaitDelay = 200 * time.Millisecond
	var output boundedGitOutput
	cmd.Stdout = &output
	err := cmd.Run()
	return output.String(), err == nil, output.overflow
}

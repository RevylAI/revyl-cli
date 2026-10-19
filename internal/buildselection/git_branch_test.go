package buildselection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func clearBranchCI(t *testing.T) {
	t.Helper()
	for _, key := range []string{"GITHUB_ACTIONS", "GITHUB_REF", "GITHUB_REF_NAME", "GITHUB_REF_TYPE", "GITHUB_SHA", "GITHUB_HEAD_REF", "GITHUB_REPOSITORY", "GITHUB_EVENT_PATH", "BUILDKITE", "BUILDKITE_TAG", "BUILDKITE_BRANCH", "BUILDKITE_REPO", "BUILDKITE_COMMIT", "BUILDKITE_PULL_REQUEST_HEAD_COMMIT", "BUILDKITE_PULL_REQUEST", "BUILDKITE_PULL_REQUEST_REPO", "BUILDKITE_PULL_REQUEST_BASE_BRANCH", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"} {
		t.Setenv(key, "")
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "GIT_DIR" && key != "GIT_WORK_TREE" && key != "GIT_INDEX_FILE" && key != "GIT_COMMON_DIR" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
	return strings.TrimRight(string(output), "\r\n")
}

func branchRepository(t *testing.T) string {
	t.Helper()
	clearBranchCI(t)
	dir := t.TempDir()
	runGitForTest(t, dir, "init", "--initial-branch=main")
	runGitForTest(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	runGitForTest(t, dir, "remote", "add", "origin", "https://github.com/example/mobile.git")
	return dir
}

func detectBranch(t *testing.T, opts GitBranchOptions) GitBranch {
	t.Helper()
	result, err := DetectGitBranch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGitBranchUsesSelectedWorktreeWithoutConfig(t *testing.T) {
	dir := branchRepository(t)
	branch := "feature/Écran\u00a0"
	worktree := filepath.Join(t.TempDir(), "selected")
	runGitForTest(t, dir, "worktree", "add", "-b", branch, worktree)
	nested := filepath.Join(worktree, "nested", "app")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(dir, ".git"))
	t.Setenv("GIT_WORK_TREE", dir)
	got := detectBranch(t, GitBranchOptions{WorkDir: nested})
	if got.Name != branch || got.Origin != GitBranchOriginLocalGit {
		t.Fatalf("selected worktree not preserved: %#v", got)
	}
	if got := detectBranch(t, GitBranchOptions{}); got.Name != "" {
		t.Fatalf("unbound context used ambient checkout: %#v", got)
	}
}

func TestGitBranchPreservesBranchNameWhenTagSharesName(t *testing.T) {
	for _, branch := range []string{"release", "heads/release", "refs/heads/release"} {
		t.Run(branch, func(t *testing.T) {
			dir := branchRepository(t)
			runGitForTest(t, dir, "config", "core.warnAmbiguousRefs", "true")
			runGitForTest(t, dir, "checkout", "-b", branch)
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != branch || got.Origin != GitBranchOriginLocalGit {
				t.Fatalf("branch before tag = %#v, want %q", got, branch)
			}
			runGitForTest(t, dir, "tag", branch)
			t.Logf("symbolic HEAD: short=%q full=%q", runGitForTest(t, dir, "symbolic-ref", "--short", "HEAD"), runGitForTest(t, dir, "symbolic-ref", "HEAD"))
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != branch || got.Origin != GitBranchOriginLocalGit {
				t.Fatalf("tag changed Git branch to %#v, want %q", got, branch)
			}
		})
	}
}

func TestGitBranchAbsentForNonBranchSymbolicHEAD(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "tag", "release")
	runGitForTest(t, dir, "symbolic-ref", "HEAD", "refs/tags/release")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" || got.Origin != GitBranchOriginNone {
		t.Fatalf("non-branch symbolic HEAD invented Git branch: %#v", got)
	}
}

func TestGitBranchAbsentForDetachedNonGitAndMissingGit(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "checkout", "--detach")
	for _, path := range []string{dir, t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if got := detectBranch(t, GitBranchOptions{WorkDir: path}); got.Name != "" {
			t.Fatalf("unexpected context for %s: %#v", path, got)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" {
		t.Fatalf("missing git invented context: %#v", got)
	}
}

func TestGitBranchDefersInvalidUTF8WithoutRenaming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows subprocess arguments require UTF-16")
	}
	dir := branchRepository(t)
	name := "feature/" + string([]byte{0xff})
	runGitForTest(t, dir, "check-ref-format", "refs/heads/"+name)
	runGitForTest(t, dir, "symbolic-ref", "HEAD", "refs/heads/"+name)
	if got := runGitForTest(t, dir, "symbolic-ref", "--short", "HEAD"); got != name {
		t.Fatalf("Git did not preserve raw ref bytes: %q", got)
	}
	got := detectBranch(t, GitBranchOptions{WorkDir: dir})
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded GitBranch
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != got || !utf8.ValidString(got.Name) || utf8.RuneCountInString(got.Name) <= 255 || utf8.RuneCountInString(got.Name) > gitBranchNameLimit || got.Origin != GitBranchOriginLocalGit {
		t.Fatalf("unsupported ref became a selectable or unstable branch: %#v", got)
	}
}

func writeBranchEvent(t *testing.T, value interface{}) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_PATH", path)
}

func pullRequestFixture(t *testing.T) (dir, head, merge string) {
	t.Helper()
	dir = branchRepository(t)
	runGitForTest(t, dir, "checkout", "-b", "feature/one")
	runGitForTest(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "head")
	head = runGitForTest(t, dir, "rev-parse", "HEAD")
	runGitForTest(t, dir, "checkout", "main")
	runGitForTest(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base")
	runGitForTest(t, dir, "-c", "commit.gpgsign=false", "merge", "--no-ff", "feature/one", "-m", "merge")
	merge = runGitForTest(t, dir, "rev-parse", "HEAD")
	return dir, head, merge
}

func pullRequestEvent(head, headRepository string) map[string]interface{} {
	return map[string]interface{}{"number": 42, "pull_request": map[string]interface{}{
		"number": 42,
		"head":   map[string]interface{}{"sha": head, "ref": "feature/one", "repo": map[string]string{"full_name": headRepository}},
		"base":   map[string]interface{}{"ref": "staging", "repo": map[string]string{"full_name": "example/mobile"}},
	}}
}

func TestGitHubBranchVerifiesDetachedPullRequestAndMerge(t *testing.T) {
	for _, tt := range []struct{ name, headRepository, want string }{
		{name: "same repository", headRepository: "example/mobile", want: "feature/one"},
		{name: "fork", headRepository: "Contributor/fork", want: "Contributor:feature/one"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir, head, merge := pullRequestFixture(t)
			runGitForTest(t, dir, "checkout", "--detach")
			t.Setenv("GITHUB_ACTIONS", "true")
			t.Setenv("GITHUB_REPOSITORY", "example/mobile")
			t.Setenv("GITHUB_REF", "refs/pull/42/merge")
			t.Setenv("GITHUB_REF_NAME", "42/merge")
			t.Setenv("GITHUB_SHA", merge)
			t.Setenv("GITHUB_HEAD_REF", "feature/one")
			writeBranchEvent(t, pullRequestEvent(head, tt.headRepository))
			want := GitBranch{Name: tt.want, Origin: GitBranchOriginGitHub}
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != want {
				t.Fatalf("merge checkout = %#v, want %#v", got, want)
			}
			runGitForTest(t, dir, "checkout", "--detach", head)
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != want {
				t.Fatalf("detached head = %#v, want %#v", got, want)
			}
			runGitForTest(t, dir, "checkout", "feature/one")
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != want {
				t.Fatalf("attached head = %#v, want %#v", got, want)
			}
			runGitForTest(t, dir, "checkout", "--detach", "main~1")
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != (GitBranch{Origin: GitBranchOriginNone}) {
				t.Fatalf("checkout without the PR head adopted it: %#v", got)
			}
			runGitForTest(t, dir, "checkout", "--detach", head)
			runGitForTest(t, dir, "remote", "set-url", "origin", "https://github.com/unrelated/project.git")
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" {
				t.Fatalf("wrong checkout repository adopted CI branch: %#v", got)
			}
			runGitForTest(t, dir, "checkout", "-b", "deliberate/local")
			if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "deliberate/local" || got.Origin != GitBranchOriginLocalGit {
				t.Fatalf("CI displaced actual checkout branch: %#v", got)
			}
		})
	}
}

func TestGitHubPullRequestTargetBaseCheckoutIsNotPullRequestCode(t *testing.T) {
	dir, head, merge := pullRequestFixture(t)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REF_NAME", "main")
	t.Setenv("GITHUB_SHA", merge)
	t.Setenv("GITHUB_HEAD_REF", "feature/one")
	writeBranchEvent(t, pullRequestEvent(head, "contributor/fork"))
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != (GitBranch{Name: "main", Origin: GitBranchOriginLocalGit}) {
		t.Fatalf("attached base checkout = %#v, want its own branch", got)
	}
	runGitForTest(t, dir, "checkout", "--detach")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != (GitBranch{Origin: GitBranchOriginNone}) {
		t.Fatalf("detached base checkout = %#v, want no branch", got)
	}
	runGitForTest(t, dir, "checkout", "--detach", head)
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != (GitBranch{Name: "contributor:feature/one", Origin: GitBranchOriginGitHub}) {
		t.Fatalf("explicit PR head checkout = %#v, want the PR's branch", got)
	}
}

func TestGitHubPushBranchRejectsTagAndWrongCommit(t *testing.T) {
	dir := branchRepository(t)
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	runGitForTest(t, dir, "checkout", "--detach")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	t.Setenv("GITHUB_SHA", head)
	t.Setenv("GITHUB_REF", "refs/heads/staging")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "staging" {
		t.Fatalf("branch push context missing: %#v", got)
	}
	t.Setenv("GITHUB_REF_TYPE", "tag")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" {
		t.Fatalf("tag used as branch: %#v", got)
	}
	t.Setenv("GITHUB_REF_TYPE", "branch")
	t.Setenv("GITHUB_SHA", strings.Repeat("0", 40))
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" {
		t.Fatalf("wrong commit used CI branch: %#v", got)
	}
}

func TestBuildkiteBranchRequiresMatchingSource(t *testing.T) {
	dir := branchRepository(t)
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	runGitForTest(t, dir, "checkout", "--detach")
	t.Setenv("BUILDKITE", "true")
	t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
	t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
	t.Setenv("BUILDKITE_COMMIT", head)
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "feature/buildkite" || got.Origin != GitBranchOriginBuildkite {
		t.Fatalf("verified Buildkite missing: %#v", got)
	}
	t.Setenv("BUILDKITE_TAG", "v1")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" {
		t.Fatal("Buildkite tag inferred branch")
	}
	t.Setenv("BUILDKITE_TAG", "")
	t.Setenv("BUILDKITE_REPO", "git@github.com:other/project.git")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got.Name != "" {
		t.Fatal("Buildkite wrong repository inferred branch")
	}
}

func TestArtifactIdentityExcludesUnrelatedCheckout(t *testing.T) {
	dir := branchRepository(t)
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	opts := GitBranchOptions{WorkDir: dir, ArtifactIdentity: &ArtifactSourceIdentity{Repository: "other/project", CommitSHA: head}}
	if got := detectBranch(t, opts); got.Name != "" {
		t.Fatal("external artifact adopted local branch")
	}
	opts.ArtifactIdentity.Repository = "example/mobile"
	if got := detectBranch(t, opts); got.Name != "main" {
		t.Fatalf("matching artifact lost local context: %#v", got)
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	t.Setenv("GITHUB_SHA", head)
	t.Setenv("GITHUB_REF", "refs/heads/main")
	opts.WorkDir = t.TempDir()
	if got := detectBranch(t, opts); got.Name != "main" || got.Origin != GitBranchOriginGitHub {
		t.Fatalf("known artifact source cannot use verified CI without checkout: %#v", got)
	}
}

func TestSubmittedGitUsesRepositoryRefInsteadOfCWD(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "branch", "release/mobile")
	runGitForTest(t, dir, "tag", "v1")
	runGitForTest(t, dir, "branch", "ambiguous")
	runGitForTest(t, dir, "tag", "ambiguous")
	for _, tt := range []struct{ ref, want string }{
		{"release/mobile", "release/mobile"}, {"refs/heads/other", "other"}, {"v1", ""}, {"ambiguous", ""},
		{"refs/tags/v1", ""}, {"HEAD", ""}, {"deadbeef", ""}, {"", ""}, {"missing", ""},
	} {
		t.Run(tt.ref, func(t *testing.T) {
			got := detectBranch(t, GitBranchOptions{WorkDir: dir, SourceGit: &GitSourceContext{RepositoryURL: dir, Ref: tt.ref}})
			if got.Name != tt.want {
				t.Fatalf("ref %q: %#v, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestGitBranchCancellationAndDeferredUnsupportedName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DetectGitBranch(ctx, GitBranchOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
	for _, name := range []string{strings.Repeat("a", 256), strings.Repeat("界", 5000), "\u00a0feature\u00a0"} {
		got := detectBranch(t, GitBranchOptions{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: "refs/heads/" + name}})
		if name == "\u00a0feature\u00a0" && got.Name != name {
			t.Fatal("Unicode whitespace was normalized")
		}
		if len([]rune(name)) > gitBranchNameLimit && (utf8.RuneCountInString(got.Name) != gitBranchNameLimit || !utf8.ValidString(got.Name)) {
			t.Fatal("oversized unsupported candidate lost bounded invalid representation")
		}
	}
	var output boundedGitOutput
	data := []byte(strings.Repeat("x", gitOutputLimit+1))
	if n, err := output.Write(data); err != nil || n != len(data) || output.Len() != gitOutputLimit || !output.overflow {
		t.Fatal("git output not bounded")
	}
}

func TestGitBranchCancelsRunningGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := DetectGitBranch(ctx, GitBranchOptions{WorkDir: t.TempDir()}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running Git cancellation = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Git exceeded cancellation bound: %v", elapsed)
	}
}

func TestGitHubPullRequestArtifactAndSubmittedSourceSelectHead(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "checkout", "-b", "feature/one")
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	writeBranchEvent(t, map[string]interface{}{"number": 42, "pull_request": map[string]interface{}{
		"number": 42,
		"head":   map[string]interface{}{"sha": head, "ref": "feature/one", "repo": map[string]string{"full_name": "contributor/fork"}},
		"base":   map[string]interface{}{"ref": "main", "repo": map[string]string{"full_name": "example/mobile"}},
	}})
	for _, opts := range []GitBranchOptions{
		{ArtifactIdentity: &ArtifactSourceIdentity{Repository: "example/mobile", CommitSHA: head, ReviewNumber: 42}},
		{WorkDir: dir, ArtifactIdentity: &ArtifactSourceIdentity{Repository: "contributor/fork", CommitSHA: head, ReviewNumber: 42}},
		{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/contributor/fork", Ref: head}},
	} {
		if got := detectBranch(t, opts); got != (GitBranch{Name: "contributor:feature/one", Origin: GitBranchOriginGitHub}) {
			t.Fatalf("verified fork PR source did not choose its own branch: %#v", got)
		}
	}
	for _, identity := range []ArtifactSourceIdentity{
		{Repository: "example/mobile", CommitSHA: head, ReviewNumber: 43},
		{Repository: "example/mobile", CommitSHA: strings.Repeat("f", 40), ReviewNumber: 42},
		{Repository: "other/mobile", CommitSHA: head, ReviewNumber: 42},
	} {
		if got := detectBranch(t, GitBranchOptions{ArtifactIdentity: &identity}); got.Name != "" {
			t.Fatalf("unmatched PR source selected a destination: %#v", got)
		}
	}
	if got := detectBranch(t, GitBranchOptions{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/other/mobile", Ref: head}}); got.Name != "" {
		t.Fatalf("unrelated submitted source adopted PR branch: %#v", got)
	}
}

func TestPullRequestHeadDoesNotDependOnBaseBranch(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "checkout", "-b", "feature/one")
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	for _, baseRef := range []interface{}{"staging", nil, "", "   ", 42, map[string]string{"invalid": "shape"}} {
		writeBranchEvent(t, map[string]interface{}{"pull_request": map[string]interface{}{
			"number": 42,
			"head":   map[string]interface{}{"sha": head, "ref": "feature/one", "repo": map[string]string{"full_name": "example/mobile"}},
			"base":   map[string]interface{}{"ref": baseRef, "repo": map[string]string{"full_name": "example/mobile"}},
		}})
		for _, opts := range []GitBranchOptions{
			{WorkDir: dir},
			{ArtifactIdentity: &ArtifactSourceIdentity{Repository: "example/mobile", CommitSHA: head, ReviewNumber: 42}},
			{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: head}},
		} {
			got, err := DetectGitBranch(context.Background(), opts)
			if err != nil || got != (GitBranch{Name: "feature/one", Origin: GitBranchOriginGitHub}) {
				t.Fatalf("base %#v changed the PR's own branch: %#v, %v", baseRef, got, err)
			}
		}
	}
}

func TestBuildkitePullRequestSelectsHead(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "checkout", "-b", "feature/buildkite")
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	t.Setenv("BUILDKITE", "true")
	t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
	t.Setenv("BUILDKITE_PULL_REQUEST_REPO", "git://github.com/example/mobile.git")
	t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
	t.Setenv("BUILDKITE_COMMIT", head)
	t.Setenv("BUILDKITE_PULL_REQUEST", "42")
	t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "staging")
	want := GitBranch{Name: "feature/buildkite", Origin: GitBranchOriginBuildkite}
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != want {
		t.Fatalf("attached Buildkite PR head = %#v, want %#v", got, want)
	}
	runGitForTest(t, dir, "checkout", "--detach")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != want {
		t.Fatalf("detached Buildkite PR head = %#v, want %#v", got, want)
	}
	t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "")
	if got := detectBranch(t, GitBranchOptions{WorkDir: dir}); got != want {
		t.Fatalf("Buildkite PR head depended on its base: %#v", got)
	}
}

func TestBuildkitePullRequestArtifactRequiresMatchingForkSource(t *testing.T) {
	head, other := strings.Repeat("a", 40), strings.Repeat("b", 40)
	fork := "https://github.com/contributor/fork.git"
	label := GitBranch{Name: "contributor:feature/buildkite", Origin: GitBranchOriginBuildkite}
	absent := GitBranch{Origin: GitBranchOriginNone}
	for _, tt := range []struct {
		name     string
		identity *ArtifactSourceIdentity
		env      map[string]string
		want     GitBranch
	}{
		{name: "fork repository", want: label},
		{name: "SSH fork identity", identity: &ArtifactSourceIdentity{Repository: "git@github.com:contributor/fork.git", CommitSHA: head, ReviewNumber: 42}, want: label},
		{name: "same repository PR", identity: &ArtifactSourceIdentity{Repository: "example/mobile", CommitSHA: head}, env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": ""}, want: GitBranch{Name: "feature/buildkite", Origin: GitBranchOriginBuildkite}},
		{name: "pipeline repository is not the fork", identity: &ArtifactSourceIdentity{Repository: "example/mobile", CommitSHA: head}, want: absent},
		{name: "unrelated repository", identity: &ArtifactSourceIdentity{Repository: "other/project", CommitSHA: head}, want: absent},
		{name: "wrong commit", identity: &ArtifactSourceIdentity{Repository: fork, CommitSHA: other}, want: absent},
		{name: "abbreviated commit", identity: &ArtifactSourceIdentity{Repository: fork, CommitSHA: head[:12]}, want: absent},
		{name: "missing commit", identity: &ArtifactSourceIdentity{Repository: fork}, want: absent},
		{name: "invalid commit", identity: &ArtifactSourceIdentity{Repository: fork, CommitSHA: strings.Repeat("z", 40)}, env: map[string]string{"BUILDKITE_COMMIT": strings.Repeat("z", 40)}, want: absent},
		{name: "wrong review", identity: &ArtifactSourceIdentity{Repository: fork, CommitSHA: head, ReviewNumber: 43}, want: absent},
		{name: "review inferred from CI", identity: &ArtifactSourceIdentity{Repository: fork, CommitSHA: head}, want: label},
		{name: "PR head overrides merge commit", env: map[string]string{"BUILDKITE_COMMIT": other, "BUILDKITE_PULL_REQUEST_HEAD_COMMIT": head}, want: label},
		{name: "merge commit is not PR head", identity: &ArtifactSourceIdentity{Repository: fork, CommitSHA: other}, env: map[string]string{"BUILDKITE_COMMIT": other, "BUILDKITE_PULL_REQUEST_HEAD_COMMIT": head}, want: absent},
		{name: "missing pipeline repository", env: map[string]string{"BUILDKITE_REPO": ""}, want: absent},
		{name: "invalid pipeline repository", env: map[string]string{"BUILDKITE_REPO": "https://github.com/example/mobile?untrusted=1"}, want: absent},
		{name: "invalid fork repository", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "https://github.com/contributor/fork?untrusted=1"}, want: absent},
		{name: "invalid fork cannot fall back to pipeline", identity: &ArtifactSourceIdentity{Repository: "example/mobile", CommitSHA: head}, env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": " "}, want: absent},
		{name: "not a PR cannot trust fork", env: map[string]string{"BUILDKITE_PULL_REQUEST": "false"}, want: absent},
		{name: "invalid PR cannot trust fork", env: map[string]string{"BUILDKITE_PULL_REQUEST": "42x"}, want: absent},
		{name: "zero PR cannot trust fork", env: map[string]string{"BUILDKITE_PULL_REQUEST": "0"}, want: absent},
		{name: "push ignores stale fork metadata", identity: &ArtifactSourceIdentity{Repository: "example/mobile", CommitSHA: head}, env: map[string]string{"BUILDKITE_PULL_REQUEST": "false"}, want: GitBranch{Name: "feature/buildkite", Origin: GitBranchOriginBuildkite}},
		{name: "tag build", env: map[string]string{"BUILDKITE_TAG": "v1"}, want: absent},
		{name: "outside Buildkite", env: map[string]string{"BUILDKITE": "false"}, want: absent},
		{name: "GitHub provider precedence", env: map[string]string{"GITHUB_ACTIONS": "true"}, want: absent},
		{name: "missing base branch", env: map[string]string{"BUILDKITE_PULL_REQUEST_BASE_BRANCH": ""}, want: label},
		{name: "owner-prefixed fork branch", env: map[string]string{"BUILDKITE_BRANCH": "contributor:feature/buildkite"}, want: label},
		{name: "fork owner keeps its case", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "git@github.com:Contributor/fork.git"}, want: GitBranch{Name: "Contributor:feature/buildkite", Origin: GitBranchOriginBuildkite}},
		{name: "fork without owner has no name", identity: &ArtifactSourceIdentity{Repository: "https://example.com/fork.git", CommitSHA: head}, env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "https://example.com/fork.git"}, want: GitBranch{Origin: GitBranchOriginBuildkite}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearBranchCI(t)
			t.Setenv("BUILDKITE", "true")
			t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
			t.Setenv("BUILDKITE_PULL_REQUEST_REPO", "git@github.com:contributor/fork.git")
			t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
			t.Setenv("BUILDKITE_COMMIT", head)
			t.Setenv("BUILDKITE_PULL_REQUEST", "42")
			t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "staging")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			t.Setenv("PATH", t.TempDir())
			identity := ArtifactSourceIdentity{Repository: fork, CommitSHA: head, ReviewNumber: 42}
			if tt.identity != nil {
				identity = *tt.identity
			}
			original := identity
			if got := detectBranch(t, GitBranchOptions{ArtifactIdentity: &identity}); got != tt.want {
				t.Fatalf("artifact PR context = %#v, want %#v", got, tt.want)
			}
			if identity != original {
				t.Fatalf("artifact identity changed from %#v to %#v", original, identity)
			}
		})
	}
}

func TestBuildkiteForkPullRequestCheckoutSelectsForkBranch(t *testing.T) {
	dir := branchRepository(t)
	runGitForTest(t, dir, "checkout", "-b", "feature/buildkite")
	runGitForTest(t, dir, "branch", "other-local")
	head := runGitForTest(t, dir, "rev-parse", "HEAD")
	label := GitBranch{Name: "contributor:feature/buildkite", Origin: GitBranchOriginBuildkite}
	for _, tt := range []struct {
		name       string
		branch     string
		repository string
		artifact   bool
		commit     string
		env        map[string]string
		want       GitBranch
	}{
		{name: "attached fork artifact", branch: "feature/buildkite", artifact: true, want: label},
		{name: "detached fork artifact", artifact: true, want: label},
		{name: "attached fork checkout", branch: "feature/buildkite", want: label},
		{name: "detached fork checkout", want: label},
		{name: "missing base branch", branch: "feature/buildkite", artifact: true, env: map[string]string{"BUILDKITE_PULL_REQUEST_BASE_BRANCH": ""}, want: label},
		{name: "owner-prefixed fork branch", branch: "feature/buildkite", env: map[string]string{"BUILDKITE_BRANCH": "contributor:feature/buildkite"}, want: label},
		{name: "fork without owner keeps the bare branch out", branch: "feature/buildkite", repository: "https://example.com/fork.git", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "https://example.com/fork.git"}, want: GitBranch{Origin: GitBranchOriginBuildkite}},
		{name: "wrong checkout repository", repository: "https://github.com/other/project", want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "pipeline checkout is not fork", repository: "https://github.com/example/mobile", want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "wrong checkout commit", commit: strings.Repeat("b", 40), want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "different attached branch retains local source", branch: "other-local", want: GitBranch{Name: "other-local", Origin: GitBranchOriginLocalGit}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearBranchCI(t)
			branch := tt.branch
			if branch == "" {
				branch = "--detach"
			}
			runGitForTest(t, dir, "checkout", branch)
			repository := tt.repository
			if repository == "" {
				repository = "https://github.com/contributor/fork.git"
			}
			runGitForTest(t, dir, "remote", "set-url", "origin", repository)
			commit := tt.commit
			if commit == "" {
				commit = head
			}
			t.Setenv("BUILDKITE", "true")
			t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
			t.Setenv("BUILDKITE_PULL_REQUEST_REPO", "git@github.com:contributor/fork.git")
			t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
			t.Setenv("BUILDKITE_COMMIT", commit)
			t.Setenv("BUILDKITE_PULL_REQUEST", "42")
			t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "staging")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			opts := GitBranchOptions{WorkDir: dir}
			if tt.artifact {
				opts.ArtifactIdentity = &ArtifactSourceIdentity{Repository: repository, CommitSHA: head, ReviewNumber: 42}
			}
			if got := detectBranch(t, opts); got != tt.want {
				t.Fatalf("checkout PR context = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSubmittedPullRequestBranchRequiresExactHead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable")
	}
	clearBranchCI(t)
	head := strings.Repeat("a", 40)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	writeBranchEvent(t, map[string]interface{}{"pull_request": map[string]interface{}{
		"number": 42,
		"head":   map[string]interface{}{"sha": head, "ref": "feature/one", "repo": map[string]string{"full_name": "example/mobile"}},
		"base":   map[string]interface{}{"ref": "staging", "repo": map[string]string{"full_name": "example/mobile"}},
	}})
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, remoteHead := range []string{head, strings.Repeat("b", 40)} {
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s\\t%s\\n' '"+remoteHead+"' 'refs/heads/feature/one'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, ref := range []string{"feature/one", "refs/heads/feature/one"} {
			got := detectBranch(t, GitBranchOptions{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: ref}})
			want := GitBranch{Name: "feature/one", Origin: GitBranchOriginSubmitted}
			if remoteHead == head {
				want.Origin = GitBranchOriginGitHub
			}
			if got != want {
				t.Fatalf("submitted ref %q at %s: %#v, want %#v", ref, remoteHead, got, want)
			}
		}
	}
}

func TestSubmittedPullRequestRejectsAmbiguousBareRef(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable")
	}
	clearBranchCI(t)
	head := strings.Repeat("a", 40)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "example/mobile")
	writeBranchEvent(t, map[string]interface{}{"pull_request": map[string]interface{}{
		"number": 42,
		"head":   map[string]interface{}{"sha": head, "ref": "feature/one", "repo": map[string]string{"full_name": "example/mobile"}},
		"base":   map[string]interface{}{"ref": "staging", "repo": map[string]string{"full_name": "example/mobile"}},
	}})
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	script := "#!/bin/sh\nprintf '%s\\t%s\\n' '" + head + "' 'refs/heads/feature/one'\ncase \"$*\" in *refs/tags/feature/one*) printf '%s\\t%s\\n' '" + strings.Repeat("b", 40) + "' 'refs/tags/feature/one';; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := detectBranch(t, GitBranchOptions{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: "feature/one"}}); got.Name != "" {
		t.Fatalf("ambiguous submitted ref was treated as PR head: %#v", got)
	}
	if got := detectBranch(t, GitBranchOptions{SourceGit: &GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: "refs/heads/feature/one"}}); got != (GitBranch{Name: "feature/one", Origin: GitBranchOriginGitHub}) {
		t.Fatalf("explicit head ref was treated as ambiguous: %#v", got)
	}
}

func TestBuildkiteSubmittedPullRequestSHARequiresMatchingSource(t *testing.T) {
	head := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	ownBranch := GitBranch{Name: "feature/buildkite", Origin: GitBranchOriginBuildkite}
	forkBranch := GitBranch{Name: "contributor:feature/buildkite", Origin: GitBranchOriginBuildkite}
	for _, tt := range []struct {
		name       string
		repository string
		ref        string
		env        map[string]string
		want       GitBranch
	}{
		{name: "pipeline repository", want: ownBranch},
		{name: "expected PR head overrides merge commit", env: map[string]string{"BUILDKITE_COMMIT": other, "BUILDKITE_PULL_REQUEST_HEAD_COMMIT": head}, want: ownBranch},
		{name: "merge commit is not the PR head", ref: other, env: map[string]string{"BUILDKITE_COMMIT": other, "BUILDKITE_PULL_REQUEST_HEAD_COMMIT": head}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "fork repository", repository: "https://github.com/contributor/fork.git", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "git@github.com:contributor/fork.git"}, want: forkBranch},
		{name: "owner-prefixed fork branch", repository: "https://github.com/contributor/fork.git", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "git@github.com:contributor/fork.git", "BUILDKITE_BRANCH": "contributor:feature/buildkite"}, want: forkBranch},
		{name: "pipeline repository is not the fork", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "git@github.com:contributor/fork.git"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "unrelated repository", repository: "https://github.com/other/project", want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "wrong head commit", ref: other, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "abbreviated commit", ref: head[:12], want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "invalid commit", ref: strings.Repeat("z", 40), env: map[string]string{"BUILDKITE_COMMIT": strings.Repeat("z", 40)}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "missing head branch", env: map[string]string{"BUILDKITE_BRANCH": ""}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "missing pipeline repository", env: map[string]string{"BUILDKITE_REPO": "", "BUILDKITE_PULL_REQUEST_REPO": "https://github.com/example/mobile"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "invalid PR repository", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": "https://github.com/example/mobile?untrusted=1"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "not a PR", env: map[string]string{"BUILDKITE_PULL_REQUEST": "false"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "zero PR", env: map[string]string{"BUILDKITE_PULL_REQUEST": "0"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "tag build", env: map[string]string{"BUILDKITE_TAG": "v1"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "outside Buildkite", env: map[string]string{"BUILDKITE": "false"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "GitHub provider has precedence", env: map[string]string{"GITHUB_ACTIONS": "true"}, want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "missing base branch", env: map[string]string{"BUILDKITE_PULL_REQUEST_BASE_BRANCH": ""}, want: ownBranch},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearBranchCI(t)
			t.Setenv("BUILDKITE", "true")
			t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
			t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
			t.Setenv("BUILDKITE_COMMIT", head)
			t.Setenv("BUILDKITE_PULL_REQUEST", "42")
			t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "staging")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			t.Setenv("PATH", t.TempDir())
			source := GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: head}
			if tt.repository != "" {
				source.RepositoryURL = tt.repository
			}
			if tt.ref != "" {
				source.Ref = tt.ref
			}
			original := source
			got := detectBranch(t, GitBranchOptions{WorkDir: t.TempDir(), SourceGit: &source})
			if got != tt.want {
				t.Fatalf("submitted PR context = %#v, want %#v", got, tt.want)
			}
			if source != original {
				t.Fatalf("source changed from %#v to %#v", original, source)
			}
		})
	}
}

func TestBuildkiteSubmittedPullRequestBranchRequiresVerifiedHead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable")
	}
	head := strings.Repeat("a", 40)
	ownBranch := GitBranch{Name: "feature/buildkite", Origin: GitBranchOriginBuildkite}
	submitted := GitBranch{Name: "feature/buildkite", Origin: GitBranchOriginSubmitted}
	forkRepository := "git@github.com:contributor/fork.git"
	for _, tt := range []struct {
		name       string
		remoteHead string
		tag        bool
		failed     bool
		repository string
		ref        string
		env        map[string]string
		want       GitBranch
	}{
		{name: "bare branch", remoteHead: head, ref: "feature/buildkite", want: ownBranch},
		{name: "qualified branch", remoteHead: head, ref: "refs/heads/feature/buildkite", want: ownBranch},
		{name: "moved bare branch", remoteHead: strings.Repeat("b", 40), ref: "feature/buildkite", want: submitted},
		{name: "moved qualified branch", remoteHead: strings.Repeat("b", 40), ref: "refs/heads/feature/buildkite", want: submitted},
		{name: "ambiguous bare ref", remoteHead: head, tag: true, ref: "feature/buildkite", want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "qualified branch disambiguates tag", remoteHead: head, tag: true, ref: "refs/heads/feature/buildkite", want: ownBranch},
		{name: "failed lookup", failed: true, ref: "feature/buildkite", want: GitBranch{Origin: GitBranchOriginNone}},
		{name: "failed qualified lookup keeps source", failed: true, ref: "refs/heads/feature/buildkite", want: submitted},
		{name: "missing base branch", remoteHead: head, ref: "feature/buildkite", env: map[string]string{"BUILDKITE_PULL_REQUEST_BASE_BRANCH": ""}, want: ownBranch},
		{name: "fork branch", remoteHead: head, repository: "https://github.com/contributor/fork", ref: "feature/buildkite", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": forkRepository}, want: GitBranch{Name: "contributor:feature/buildkite", Origin: GitBranchOriginBuildkite}},
		{name: "owner-prefixed fork branch", remoteHead: head, repository: "https://github.com/contributor/fork", ref: "refs/heads/feature/buildkite", env: map[string]string{"BUILDKITE_PULL_REQUEST_REPO": forkRepository, "BUILDKITE_BRANCH": "contributor:feature/buildkite"}, want: GitBranch{Name: "contributor:feature/buildkite", Origin: GitBranchOriginBuildkite}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearBranchCI(t)
			t.Setenv("BUILDKITE", "true")
			t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
			t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
			t.Setenv("BUILDKITE_COMMIT", head)
			t.Setenv("BUILDKITE_PULL_REQUEST", "42")
			t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "staging")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			script := "#!/bin/sh\n"
			if tt.failed {
				script += "exit 1\n"
			} else {
				script += "printf '%s\\t%s\\n' '" + tt.remoteHead + "' 'refs/heads/feature/buildkite'\n"
				if tt.tag {
					script += "case \"$*\" in *refs/tags/feature/buildkite*) printf '%s\\t%s\\n' '" + head + "' 'refs/tags/feature/buildkite';; esac\n"
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			source := GitSourceContext{RepositoryURL: "https://github.com/example/mobile", Ref: tt.ref}
			if tt.repository != "" {
				source.RepositoryURL = tt.repository
			}
			original := source
			got := detectBranch(t, GitBranchOptions{SourceGit: &source})
			if got != tt.want {
				t.Fatalf("submitted Git branch = %#v, want %#v", got, tt.want)
			}
			if source != original {
				t.Fatalf("source changed from %#v to %#v", original, source)
			}
		})
	}
}

func TestCIWithoutCheckoutUsesItsOwnBranch(t *testing.T) {
	head := strings.Repeat("a", 40)
	none := GitBranch{Origin: GitBranchOriginNone}
	github := func(name string) GitBranch { return GitBranch{Name: name, Origin: GitBranchOriginGitHub} }
	buildkite := func(name string) GitBranch { return GitBranch{Name: name, Origin: GitBranchOriginBuildkite} }
	otherBase := pullRequestEvent(head, "example/mobile")
	otherBase["pull_request"].(map[string]interface{})["base"] = map[string]interface{}{"ref": "main", "repo": map[string]string{"full_name": "other/project"}}
	pullRequestRun := map[string]string{"GITHUB_REF": "refs/pull/42/merge", "GITHUB_HEAD_REF": "feature/one"}
	pullRequestTargetRun := map[string]string{"GITHUB_REF": "refs/heads/staging", "GITHUB_HEAD_REF": "feature/one"}
	for _, tt := range []struct {
		name  string
		ci    string
		event map[string]interface{}
		env   map[string]string
		want  GitBranch
	}{
		{name: "GitHub pull request", ci: "GITHUB_ACTIONS", event: pullRequestEvent(head, "example/mobile"), env: pullRequestRun, want: github("feature/one")},
		{name: "GitHub fork pull request", ci: "GITHUB_ACTIONS", event: pullRequestEvent(head, "contributor/fork"), env: pullRequestRun, want: github("contributor:feature/one")},
		{name: "GitHub pull_request_target", ci: "GITHUB_ACTIONS", event: pullRequestEvent(head, "contributor/fork"), env: pullRequestTargetRun, want: github("contributor:feature/one")},
		{name: "GitHub pull request without event", ci: "GITHUB_ACTIONS", env: pullRequestRun, want: none},
		{name: "GitHub pull_request_target without event", ci: "GITHUB_ACTIONS", env: pullRequestTargetRun, want: none},
		{name: "GitHub event for another repository", ci: "GITHUB_ACTIONS", event: otherBase, env: pullRequestRun, want: none},
		{name: "GitHub push", ci: "GITHUB_ACTIONS", event: map[string]interface{}{"ref": "refs/heads/release/2"}, env: map[string]string{"GITHUB_REF": "refs/heads/release/2"}, want: github("release/2")},
		{name: "GitHub manual run", ci: "GITHUB_ACTIONS", env: map[string]string{"GITHUB_REF": "refs/heads/staging"}, want: github("staging")},
		{name: "GitHub tag", ci: "GITHUB_ACTIONS", env: map[string]string{"GITHUB_REF": "refs/tags/v1", "GITHUB_REF_TYPE": "tag"}, want: none},
		{name: "GitHub without repository", ci: "GITHUB_ACTIONS", env: map[string]string{"GITHUB_REF": "refs/heads/staging", "GITHUB_REPOSITORY": ""}, want: none},
		{name: "Buildkite branch", ci: "BUILDKITE", want: buildkite("feature/buildkite")},
		{name: "Buildkite pull request", ci: "BUILDKITE", env: map[string]string{"BUILDKITE_PULL_REQUEST": "42", "BUILDKITE_PULL_REQUEST_REPO": "git://github.com/example/mobile.git"}, want: buildkite("feature/buildkite")},
		{name: "Buildkite fork pull request", ci: "BUILDKITE", env: map[string]string{"BUILDKITE_PULL_REQUEST": "42", "BUILDKITE_PULL_REQUEST_REPO": "git://github.com/contributor/fork.git"}, want: buildkite("contributor:feature/buildkite")},
		{name: "Buildkite tag", ci: "BUILDKITE", env: map[string]string{"BUILDKITE_TAG": "v1"}, want: none},
		{name: "outside CI", want: none},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearBranchCI(t)
			if tt.event != nil {
				writeBranchEvent(t, tt.event)
			}
			if tt.ci != "" {
				t.Setenv(tt.ci, "true")
			}
			t.Setenv("GITHUB_REPOSITORY", "example/mobile")
			t.Setenv("GITHUB_SHA", strings.Repeat("b", 40))
			t.Setenv("BUILDKITE_REPO", "git@github.com:example/mobile.git")
			t.Setenv("BUILDKITE_BRANCH", "feature/buildkite")
			t.Setenv("BUILDKITE_COMMIT", strings.Repeat("c", 40))
			t.Setenv("BUILDKITE_PULL_REQUEST", "false")
			t.Setenv("BUILDKITE_PULL_REQUEST_BASE_BRANCH", "staging")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			for _, workDir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
				if got := detectBranch(t, GitBranchOptions{WorkDir: workDir}); got != tt.want {
					t.Fatalf("no checkout at %s = %#v, want %#v", workDir, got, tt.want)
				}
			}
			if got := detectBranch(t, GitBranchOptions{}); got != none {
				t.Fatalf("context without a work directory used CI: %#v", got)
			}
		})
	}
}

func TestPullRequestHeadBranchQualifiesForksWithOwner(t *testing.T) {
	for _, tt := range []struct {
		branch, repository string
		fork               bool
		want               string
	}{
		{branch: "main", repository: "example/mobile", want: "main"},
		{branch: "main", repository: "Contributor/fork", fork: true, want: "Contributor:main"},
		{branch: "feature/one", repository: "git@github.com:contributor/fork.git", fork: true, want: "contributor:feature/one"},
		{branch: "feature/one", repository: "https://gitlab.example.com/group/team/fork.git", fork: true, want: "group/team:feature/one"},
		{branch: "feature/one", repository: "https://example.com/fork.git", fork: true},
		{branch: "feature/one", repository: "https://github.com//fork", fork: true},
		{repository: "example/mobile"},
	} {
		got := pullRequestHeadBranch(tt.branch, tt.repository, tt.fork, GitBranchOriginGitHub)
		if got != (GitBranch{Name: tt.want, Origin: GitBranchOriginGitHub}) {
			t.Fatalf("head %q from %q (fork=%t) = %#v, want %q", tt.branch, tt.repository, tt.fork, got, tt.want)
		}
	}
}

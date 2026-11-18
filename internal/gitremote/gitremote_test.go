package gitremote

import (
	"os"
	"os/exec"
	"testing"
)

func TestParseGithubRemote(t *testing.T) {
	tests := []struct {
		name      string
		remote    string
		wantOwner string
		wantRepo  string
		wantOK    bool
	}{
		{name: "ssh scp", remote: "git@github.com:revyl/app.git", wantOwner: "revyl", wantRepo: "app", wantOK: true},
		{name: "https", remote: "https://github.com/revyl/app.git", wantOwner: "revyl", wantRepo: "app", wantOK: true},
		{name: "https no suffix", remote: "https://github.com/revyl/app", wantOwner: "revyl", wantRepo: "app", wantOK: true},
		{name: "ssh url", remote: "ssh://git@github.com/revyl/app.git", wantOwner: "revyl", wantRepo: "app", wantOK: true},
		{name: "nested path", remote: "https://github.com/revyl/app/extra", wantOwner: "revyl", wantRepo: "app", wantOK: true},
		{name: "github shorthand", remote: "github:Owner/app", wantOwner: "Owner", wantRepo: "app", wantOK: true},
		{name: "gh shorthand", remote: "gh:Owner/app.git", wantOwner: "Owner", wantRepo: "app", wantOK: true},
		{name: "non-github", remote: "https://gitlab.com/revyl/app.git", wantOK: false},
		{name: "empty", remote: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			owner, repo, ok := ParseGithubRemote(tc.remote)
			if ok != tc.wantOK {
				t.Fatalf("ParseGithubRemote(%q) ok = %v, want %v", tc.remote, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if owner != tc.wantOwner || repo != tc.wantRepo {
				t.Fatalf("ParseGithubRemote(%q) = (%q, %q), want (%q, %q)",
					tc.remote, owner, repo, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

func TestResolveSlugOverride(t *testing.T) {
	owner, repo, err := ResolveSlug("/tmp/does-not-matter", "acme/widgets")
	if err != nil {
		t.Fatalf("ResolveSlug() error = %v", err)
	}
	if owner != "acme" || repo != "widgets" {
		t.Fatalf("ResolveSlug() = (%q, %q), want (acme, widgets)", owner, repo)
	}
}

func TestResolveSlugInvalidOverride(t *testing.T) {
	if _, _, err := ResolveSlug("/tmp", "not-a-slug"); err == nil {
		t.Fatalf("ResolveSlug() error = nil, want error for invalid override")
	}
}

func TestRepositoryRemoteIdentity(t *testing.T) {
	for _, tc := range []struct {
		remote, provider, origin, namespace string
	}{
		{"https://github.com/team/app.git", "github", "", "team"},
		{"git@gitlab.com:team/subgroup/app.git", "gitlab", "https://gitlab.com", "team/subgroup"},
		{"ssh://git@gitlab.example.com:22/team/app.git", "gitlab", "https://gitlab.example.com", "team"},
		{"https://gitlab.example.com:8443/team/subgroup/app.git", "gitlab", "https://gitlab.example.com:8443", "team/subgroup"},
		{"https://[2606:4700::1111]:8443/team/app.git", "gitlab", "https://[2606:4700::1111]:8443", "team"},
		{"git@github.com:team/app.git", "github", "", "team"},
		{"ssh://git@github.com/team/app.git", "github", "", "team"},
		{"github:team/app", "github", "", "team"},
		{"gh:team/app.git", "github", "", "team"},
		{"GitHub:team/app", "github", "", "team"},
		{"git@github.example.com:team/app.git", "gitlab", "https://github.example.com", "team"},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			got, err := ParseRepositoryRemote(tc.remote)
			if err != nil {
				t.Fatal(err)
			}
			origin := ""
			if got.InstanceURL != nil {
				origin = *got.InstanceURL
			}
			if got.Provider != tc.provider || origin != tc.origin || got.Namespace != tc.namespace || got.Name != "app" {
				t.Fatalf("unexpected identity: %#v, origin %q", got, origin)
			}
		})
	}
}

func TestRepositoryRemoteRejectsAmbiguousIdentity(t *testing.T) {
	for _, remote := range []string{
		"http://gitlab.com/team/app", "https://user:password@gitlab.com/team/app",
		"https://gitlab.com/team/../app", "https://gitlab.com/team%2Fsubgroup/app",
		"https://gitlab.com/team/app?token=fixture", "https://gitlab.com/team/app#ref",
		"ssh://git@gitlab.com:2222/team/app", "ssh://user@gitlab.com/team/app",
		"https://github.com:8443/team/app", "https://github.com/team/subgroup/app",
		"https://gitlab.com:0/team/app", "https://gitlab.com:65536/team/app",
		"github:team/subgroup/app", "gh:team", "github://team/app", "gh:",
	} {
		t.Run(remote, func(t *testing.T) {
			if _, err := ParseRepositoryRemote(remote); err == nil {
				t.Fatal("ambiguous origin accepted")
			}
		})
	}
}

func TestParseRepositorySlug(t *testing.T) {
	got, err := ParseRepositorySlug(" Owner/app ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "github" || got.InstanceURL != nil || got.Namespace != "Owner" || got.Name != "app" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	for _, slug := range []string{"", "owner", "owner/", "a/b/c", "owner/app?x=1", "../app"} {
		if _, err := ParseRepositorySlug(slug); err == nil {
			t.Fatalf("ParseRepositorySlug(%q) accepted an invalid slug", slug)
		}
	}
}

func initGitRepositoryWithOrigin(t *testing.T, origin string, config ...[2]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	commands := [][]string{{"init", "-q"}}
	for _, entry := range config {
		commands = append(commands, []string{"config", entry[0], entry[1]})
	}
	commands = append(commands, []string{"remote", "add", "origin", origin})
	for _, args := range commands {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

func TestResolveRepositoryAppliesInsteadOfWhenConfiguredOriginDoesNotParse(t *testing.T) {
	root := initGitRepositoryWithOrigin(t, "corp:team/app.git", [2]string{"url.https://github.com/.insteadOf", "corp:"})

	got, err := ResolveRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "github" || got.Namespace != "team" || got.Name != "app" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	owner, repo, err := ResolveSlug(root, "")
	if err != nil || owner != "team" || repo != "app" {
		t.Fatalf("ResolveSlug() = (%q, %q, %v), want (team, app)", owner, repo, err)
	}
}

func TestResolveRepositoryKeepsConfiguredOriginOverInsteadOfMirror(t *testing.T) {
	root := initGitRepositoryWithOrigin(t, "https://github.com/team/app.git", [2]string{"url.https://mirror.example.com/.insteadOf", "https://github.com/"})

	got, err := ResolveRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "github" || got.InstanceURL != nil || got.Namespace != "team" || got.Name != "app" {
		t.Fatalf("unexpected identity: %#v", got)
	}
}

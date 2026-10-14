package gitremote

import "testing"

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
	} {
		t.Run(remote, func(t *testing.T) {
			if _, err := ParseRepositoryRemote(remote); err == nil {
				t.Fatal("ambiguous origin accepted")
			}
		})
	}
}

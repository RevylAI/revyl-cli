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

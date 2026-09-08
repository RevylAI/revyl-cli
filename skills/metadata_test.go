package skills

import (
	"bytes"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Name                   string            `yaml:"name"`
	Description            string            `yaml:"description"`
	License                string            `yaml:"license"`
	Compatibility          string            `yaml:"compatibility"`
	Metadata               map[string]string `yaml:"metadata"`
	AllowedTools           string            `yaml:"allowed-tools"`
	DisableModelInvocation bool              `yaml:"disable-model-invocation"`
}

type openAIMetadata struct {
	Interface struct {
		DisplayName      string `yaml:"display_name"`
		ShortDescription string `yaml:"short_description"`
		IconSmall        string `yaml:"icon_small"`
		IconLarge        string `yaml:"icon_large"`
		BrandColor       string `yaml:"brand_color"`
		DefaultPrompt    string `yaml:"default_prompt"`
	} `yaml:"interface"`
	Policy struct {
		AllowImplicitInvocation *bool `yaml:"allow_implicit_invocation"`
	} `yaml:"policy"`
}

func TestShippedSkillMetadata(t *testing.T) {
	validName := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	brandColor := regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	for name, content := range shippedContents {
		t.Run(name, func(t *testing.T) {
			parts := strings.SplitN(content, "---\n", 3)
			if len(parts) != 3 || parts[0] != "" {
				t.Fatal("SKILL.md requires YAML frontmatter")
			}
			var frontmatter skillFrontmatter
			decoder := yaml.NewDecoder(strings.NewReader(parts[1]))
			decoder.KnownFields(true)
			if err := decoder.Decode(&frontmatter); err != nil {
				t.Fatal(err)
			}
			if frontmatter.Name != name || len(name) > 64 || !validName.MatchString(name) {
				t.Errorf("invalid or mismatched skill name %q", frontmatter.Name)
			}
			if strings.TrimSpace(frontmatter.Description) == "" || utf8.RuneCountInString(frontmatter.Description) > 1024 {
				t.Error("description must contain 1-1024 characters")
			}
			if strings.ContainsAny(frontmatter.Name+frontmatter.Description, "<>") {
				t.Error("skill name and description must not contain angle brackets")
			}
			if utf8.RuneCountInString(frontmatter.Compatibility) > 500 {
				t.Error("optional compatibility must not exceed 500 characters")
			}
			if lines := len(strings.Split(strings.TrimSuffix(content, "\n"), "\n")); lines >= 500 {
				t.Errorf("root skill has %d lines; must stay under 500", lines)
			}
			files, err := Files(name)
			if err != nil {
				t.Fatal(err)
			}
			filePaths := make(map[string]bool, len(files))
			var metadataBytes []byte
			for _, file := range files {
				filePaths[file.Path] = true
				if file.Path == "agents/openai.yaml" {
					metadataBytes = file.Content
				}
			}
			if len(metadataBytes) == 0 {
				t.Fatal("missing agents/openai.yaml")
			}
			var metadata openAIMetadata
			decoder = yaml.NewDecoder(bytes.NewReader(metadataBytes))
			decoder.KnownFields(true)
			if err := decoder.Decode(&metadata); err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(metadata.Interface.DisplayName) == "" {
				t.Error("display_name must not be empty")
			}
			if length := utf8.RuneCountInString(metadata.Interface.ShortDescription); length < 25 || length > 64 {
				t.Errorf("short_description has %d characters; want 25-64", length)
			}
			manualOnly := name == RevylCLIAtlasReviewName || name == RevylCLIAuthBypassName
			if frontmatter.DisableModelInvocation != manualOnly {
				t.Errorf("disable-model-invocation = %t, want %t", frontmatter.DisableModelInvocation, manualOnly)
			}
			if metadata.Policy.AllowImplicitInvocation == nil || *metadata.Policy.AllowImplicitInvocation == manualOnly {
				t.Errorf("allow_implicit_invocation must explicitly match manual-only policy %t", manualOnly)
			}
			for _, icon := range []string{metadata.Interface.IconSmall, metadata.Interface.IconLarge} {
				if icon == "" {
					continue
				}
				iconPath := strings.TrimPrefix(icon, "./")
				if !fs.ValidPath(iconPath) || !strings.HasPrefix(iconPath, "assets/") || !filePaths[iconPath] {
					t.Errorf("optional icon %q must name an embedded assets/ file", icon)
				}
			}
			if color := metadata.Interface.BrandColor; color != "" && !brandColor.MatchString(color) {
				t.Errorf("invalid optional brand_color %q", color)
			}
			if prompt := metadata.Interface.DefaultPrompt; prompt != "" && !strings.Contains(prompt, "$"+name) {
				t.Errorf("optional default_prompt must mention $%s", name)
			}
		})
	}
}

func TestAllPackageResourceLinksAreEmbedded(t *testing.T) {
	links := regexp.MustCompile(`\]\(([^\s)]+)\)`)
	for name := range shippedContents {
		files, err := Files(name)
		if err != nil {
			t.Fatal(err)
		}
		filePaths := make(map[string]bool, len(files))
		for _, file := range files {
			filePaths[file.Path] = true
		}
		for _, file := range files {
			if !strings.HasSuffix(file.Path, ".md") {
				continue
			}
			for _, match := range links.FindAllSubmatch(file.Content, -1) {
				target, err := url.Parse(string(match[1]))
				if err != nil {
					t.Errorf("%s/%s: malformed reference %q", name, file.Path, match[1])
					continue
				}
				if target.IsAbs() || target.Host != "" || target.Path == "" {
					continue
				}
				relativePath := path.Join(path.Dir(file.Path), target.Path)
				if !fs.ValidPath(relativePath) || !filePaths[relativePath] {
					t.Errorf("%s/%s: referenced file %q is not embedded", name, file.Path, target.Path)
				}
			}
		}
	}
}

func TestAuthBypassLoadsOnlyMatchingPlatformReference(t *testing.T) {
	for _, required := range []string{
		"## Shared Contract", "## Detect the App Stack", "Expo Router",
		"## Implement for the Detected Stack", "Read only the reference matching the detected app stack",
		"do not load unrelated platform recipes", "## Implementation Rules", "## Verification",
		"Do not commit real tokens", "Validate the token before changing app state",
		"Allowlist roles and redirects", "existing auth/session primitives", "accepted and rejected states",
		"separate from normal production login", "Make failure observable", "Production builds cannot activate",
		"REVYL_AUTH_BYPASS_ENABLED=true", "Unknown role is rejected", "Unknown redirect is rejected",
		"Disabled or missing launch-var gate is rejected", "Wrong token is rejected",
	} {
		if !strings.Contains(RevylCLIAuthBypassContent, required) {
			t.Errorf("auth-bypass root is missing shared instruction %q", required)
		}
	}
	platforms := []string{"expo", "react-native", "ios", "android", "flutter"}
	for _, platform := range platforms {
		reference := "references/" + platform + ".md"
		if !strings.Contains(RevylCLIAuthBypassContent, "("+reference+")") {
			t.Errorf("auth-bypass root must link to %s", reference)
		}
		content, err := packages.ReadFile(RevylCLIAuthBypassName + "/" + reference)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(content, []byte("shared contract, implementation rules, and verification")) {
			t.Errorf("%s must retain the shared safety contract", reference)
		}
		for _, other := range platforms {
			if other != platform && bytes.Contains(content, []byte(other+".md")) {
				t.Errorf("%s must not direct readers to unrelated recipe %s", reference, other)
			}
		}
	}
	for _, implementation := range []string{"### Expo", "### React Native", "### Native iOS", "### Native Android", "### Flutter", "func launchValue"} {
		if strings.Contains(RevylCLIAuthBypassContent, implementation) {
			t.Errorf("platform implementation %q belongs in its reference, not the root", implementation)
		}
	}
}

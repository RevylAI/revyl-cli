package skillcatalog

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/skills"
)

func TestCatalogFilesMatchEmbeddedPackages(t *testing.T) {
	seen := make(map[string]bool)
	for _, skill := range All() {
		t.Run(skill.Name, func(t *testing.T) {
			if seen[skill.Name] || skill.Description == "" {
				t.Fatal("catalog entries must have unique names and non-empty descriptions")
			}
			seen[skill.Name] = true
			files, err := skill.Files()
			if err != nil {
				t.Fatal(err)
			}
			embedded, err := skills.Files(skill.Name)
			if err != nil || !reflect.DeepEqual(files, embedded) {
				t.Fatalf("catalog Files() differs from embedded package: %v", err)
			}
			foundContent := false
			for _, file := range files {
				if file.Path == SkillFileName {
					foundContent = true
					if string(file.Content) != skill.Content {
						t.Error("catalog Content differs from package SKILL.md")
					}
				}
			}
			if !foundContent {
				t.Error("package is missing SKILL.md")
			}
		})
	}
}

func TestAuthBypassAliasesResolveCompleteCanonicalPackage(t *testing.T) {
	want, err := skills.Files(skills.RevylCLIAuthBypassName)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range AuthBypassLeafAliases() {
		skill, ok := Get(alias)
		if !ok || skill.Name != skills.RevylCLIAuthBypassName {
			t.Fatalf("alias %q did not resolve to the shipped name", alias)
		}
		files, err := skill.Files()
		if err != nil || !reflect.DeepEqual(files, want) {
			t.Errorf("alias %q did not resolve the complete package: %v", alias, err)
		}
	}
}

func TestSkillFilesRejectUnknownNames(t *testing.T) {
	for _, name := range []string{"", "unknown", "../revyl-cli", "revyl-cli/../revyl-mcp"} {
		files, err := (Skill{Name: name}).Files()
		if files != nil || err == nil {
			t.Errorf("Skill{Name: %q}.Files() = %v, %v; want error", name, files, err)
		}
	}
}

func TestSkillsInstallerOffersOnlyDefaultInstall(t *testing.T) {
	defaults := make(map[string]bool)
	for _, skill := range DefaultInstall() {
		defaults[skill.Name] = true
	}
	for _, skill := range All() {
		t.Run(skill.Name, func(t *testing.T) {
			parts := strings.SplitN(skill.Content, "---\n", 3)
			if len(parts) != 3 || parts[0] != "" {
				t.Fatal("SKILL.md requires YAML frontmatter")
			}
			var frontmatter struct {
				Metadata struct {
					Internal bool `yaml:"internal"`
				} `yaml:"metadata"`
			}
			if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
				t.Fatal(err)
			}
			if frontmatter.Metadata.Internal == defaults[skill.Name] {
				t.Errorf("metadata.internal = %t; set it to true exactly for skills outside DefaultInstall so npx skills add offers the same set as revyl skill install --yes", frontmatter.Metadata.Internal)
			}
		})
	}
}

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/api"
)

var (
	snakeCaseIdentifier   = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)
	compositeActionList   = regexp.MustCompile(`\(action: [^)]*\)`)
	instructionToolCalled = regexp.MustCompile(`\b([a-z][a-z0-9]*(?:_[a-z0-9]+)*)\(`)
)

// instructionTermsThatAreNotTools are the argument, result and outcome field
// names the instructions mention. Every other snake_case name in them must be
// a tool the profile registers.
var instructionTermsThatAreNotTools = map[string]bool{
	"add_card":             true,
	"billing_action":       true,
	"billing_next_step":    true,
	"billing_notice":       true,
	"idle_seconds":         true,
	"idle_timeout_seconds": true,
	"module_names_or_ids":  true,
	"next_steps":           true,
	"queue_notice":         true,
	"restart_required":     true,
	"session_id":           true,
	"session_index":        true,
	"test_name":            true,
	"viewer_url":           true,
	"yaml_content":         true,
}

type publishedProfile struct {
	name    string
	options []ServerOption
}

var publishedProfiles = []publishedProfile{
	{name: "flat"},
	{name: "core", options: []ServerOption{WithProfile(ProfileCore)}},
	{name: "full", options: []ServerOption{WithProfile(ProfileFull)}},
	{name: "dev", options: []ServerOption{WithProfile(ProfileDev)}},
}

// connectToProfile returns what an MCP client of the profile sees: the
// server instructions and the listed tools.
func connectToProfile(t *testing.T, profile publishedProfile) (string, []*mcpsdk.Tool) {
	t.Helper()
	server, err := NewServer("test", false, profile.options...)
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "instructions-test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools(): %v", err)
	}
	return clientSession.InitializeResult().Instructions, listed.Tools
}

func TestPublishedInstructionsNameOnlyToolsTheProfileRegisters(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	for _, profile := range publishedProfiles {
		t.Run(profile.name, func(t *testing.T) {
			instructions, listed := connectToProfile(t, profile)
			registered := map[string]bool{}
			for _, tool := range listed {
				registered[tool.Name] = true
			}
			if instructions == "" {
				t.Fatal("the server publishes no instructions")
			}
			for _, match := range instructionToolCalled.FindAllStringSubmatch(instructions, -1) {
				if !registered[match[1]] {
					t.Errorf("instructions call %s(), which this profile does not register", match[1])
				}
			}
			withoutActionLists := compositeActionList.ReplaceAllString(instructions, "")
			for _, name := range snakeCaseIdentifier.FindAllString(withoutActionLists, -1) {
				if !registered[name] && !instructionTermsThatAreNotTools[name] {
					t.Errorf("instructions name %s, which this profile does not register", name)
				}
			}
		})
	}
}

// Test and workflow names no longer come from aliases in .revyl/config.yaml:
// a test name is its file name under .revyl/tests/ or its Revyl name.
func TestPublishedToolsDoNotDescribeNamesAsConfigAliases(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	for _, profile := range publishedProfiles {
		t.Run(profile.name, func(t *testing.T) {
			_, listed := connectToProfile(t, profile)
			for _, tool := range listed {
				schema, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				text := tool.Description + string(schema)
				for _, retired := range []string{"alias from", "(from config)", "tests from .revyl/config.yaml", "tests from the project's .revyl/config.yaml"} {
					if strings.Contains(text, retired) {
						t.Errorf("%s describes names as config aliases (%q)", tool.Name, retired)
					}
				}
			}
		})
	}
}

var (
	compositeActionLine  = regexp.MustCompile(`(?m)^\s+([a-z_]+)\s+- (.*)$`)
	parenthetical        = regexp.MustCompile(`\([^()]*\)`)
	documentedParamKey   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	instructionRecipe    = regexp.MustCompile(`\b(manage_[a-z]+)\(action="([a-z_]+)", params=\{([^}]*)\}\)`)
	instructionRecipeKey = regexp.MustCompile(`([a-z][a-z0-9_]*):`)
)

// documentedParams returns the argument keys an action line of a composite
// tool description lists after "Params:", or none when it lists none.
func documentedParams(t *testing.T, tool, action, line string) []string {
	t.Helper()
	_, params, found := strings.Cut(line, "Params: ")
	if !found {
		return nil
	}
	for parenthetical.MatchString(params) {
		params = parenthetical.ReplaceAllString(params, "")
	}
	params, _, _ = strings.Cut(params, ".")
	var keys []string
	for _, key := range strings.Split(params, ",") {
		key = strings.TrimSpace(key)
		if !documentedParamKey.MatchString(key) {
			t.Fatalf("%s action %s documents a parameter this test cannot read: %q", tool, action, key)
		}
		keys = append(keys, key)
	}
	return keys
}

// Every argument key a composite recipe documents must decode into the
// action's input. Each key is sent alone as null, so only an unknown key,
// never a value or another key, can fail decoding.
func TestPublishedCompositeRecipesUseArgumentsTheActionsAccept(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	for _, profile := range publishedProfiles {
		if profile.name != "core" && profile.name != "full" {
			continue
		}
		t.Run(profile.name, func(t *testing.T) {
			instructions, listed := connectToProfile(t, profile)
			recipes := map[[2]string][]string{}
			for _, tool := range listed {
				if !strings.HasPrefix(tool.Name, "manage_") || !strings.Contains(instructions, "**"+tool.Name+"**") {
					continue
				}
				for _, match := range compositeActionLine.FindAllStringSubmatch(tool.Description, -1) {
					recipes[[2]string{tool.Name, match[1]}] = documentedParams(t, tool.Name, match[1], match[2])
				}
			}
			for _, match := range instructionRecipe.FindAllStringSubmatch(instructions, -1) {
				var keys []string
				for _, key := range instructionRecipeKey.FindAllStringSubmatch(match[3], -1) {
					keys = append(keys, key[1])
				}
				recipes[[2]string{match[1], match[2] + " (instructions)"}] = keys
			}
			if len(recipes) == 0 {
				t.Fatal("found no composite recipes in the published tools")
			}
			server, err := NewServer("test", false, profile.options...)
			if err != nil {
				t.Fatal(err)
			}
			server.apiClient = api.NewClientWithBaseURL("test-key", backend.URL)
			client := connectWorkspaceTestClient(t, server)
			for recipe, keys := range recipes {
				action, _, _ := strings.Cut(recipe[1], " ")
				for _, key := range keys {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					result, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: recipe[0], Arguments: map[string]any{"action": action, "params": map[string]any{key: nil}}})
					cancel()
					text := fmt.Sprint(err)
					if result != nil {
						for _, content := range result.Content {
							if textContent, ok := content.(*mcpsdk.TextContent); ok {
								text += " " + textContent.Text
							}
						}
					}
					if strings.Contains(text, "invalid params for action") || strings.Contains(text, "unknown action") {
						t.Errorf("%s(action=%q) documents %s: %s", recipe[0], recipe[1], key, text)
					}
				}
			}
		})
	}
}

var (
	groundedActionList = regexp.MustCompile(`\(grounded by default\): ([^\n]*)`)
	fixedIdleLimit     = regexp.MustCompile(`(?i)after \d+ minutes of inactivity`)
)

// The tools the instructions call grounded must take a natural-language
// target, and the idle limit must come from the session, since it differs by
// platform and project.
func TestPublishedInstructionsDescribeGroundingAndIdleLimitsAsRegistered(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	for _, profile := range publishedProfiles {
		if profile.name == "dev" {
			continue
		}
		t.Run(profile.name, func(t *testing.T) {
			instructions, listed := connectToProfile(t, profile)
			schemas := map[string]string{}
			for _, tool := range listed {
				schema, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				schemas[tool.Name] = string(schema)
			}
			match := groundedActionList.FindStringSubmatch(instructions)
			if match == nil {
				t.Fatal("the instructions list no grounded device actions")
			}
			for _, name := range snakeCaseIdentifier.FindAllString(match[1], -1) {
				if !strings.Contains(schemas[name], `"target":`) {
					t.Errorf("the instructions call %s grounded, but it takes no target", name)
				}
			}
			if fixedIdleLimit.MatchString(instructions) || !strings.Contains(instructions, "idle_timeout_seconds") {
				t.Error("the instructions must point at the session's idle_timeout_seconds instead of a fixed idle limit")
			}
			for _, platformDefault := range []string{"project's session idle timeout", "600 on Web", "900 on Android and iOS"} {
				if !strings.Contains(schemas["start_device_session"], platformDefault) {
					t.Errorf("start_device_session idle_timeout does not describe its default %q", platformDefault)
				}
			}
		})
	}
}

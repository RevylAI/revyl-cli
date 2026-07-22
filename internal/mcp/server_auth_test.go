package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/auth"
)

// TestNewServerAcceptsAccessTokenCredentials verifies browser login credentials start MCP successfully.
func TestNewServerAcceptsAccessTokenCredentials(t *testing.T) {
	prepareServerAuthTest(t)
	expiresAt := time.Now().Add(time.Hour)
	saveServerCredentials(t, &auth.Credentials{
		AccessToken: "test-access-token",
		ExpiresAt:   &expiresAt,
		AuthMethod:  "browser",
	})

	server, err := NewServer("test", false, WithProfile(ProfileCore))
	if err != nil {
		t.Fatalf("NewServer() with access token: %v", err)
	}
	requireServerTool(t, server, "start_device_session")
}

// TestNewServerAcceptsAPIKeyCredentials verifies persistent API-key login starts MCP successfully.
func TestNewServerAcceptsAPIKeyCredentials(t *testing.T) {
	prepareServerAuthTest(t)
	saveServerCredentials(t, &auth.Credentials{
		APIKey:     "test-api-key",
		AuthMethod: "api_key",
	})

	if _, err := NewServer("test", false, WithProfile(ProfileCore)); err != nil {
		t.Fatalf("NewServer() with API key: %v", err)
	}
}

// TestNewServerAcceptsEnvironmentAPIKey verifies environment credentials remain the highest-priority startup path.
func TestNewServerAcceptsEnvironmentAPIKey(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")

	if _, err := NewServer("test", false, WithProfile(ProfileCore)); err != nil {
		t.Fatalf("NewServer() with environment API key: %v", err)
	}
}

// TestNewServerHonorsLocalAuthOverride verifies MCP uses the shared CLI credential precedence.
func TestNewServerHonorsLocalAuthOverride(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	saveServerCredentials(t, &auth.Credentials{
		APIKey:            "test-browser-api-key",
		AuthMethod:        "browser_api_key",
		LocalAuthOverride: true,
	})

	server, err := NewServer("test", false, WithProfile(ProfileCore))
	if err != nil {
		t.Fatalf("NewServer() with local auth override: %v", err)
	}
	if got := server.apiClient.GetAPIKey(); got != "test-browser-api-key" {
		t.Fatalf("MCP API key = %q, want local override token", got)
	}
}

// TestNewServerRejectsExpiredAccessToken verifies non-dev profiles continue failing closed.
func TestNewServerRejectsExpiredAccessToken(t *testing.T) {
	profiles := []struct {
		name    string
		options []ServerOption
	}{
		{name: "legacy"},
		{name: "core", options: []ServerOption{WithProfile(ProfileCore)}},
		{name: "full", options: []ServerOption{WithProfile(ProfileFull)}},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			prepareServerAuthTest(t)
			expiresAt := time.Now().Add(-time.Hour)
			saveServerCredentials(t, &auth.Credentials{
				AccessToken: "expired-access-token",
				ExpiresAt:   &expiresAt,
				AuthMethod:  "browser",
			})

			if _, err := NewServer("test", false, profile.options...); err == nil {
				t.Fatal("NewServer() with expired access token succeeded, want authentication error")
			}
		})
	}
}

// TestNewServerRejectsMissingCredentials verifies non-dev profiles continue failing closed.
func TestNewServerRejectsMissingCredentials(t *testing.T) {
	profiles := []struct {
		name    string
		options []ServerOption
	}{
		{name: "legacy"},
		{name: "core", options: []ServerOption{WithProfile(ProfileCore)}},
		{name: "full", options: []ServerOption{WithProfile(ProfileFull)}},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			prepareServerAuthTest(t)
			if _, err := NewServer("test", false, profile.options...); err == nil {
				t.Fatal("NewServer() without credentials succeeded, want authentication error")
			}
		})
	}
}

// TestCoreProfileToolSchemasAreConcrete rejects untyped output properties that Cursor cannot load.
func TestCoreProfileToolSchemasAreConcrete(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	server, err := NewServer("test", false, WithProfile(ProfileCore))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}

	for _, tool := range listServerTools(t, server) {
		requireConcreteToolSchema(t, tool)
	}
}

// TestDevProfileToolListAndSchemas locks the focused Cursor development surface.
func TestDevProfileToolListAndSchemas(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}

	expected := map[string]bool{
		"setup_status":      false,
		"start_dev_loop":    false,
		"get_dev_status":    false,
		"rebuild":           false,
		"wait_for_rebuild":  false,
		"stop_dev_loop":     false,
		"device_session":    false,
		"screenshot":        false,
		"interact":          false,
		"device_navigate":   false,
		"device_validation": false,
	}
	tools := listServerTools(t, server)
	if len(tools) != len(expected) {
		t.Fatalf("dev profile tool count = %d, want %d", len(tools), len(expected))
	}
	encodedTools, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal dev profile tools: %v", err)
	}
	if len(encodedTools) > 30*1024 {
		t.Fatalf("dev profile schema size = %d bytes, budget is 30720", len(encodedTools))
	}
	for _, tool := range tools {
		if _, ok := expected[tool.Name]; !ok {
			t.Errorf("unexpected dev profile tool %q", tool.Name)
			continue
		}
		expected[tool.Name] = true
		requireConcreteToolSchema(t, tool)
		switch tool.Name {
		case "setup_status":
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("setup_status must remain read-only")
			}
			requireRemediationSchema(t, tool)
		case "start_dev_loop", "get_dev_status", "rebuild", "wait_for_rebuild", "stop_dev_loop":
			requireRemediationSchema(t, tool)
		}
		if tool.Name == "interact" {
			schemaJSON, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal interact input schema: %v", err)
			}
			schemaText := string(schemaJSON)
			for _, forbidden := range []string{"screen_token", `"x"`, `"y"`, "start_x", "end_x", "coordinates"} {
				if strings.Contains(schemaText, forbidden) {
					t.Errorf("interact schema exposes forbidden field %q", forbidden)
				}
			}
		}
		if tool.Name == "rebuild" {
			schemaJSON, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal rebuild input schema: %v", err)
			}
			schemaText := string(schemaJSON)
			for _, forbidden := range []string{"timeout", "validation", "screenshot", "screen_token", "image_path"} {
				if strings.Contains(schemaText, forbidden) {
					t.Errorf("rebuild schema exposes forbidden field %q", forbidden)
				}
			}
			outputProperties := toolOutputProperties(t, tool)
			requireSchemaProperties(t, outputProperties, "success", "outcome", "handle")
			requireMissingSchemaProperties(t, outputProperties, "rebuild", "screenshot", "validation")
			requireSchemaPropertyType(t, tool.OutputSchema, "process_started_at_nano", "integer")
			requireSchemaPropertyType(t, tool.OutputSchema, "process_generation", "string")
		}
		if tool.Name == "wait_for_rebuild" {
			outputProperties := toolOutputProperties(t, tool)
			requireSchemaProperties(t, outputProperties, "success", "outcome", "rebuild")
			requireMissingSchemaProperties(t, outputProperties, "handle", "screenshot", "validation")
			requireSchemaPropertyType(t, tool.InputSchema, "process_started_at_nano", "integer")
			requireSchemaPropertyType(t, tool.InputSchema, "process_generation", "string")
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing dev profile tool %q", name)
		}
	}
}

// TestDevProfileInstructionsEnforceStartBoundary locks the one-remediation retry contract.
func TestDevProfileInstructionsEnforceStartBoundary(t *testing.T) {
	instructions := instructionsForProfile(ProfileDev, "fallback")
	required := []string{
		"Call start_dev_loop first",
		"follow its one remediation action",
		"retry start_dev_loop once",
		"setup_status is optional diagnostics",
		"restart_required is true",
		"one retry fails",
	}
	for _, fragment := range required {
		if !strings.Contains(instructions, fragment) {
			t.Errorf("dev instructions missing %q", fragment)
		}
	}
	if got := instructionsForProfile(ProfileCore, "fallback"); got != "fallback" {
		t.Fatalf("core instructions = %q, want fallback", got)
	}
}

func TestDevLoopOutputSchemasPreserveProfileCompatibility(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")

	tests := []struct {
		name    string
		options []ServerOption
		nested  bool
	}{
		{name: "legacy"},
		{name: "core", options: []ServerOption{WithProfile(ProfileCore)}},
		{name: "full", options: []ServerOption{WithProfile(ProfileFull)}},
		{name: "dev", options: []ServerOption{WithProfile(ProfileDev)}, nested: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, err := NewServer("test", false, test.options...)
			if err != nil {
				t.Fatalf("NewServer(): %v", err)
			}
			tools := listServerTools(t, server)
			startProperties := toolOutputProperties(t, serverToolByName(t, tools, "start_dev_loop"))
			stopProperties := toolOutputProperties(t, serverToolByName(t, tools, "stop_dev_loop"))
			if test.nested {
				requireSchemaProperties(t, startProperties, "outcome", "result")
				requireSchemaProperties(t, stopProperties, "outcome", "result")
				requireMissingSchemaProperties(t, stopProperties, "message")
				return
			}
			requireSchemaProperties(t, startProperties, "session_index", "viewer_url", "preflight")
			requireMissingSchemaProperties(t, startProperties, "outcome", "result")
			requireSchemaProperties(t, stopProperties, "message")
			requireMissingSchemaProperties(t, stopProperties, "outcome", "result")
		})
	}
}

func TestScreenshotAppResourceAndToolMetadata(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "test-environment-api-key")
	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}

	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "app-test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	resource, err := clientSession.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: screenshotAppURI})
	if err != nil {
		t.Fatalf("ReadResource(): %v", err)
	}
	if len(resource.Contents) != 1 ||
		resource.Contents[0].MIMEType != screenshotAppMIMEType ||
		!strings.Contains(resource.Contents[0].Text, "ui/notifications/tool-result") ||
		!strings.Contains(resource.Contents[0].Text, `method:"ui/open-link"`) ||
		!strings.Contains(resource.Contents[0].Text, "Open live device") ||
		!strings.Contains(resource.Contents[0].Text, "structuredContent") {
		t.Fatalf("unexpected screenshot app resource: %+v", resource.Contents)
	}

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools(): %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "interact" {
			continue
		}
		uiMeta, _ := tool.Meta["ui"].(map[string]any)
		if uiMeta["resourceUri"] != screenshotAppURI {
			t.Fatalf("interact resourceUri = %v", uiMeta["resourceUri"])
		}
		return
	}
	t.Fatal("interact tool not found")
}

// prepareServerAuthTest isolates credential and project paths for one MCP startup test.
func prepareServerAuthTest(t *testing.T) {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	// Windows credential and temp resolution use USERPROFILE, not HOME.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", homeDir)
		t.Setenv("APPDATA", filepath.Join(homeDir, "AppData", "Roaming"))
		t.Setenv("LOCALAPPDATA", filepath.Join(homeDir, "AppData", "Local"))
	}
	// Keep the project under the isolated home so FindRepoRoot cannot walk into a
	// real ~/.revyl credential store when TEMP lives under the user profile.
	projectDir := filepath.Join(homeDir, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatalf("create isolated project directory: %v", err)
	}
	t.Setenv("REVYL_PROJECT_DIR", projectDir)
	t.Setenv("REVYL_API_KEY", "")
	t.Setenv("CURSOR_AGENT", "")
}

// saveServerCredentials persists a structured credential fixture in the isolated test home.
func saveServerCredentials(t *testing.T, credentials *auth.Credentials) {
	t.Helper()
	if err := auth.NewManager().SaveCredentials(credentials); err != nil {
		t.Fatalf("SaveCredentials(): %v", err)
	}
}

// requireServerTool verifies a fully initialized MCP server advertises a normal Revyl tool.
func requireServerTool(t *testing.T, server *Server, toolName string) {
	t.Helper()
	for _, tool := range listServerTools(t, server) {
		if tool.Name == toolName {
			return
		}
	}
	t.Fatalf("MCP tool %q was not registered", toolName)
}

// listServerTools returns the tools advertised by an initialized server.
func listServerTools(t *testing.T, server *Server) []*mcpsdk.Tool {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	serverSession, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "auth-test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools(): %v", err)
	}
	return result.Tools
}

// serverToolByName returns one advertised tool by exact name.
func serverToolByName(t *testing.T, tools []*mcpsdk.Tool, name string) *mcpsdk.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

// toolOutputProperties returns the top-level property schemas for one tool.
func toolOutputProperties(t *testing.T, tool *mcpsdk.Tool) map[string]json.RawMessage {
	t.Helper()
	content, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		t.Fatalf("marshal %s output schema: %v", tool.Name, err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decode %s output schema: %v", tool.Name, err)
	}
	return schema.Properties
}

// requireSchemaProperties verifies required compatibility keys are advertised.
func requireSchemaProperties(t *testing.T, properties map[string]json.RawMessage, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := properties[name]; !ok {
			t.Errorf("output schema missing property %q", name)
		}
	}
}

// requireSchemaPropertyType verifies every named property has the expected concrete type.
func requireSchemaPropertyType(t *testing.T, schemaValue any, propertyName, expectedType string) {
	t.Helper()
	content, err := json.Marshal(schemaValue)
	if err != nil {
		t.Fatalf("marshal schema containing %s: %v", propertyName, err)
	}
	var schema any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decode schema containing %s: %v", propertyName, err)
	}

	found := false
	var inspect func(any)
	inspect = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if properties, ok := typed["properties"].(map[string]any); ok {
				if property, ok := properties[propertyName].(map[string]any); ok {
					found = true
					if property["type"] != expectedType {
						t.Errorf(
							"schema property %q type = %v, want %q",
							propertyName,
							property["type"],
							expectedType,
						)
					}
				}
			}
			for _, nested := range typed {
				inspect(nested)
			}
		case []any:
			for _, nested := range typed {
				inspect(nested)
			}
		}
	}
	inspect(schema)
	if !found {
		t.Errorf("schema missing property %q", propertyName)
	}
}

// requireMissingSchemaProperties verifies incompatible profile keys are absent.
func requireMissingSchemaProperties(t *testing.T, properties map[string]json.RawMessage, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, ok := properties[name]; ok {
			t.Errorf("output schema unexpectedly contains property %q", name)
		}
	}
}

// requireConcreteToolSchema verifies Cursor can validate one tool's output schema.
func requireConcreteToolSchema(t *testing.T, tool *mcpsdk.Tool) {
	t.Helper()
	content, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		t.Fatalf("marshal %s output schema: %v", tool.Name, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatalf("decode %s output schema: %v", tool.Name, err)
	}
	if path := firstUntypedProperty(schema, "outputSchema"); path != "" {
		t.Errorf("tool %s has untyped property at %s", tool.Name, path)
	}
}

// requireRemediationSchema verifies one tool publishes the complete shared remediation contract.
//
// Parameters:
//   - t: Active test.
//   - tool: Tool whose concrete output schema is inspected.
func requireRemediationSchema(t *testing.T, tool *mcpsdk.Tool) {
	t.Helper()
	content, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		t.Fatalf("marshal %s remediation schema: %v", tool.Name, err)
	}
	schemaText := string(content)
	for _, field := range []string{
		`"action_kind"`,
		`"command"`,
		`"env_name"`,
		`"working_directory"`,
		`"candidate_roots"`,
		`"config_path"`,
		`"restart_required"`,
	} {
		if !strings.Contains(schemaText, field) {
			t.Errorf("tool %s remediation schema missing %s", tool.Name, field)
		}
	}
	if strings.Contains(schemaText, `"project_dir_guidance"`) {
		t.Errorf("tool %s exposes deprecated project_dir_guidance", tool.Name)
	}
}

// firstUntypedProperty returns the first property path whose schema is empty.
func firstUntypedProperty(schema map[string]any, path string) string {
	properties, _ := schema["properties"].(map[string]any)
	for name, rawProperty := range properties {
		propertyPath := fmt.Sprintf("%s.properties.%s", path, name)
		if allowed, ok := rawProperty.(bool); ok {
			if allowed {
				return propertyPath
			}
			continue
		}
		property, ok := rawProperty.(map[string]any)
		if !ok {
			continue
		}
		if len(property) == 0 {
			return propertyPath
		}
		if _, hasType := property["type"]; !hasType {
			_, hasRef := property["$ref"]
			_, hasAnyOf := property["anyOf"]
			_, hasOneOf := property["oneOf"]
			_, hasAllOf := property["allOf"]
			if !hasRef && !hasAnyOf && !hasOneOf && !hasAllOf {
				return propertyPath
			}
		}
		if nested := firstUntypedProperty(property, propertyPath); nested != "" {
			return nested
		}
	}
	return ""
}

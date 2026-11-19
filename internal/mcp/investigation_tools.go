package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const investigationOutputLimitBytes = 60000

var investigationToolNamePattern = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)

// InvestigateInput selects one catalog operation, mirroring `revyl tools`.
type InvestigateInput struct {
	Action    string         `json:"action" jsonschema:"search, describe, cookbook or call"`
	Query     string         `json:"query,omitempty" jsonschema:"Words to search tool names and descriptions (search)"`
	Name      string         `json:"name,omitempty" jsonschema:"Tool name such as atlas.graph (describe, call) or cookbook name (cookbook)"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"Tool arguments matching the describe schema (call)"`
}

// InvestigateOutput carries the catalog's JSON result; list results from
// search and cookbook arrive under "tools" and "cookbooks".
type InvestigateOutput struct {
	Result map[string]any `json:"result"`
}

type investigationRunner func(ctx context.Context, args []string, stdin []byte) ([]byte, error)

func (s *Server) registerInvestigateTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "investigate",
		Description: "Read Revyl evidence through the same catalog as `revyl tools` and Atlas Chat: Atlas maps (atlas.graph, atlas.filters, atlas.screen, atlas.search, atlas.edge and more), reports, sessions, builds, tests and annotations, including each test's newest run and history on a Git branch or commit (tests.status, tests.history, sessions.list). " +
			"Start with action=search or action=cookbook, read a tool's schema with action=describe, then action=call with name and arguments. " +
			"Atlas graph reads accept the map's filters: build, branch_id, range, device_model, runtime, report_id, test_id, workflow_execution_id and launch (KEY=VALUE list, or KEY for runs launched with it set; secret keys filter only by whether they were set; values travel on stdin, never in argv or URLs). " +
			"atlas.brief returns the common build, branch, device and OS values and launch keys inline (atlas.graph only with with_filters=true); atlas.diff compares two filter scopes or lists screens unique to each value of one filter in a single call. A search matching at most 6 tools includes their input schemas. " +
			"Tools marked read_only=false change remote state; call them only when the user asked for that change.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Investigate",
			OpenWorldHint: boolPtr(true),
		},
	}, s.handleInvestigate)
}

func (s *Server) handleInvestigate(ctx context.Context, _ *mcp.CallToolRequest, input InvestigateInput) (*mcp.CallToolResult, InvestigateOutput, error) {
	var args []string
	var stdin []byte
	switch input.Action {
	case "search":
		args = []string{"tools", "search"}
		if query := strings.TrimSpace(input.Query); query != "" {
			args = append(args, query)
		}
	case "describe", "call":
		if !investigationToolNamePattern.MatchString(input.Name) {
			return nil, InvestigateOutput{}, fmt.Errorf("name must be a catalog tool such as atlas.graph; use action=search")
		}
		args = []string{"tools", input.Action, input.Name}
		if input.Action == "call" {
			arguments := input.Arguments
			if arguments == nil {
				arguments = map[string]any{}
			}
			encoded, err := json.Marshal(arguments)
			if err != nil {
				return nil, InvestigateOutput{}, fmt.Errorf("encode arguments: %w", err)
			}
			args = append(args, "-", fmt.Sprintf("--max-output-bytes=%d", investigationOutputLimitBytes))
			stdin = encoded
		}
	case "cookbook":
		args = []string{"tools", "cookbook"}
		if input.Name != "" {
			args = append(args, input.Name)
		}
	default:
		return nil, InvestigateOutput{}, fmt.Errorf("action must be search, describe, cookbook or call")
	}
	if s.devMode {
		args = append([]string{"--dev"}, args...)
	}
	run := s.runInvestigation
	if run == nil {
		run = runInvestigationCommand
	}
	output, runErr := run(ctx, args, stdin)
	var decoded any
	if err := json.Unmarshal(output, &decoded); err != nil {
		if runErr != nil {
			return nil, InvestigateOutput{}, fmt.Errorf("investigation %s failed; use action=describe to check arguments", input.Action)
		}
		return nil, InvestigateOutput{}, fmt.Errorf("investigation %s returned invalid JSON", input.Action)
	}
	result, isObject := decoded.(map[string]any)
	if list, isList := decoded.([]any); isList {
		key := "tools"
		if input.Action == "cookbook" {
			key = "cookbooks"
		}
		result, isObject = map[string]any{key: list}, true
	}
	if !isObject {
		return nil, InvestigateOutput{}, fmt.Errorf("investigation %s returned an unexpected result", input.Action)
	}
	if runErr != nil {
		return &mcp.CallToolResult{IsError: true}, InvestigateOutput{Result: result}, nil
	}
	return nil, InvestigateOutput{Result: result}, nil
}

func runInvestigationCommand(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...) // #nosec G204 -- Runs this same CLI binary without a shell; call arguments go on stdin.
	command.Stdin = bytes.NewReader(stdin)
	command.WaitDelay = 2 * time.Second
	var stdout bytes.Buffer
	command.Stdout = &stdout
	err = command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("investigation exceeded 90 seconds")
	}
	return stdout.Bytes(), err
}

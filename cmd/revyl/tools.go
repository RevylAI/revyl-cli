package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/toolcatalog"
	"github.com/spf13/cobra"
)

const toolOutputLimitBytes = 120000

func newToolsCommand() *cobra.Command {
	command := &cobra.Command{Use: "tools", Short: "Discover and call composable investigation tools", Long: "Discover tools, inspect their schemas, then call with a JSON object. Use '-' to read private arguments from stdin. Results preserve the underlying CLI JSON contract. No shell code is accepted."}
	command.AddCommand(&cobra.Command{Use: "search [words]", Short: "Find tools by intent (JSON)", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		catalog, err := toolcatalog.Build(cmd.Root(), investigationToolSpecs())
		if err != nil {
			return err
		}
		words := []string{}
		if len(args) > 0 {
			words = strings.Fields(strings.ToLower(args[0]))
		}
		type summary struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ReadOnly    bool   `json:"read_only"`
		}
		matches := []summary{}
		for _, tool := range catalog {
			match := true
			for _, word := range words {
				if !strings.Contains(strings.ToLower(tool.Name+" "+tool.Description), word) {
					match = false
				}
			}
			if match {
				matches = append(matches, summary{tool.Name, tool.Description, tool.ReadOnly})
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(matches)
	}})
	command.AddCommand(&cobra.Command{Use: "describe <tool>", Short: "Read a tool's input schema and command mapping", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		tool, err := findInvestigationTool(cmd, args[0])
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(tool)
	}})
	call := &cobra.Command{Use: "call <tool> <json|->", Short: "Execute a validated tool call; '-' reads JSON from stdin", Args: cobra.ExactArgs(2), RunE: runInvestigationTool}
	call.Flags().Bool("read-only", false, "Reject tools that change remote state")
	call.Flags().Bool("view", false, "Return data with a replayable native view recipe")
	command.AddCommand(call)
	command.AddCommand(&cobra.Command{Use: "cookbook [name]", Short: "Read reusable investigation workflows and evidence boundaries", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(toolcatalog.Cookbooks())
		}
		for _, cookbook := range toolcatalog.Cookbooks() {
			if cookbook.Name == args[0] {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(cookbook)
			}
		}
		return fmt.Errorf("unknown cookbook; run revyl tools cookbook")
	}})
	command.AddCommand(&cobra.Command{Use: "hydrate <view-json|->", Short: "Re-run a read-only native view recipe using current authorization", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := readToolInput(cmd, args[0])
		if err != nil {
			return err
		}
		var view toolcatalog.View
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&view); err != nil {
			return fmt.Errorf("invalid native view recipe")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return fmt.Errorf("expected one native view recipe")
		}
		tool, err := findInvestigationTool(cmd, view.Query.Tool)
		if err != nil {
			return err
		}
		if err = tool.ValidateView(view); err != nil {
			return err
		}
		return executeInvestigationTool(cmd, tool, view.Query.Arguments, &view)
	}})
	for _, child := range command.Commands() {
		run := child.RunE
		child.RunE = func(cmd *cobra.Command, args []string) error {
			return analytics.WithSafeDiagnostic(run(cmd, args), "investigation command failed")
		}
	}
	return command
}

func findInvestigationTool(cmd *cobra.Command, name string) (toolcatalog.Definition, error) {
	catalog, err := toolcatalog.Build(cmd.Root(), investigationToolSpecs())
	if err != nil {
		return toolcatalog.Definition{}, err
	}
	for _, tool := range catalog {
		if tool.Name == name {
			return tool, nil
		}
	}
	return toolcatalog.Definition{}, fmt.Errorf("unknown tool; use revyl tools search to discover available tools")
}

func runInvestigationTool(cmd *cobra.Command, args []string) error {
	tool, err := findInvestigationTool(cmd, args[0])
	if err != nil {
		return err
	}
	readOnly, _ := cmd.Flags().GetBool("read-only")
	if readOnly && !tool.ReadOnly {
		return fmt.Errorf("this tool writes data and is unavailable in read-only mode")
	}
	raw, err := readToolInput(cmd, args[1])
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var input map[string]any
	if err = decoder.Decode(&input); err != nil || input == nil {
		return fmt.Errorf("tool arguments must be a JSON object")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("tool arguments must contain exactly one JSON object")
	}
	var view *toolcatalog.View
	withView, _ := cmd.Flags().GetBool("view")
	if withView {
		recipe, err := tool.View(input)
		if err != nil {
			return err
		}
		view = &recipe
	}
	return executeInvestigationTool(cmd, tool, input, view)
}

func readToolInput(cmd *cobra.Command, value string) ([]byte, error) {
	var reader io.Reader = strings.NewReader(value)
	if value == "-" {
		reader = cmd.InOrStdin()
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 16001))
	if err != nil {
		return nil, fmt.Errorf("read tool arguments: %w", err)
	}
	if len(raw) > 16000 {
		return nil, fmt.Errorf("tool arguments exceed 16000 bytes")
	}
	return raw, nil
}

func executeInvestigationTool(cmd *cobra.Command, tool toolcatalog.Definition, input map[string]any, view *toolcatalog.View) error {
	argv, err := tool.Arguments(input)
	if err != nil {
		return analytics.WithSafeDiagnostic(err, "invalid investigation tool arguments")
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	if devMode {
		argv = append([]string{"--dev"}, argv...)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, argv...)
	if body, ok := input["body"].(string); ok {
		for index, arg := range child.Args {
			if strings.HasPrefix(arg, "--body=") {
				child.Args[index] = "--body-file=-"
			}
		}
		child.Stdin = strings.NewReader(body)
	}
	child.WaitDelay = 2 * time.Second
	child.Env = append(os.Environ(), "REVYL_TELEMETRY_DISABLED=1")
	stdout := &boundedToolOutput{limit: toolOutputLimitBytes, cancel: cancel}
	stderr := &boundedToolOutput{limit: 16000, cancel: cancel}
	child.Stdout, child.Stderr = stdout, stderr
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "investigation." + tool.Name, DomainStatus: "attempted"})
	if err = child.Run(); err != nil {
		if !tool.ReadOnly {
			return fmt.Errorf("tool %s did not return a confirmed result; the write may have completed. Inspect the thread or run history before retrying; preserve client_request_id when supported", tool.Name)
		}
		if stdout.exceeded || stderr.exceeded {
			return fmt.Errorf("tool output exceeded its budget; narrow the query or filter diagnostics")
		}
		if ctx.Err() != nil {
			return fmt.Errorf("tool cancelled or exceeded 60 seconds; narrow the query")
		}
		return fmt.Errorf("tool %s: %s", tool.Name, investigationFailureHint(stderr.String()))
	}
	if !json.Valid(stdout.Bytes()) {
		return fmt.Errorf("tool returned invalid JSON; no result was accepted")
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "investigation." + tool.Name, DomainStatus: "completed"})
	if view != nil {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			View *toolcatalog.View `json:"view"`
			Data json.RawMessage   `json:"data"`
		}{view, stdout.Bytes()})
	}
	_, err = cmd.OutOrStdout().Write(stdout.Bytes())
	return err
}

func investigationFailureHint(stderr string) string {
	message := strings.ToLower(stderr)
	switch {
	case strings.Contains(message, "context deadline"), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return "query timed out; scope to a build, report, or smaller time window before retrying"
	case strings.Contains(message, "500"), strings.Contains(message, "502"), strings.Contains(message, "503"), strings.Contains(message, "504"):
		return "backend read failed; try a smaller scope or another evidence source"
	case strings.Contains(message, "no such host"), strings.Contains(message, "connection refused"), strings.Contains(message, "dial tcp"), strings.Contains(message, "tls handshake timeout"):
		return "backend unavailable; check the configured backend connection before retrying"
	case strings.Contains(message, "not authenticated"), strings.Contains(message, "auth login"), strings.Contains(message, "401"):
		return "authentication unavailable; authenticate the CLI for the selected environment"
	case strings.Contains(message, "403"), strings.Contains(message, "404"), strings.Contains(message, "not found"):
		return "resource unavailable; verify the exact identifier and organization"
	case strings.Contains(message, "no device logs"), strings.Contains(message, "not yet available"), strings.Contains(message, "no hardware metrics"), strings.Contains(message, "no network"), strings.Contains(message, "no device state"):
		return "this diagnostic was not captured or is not available yet; inspect report capture availability"
	default:
		return "the underlying read failed; narrow the query or inspect the command shown by tools describe"
	}
}

type boundedToolOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedToolOutput) Len() int       { return b.buffer.Len() }
func (b *boundedToolOutput) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedToolOutput) String() string { return b.buffer.String() }

func (b *boundedToolOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		b.cancel()
		return 0, fmt.Errorf("tool output budget exceeded")
	}
	return b.buffer.Write(p)
}

package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

var persistentComputerSaveWait = 2 * time.Minute

const persistentComputerListLimit = 25

var persistentComputerActions = []string{"create", "list", "get", "start", "stop", "delete", "snapshot_save", "snapshot_list", "snapshot_get", "snapshot_delete"}

type PersistentComputerParams struct {
	ComputerID       string `json:"computer_id,omitempty"`
	FromSnapshot     string `json:"from_snapshot,omitempty"`
	TimeoutSeconds   *int   `json:"timeout_seconds,omitempty"`
	WithoutInference bool   `json:"without_inference,omitempty"`
	Wait             bool   `json:"wait,omitempty"`
	Confirm          bool   `json:"confirm,omitempty"`
	Name             string `json:"name,omitempty"`
	Description      string `json:"description,omitempty"`
	Snapshot         string `json:"snapshot,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	Offset           int    `json:"offset,omitempty"`
}

type PersistentComputerOutput struct {
	Computer  *api.PersistentComputerStatus  `json:"computer,omitempty"`
	Computers []api.PersistentComputerStatus `json:"computers,omitempty"`
	Snapshot  *api.ComputerSnapshot          `json:"snapshot,omitempty"`
	Snapshots []api.ComputerSnapshot         `json:"snapshots,omitempty"`
	Total     *int                           `json:"total,omitempty"`
	Deleted   bool                           `json:"deleted,omitempty"`
	Message   string                         `json:"message,omitempty"`
}

var persistentComputerRefusals = map[string]string{
	"persistent_computers_not_enabled": "Persistent computers aren't enabled for this organization; they are included in paid plans. Don't retry.",
	"computer_not_persistent":          "This computer isn't persistent. Create one with action create.",
	"saved_disk_unavailable":           "The Mac holding this computer's save is offline and the save isn't copied off it yet, or no Mac can start it right now. The disk stays saved; try start again later.",
	"saved_disk_expired":               "This computer's saved disk can no longer start: it ends within 24 hours, the longest a run lasts. Delete it with action delete, then create a new computer.",
	"computer_not_stopped":             "Only a stopped computer can be snapshotted. Stop it with action stop and wait true, then save the snapshot.",
	"saved_disk_not_durable":           "This computer's save is still only on its Mac. Wait until action get shows durable true, then save the snapshot.",
	"snapshot_name_taken":              "The organization already has a snapshot with this name. Pick another name, or delete the old snapshot first.",
	"snapshot_expired":                 "This snapshot's saved disk has ended, so it can't create a computer. Save a new snapshot of a recently stopped computer.",
	"persistent_computer_limit":        "The organization has as many persistent computers as it may keep. Delete one it no longer needs with action delete, then try again.",
	"snapshot_limit":                   "The organization has as many snapshots as it may keep. Delete one it no longer needs with action snapshot_delete, then save again.",
	"run_conflict":                     "This computer's last run hasn't finished stopping. The disk is kept; try start again in a few minutes, or check it with action get.",
	"inference_unavailable":            "No Mac with its own vision model is up, so this computer did not start. Retry later, or start it with without_inference true.",
	"inaccessible":                     "No persistent computer or snapshot with that ID or name is in this organization.",
}

func (s *Server) registerManagePersistentComputersTool() {
	addCompositeTool(s.mcpServer, &mcp.Tool{
		Name: "manage_persistent_computers",
		Description: `Manage persistent Mac computers: a computer whose disk is kept when it stops, so it starts again later under the same computer_id. Use the "action" param to select an operation.

Each start is a cold boot from the saved disk: files and installed apps survive, memory and running apps don't. A run lasts timeout_seconds (2 hours by default) and starts saving its disk 3 minutes before it ends. Stop the computer when your session ends. Saved disks bill as storage and snapshots are capped per organization, so offer to delete copies the user no longer needs. Delete actions are permanent and need confirm true; call them only when the user agrees.

Actions:
  create          - Create and boot a new persistent computer. Params: from_snapshot (snapshot ID or name to copy), timeout_seconds
  list            - List the organization's persistent computers, newest first. Params: limit (1-100, default 25), offset (skip that many to read the next page)
  get             - Show one computer: state (starting, running, stopping while it saves, stopped), run_session_id of its current run, and durable once the save is off its Mac. Params: computer_id (required)
  start           - Start a stopped computer as a new run. Params: computer_id (required), timeout_seconds, without_inference
  stop            - Stop the computer and save its disk. Params: computer_id (required), wait (wait up to 2 minutes for the save)
  delete          - Delete the computer and its saved disk for good; its snapshots stay. Params: computer_id (required), confirm (required, true)
  snapshot_save   - Name a stopped, durable computer's save as a snapshot that create can copy. Anyone in the organization can use it. Params: computer_id (required), name (required), description
  snapshot_list   - List the organization's snapshots, newest first. Params: limit (1-100, default 25), offset
  snapshot_get    - Show one snapshot. Params: snapshot (ID or name, required)
  snapshot_delete - Delete a snapshot for good; computers created from it keep their disks. Params: snapshot (required), confirm (required, true)`,
		Annotations: &mcp.ToolAnnotations{
			Title:           "Manage Persistent Computers",
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
		},
	}, s.handleManagePersistentComputers)
}

func (s *Server) handleManagePersistentComputers(ctx context.Context, req *mcp.CallToolRequest, input CompositeInput) (*mcp.CallToolResult, CompositeOutput, error) {
	return dispatchComposite(ctx, req, input, func(ctx context.Context, _ *mcp.CallToolRequest, params PersistentComputerParams) (*mcp.CallToolResult, PersistentComputerOutput, error) {
		output, err := s.runPersistentComputerAction(ctx, input.Action, params)
		var refusal *api.APIError
		if errors.As(err, &refusal) {
			if message, ok := persistentComputerRefusals[refusal.Code]; ok {
				err = errors.New(message)
			}
		}
		return nil, output, err
	})
}

func (s *Server) runPersistentComputerAction(ctx context.Context, action string, params PersistentComputerParams) (PersistentComputerOutput, error) {
	if !slices.Contains(persistentComputerActions, action) {
		return PersistentComputerOutput{}, fmt.Errorf("unknown action %q for manage_persistent_computers; valid: %s", action, strings.Join(persistentComputerActions, ", "))
	}
	properties := map[string]interface{}{"domain": "persistent_computer", "domain_status": action}
	defer func() { analytics.SetMCPToolCallProperties(ctx, properties) }()
	switch action {
	case "create":
		if params.FromSnapshot != "" && !api.ValidComputerSnapshotReference(params.FromSnapshot) {
			return PersistentComputerOutput{}, errors.New("from_snapshot must be a snapshot ID or name")
		}
		id := uuid.New()
		persistent := true
		request := api.ComputerRunCreate{ComputerSessionId: id, Os: "macos", Persistent: &persistent, TimeoutSeconds: params.TimeoutSeconds}
		if params.FromSnapshot != "" {
			request.FromSnapshot = &params.FromSnapshot
		}
		properties["entity_id"] = id.String()
		if _, err := s.apiClient.CreateComputerRun(ctx, request); err != nil {
			return PersistentComputerOutput{}, err
		}
		detail, err := s.apiClient.GetComputerRun(ctx, id)
		if err != nil {
			return PersistentComputerOutput{}, err
		}
		return PersistentComputerOutput{Computer: detail.Persistent, Message: "The computer is starting. Stop it with action stop when your session ends."}, nil
	case "list", "snapshot_list":
		limit := params.Limit
		if limit == 0 {
			limit = persistentComputerListLimit
		}
		if action == "list" {
			list, err := s.apiClient.ListPersistentComputers(ctx, limit, params.Offset)
			if err != nil {
				return PersistentComputerOutput{}, err
			}
			return PersistentComputerOutput{Computers: list.Computers, Total: &list.Total}, nil
		}
		list, err := s.apiClient.ListComputerSnapshots(ctx, limit, params.Offset)
		if err != nil {
			return PersistentComputerOutput{}, err
		}
		return PersistentComputerOutput{Snapshots: list.Snapshots, Total: &list.Total}, nil
	case "snapshot_get", "snapshot_delete":
		reference := strings.TrimSpace(params.Snapshot)
		if reference == "" {
			return PersistentComputerOutput{}, errors.New("snapshot is required: a snapshot ID or name")
		}
		if action == "snapshot_get" {
			snapshot, err := s.apiClient.GetComputerSnapshot(ctx, reference)
			return PersistentComputerOutput{Snapshot: snapshot}, err
		}
		if !params.Confirm {
			return PersistentComputerOutput{}, errors.New("snapshot_delete deletes the snapshot for good; pass confirm true once the user agrees")
		}
		if err := s.apiClient.DeleteComputerSnapshot(ctx, reference); err != nil {
			return PersistentComputerOutput{}, err
		}
		return PersistentComputerOutput{Deleted: true}, nil
	}

	id, err := uuid.Parse(strings.TrimSpace(params.ComputerID))
	if err != nil || id == uuid.Nil {
		return PersistentComputerOutput{}, errors.New("computer_id is required: the persistent computer's UUID")
	}
	properties["entity_id"] = id.String()
	switch action {
	case "get":
		detail, err := s.apiClient.GetComputerRun(ctx, id)
		if err != nil {
			return PersistentComputerOutput{}, err
		}
		if detail.Persistent == nil {
			return PersistentComputerOutput{}, errors.New(persistentComputerRefusals["computer_not_persistent"])
		}
		return PersistentComputerOutput{Computer: detail.Persistent}, nil
	case "start":
		status, err := s.apiClient.StartComputer(ctx, id, api.PersistentComputerStartRequest{TimeoutSeconds: params.TimeoutSeconds, WithoutInference: params.WithoutInference})
		if err != nil {
			return PersistentComputerOutput{}, err
		}
		return PersistentComputerOutput{Computer: status, Message: "The computer is cold booting from its saved disk as run run_session_id; keep using computer_id to stop, snapshot or delete it."}, nil
	case "stop":
		_, status, err := s.apiClient.StopComputerRun(ctx, id)
		if err != nil {
			return PersistentComputerOutput{}, err
		}
		if status == nil {
			return PersistentComputerOutput{Message: "The run stopped. It isn't a persistent computer, so its disk wasn't kept."}, nil
		}
		if params.Wait && status.State == api.PersistentComputerStatusStateStopping {
			waitCtx, cancel := context.WithTimeout(ctx, persistentComputerSaveWait)
			defer cancel()
			if status, err = s.apiClient.AwaitPersistentComputer(waitCtx, status, api.PersistentComputerStatusStateStopping); err != nil {
				return PersistentComputerOutput{}, err
			}
		}
		if status.State == api.PersistentComputerStatusStateStopping {
			return PersistentComputerOutput{Computer: status, Message: "Its Mac is still saving the disk. Check with action get until state is stopped."}, nil
		}
		return PersistentComputerOutput{Computer: status}, nil
	case "delete":
		if !params.Confirm {
			return PersistentComputerOutput{}, errors.New("delete removes the computer and its saved disk for good; pass confirm true once the user agrees")
		}
		if err := s.apiClient.DeleteComputer(ctx, id); err != nil {
			return PersistentComputerOutput{}, err
		}
		return PersistentComputerOutput{Deleted: true}, nil
	default:
		if strings.TrimSpace(params.Name) == "" {
			return PersistentComputerOutput{}, errors.New("name is required: 1-64 letters, digits, '.', '_' or '-'")
		}
		snapshot, err := s.apiClient.SaveComputerSnapshot(ctx, id, strings.TrimSpace(params.Name), params.Description)
		return PersistentComputerOutput{Snapshot: snapshot}, err
	}
}

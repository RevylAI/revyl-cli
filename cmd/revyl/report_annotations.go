// Package main: `revyl report annotations` — run-native annotation verbs.
//
// Mirrors the `revyl atlas annotations` family but addresses the caller's
// current device session instead of Atlas observation ids: create posts to
// the session-scoped grounded-annotation endpoint, while list/get/reply and
// the status verbs reuse the existing Atlas annotation endpoints after
// resolving the session's app.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

type reportAnnotationCreateOptions struct {
	annotationBodyOptions
	target            string
	severity          string
	actionID          string
	role              string
	clientRequestID   string
	previewOut        string
	previewReceiptOut string
	previewReceipt    string
	dryRun            bool
	mentions          []string
}

type reportAnnotationPreviewReceiptFile struct {
	Version        int    `json:"version"`
	SessionID      string `json:"session_id"`
	Target         string `json:"target"`
	ActionID       string `json:"action_id,omitempty"`
	ScreenshotRole string `json:"screenshot_role"`
	PreviewReceipt string `json:"preview_receipt"`
}

func readReportAnnotationBody(
	command *cobra.Command,
	args []string,
	options annotationBodyOptions,
) (string, error) {
	if len(args) == 1 {
		if command.Flags().Changed("body") || command.Flags().Changed("body-file") {
			return "", fmt.Errorf("positional body cannot be combined with --body or --body-file")
		}
		body := strings.TrimSpace(args[0])
		if body == "" {
			return "", fmt.Errorf("annotation body cannot be empty")
		}
		return body, nil
	}
	return readAnnotationBody(command, options)
}

func writeReportAnnotationPreviewReceipt(
	path string,
	receipt reportAnnotationPreviewReceiptFile,
) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".revyl-annotation-preview-receipt-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func readReportAnnotationPreviewReceipt(
	path string,
) (reportAnnotationPreviewReceiptFile, error) {
	// #nosec G304 -- this local CLI path is explicitly selected by the caller;
	// reads are size-bounded and parsed as the receipt's closed JSON schema.
	file, err := os.Open(path)
	if err != nil {
		return reportAnnotationPreviewReceiptFile{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return reportAnnotationPreviewReceiptFile{}, err
	}
	if len(data) > 64<<10 {
		return reportAnnotationPreviewReceiptFile{}, fmt.Errorf("preview receipt exceeds 64 KiB")
	}
	var receipt reportAnnotationPreviewReceiptFile
	if err := json.Unmarshal(data, &receipt); err != nil {
		return reportAnnotationPreviewReceiptFile{}, fmt.Errorf("read preview receipt: %w", err)
	}
	if receipt.Version != 1 || strings.TrimSpace(receipt.PreviewReceipt) == "" {
		return reportAnnotationPreviewReceiptFile{}, fmt.Errorf("unsupported or invalid preview receipt")
	}
	return receipt, nil
}

func newReportCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "report",
		Short: "Run-native commands addressed at the current device session",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newReportAnnotationsCommand())
	return command
}

func newReportAnnotationsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "annotations",
		Short: "Annotate the current device session's screenshot evidence",
		Long: `Annotate the current device session's screenshot evidence.

Unlike 'revyl atlas annotations' (observation-addressed), these verbs resolve
against the caller's active dev/proof session: create grounds a visual target
on the session's latest screenshot, and the remaining verbs resolve the
session's app automatically. Pass --session-id to target a session directly,
or --app to skip the session-to-app lookup.`,
		Args: cobra.NoArgs,
	}
	command.PersistentFlags().String("session-id", "", "Device session id (default: the active session)")
	command.PersistentFlags().Bool("json", false, "Output results as JSON")
	command.AddCommand(
		newReportAnnotationsCreateCommand(),
		newReportAnnotationsListCommand(),
		newReportAnnotationsGetCommand(),
		newReportAnnotationsReplyCommand(),
		newReportAnnotationsStatusCommand("resolve"),
		newReportAnnotationsStatusCommand("dismiss"),
		newReportAnnotationsStatusCommand("reopen"),
		newReportAnnotationsSeverityCommand(),
	)
	return command
}

func reportAnnotationsJSONOutput(cmd *cobra.Command) bool {
	if value, err := cmd.Flags().GetBool("json"); err == nil && value {
		return true
	}
	globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
	return globalJSON
}

func printReportAnnotationResult(cmd *cobra.Command, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}

func reportAnnotationMutationError(cmd *cobra.Command, requestID string, err error) error {
	if requestID != "" {
		cmd.PrintErrf("Retry the exact same payload with --client-request-id %s\n", requestID)
	}
	if reportAnnotationsJSONOutput(cmd) {
		_ = printReportAnnotationResult(cmd, map[string]interface{}{
			"error":             err.Error(),
			"client_request_id": requestID,
		})
	}
	return err
}

// resolveReportAnnotationSessionID returns --session-id when given, otherwise
// the active device session the same way `revyl device report` resolves it.
func resolveReportAnnotationSessionID(cmd *cobra.Command) (string, error) {
	direct, _ := cmd.Flags().GetString("session-id")
	if strings.TrimSpace(direct) != "" {
		return strings.TrimSpace(direct), nil
	}
	mgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return "", err
	}
	session, err := mgr.ResolveSession(-1)
	if err != nil {
		return "", fmt.Errorf("no active device session (pass --session-id to target one directly): %w", humanizeDeviceSessionResolveError(cmd, err))
	}
	return session.SessionID, nil
}

func resolveReportAnnotationScopeSessionID(cmd *cobra.Command, appInput string) (string, error) {
	if strings.TrimSpace(appInput) != "" {
		return "", nil
	}
	return resolveReportAnnotationSessionID(cmd)
}

// resolveReportAnnotationApp resolves the app the Atlas annotation endpoints
// require. --app wins; otherwise the session's report supplies the app name.
func resolveReportAnnotationApp(cmd *cobra.Command, client *api.Client, appInput, sessionID string) (*api.App, error) {
	if strings.TrimSpace(appInput) != "" {
		return resolveAtlasApp(cmd, client, strings.TrimSpace(appInput))
	}
	envelope, err := client.GetReportBySession(cmd.Context(), sessionID, false, false, false)
	if err != nil {
		return nil, fmt.Errorf("resolve app for session %s (pass --app to provide it directly): %w", sessionID, err)
	}
	if envelope.Report == nil || strings.TrimSpace(stringValue(envelope.Report.AppName)) == "" {
		return nil, fmt.Errorf("session %s has no app context yet; pass --app <name-or-id>", sessionID)
	}
	return resolveAtlasApp(cmd, client, strings.TrimSpace(*envelope.Report.AppName))
}

func resolveReportAnnotationRole(role string) (*api.AtlasSessionGroundedAnnotationThreadCreateRequestScreenshotRole, error) {
	value := strings.TrimSpace(role)
	switch value {
	case "before", "after":
		typed := api.AtlasSessionGroundedAnnotationThreadCreateRequestScreenshotRole(value)
		return &typed, nil
	default:
		return nil, fmt.Errorf("--role must be before or after")
	}
}

func resolveReportAnnotationActionID(actionID string) (*openapi_types.UUID, error) {
	if strings.TrimSpace(actionID) == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(strings.TrimSpace(actionID))
	if err != nil {
		return nil, fmt.Errorf("--action must be a UUID: %w", err)
	}
	typed := openapi_types.UUID(parsed)
	return &typed, nil
}

func newReportAnnotationsCreateCommand() *cobra.Command {
	options := reportAnnotationCreateOptions{}
	command := &cobra.Command{
		Use:   "create [body]",
		Short: "Ground a visual target on the current session and create an annotation",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := resolveReportAnnotationRole(options.role)
			if err != nil {
				return err
			}
			actionID, err := resolveReportAnnotationActionID(options.actionID)
			if err != nil {
				return err
			}
			severityValue, err := resolveAnnotationSeverity(cmd, options.severity, false)
			if err != nil {
				return err
			}
			if options.dryRun {
				if len(args) != 0 || cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
					return fmt.Errorf("body is not accepted with --dry-run")
				}
				if strings.TrimSpace(options.target) == "" {
					return fmt.Errorf("--target is required")
				}
				if strings.TrimSpace(options.previewOut) == "" || strings.TrimSpace(options.previewReceiptOut) == "" {
					return fmt.Errorf("--dry-run requires --preview-out and --preview-receipt-out")
				}
				if cmd.Flags().Changed("severity") || len(options.mentions) != 0 || cmd.Flags().Changed("client-request-id") || cmd.Flags().Changed("preview-receipt") {
					return fmt.Errorf("--severity, --mention, --client-request-id, and --preview-receipt are not accepted with --dry-run")
				}
				sessionID, err := resolveReportAnnotationSessionID(cmd)
				if err != nil {
					return err
				}
				client, err := atlasClient(cmd)
				if err != nil {
					return err
				}
				previewRole := api.AtlasSessionAnnotationAnchorPreviewRequestScreenshotRole(*role)
				preview, err := client.PreviewSessionAtlasAnnotationAnchor(cmd.Context(), sessionID, &api.AtlasSessionAnnotationAnchorPreviewRequest{
					ActionId:       actionID,
					ScreenshotRole: &previewRole,
					Target:         strings.TrimSpace(options.target),
				})
				if err != nil {
					return err
				}
				grounding := api.AtlasAnnotationAnchorPreviewResponse{
					Evidence:         preview.Evidence,
					NormalizedX:      preview.NormalizedX,
					NormalizedY:      preview.NormalizedY,
					ObservationId:    preview.ObservationId,
					PixelX:           preview.PixelX,
					PixelY:           preview.PixelY,
					ScreenshotHeight: preview.ScreenshotHeight,
					ScreenshotWidth:  preview.ScreenshotWidth,
				}
				if err := writeAnnotationPreviewFromScreenshotURL(cmd.Context(), preview.ScreenshotUrl, &grounding, options.previewOut); err != nil {
					return fmt.Errorf("write marked screenshot: %w", err)
				}
				receipt := reportAnnotationPreviewReceiptFile{
					Version:        1,
					SessionID:      sessionID,
					Target:         strings.TrimSpace(options.target),
					ScreenshotRole: options.role,
					PreviewReceipt: preview.PreviewReceipt,
				}
				if actionID != nil {
					receipt.ActionID = uuid.UUID(*actionID).String()
				}
				if err := writeReportAnnotationPreviewReceipt(options.previewReceiptOut, receipt); err != nil {
					return fmt.Errorf("write preview receipt: %w", err)
				}
				return printReportAnnotationResult(cmd, map[string]interface{}{
					"dry_run":              true,
					"session_id":           sessionID,
					"grounding":            grounding,
					"preview_path":         options.previewOut,
					"preview_receipt_path": options.previewReceiptOut,
				})
			}

			body, err := readReportAnnotationBody(cmd, args, options.annotationBodyOptions)
			if err != nil {
				return err
			}
			previewReceipt := ""
			var previewReceiptFile *reportAnnotationPreviewReceiptFile
			if strings.TrimSpace(options.previewReceipt) != "" {
				receipt, err := readReportAnnotationPreviewReceipt(options.previewReceipt)
				if err != nil {
					return err
				}
				previewReceiptFile = &receipt
				if cmd.Flags().Changed("target") && strings.TrimSpace(options.target) != receipt.Target {
					return fmt.Errorf("--target does not match the preview receipt")
				}
				if cmd.Flags().Changed("action") && strings.TrimSpace(options.actionID) != receipt.ActionID {
					return fmt.Errorf("--action does not match the preview receipt")
				}
				if cmd.Flags().Changed("role") && options.role != receipt.ScreenshotRole {
					return fmt.Errorf("--role does not match the preview receipt")
				}
				options.target = receipt.Target
				options.actionID = receipt.ActionID
				options.role = receipt.ScreenshotRole
				previewReceipt = receipt.PreviewReceipt
				role, err = resolveReportAnnotationRole(options.role)
				if err != nil {
					return err
				}
				actionID, err = resolveReportAnnotationActionID(options.actionID)
				if err != nil {
					return err
				}
			}
			if strings.TrimSpace(options.target) == "" {
				return fmt.Errorf("--target is required unless --preview-receipt is provided")
			}
			sessionID, err := resolveReportAnnotationSessionID(cmd)
			if err != nil {
				return err
			}
			if previewReceiptFile != nil {
				if previewReceiptFile.SessionID != sessionID {
					return fmt.Errorf("preview receipt belongs to session %s, not %s", previewReceiptFile.SessionID, sessionID)
				}
			}
			client, err := atlasClient(cmd)
			if err != nil {
				return err
			}
			body, mentionInputs, err := resolveAnnotationMentions(cmd.Context(), client, body, options.mentions)
			if err != nil {
				return err
			}
			requestID, err := resolveAnnotationRequestID(options.clientRequestID)
			if err != nil {
				return err
			}
			cmd.PrintErrf("Request ID: %s\n", requestID)
			result, err := client.CreateSessionGroundedAtlasAnnotationThread(cmd.Context(), sessionID, &api.AtlasSessionGroundedAnnotationThreadCreateRequest{
				ActionId:        actionID,
				Body:            body,
				ClientRequestId: openapi_types.UUID(uuid.MustParse(requestID)),
				Mentions:        optionalAnnotationMentions(mentionInputs),
				PreviewReceipt:  optionalReportAnnotationString(previewReceipt),
				ScreenshotRole:  role,
				Severity:        severityValue,
				Target:          strings.TrimSpace(options.target),
			})
			if err != nil {
				return reportAnnotationMutationError(cmd, requestID, err)
			}
			output := map[string]interface{}{
				"session_id":        sessionID,
				"thread":            result.Thread,
				"grounding":         result.Grounding,
				"atlas_url":         result.AtlasUrl,
				"client_request_id": requestID,
				"idempotent_replay": result.IdempotentReplay,
				"preview_path":      nil,
			}
			if options.previewOut != "" {
				if previewErr := writeThreadAnnotationPreview(cmd, client, result.Thread.AppId, result.Thread.Id, sessionID, &result.Grounding, options.previewOut); previewErr != nil {
					cmd.PrintErrf("Thread %s is available, but the preview download failed: %v\n", result.Thread.Id, previewErr)
					output["preview_error"] = previewErr.Error()
				} else {
					output["preview_path"] = options.previewOut
				}
			}
			return printReportAnnotationResult(cmd, output)
		},
	}
	command.Flags().StringVar(&options.target, "target", "", "Visually concrete target on the session's screenshot")
	command.Flags().StringVar(&options.severity, "severity", "", "Finding severity (blocker, issue, or polish)")
	command.Flags().StringVar(&options.actionID, "action", "", "Pin to a specific action id's screenshot instead of the latest")
	command.Flags().StringVar(&options.role, "role", "after", "Screenshot role to pin: before or after")
	command.Flags().StringArrayVar(&options.mentions, "mention", nil, "Bind @{alias} in the body to a member user id (alias=user-id, repeatable)")
	command.Flags().StringVar(&options.previewOut, "preview-out", "", "Write a marked screenshot showing where the pin landed")
	command.Flags().BoolVar(&options.dryRun, "dry-run", false, "Ground without creating a thread")
	command.Flags().StringVar(&options.previewReceiptOut, "preview-receipt-out", "", "Write the private receipt required to create from this preview")
	command.Flags().StringVar(&options.previewReceipt, "preview-receipt", "", "Create from a private preview receipt file")
	command.Flags().StringVar(&options.clientRequestID, "client-request-id", "", "UUID for idempotent retry recovery")
	addAnnotationBodyFlags(command, &options.annotationBodyOptions)
	return command
}

func optionalReportAnnotationString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func writeThreadAnnotationPreview(command *cobra.Command, client *api.Client, appID, threadID, sessionID string, preview *api.AtlasAnnotationAnchorPreviewResponse, outputPath string) error {
	screenshotURL, err := client.GetAtlasAnnotationThreadScreenshotURLForSession(command.Context(), appID, threadID, sessionID)
	if err == nil && strings.TrimSpace(screenshotURL) != "" {
		return writeAnnotationPreviewFromScreenshotURL(command.Context(), screenshotURL, preview, outputPath)
	}
	return writeAnnotationPreview(command, client, appID, preview, outputPath)
}

func newReportAnnotationsListCommand() *cobra.Command {
	var appInput, status, severity, cursor string
	var limit int
	command := &cobra.Command{
		Use:   "list",
		Short: "List annotation threads for the current session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch status {
			case "open", "resolved", "dismissed", "closed", "all":
			default:
				return fmt.Errorf("--status must be open, resolved, dismissed, closed, or all")
			}
			switch severity {
			case "all", "blocker", "issue", "polish", "none":
			default:
				return fmt.Errorf("--severity must be all, blocker, issue, polish, or none")
			}
			if limit < 1 || limit > 100 {
				return fmt.Errorf("--limit must be between 1 and 100")
			}
			client, err := atlasClient(cmd)
			if err != nil {
				return err
			}
			sessionID, err := resolveReportAnnotationScopeSessionID(cmd, appInput)
			if err != nil {
				return err
			}
			app, err := resolveReportAnnotationApp(cmd, client, appInput, sessionID)
			if err != nil {
				return err
			}
			result, err := client.ListAtlasAnnotationFeedback(cmd.Context(), app.ID, sessionID, "", status, severity, cursor, limit)
			if err != nil {
				return err
			}
			if reportAnnotationsJSONOutput(cmd) {
				return printReportAnnotationResult(cmd, result)
			}
			for _, item := range result.Items {
				fmt.Fprintln(cmd.OutOrStdout(), reportAnnotationFeedbackRow(item))
			}
			if result.NextCursor != nil && *result.NextCursor != "" {
				cmd.PrintErrf("More threads available: --cursor %s\n", *result.NextCursor)
			}
			return nil
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id (default: resolved from the session)")
	command.Flags().StringVar(&status, "status", "open", "Thread status: open, resolved, dismissed, closed, or all")
	command.Flags().StringVar(&severity, "severity", "all", "Finding severity filter: all, blocker, issue, polish, or none")
	command.Flags().IntVar(&limit, "limit", 25, "Maximum threads in this page (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "Opaque cursor returned by a previous page")
	return command
}

// reportAnnotationFeedbackRow renders one machine-usable, tab-separated row:
// thread id, severity, status, created_at, first-line body.
func reportAnnotationFeedbackRow(item api.AtlasAnnotationFeedbackItem) string {
	severity := "none"
	if item.Severity != nil {
		severity = string(*item.Severity)
	}
	firstLine := ""
	if item.PreviewText != nil {
		firstLine = strings.TrimSpace(strings.SplitN(*item.PreviewText, "\n", 2)[0])
	}
	return strings.Join([]string{
		item.ThreadId,
		severity,
		string(item.Status),
		item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		firstLine,
	}, "\t")
}

func newReportAnnotationsGetCommand() *cobra.Command {
	var appInput, screenshotOut string
	command := &cobra.Command{
		Use:   "get <thread-id>",
		Short: "Get one annotation thread with its full comment history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := atlasClient(cmd)
			if err != nil {
				return err
			}
			sessionID, err := resolveReportAnnotationScopeSessionID(cmd, appInput)
			if err != nil {
				return err
			}
			app, err := resolveReportAnnotationApp(cmd, client, appInput, sessionID)
			if err != nil {
				return err
			}
			result, err := client.GetAtlasAnnotationThreadForSession(cmd.Context(), app.ID, args[0], sessionID)
			if err != nil {
				return err
			}
			output := map[string]interface{}{
				"thread":          result.Thread,
				"screenshot_path": nil,
			}
			if screenshotOut != "" {
				// Prefer the thread's own evidence URL: it resolves even for
				// capture-minted observations still awaiting screen
				// assignment, where the Atlas projection lookup 404s.
				screenshotURL, urlErr := client.GetAtlasAnnotationThreadScreenshotURLForSession(
					cmd.Context(), app.ID, args[0], sessionID,
				)
				if urlErr != nil || screenshotURL == "" {
					observation, obsErr := client.GetAtlasObservation(cmd.Context(), api.AtlasQuery{AppID: app.ID, IncludeScreenshots: true}, result.Thread.Anchor.ObservationId)
					if obsErr != nil {
						return obsErr
					}
					screenshotURL = findAtlasScreenshotURL(observation)
				}
				if screenshotURL == "" {
					return fmt.Errorf("observation %s did not include a screenshot URL", result.Thread.Anchor.ObservationId)
				}
				if downloadErr := client.DownloadFileFromURL(cmd.Context(), screenshotURL, screenshotOut); downloadErr != nil {
					return fmt.Errorf("download pinned screenshot: %w", downloadErr)
				}
				output["screenshot_path"] = screenshotOut
			}
			return printReportAnnotationResult(cmd, output)
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id (default: resolved from the session)")
	command.Flags().StringVar(&screenshotOut, "screenshot-out", "", "Download the thread's pinned observation screenshot to this path")
	return command
}

func newReportAnnotationsReplyCommand() *cobra.Command {
	var appInput, clientRequestID string
	var attachments, mentions []string
	bodyOptions := annotationBodyOptions{}
	command := &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to an annotation thread from the current session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readAnnotationBody(cmd, bodyOptions)
			if err != nil {
				return err
			}
			requestID, err := resolveAnnotationRequestID(clientRequestID)
			if err != nil {
				return err
			}
			cmd.PrintErrf("Request ID: %s\n", requestID)
			client, err := atlasClient(cmd)
			if err != nil {
				return reportAnnotationMutationError(cmd, requestID, err)
			}
			sessionID, err := resolveReportAnnotationScopeSessionID(cmd, appInput)
			if err != nil {
				return reportAnnotationMutationError(cmd, requestID, err)
			}
			app, err := resolveReportAnnotationApp(cmd, client, appInput, sessionID)
			if err != nil {
				return reportAnnotationMutationError(cmd, requestID, err)
			}
			body, mentionInputs, err := resolveAnnotationMentions(cmd.Context(), client, body, mentions)
			if err != nil {
				return reportAnnotationMutationError(cmd, requestID, err)
			}
			attachmentIDs, attachmentIDStrings, err := uploadAnnotationAttachments(cmd.Context(), client, app.ID, requestID, attachments)
			if err != nil {
				return annotationMutationErrorWithAttachments(cmd, requestID, attachmentIDStrings, err)
			}
			result, err := client.AddAtlasAnnotationReplyForSession(cmd.Context(), app.ID, args[0], sessionID, &api.AtlasAnnotationReplyRequest{Body: body, ClientRequestId: &requestID, AttachmentIds: optionalUUIDs(attachmentIDs), Mentions: optionalAnnotationMentions(mentionInputs)})
			if err != nil {
				return annotationMutationErrorWithAttachments(cmd, requestID, attachmentIDStrings, err)
			}
			thread, err := client.GetAtlasAnnotationThreadForSession(cmd.Context(), app.ID, args[0], sessionID)
			if err != nil {
				return annotationMutationErrorWithAttachments(cmd, requestID, attachmentIDStrings, err)
			}
			return printReportAnnotationResult(cmd, map[string]interface{}{
				"comment": result.Comment, "thread": thread.Thread, "client_request_id": requestID, "attachment_ids": attachmentIDStrings,
				"idempotent_replay": result.IdempotentReplay,
			})
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id (default: resolved from the session)")
	addAnnotationBodyFlags(command, &bodyOptions)
	command.Flags().StringVar(&clientRequestID, "client-request-id", "", "UUID for idempotent retry recovery")
	command.Flags().StringSliceVar(&attachments, "attach", nil, "Attach a local file (repeatable)")
	command.Flags().StringArrayVar(&mentions, "mention", nil, "Bind @{alias} in the body to a member user id (alias=user-id, repeatable)")
	return command
}

func newReportAnnotationsStatusCommand(action string) *cobra.Command {
	var appInput string
	var expectedVersion int
	command := &cobra.Command{
		Use:   action + " <thread-id>",
		Short: strings.ToUpper(action[:1]) + action[1:] + " an annotation thread from the current session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := atlasClient(cmd)
			if err != nil {
				return err
			}
			sessionID, err := resolveReportAnnotationScopeSessionID(cmd, appInput)
			if err != nil {
				return err
			}
			app, err := resolveReportAnnotationApp(cmd, client, appInput, sessionID)
			if err != nil {
				return err
			}
			version := expectedVersion
			if !cmd.Flags().Changed("expected-version") {
				current, err := client.GetAtlasAnnotationThreadForSession(cmd.Context(), app.ID, args[0], sessionID)
				if err != nil {
					return err
				}
				version = current.Thread.Version
			}
			result, err := client.ChangeAtlasAnnotationStatusForSession(cmd.Context(), app.ID, args[0], sessionID, action, &api.AtlasAnnotationStatusChangeRequest{ExpectedVersion: version})
			if err != nil {
				return err
			}
			return printReportAnnotationResult(cmd, result)
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id (default: resolved from the session)")
	command.Flags().IntVar(&expectedVersion, "expected-version", 0, "Expected thread version")
	return command
}

func newReportAnnotationsSeverityCommand() *cobra.Command {
	var appInput, severity string
	var clear bool
	var expectedVersion int
	command := &cobra.Command{
		Use:   "severity <thread-id>",
		Short: "Set or clear a finding's severity from the current session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			severityValue, err := resolveAnnotationSeverity(cmd, severity, clear)
			if err != nil {
				return err
			}
			if !clear && severityValue == nil {
				return fmt.Errorf("provide --severity <blocker|issue|polish> or --clear")
			}
			client, err := atlasClient(cmd)
			if err != nil {
				return err
			}
			sessionID, err := resolveReportAnnotationScopeSessionID(cmd, appInput)
			if err != nil {
				return err
			}
			app, err := resolveReportAnnotationApp(cmd, client, appInput, sessionID)
			if err != nil {
				return err
			}
			version := expectedVersion
			if !cmd.Flags().Changed("expected-version") {
				current, err := client.GetAtlasAnnotationThreadForSession(cmd.Context(), app.ID, args[0], sessionID)
				if err != nil {
					return err
				}
				version = current.Thread.Version
			}
			result, err := client.SetAtlasAnnotationSeverityForSession(cmd.Context(), app.ID, args[0], sessionID, &api.AtlasAnnotationSeverityChangeRequest{
				ExpectedVersion: version,
				Severity:        severityValue,
			})
			if err != nil {
				return err
			}
			return printReportAnnotationResult(cmd, result)
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id (default: resolved from the session)")
	command.Flags().StringVar(&severity, "severity", "", "Finding severity (blocker, issue, or polish)")
	command.Flags().BoolVar(&clear, "clear", false, "Clear severity, demoting the finding to a plain conversation")
	command.Flags().IntVar(&expectedVersion, "expected-version", 0, "Expected thread version")
	return command
}

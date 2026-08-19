package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

const annotationPreviewImageLimit = 20 << 20

type annotationBodyOptions struct {
	body     string
	bodyFile string
}

type annotationCreateOptions struct {
	app             string
	observation     string
	target          string
	clientRequestID string
	dryRun          bool
	previewOut      string
	annotationBodyOptions
}

type annotationMoveOptions struct {
	app             string
	target          string
	previewOut      string
	expectedVersion int
	dryRun          bool
}

func newAtlasAnnotationsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "annotations",
		Short: "Inspect and manage grounded Atlas feedback",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(
		newAtlasAnnotationsListCommand(),
		newAtlasAnnotationsGetCommand(),
		newAtlasAnnotationsCreateCommand(),
		newAtlasAnnotationsMoveCommand(),
		newAtlasAnnotationsReplyCommand(),
		newAtlasAnnotationsEditCommand(),
		newAtlasAnnotationsDeleteCommand(),
		newAtlasAnnotationsStatusCommand("resolve"),
		newAtlasAnnotationsStatusCommand("dismiss"),
		newAtlasAnnotationsStatusCommand("reopen"),
	)
	return command
}

func annotationClientAndApp(cmd *cobra.Command, appInput string) (*api.Client, *api.App, error) {
	client, err := atlasClient(cmd)
	if err != nil {
		return nil, nil, err
	}
	app, err := resolveAtlasApp(cmd, client, appInput)
	if err != nil {
		return nil, nil, err
	}
	return client, app, nil
}

func printAnnotationResult(cmd *cobra.Command, value interface{}) error {
	if atlasJSONOutput(cmd) {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}

func annotationMutationError(cmd *cobra.Command, requestID string, err error) error {
	if requestID != "" {
		cmd.PrintErrf("Retry the exact same payload with --client-request-id %s\n", requestID)
	}
	if atlasJSONOutput(cmd) {
		_ = printAnnotationResult(cmd, map[string]interface{}{
			"error":             err.Error(),
			"client_request_id": requestID,
		})
	}
	return err
}

func newAtlasAnnotationsListCommand() *cobra.Command {
	var appInput, observationID, status, cursor string
	var limit int
	command := &cobra.Command{
		Use:   "list",
		Short: "List one page of Atlas annotation threads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch status {
			case "open", "resolved", "dismissed", "closed", "all":
			default:
				return fmt.Errorf("--status must be open, resolved, dismissed, closed, or all")
			}
			if limit < 1 || limit > 100 {
				return fmt.Errorf("--limit must be between 1 and 100")
			}
			client, app, err := annotationClientAndApp(cmd, appInput)
			if err != nil {
				return err
			}
			result, err := client.ListAtlasAnnotationFeedback(cmd.Context(), app.ID, observationID, status, cursor, limit)
			if err != nil {
				return err
			}
			return printAnnotationResult(cmd, result)
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id")
	command.Flags().StringVar(&observationID, "observation", "", "Filter to an exact observation id")
	command.Flags().StringVar(&status, "status", "open", "Thread status: open, resolved, dismissed, closed, or all")
	command.Flags().IntVar(&limit, "limit", 25, "Maximum threads in this page (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "Opaque cursor returned by a previous page")
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsGetCommand() *cobra.Command {
	var appInput string
	command := &cobra.Command{
		Use:   "get <thread-id>",
		Short: "Get one Atlas annotation thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, app, err := annotationClientAndApp(cmd, appInput)
			if err != nil {
				return err
			}
			result, err := client.GetAtlasAnnotationThread(cmd.Context(), app.ID, args[0])
			if err != nil {
				return err
			}
			return printAnnotationResult(cmd, result)
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id")
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsCreateCommand() *cobra.Command {
	options := annotationCreateOptions{}
	command := &cobra.Command{
		Use:   "create",
		Short: "Ground a visual target and create an Atlas annotation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(options.observation) == "" || strings.TrimSpace(options.target) == "" {
				return fmt.Errorf("--observation and --target are required")
			}
			if options.previewOut != "" && !options.dryRun {
				return fmt.Errorf("--preview-out is valid only with --dry-run")
			}
			if options.dryRun && (cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")) {
				return fmt.Errorf("--dry-run prohibits --body and --body-file")
			}
			client, app, err := annotationClientAndApp(cmd, options.app)
			if err != nil {
				return err
			}
			if options.dryRun {
				preview, err := client.PreviewAtlasAnnotationAnchor(cmd.Context(), app.ID, options.observation, &api.AtlasAnnotationAnchorPreviewRequest{Target: strings.TrimSpace(options.target)})
				if err != nil {
					return err
				}
				if options.previewOut != "" {
					if err := writeAnnotationPreview(cmd, client, app.ID, preview, options.previewOut); err != nil {
						return err
					}
				}
				return printAnnotationResult(cmd, map[string]interface{}{"dry_run": true, "grounding": preview, "preview_path": optionalString(options.previewOut)})
			}
			body, err := readAnnotationBody(cmd, options.annotationBodyOptions)
			if err != nil {
				return err
			}
			requestID, err := resolveAnnotationRequestID(options.clientRequestID)
			if err != nil {
				return err
			}
			cmd.PrintErrf("Request ID: %s\n", requestID)
			result, err := client.CreateGroundedAtlasAnnotationThread(cmd.Context(), app.ID, options.observation, &api.AtlasGroundedAnnotationThreadCreateRequest{
				Body: body, ClientRequestId: requestID, Target: strings.TrimSpace(options.target),
			})
			if err != nil {
				return annotationMutationError(cmd, requestID, err)
			}
			return printAnnotationResult(cmd, map[string]interface{}{
				"thread": result.Thread, "grounding": result.Grounding, "atlas_url": result.AtlasUrl,
				"client_request_id": requestID, "idempotent_replay": result.IdempotentReplay,
			})
		},
	}
	command.Flags().StringVar(&options.app, "app", "", "App name or app id")
	command.Flags().StringVar(&options.observation, "observation", "", "Exact Atlas observation id")
	command.Flags().StringVar(&options.target, "target", "", "Visually concrete target on the observation")
	addAnnotationBodyFlags(command, &options.annotationBodyOptions)
	command.Flags().StringVar(&options.clientRequestID, "client-request-id", "", "UUID for idempotent retry recovery")
	command.Flags().BoolVar(&options.dryRun, "dry-run", false, "Ground the target without creating a thread")
	command.Flags().StringVar(&options.previewOut, "preview-out", "", "Write a marked screenshot during --dry-run")
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsMoveCommand() *cobra.Command {
	options := annotationMoveOptions{}
	command := &cobra.Command{
		Use:   "move <thread-id>",
		Short: "Ground a target and move an existing annotation pin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(options.target) == "" {
				return fmt.Errorf("--target is required")
			}
			if options.previewOut != "" && !options.dryRun {
				return fmt.Errorf("--preview-out is valid only with --dry-run")
			}
			client, app, err := annotationClientAndApp(cmd, options.app)
			if err != nil {
				return err
			}
			current, err := client.GetAtlasAnnotationThread(cmd.Context(), app.ID, args[0])
			if err != nil {
				return err
			}
			preview, err := client.PreviewAtlasAnnotationAnchor(cmd.Context(), app.ID, current.Thread.Anchor.ObservationId, &api.AtlasAnnotationAnchorPreviewRequest{Target: strings.TrimSpace(options.target)})
			if err != nil {
				return err
			}
			if options.previewOut != "" {
				if err := writeAnnotationPreview(cmd, client, app.ID, preview, options.previewOut); err != nil {
					return err
				}
			}
			if options.dryRun {
				return printAnnotationResult(cmd, map[string]interface{}{"dry_run": true, "thread_id": args[0], "current_version": current.Thread.Version, "grounding": preview, "preview_path": optionalString(options.previewOut)})
			}
			version := options.expectedVersion
			if !cmd.Flags().Changed("expected-version") {
				version = current.Thread.Version
			}
			result, err := client.MoveAtlasAnnotationThread(cmd.Context(), app.ID, args[0], &api.AtlasAnnotationAnchorMoveRequest{ExpectedVersion: version, X: preview.NormalizedX, Y: preview.NormalizedY})
			if err != nil {
				return err
			}
			return printAnnotationResult(cmd, result)
		},
	}
	command.Flags().StringVar(&options.app, "app", "", "App name or app id")
	command.Flags().StringVar(&options.target, "target", "", "Visually concrete target on the thread observation")
	command.Flags().IntVar(&options.expectedVersion, "expected-version", 0, "Expected thread version")
	command.Flags().BoolVar(&options.dryRun, "dry-run", false, "Ground the target without moving the thread")
	command.Flags().StringVar(&options.previewOut, "preview-out", "", "Write a marked screenshot during --dry-run")
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsReplyCommand() *cobra.Command {
	var appInput, clientRequestID string
	bodyOptions := annotationBodyOptions{}
	command := &cobra.Command{
		Use:   "reply <thread-id>",
		Short: "Reply to an Atlas annotation thread",
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
			client, app, err := annotationClientAndApp(cmd, appInput)
			if err != nil {
				return annotationMutationError(cmd, requestID, err)
			}
			result, err := client.AddAtlasAnnotationReply(cmd.Context(), app.ID, args[0], &api.AtlasAnnotationReplyRequest{Body: body, ClientRequestId: &requestID})
			if err != nil {
				return annotationMutationError(cmd, requestID, err)
			}
			thread, err := client.GetAtlasAnnotationThread(cmd.Context(), app.ID, args[0])
			if err != nil {
				return annotationMutationError(cmd, requestID, err)
			}
			return printAnnotationResult(cmd, map[string]interface{}{
				"comment": result.Comment, "thread": thread.Thread, "client_request_id": requestID,
				"idempotent_replay": result.IdempotentReplay,
			})
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id")
	addAnnotationBodyFlags(command, &bodyOptions)
	command.Flags().StringVar(&clientRequestID, "client-request-id", "", "UUID for idempotent retry recovery")
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsEditCommand() *cobra.Command {
	var appInput string
	bodyOptions := annotationBodyOptions{}
	command := &cobra.Command{
		Use:   "edit <comment-id>",
		Short: "Edit an Atlas annotation comment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readAnnotationBody(cmd, bodyOptions)
			if err != nil {
				return err
			}
			client, app, err := annotationClientAndApp(cmd, appInput)
			if err != nil {
				return err
			}
			comment, err := client.EditAtlasAnnotationComment(cmd.Context(), app.ID, args[0], &api.AtlasAnnotationCommentEditRequest{Body: body})
			if err != nil {
				return err
			}
			thread, err := client.GetAtlasAnnotationThread(cmd.Context(), app.ID, comment.ThreadId)
			if err != nil {
				return err
			}
			return printAnnotationResult(cmd, map[string]interface{}{"comment": comment, "thread": thread.Thread})
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id")
	addAnnotationBodyFlags(command, &bodyOptions)
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsDeleteCommand() *cobra.Command {
	var appInput string
	var confirmed bool
	command := &cobra.Command{
		Use:   "delete <comment-id>",
		Short: "Delete an Atlas annotation comment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirmed {
				return fmt.Errorf("--yes is required to delete an annotation comment")
			}
			cmd.PrintErrln("WARNING: deleting a root comment removes the entire thread from product and public-share surfaces.")
			client, app, err := annotationClientAndApp(cmd, appInput)
			if err != nil {
				return err
			}
			comment, err := client.DeleteAtlasAnnotationComment(cmd.Context(), app.ID, args[0])
			if err != nil {
				return err
			}
			return printAnnotationResult(cmd, map[string]interface{}{"comment": comment, "thread_id": comment.ThreadId})
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id")
	command.Flags().BoolVar(&confirmed, "yes", false, "Confirm deletion")
	_ = command.MarkFlagRequired("app")
	return command
}

func newAtlasAnnotationsStatusCommand(action string) *cobra.Command {
	var appInput string
	var expectedVersion int
	command := &cobra.Command{
		Use:   action + " <thread-id>",
		Short: strings.ToUpper(action[:1]) + action[1:] + " an Atlas annotation thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, app, err := annotationClientAndApp(cmd, appInput)
			if err != nil {
				return err
			}
			version := expectedVersion
			if !cmd.Flags().Changed("expected-version") {
				current, err := client.GetAtlasAnnotationThread(cmd.Context(), app.ID, args[0])
				if err != nil {
					return err
				}
				version = current.Thread.Version
			}
			result, err := client.ChangeAtlasAnnotationStatus(cmd.Context(), app.ID, args[0], action, &api.AtlasAnnotationStatusChangeRequest{ExpectedVersion: version})
			if err != nil {
				return err
			}
			return printAnnotationResult(cmd, result)
		},
	}
	command.Flags().StringVar(&appInput, "app", "", "App name or app id")
	command.Flags().IntVar(&expectedVersion, "expected-version", 0, "Expected thread version")
	_ = command.MarkFlagRequired("app")
	return command
}

func addAnnotationBodyFlags(command *cobra.Command, options *annotationBodyOptions) {
	command.Flags().StringVar(&options.body, "body", "", "Plain-text comment body")
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "Read body from a file, or - for stdin")
}

func readAnnotationBody(command *cobra.Command, options annotationBodyOptions) (string, error) {
	providedBody := command.Flags().Changed("body")
	providedFile := command.Flags().Changed("body-file")
	if providedBody == providedFile {
		return "", fmt.Errorf("exactly one of --body or --body-file is required")
	}
	if providedBody {
		body := strings.TrimSpace(options.body)
		if body == "" {
			return "", fmt.Errorf("annotation body cannot be empty")
		}
		return body, nil
	}
	var reader io.Reader
	var file *os.File
	if options.bodyFile == "-" {
		reader = command.InOrStdin()
	} else {
		var err error
		file, err = os.Open(options.bodyFile)
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader = file
	}
	contents, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	if err != nil {
		return "", err
	}
	if len(contents) > 64<<10 {
		return "", fmt.Errorf("annotation body exceeds 64 KiB")
	}
	body := strings.TrimSpace(string(contents))
	if body == "" {
		return "", fmt.Errorf("annotation body cannot be empty")
	}
	return body, nil
}

func resolveAnnotationRequestID(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return uuid.NewString(), nil
	}
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("--client-request-id must be a UUID: %w", err)
	}
	return parsed.String(), nil
}

func optionalString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func writeAnnotationPreview(command *cobra.Command, client *api.Client, appID string, preview *api.AtlasAnnotationAnchorPreviewResponse, outputPath string) error {
	response, err := client.GetAtlasObservation(command.Context(), api.AtlasQuery{AppID: appID, IncludeScreenshots: true}, preview.ObservationId)
	if err != nil {
		return err
	}
	screenshotURL := findAtlasScreenshotURL(response)
	if screenshotURL == "" {
		return fmt.Errorf("Atlas observation %s did not include a screenshot URL", preview.ObservationId)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	httpResponse, err := httpClient.Get(screenshotURL)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return fmt.Errorf("download preview screenshot: %s", httpResponse.Status)
	}
	contents, err := io.ReadAll(io.LimitReader(httpResponse.Body, annotationPreviewImageLimit+1))
	if err != nil {
		return err
	}
	if len(contents) > annotationPreviewImageLimit {
		return fmt.Errorf("preview screenshot exceeds 20 MiB")
	}
	decoded, _, err := image.Decode(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("decode preview screenshot: %w", err)
	}
	marked := image.NewRGBA(decoded.Bounds())
	draw.Draw(marked, marked.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	drawAnnotationMarker(marked, preview.PixelX, preview.PixelY)
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil && filepath.Dir(outputPath) != "." {
		return err
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, marked)
}

func findAtlasScreenshotURL(value interface{}) string {
	switch typed := value.(type) {
	case api.AtlasResponse:
		return findAtlasScreenshotURL(map[string]interface{}(typed))
	case map[string]interface{}:
		for _, key := range []string{"screenshot_url", "thumbnail_url"} {
			if candidate, ok := typed[key].(string); ok && strings.TrimSpace(candidate) != "" {
				return candidate
			}
		}
		for _, child := range typed {
			if candidate := findAtlasScreenshotURL(child); candidate != "" {
				return candidate
			}
		}
	case []interface{}:
		for _, child := range typed {
			if candidate := findAtlasScreenshotURL(child); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func drawAnnotationMarker(target draw.Image, centerX, centerY int) {
	bounds := target.Bounds()
	for radius := 14; radius >= 8; radius-- {
		markerColor := color.RGBA{R: 255, G: 255, B: 255, A: 230}
		if radius <= 11 {
			markerColor = color.RGBA{R: 226, G: 49, B: 49, A: 255}
		}
		for y := centerY - radius; y <= centerY+radius; y++ {
			for x := centerX - radius; x <= centerX+radius; x++ {
				distance := (x-centerX)*(x-centerX) + (y-centerY)*(y-centerY)
				if distance <= radius*radius && image.Pt(x, y).In(bounds) {
					target.Set(x, y, markerColor)
				}
			}
		}
	}
}

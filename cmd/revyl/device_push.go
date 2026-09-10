package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/spf13/cobra"
)

const maxPushPayloadBytes = 4096

func validatePushPayloadSize(payload map[string]any) error {
	marshalled, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("could not encode push payload: %w", err)
	}
	if len(marshalled) > maxPushPayloadBytes {
		return fmt.Errorf(
			"payload is %d bytes; APNs allows at most %d", len(marshalled), maxPushPayloadBytes)
	}
	return nil
}

func loadPushPayloadFile(path string) (map[string]any, error) {
	// #nosec G304 -- this local CLI path is explicitly selected by the caller;
	// reads are size-bounded and parsed as the notification payload's JSON schema.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	if len(raw) > maxPushPayloadBytes {
		return nil, fmt.Errorf(
			"payload is %d bytes; APNs allows at most %d", len(raw), maxPushPayloadBytes)
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if _, ok := payload["aps"]; !ok {
		return nil, fmt.Errorf("%s must contain a top-level \"aps\" key", path)
	}
	if err := validatePushPayloadSize(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func buildPushPayloadFromFlags(title, body string, badge int, badgeSet bool) map[string]any {
	alert := map[string]any{}
	if title != "" {
		alert["title"] = title
	}
	if body != "" {
		alert["body"] = body
	}
	aps := map[string]any{}
	if len(alert) > 0 {
		aps["alert"] = alert
	}
	if badgeSet {
		aps["badge"] = badge
	}
	return map[string]any{"aps": aps}
}

func resolvePushBundleID(payload map[string]any, flagValue string) string {
	if fromFile, ok := payload["Simulator Target Bundle"].(string); ok && fromFile != "" {
		return fromFile
	}
	return flagValue
}

func pushNotificationToDevice(
	ctx context.Context,
	requester workerSessionRequester,
	session *mcppkg.DeviceSession,
	payload map[string]any,
	bundleID string,
) (string, error) {
	body := map[string]any{"payload": payload}
	if bundleID != "" {
		body["bundle_id"] = bundleID
	}
	respBody, err := requester.WorkerRequestOnSession(ctx, session, "/push_notification", body)
	if err != nil {
		return "", err
	}
	if err := ensureWorkerActionSucceeded(respBody, "push_notification"); err != nil {
		return "", err
	}
	if resolved := extractInstallBundleID(respBody); resolved != "" {
		return resolved, nil
	}
	return bundleID, nil
}

var devicePushCmd = &cobra.Command{
	Use:   "push",
	Short: "Deliver a notification to the session's app",
	Example: `  revyl device push --title "Order shipped" --body "Track it now"
  revyl device push apns payload.apns`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		body, _ := cmd.Flags().GetString("body")
		badge, _ := cmd.Flags().GetInt("badge")
		badgeSet := cmd.Flags().Changed("badge")
		if title == "" && body == "" && !badgeSet {
			return fmt.Errorf(
				"provide --title/--body, or a payload file: revyl device push apns <file>")
		}
		payload := buildPushPayloadFromFlags(title, body, badge, badgeSet)
		if err := validatePushPayloadSize(payload); err != nil {
			return err
		}
		return runDevicePush(cmd, payload)
	},
}

var devicePushApnsCmd = &cobra.Command{
	Use:   "apns [file]",
	Short: "Deliver a raw APNs payload file to the session's app",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := loadPushPayloadFile(args[0])
		if err != nil {
			return err
		}
		return runDevicePush(cmd, payload)
	},
}

func runDevicePush(cmd *cobra.Command, payload map[string]any) error {
	mgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
	session, err := resolveSessionTarget(cmd, mgr)
	if err != nil {
		return err
	}
	flagBundleID, _ := cmd.Flags().GetString("bundle-id")
	bundleID := resolvePushBundleID(payload, flagBundleID)
	resolvedBundleID, err := pushNotificationToDevice(cmd.Context(), mgr, session, payload, bundleID)
	if err != nil {
		return err
	}
	jsonOrPrint(cmd, map[string]string{"status": "delivered", "bundle_id": resolvedBundleID},
		"Notification delivered")
	return nil
}

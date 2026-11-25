package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrUninstallUnsupported reports a device worker that predates uninstall_app.
var ErrUninstallUnsupported = errors.New("this device's worker does not support uninstall yet; use install --clean to reset the app's data, or start a new session")

// Clean install methods, reported in command and tool output.
const (
	CleanMethodUninstall = "uninstall"
	CleanMethodClearData = "clear_data"
)

// DeviceUninstallResult describes a completed uninstall_app call.
type DeviceUninstallResult struct {
	BundleID     string `json:"bundle_id"`
	WasInstalled bool   `json:"was_installed"`
}

// CleanInstallOutcome describes how a clean install reset the app's data.
type CleanInstallOutcome struct {
	Method                 string `json:"clean_method"`
	PreviousInstallRemoved bool   `json:"previous_install_removed"`
}

func isWorkerRouteMissing(err error) bool {
	var workerErr *WorkerHTTPError
	if !errors.As(err, &workerErr) || workerErr.StatusCode != http.StatusNotFound {
		return false
	}
	var body struct {
		Detail string `json:"detail"`
	}
	return json.Unmarshal([]byte(workerErr.Body), &body) == nil && strings.EqualFold(strings.TrimSpace(body.Detail), "Not Found")
}

// UninstallAppOnSession removes bundleID and its data from the session's
// device; an empty bundleID removes the session's app. An app that was not
// installed succeeds with WasInstalled false.
func (m *DeviceSessionManager) UninstallAppOnSession(ctx context.Context, session *DeviceSession, bundleID string) (*DeviceUninstallResult, error) {
	body := map[string]string{}
	if bundleID = strings.TrimSpace(bundleID); bundleID != "" {
		body["bundle_id"] = bundleID
	}
	respBody, err := m.WorkerRequestOnSession(ctx, session, "/uninstall_app", body)
	if err != nil {
		if isWorkerRouteMissing(err) {
			return nil, ErrUninstallUnsupported
		}
		return nil, err
	}
	if err := EnsureWorkerActionSucceeded(respBody, "uninstall_app"); err != nil {
		return nil, err
	}
	var resp struct {
		BundleID     string `json:"bundle_id"`
		WasInstalled *bool  `json:"was_installed"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil || resp.WasInstalled == nil || resp.BundleID == "" {
		return nil, fmt.Errorf("device action uninstall_app returned an unexpected response")
	}
	return &DeviceUninstallResult{BundleID: resp.BundleID, WasInstalled: *resp.WasInstalled}, nil
}

// clearAppDataOnSession empties bundleID's data and, on iOS, the simulator
// keychain, which outlives app data there.
func (m *DeviceSessionManager) clearAppDataOnSession(ctx context.Context, session *DeviceSession, bundleID string) error {
	actions := []string{"clear_app_state"}
	if strings.EqualFold(session.Platform, "ios") {
		actions = append(actions, "clear_keychain")
	}
	for _, action := range actions {
		var body interface{}
		if action == "clear_app_state" {
			body = map[string]string{"bundle_id": bundleID}
		}
		respBody, err := m.WorkerRequestOnSession(ctx, session, "/"+action, body)
		if err == nil {
			err = EnsureWorkerActionSucceeded(respBody, action)
		}
		if isWorkerRouteMissing(err) {
			return fmt.Errorf("this device's worker cannot reset app data; start a new session for a clean install")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CleanInstallOnSession runs install so the app starts with fresh data. It
// uninstalls bundleID first; when the worker predates uninstall_app or the
// bundle ID is unknown until install reports it, it clears the installed
// app's data afterwards instead. A failed uninstall stops before installing,
// because a timed-out uninstall may still be running on the device and would
// race the install. install returns the installed bundle ID.
func (m *DeviceSessionManager) CleanInstallOnSession(ctx context.Context, session *DeviceSession, bundleID string, install func() (string, error)) (*CleanInstallOutcome, error) {
	outcome := &CleanInstallOutcome{Method: CleanMethodClearData}
	if bundleID = strings.TrimSpace(bundleID); bundleID != "" {
		result, err := m.UninstallAppOnSession(ctx, session, bundleID)
		switch {
		case errors.Is(err, ErrUninstallUnsupported):
		case err != nil:
			return nil, fmt.Errorf("clean install stopped because the uninstall of %s did not finish, so nothing was installed and the app may be partly removed; run the install again: %w", bundleID, err)
		default:
			outcome.Method = CleanMethodUninstall
			outcome.PreviousInstallRemoved = result.WasInstalled
		}
	}
	installedBundleID, err := install()
	if err != nil {
		if outcome.PreviousInstallRemoved {
			return nil, fmt.Errorf("%s was uninstalled, but the install failed: %w", bundleID, err)
		}
		return nil, err
	}
	installedBundleID = strings.TrimSpace(installedBundleID)
	if outcome.Method == CleanMethodUninstall && (installedBundleID == "" || installedBundleID == bundleID) {
		return outcome, nil
	}
	outcome.Method = CleanMethodClearData
	if installedBundleID == "" {
		installedBundleID = bundleID
	}
	if installedBundleID == "" {
		return nil, fmt.Errorf("the app installed, but its bundle ID is unknown, so its data was not reset; rerun with the bundle ID")
	}
	if err := m.clearAppDataOnSession(ctx, session, installedBundleID); err != nil {
		return nil, fmt.Errorf("%s installed, but its data was not reset: %w", installedBundleID, err)
	}
	return outcome, nil
}

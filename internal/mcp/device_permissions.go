package mcp

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DevicePermissionValues are the states the worker's /set_permissions accepts.
var DevicePermissionValues = []string{"allow", "deny", "unset"}

// IOSGrantablePermissions are the names iOS simulators set through simctl privacy.
var IOSGrantablePermissions = []string{"calendar", "contacts", "location", "medialibrary", "microphone", "motion", "photos", "reminders", "siri"}

// AndroidPermissionGroups are the names Android workers map to permissions.
var AndroidPermissionGroups = []string{"bluetooth", "calendar", "camera", "contacts", "location", "medialibrary", "microphone", "notifications", "phone", "sms", "storage"}

var devicePermissionGroupNames = []string{
	"all", "bluetooth", "calendar", "camera", "contacts", "health", "homekit",
	"location", "medialibrary", "microphone", "motion", "notifications", "phone",
	"photos", "reminders", "siri", "sms", "speech", "storage", "userTracking",
}

var androidPermissionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*\.permission\.[A-Z][A-Z0-9_]{0,127}$`)

var iosMicrophoneRefusalHint = map[string]string{
	"microphone": "Leave microphone out, or set it to allow.",
	"all":        "all includes microphone; name the permissions to change instead.",
}

var iosAlertOnlyPermissions = map[string]string{
	"notifications": "notifications",
	"userTracking":  "App Tracking Transparency (userTracking)",
}

// ParseDevicePermissionAssignments parses name=value arguments into a permission map.
func ParseDevicePermissionAssignments(assignments []string) (map[string]string, error) {
	if len(assignments) == 0 {
		return nil, fmt.Errorf("name at least one permission, for example location=allow")
	}
	permissions := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		name, value, found := strings.Cut(strings.TrimSpace(assignment), "=")
		name, value = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(value))
		if !found || name == "" || value == "" {
			return nil, fmt.Errorf("permission %q must be name=value, for example location=allow", assignment)
		}
		if !slices.Contains(devicePermissionGroupNames, name) && !androidPermissionNamePattern.MatchString(name) {
			return nil, fmt.Errorf("unknown permission %q; use one of %s, or a fully qualified Android permission", name, strings.Join(devicePermissionGroupNames, ", "))
		}
		if !slices.Contains(DevicePermissionValues, value) {
			return nil, fmt.Errorf("permission %s has value %q; use %s", name, value, strings.Join(DevicePermissionValues, ", "))
		}
		if _, repeated := permissions[name]; repeated {
			return nil, fmt.Errorf("permission %s is given more than once", name)
		}
		permissions[name] = value
	}
	return permissions, nil
}

// ValidateDevicePermissionsForPlatform rejects permissions unsupported on platform.
func ValidateDevicePermissionsForPlatform(platform string, permissions map[string]string) error {
	if platform != "ios" && platform != "android" {
		return fmt.Errorf("app permissions can be set only on iOS and Android sessions, not %s", platform)
	}
	names := make([]string, 0, len(permissions))
	for name := range permissions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if platform == "ios" && (name == "microphone" || name == "all") && permissions[name] != "allow" {
			return fmt.Errorf("iOS sessions cannot set %s=%s: Revyl's audio injection grants the iOS microphone on every app launch, so a deny or reset would not hold. %s", name, permissions[name], iosMicrophoneRefusalHint[name])
		}
		if name == "all" {
			continue
		}
		if platform == "android" {
			if !slices.Contains(AndroidPermissionGroups, name) && !androidPermissionNamePattern.MatchString(name) {
				return fmt.Errorf("Android sessions cannot set %s; use all, %s, or a fully qualified Android permission", name, strings.Join(AndroidPermissionGroups, ", "))
			}
			continue
		}
		if label, alertOnly := iosAlertOnlyPermissions[name]; alertOnly {
			return fmt.Errorf("iOS simulators cannot pre-grant %s; let the app show its alert and answer it (the Revyl Maestro driver answers it for you). iOS pre-grants only all, %s", label, strings.Join(IOSGrantablePermissions, ", "))
		}
		if androidPermissionNamePattern.MatchString(name) {
			return fmt.Errorf("%s is an Android permission; iOS sessions take all, %s", name, strings.Join(IOSGrantablePermissions, ", "))
		}
		if !slices.Contains(IOSGrantablePermissions, name) {
			return fmt.Errorf("iOS simulators cannot set %s; iOS sessions take all, %s", name, strings.Join(IOSGrantablePermissions, ", "))
		}
	}
	return nil
}

// SetAppPermissions applies permissions via the worker /set_permissions action.
func (m *DeviceSessionManager) SetAppPermissions(ctx context.Context, session *DeviceSession, bundleID string, permissions map[string]string) error {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return fmt.Errorf("bundle ID is required: name the app whose permissions change")
	}
	if err := ValidateDevicePermissionsForPlatform(session.Platform, permissions); err != nil {
		return err
	}
	respBody, err := m.WorkerRequestOnSession(ctx, session, "/set_permissions", map[string]interface{}{
		"bundle_id":   bundleID,
		"permissions": permissions,
	})
	if err != nil {
		return err
	}
	return EnsureWorkerActionSucceeded(respBody, "set_permissions")
}

type DeviceSetPermissionsInput struct {
	BundleID     string            `json:"bundle_id" jsonschema:"Bundle ID or package name of the app whose permissions change (REQUIRED)"`
	Permissions  map[string]string `json:"permissions" jsonschema:"Permission names mapped to allow, deny, or unset, e.g. {\"location\": \"allow\", \"motion\": \"allow\"} (REQUIRED)"`
	SessionIndex *int              `json:"session_index,omitempty" jsonschema:"Session index to target. Omit for active session."`
	SessionID    string            `json:"session_id,omitempty" jsonschema:"Server-issued session ID from start_device_session or list_device_sessions. Wins over session_index and never falls back to the active session, so pass it whenever several sessions are live."`
}

type DeviceSetPermissionsOutput struct {
	Success     bool              `json:"success"`
	BundleID    string            `json:"bundle_id,omitempty"`
	Permissions map[string]string `json:"permissions,omitempty"`
	Error       string            `json:"error,omitempty"`
	NextSteps   []NextStep        `json:"next_steps,omitempty"`
}

func (s *Server) handleDeviceSetPermissions(ctx context.Context, req *mcp.CallToolRequest, input DeviceSetPermissionsInput) (*mcp.CallToolResult, DeviceSetPermissionsOutput, error) {
	assignments := make([]string, 0, len(input.Permissions))
	for name, value := range input.Permissions {
		assignments = append(assignments, name+"="+value)
	}
	sort.Strings(assignments)
	permissions, err := ParseDevicePermissionAssignments(assignments)
	if err != nil {
		return nil, DeviceSetPermissionsOutput{Success: false, BundleID: input.BundleID, Error: err.Error()}, nil
	}
	session, err := s.resolveToolSession(ctx, input.SessionIndex, input.SessionID)
	if err != nil {
		return nil, DeviceSetPermissionsOutput{Success: false, BundleID: input.BundleID, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	s.sessionMgr.ResetIdleTimer(session.Index)
	if err := s.sessionMgr.SetAppPermissions(ctx, session, input.BundleID, permissions); err != nil {
		return nil, DeviceSetPermissionsOutput{Success: false, BundleID: input.BundleID, Error: err.Error(), NextSteps: errorNextSteps(err)}, nil
	}
	return nil, DeviceSetPermissionsOutput{
		Success:     true,
		BundleID:    strings.TrimSpace(input.BundleID),
		Permissions: permissions,
		NextSteps: []NextStep{
			{Tool: "launch_app", Reason: "Launch the app with these permissions in place"},
		},
	}, nil
}

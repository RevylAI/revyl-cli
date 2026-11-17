package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

var devicePermissionsCmd = &cobra.Command{
	Use:   "permissions",
	Short: "Set an app's OS permissions on the session's device",
}

var devicePermissionsSetCmd = &cobra.Command{
	Use:   "set <name=value>...",
	Short: "Allow, deny, or reset app permissions so OS prompts don't appear",
	Long: `Set OS permissions for one app on the session's device. Each argument is
name=value, where value is allow, deny, or unset.

iOS simulators pre-grant calendar, contacts, location (Always), medialibrary,
microphone, motion, photos, reminders, and siri. They cannot pre-grant
notifications or App Tracking Transparency (userTracking): let the app show
those alerts and answer them. iOS also refuses microphone=deny|unset and
all=deny|unset, because Revyl's audio injection grants the microphone on every
app launch. Android takes bluetooth, calendar, camera,
contacts, location, medialibrary, microphone, notifications, phone, sms,
storage, or fully qualified permissions such as android.permission.CAMERA, and
skips any the app doesn't request. Both take all.

Setting a permission stops the app if it is running. On iOS, set permissions
before the app's first launch: start the session without an app, install the
app, set permissions, then launch it.`,
	Example: `  revyl device start --platform ios
  revyl device install --app-id <app-id>
  revyl device permissions set --bundle-id com.example.app location=allow motion=allow
  revyl device launch com.example.app`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		permissions, err := mcppkg.ParseDevicePermissionAssignments(args)
		if err != nil {
			return err
		}
		bundleID, _ := cmd.Flags().GetString("bundle-id")
		bundleID = strings.TrimSpace(bundleID)
		if bundleID == "" {
			return fmt.Errorf("--bundle-id is required: name the app whose permissions change, for example --bundle-id com.example.app")
		}
		mgr, err := getDeviceSessionMgr(cmd)
		if err != nil {
			return err
		}
		session, err := resolveSessionTarget(cmd, mgr)
		if err != nil {
			return err
		}
		completion := analytics.CommandCompletion{
			Domain:       "device_permissions_set",
			DomainStatus: "applied",
			Properties: map[string]interface{}{
				"platform":         session.Platform,
				"permission_count": len(permissions),
			},
		}
		if err := mgr.SetAppPermissions(cmd.Context(), session, bundleID, permissions); err != nil {
			completion.DomainStatus = "failed"
			analytics.SetCommandCompletion(cmd.Context(), completion)
			return err
		}
		analytics.SetCommandCompletion(cmd.Context(), completion)

		names := make([]string, 0, len(permissions))
		for name, value := range permissions {
			names = append(names, name+"="+value)
		}
		sort.Strings(names)
		jsonOrPrint(cmd, map[string]interface{}{
			"bundle_id":   bundleID,
			"permissions": permissions,
			"status":      "applied",
		}, fmt.Sprintf("Set %s for %s", strings.Join(names, ", "), bundleID))
		return nil
	},
}

func init() {
	devicePermissionsSetCmd.Flags().String("bundle-id", "", "Bundle ID or package name of the app whose permissions change (required)")
	devicePermissionsSetCmd.Flags().Bool("json", false, "Output as JSON")
	registerSessionTargetFlags(devicePermissionsSetCmd)
	devicePermissionsCmd.AddCommand(devicePermissionsSetCmd)
	deviceCmd.AddCommand(devicePermissionsCmd)
}

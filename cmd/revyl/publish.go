// Package main provides publish commands for distributing apps to App Store / Google Play.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/asc"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/store"
	"github.com/revyl/cli/internal/ui"
)

// publishCmd is the parent command for app store publishing operations.
var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish to App Store / Google Play",
	Long: `Publish apps to App Store Connect and Google Play.

COMMANDS:
  auth        - Configure store credentials
  testflight  - Distribute to TestFlight
  playstore   - Distribute to Google Play internal testing
  status      - Check build processing / review status`,
}

// --- Auth subcommands ---

var publishAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Configure store credentials",
	Long: `Configure credentials for App Store Connect and Google Play.

COMMANDS:
  ios      - Set up App Store Connect API key
  android  - Set up Google Play service account
  status   - Show configured store credentials`,
}

var publishAuthIOSCmd = &cobra.Command{
	Use:   "ios",
	Short: "Set up App Store Connect API key",
	Long: `Configure App Store Connect API credentials.

Generate API keys at: https://appstoreconnect.apple.com/access/integrations/api

EXAMPLE:
  revyl publish auth ios --key-id ABC123 --issuer-id DEF456 --private-key ./AuthKey.p8`,
	RunE: runPublishAuthIOS,
}

var publishAuthAndroidCmd = &cobra.Command{
	Use:   "android",
	Short: "Set up Google Play service account",
	Long: `Configure Google Play Developer API credentials.

Create a service account at: https://console.cloud.google.com/iam-admin/serviceaccounts

EXAMPLE:
  revyl publish auth android --service-account ./service-account.json`,
	RunE: runPublishAuthAndroid,
}

var publishAuthStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show configured store credentials",
	RunE:  runPublishAuthStatus,
}

// --- TestFlight command ---

var publishTestFlightCmd = &cobra.Command{
	Use:   "testflight",
	Short: "Distribute build to TestFlight",
	Long: `Upload and distribute an IPA to TestFlight beta groups.

The command will:
  1. Upload the IPA to App Store Connect (if --ipa is provided)
  2. Wait for build processing
  3. Distribute to the specified beta group(s)

EXAMPLES:
  # Distribute latest build to default group
  revyl publish testflight

  # Upload and distribute
  revyl publish testflight --ipa ./build/MyApp.ipa --group "Beta Testers"

  # Multiple groups
  revyl publish testflight --ipa ./build/MyApp.ipa --group "Internal,External"`,
	RunE: runPublishTestFlight,
}

// --- Play Store command ---

var publishPlayStoreCmd = &cobra.Command{
	Use:   "playstore",
	Short: "Distribute build to Google Play",
	Long: `Upload an AAB to Google Play internal testing.

EXAMPLES:
  # Upload to internal testing (default)
  revyl publish playstore --aab ./build/app-release.aab

  # Upload to a specific track
  revyl publish playstore --aab ./build/app-release.aab --track alpha`,
	RunE: runPublishPlayStore,
}

// --- Status command ---

var publishStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check build processing / review status",
	Long: `Check the processing or review status of your latest build.

EXAMPLES:
  revyl publish status
  revyl publish status --platform ios
  revyl publish status --build-id BUILD_ID`,
	RunE: runPublishStatus,
}

func init() {
	// Auth subcommands
	publishAuthIOSCmd.Flags().String("key-id", "", "App Store Connect API Key ID (required)")
	publishAuthIOSCmd.Flags().String("issuer-id", "", "App Store Connect Issuer ID (required)")
	publishAuthIOSCmd.Flags().String("private-key", "", "Path to .p8 private key file (required)")

	publishAuthAndroidCmd.Flags().String("service-account", "", "Path to service account JSON key file (required)")

	publishAuthCmd.AddCommand(publishAuthIOSCmd)
	publishAuthCmd.AddCommand(publishAuthAndroidCmd)
	publishAuthCmd.AddCommand(publishAuthStatusCmd)

	// TestFlight flags
	publishTestFlightCmd.Flags().String("ipa", "", "Path to the .ipa file to upload")
	publishTestFlightCmd.Flags().String("group", "", "TestFlight beta group name(s), comma-separated")
	publishTestFlightCmd.Flags().String("whats-new", "", "\"What to Test\" notes")
	publishTestFlightCmd.Flags().String("version", "", "Version string (auto-extracted from IPA if not specified)")
	publishTestFlightCmd.Flags().String("build-number", "", "Build number (auto-extracted from IPA if not specified)")
	publishTestFlightCmd.Flags().String("app-id", "", "App Store Connect app ID (auto-resolved from config if not specified)")
	publishTestFlightCmd.Flags().Bool("wait", true, "Wait for build processing to complete")
	publishTestFlightCmd.Flags().Duration("timeout", 30*time.Minute, "Timeout for build processing")

	// Play Store flags
	publishPlayStoreCmd.Flags().String("aab", "", "Path to the .aab file to upload (required)")
	publishPlayStoreCmd.Flags().String("track", "", "Release track (internal, alpha, beta, production)")
	publishPlayStoreCmd.Flags().String("package-name", "", "Android package name (auto-resolved from config if not specified)")

	// Status flags
	publishStatusCmd.Flags().String("platform", "", "Platform to check (ios, android)")
	publishStatusCmd.Flags().String("build-id", "", "Specific build ID to check")
	publishStatusCmd.Flags().String("app-id", "", "App Store Connect app ID (auto-resolved from config)")

	// Attach all to publish
	publishCmd.AddCommand(publishAuthCmd)
	publishCmd.AddCommand(publishTestFlightCmd)
	publishCmd.AddCommand(publishPlayStoreCmd)
	publishCmd.AddCommand(publishStatusCmd)
}

// --- Command implementations ---

// runPublishAuthIOS stores App Store Connect API credentials.
func runPublishAuthIOS(cmd *cobra.Command, args []string) error {
	keyID, _ := cmd.Flags().GetString("key-id")
	issuerID, _ := cmd.Flags().GetString("issuer-id")
	privateKeyPath, _ := cmd.Flags().GetString("private-key")

	if keyID == "" || issuerID == "" || privateKeyPath == "" {
		ui.PrintError("All flags are required: --key-id, --issuer-id, --private-key")
		ui.PrintInfo("Generate API keys at: https://appstoreconnect.apple.com/access/integrations/api")
		return fmt.Errorf("missing required flags")
	}

	// Resolve absolute path for the private key
	absPath, err := filepath.Abs(privateKeyPath)
	if err != nil {
		return fmt.Errorf("failed to resolve private key path: %w", err)
	}

	// Validate the key file exists and is readable
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		ui.PrintError("Private key file not found: %s", absPath)
		return fmt.Errorf("private key file not found")
	}

	// Validate the key can be loaded
	if _, err := asc.LoadPrivateKey(absPath); err != nil {
		ui.PrintError("Invalid private key: %s", err)
		return fmt.Errorf("invalid private key: %w", err)
	}

	mgr := store.NewManager()
	if err := mgr.SaveIOSCredentials(&store.IOSCredentials{
		KeyID:          keyID,
		IssuerID:       issuerID,
		PrivateKeyPath: absPath,
	}); err != nil {
		ui.PrintError("Failed to save iOS credentials: %s", err)
		return err
	}

	ui.PrintSuccess("App Store Connect credentials saved")
	ui.PrintInfo("Key ID: %s", keyID)
	ui.PrintInfo("Issuer ID: %s", issuerID)
	ui.PrintInfo("Private Key: %s", absPath)

	return nil
}

// runPublishAuthAndroid stores Google Play service account credentials.
func runPublishAuthAndroid(cmd *cobra.Command, args []string) error {
	serviceAccountPath, _ := cmd.Flags().GetString("service-account")

	if serviceAccountPath == "" {
		ui.PrintError("--service-account flag is required")
		ui.PrintInfo("Create a service account at: https://console.cloud.google.com/iam-admin/serviceaccounts")
		return fmt.Errorf("missing required flag")
	}

	absPath, err := filepath.Abs(serviceAccountPath)
	if err != nil {
		return fmt.Errorf("failed to resolve service account path: %w", err)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		ui.PrintError("Service account file not found: %s", absPath)
		return fmt.Errorf("service account file not found")
	}

	mgr := store.NewManager()
	if err := mgr.SaveAndroidCredentials(&store.AndroidCredentials{
		ServiceAccountPath: absPath,
	}); err != nil {
		ui.PrintError("Failed to save Android credentials: %s", err)
		return err
	}

	ui.PrintSuccess("Google Play credentials saved")
	ui.PrintInfo("Service Account: %s", absPath)

	return nil
}

// runPublishAuthStatus shows the current store credential status.
func runPublishAuthStatus(cmd *cobra.Command, args []string) error {
	mgr := store.NewManager()

	fmt.Println()
	ui.PrintInfo("Store Credentials Status")
	fmt.Println()

	// iOS
	if mgr.HasIOSCredentials() {
		creds, _ := mgr.Load()
		ui.PrintSuccess("iOS (App Store Connect): Configured")
		ui.PrintInfo("  Key ID:      %s", creds.IOS.KeyID)
		ui.PrintInfo("  Issuer ID:   %s", creds.IOS.IssuerID)
		ui.PrintInfo("  Private Key: %s", creds.IOS.PrivateKeyPath)

		if err := mgr.ValidateIOSCredentials(); err != nil {
			ui.PrintWarning("  Validation: %s", err)
		}
	} else {
		ui.PrintWarning("iOS (App Store Connect): Not configured")
		ui.PrintInfo("  Run: revyl publish auth ios --key-id ... --issuer-id ... --private-key ...")
	}

	fmt.Println()

	// Android
	if mgr.HasAndroidCredentials() {
		creds, _ := mgr.Load()
		ui.PrintSuccess("Android (Google Play): Configured")
		ui.PrintInfo("  Service Account: %s", creds.Android.ServiceAccountPath)

		if err := mgr.ValidateAndroidCredentials(); err != nil {
			ui.PrintWarning("  Validation: %s", err)
		}
	} else {
		ui.PrintWarning("Android (Google Play): Not configured")
		ui.PrintInfo("  Run: revyl publish auth android --service-account ...")
	}

	fmt.Println()
	return nil
}

// runPublishTestFlight uploads and distributes a build to TestFlight.
func runPublishTestFlight(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Validate iOS credentials
	storeMgr := store.NewManager()
	if err := storeMgr.ValidateIOSCredentials(); err != nil {
		return err
	}

	creds, err := storeMgr.Load()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	// Resolve app ID from flag or config
	appID, _ := cmd.Flags().GetString("app-id")
	if appID == "" {
		appID = resolveASCAppID()
	}
	if appID == "" {
		return fmt.Errorf("app ID is required: use --app-id flag or set publish.ios.asc_app_id in .revyl/config.yaml")
	}

	// Create ASC client
	client, err := asc.NewClient(creds.IOS.KeyID, creds.IOS.IssuerID, creds.IOS.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("failed to create ASC client: %w", err)
	}

	// Handle IPA upload if specified
	ipaPath, _ := cmd.Flags().GetString("ipa")
	var buildID string

	if ipaPath != "" {
		version, _ := cmd.Flags().GetString("version")
		buildNumber, _ := cmd.Flags().GetString("build-number")

		if version == "" || buildNumber == "" {
			ui.PrintWarning("--version and --build-number are recommended for upload tracking")
			if version == "" {
				version = "1.0.0"
			}
			if buildNumber == "" {
				buildNumber = fmt.Sprintf("%d", time.Now().Unix())
			}
		}

		ui.PrintInfo("Uploading %s to App Store Connect...", filepath.Base(ipaPath))

		uploadID, err := client.UploadIPA(ctx, appID, ipaPath, version, buildNumber)
		if err != nil {
			ui.PrintError("Upload failed: %s", err)
			return err
		}

		ui.PrintSuccess("Upload complete (ID: %s)", uploadID)

		// Wait for processing
		shouldWait, _ := cmd.Flags().GetBool("wait")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		if shouldWait {
			ui.PrintInfo("Waiting for build processing...")

			// Find the build by polling builds list
			var build *asc.Build
			deadline := time.Now().Add(timeout)
			for {
				builds, err := client.ListBuilds(ctx, appID, 5)
				if err != nil {
					return fmt.Errorf("failed to list builds: %w", err)
				}

				if len(builds) > 0 {
					latest := builds[0]
					if latest.Attributes.ProcessingState == asc.ProcessingStateValid {
						build = &latest
						buildID = latest.ID
						break
					}
					if latest.Attributes.ProcessingState == asc.ProcessingStateFailed ||
						latest.Attributes.ProcessingState == asc.ProcessingStateInvalid {
						return fmt.Errorf("build processing failed (state: %s)", latest.Attributes.ProcessingState)
					}
				}

				if time.Now().After(deadline) {
					return fmt.Errorf("timed out waiting for build processing")
				}

				ui.PrintInfo("Still processing...")
				time.Sleep(30 * time.Second)
			}

			ui.PrintSuccess("Build processed: %s (v%s)", build.ID, build.Attributes.Version)
		}
	}

	// If no upload, find the latest build
	if buildID == "" {
		builds, err := client.ListBuilds(ctx, appID, 1)
		if err != nil {
			return fmt.Errorf("failed to list builds: %w", err)
		}
		if len(builds) == 0 {
			return fmt.Errorf("no builds found for app %s", appID)
		}
		buildID = builds[0].ID
		ui.PrintInfo("Using latest build: %s (v%s)", buildID, builds[0].Attributes.Version)
	}

	// Set "What to Test" if specified
	whatsNew, _ := cmd.Flags().GetString("whats-new")
	if whatsNew != "" {
		if err := client.SetWhatsNewForBuild(ctx, buildID, whatsNew, "en-US"); err != nil {
			ui.PrintWarning("Failed to set 'What to Test': %s", err)
		}
	}

	// Resolve beta groups
	groupNames := resolveTestFlightGroups(cmd)
	if len(groupNames) == 0 {
		ui.PrintWarning("No TestFlight groups specified. Build uploaded but not distributed.")
		ui.PrintInfo("Use --group to specify groups, or set publish.ios.testflight_groups in config")
		return nil
	}

	// Find and assign beta groups
	for _, groupName := range groupNames {
		groupName = strings.TrimSpace(groupName)
		if groupName == "" {
			continue
		}

		group, err := client.FindBetaGroupByName(ctx, appID, groupName)
		if err != nil {
			ui.PrintError("Failed to find beta group '%s': %s", groupName, err)
			continue
		}
		if group == nil {
			ui.PrintWarning("Beta group '%s' not found", groupName)
			continue
		}

		if err := client.AddBuildToBetaGroup(ctx, group.ID, buildID); err != nil {
			ui.PrintError("Failed to add build to group '%s': %s", groupName, err)
			continue
		}

		ui.PrintSuccess("Distributed to TestFlight group: %s", groupName)
	}

	return nil
}

// runPublishPlayStore uploads and distributes a build to Google Play.
func runPublishPlayStore(cmd *cobra.Command, args []string) error {
	ui.PrintWarning("Google Play publishing is not yet implemented")
	ui.PrintInfo("Coming soon. For now, use 'eas submit' or the Play Console.")
	return nil
}

// runPublishStatus checks build processing/review status.
func runPublishStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	platform, _ := cmd.Flags().GetString("platform")
	if platform == "" {
		platform = "ios" // Default to iOS
	}

	if platform != "ios" {
		ui.PrintWarning("Only iOS status is currently supported")
		return nil
	}

	storeMgr := store.NewManager()
	if err := storeMgr.ValidateIOSCredentials(); err != nil {
		return err
	}

	creds, err := storeMgr.Load()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	appID, _ := cmd.Flags().GetString("app-id")
	if appID == "" {
		appID = resolveASCAppID()
	}
	if appID == "" {
		return fmt.Errorf("app ID is required: use --app-id flag or set publish.ios.asc_app_id in .revyl/config.yaml")
	}

	client, err := asc.NewClient(creds.IOS.KeyID, creds.IOS.IssuerID, creds.IOS.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("failed to create ASC client: %w", err)
	}

	// Check specific build or latest
	buildID, _ := cmd.Flags().GetString("build-id")

	fmt.Println()
	ui.PrintInfo("App Store Connect Status")
	fmt.Println()

	if buildID != "" {
		build, err := client.GetBuild(ctx, buildID)
		if err != nil {
			return fmt.Errorf("failed to get build: %w", err)
		}
		printBuildStatus(build)
	} else {
		// Show latest builds
		builds, err := client.ListBuilds(ctx, appID, 5)
		if err != nil {
			return fmt.Errorf("failed to list builds: %w", err)
		}

		if len(builds) == 0 {
			ui.PrintWarning("No builds found")
			return nil
		}

		for _, build := range builds {
			printBuildStatus(&build)
			fmt.Println()
		}
	}

	// Show App Store version status
	versions, err := client.ListAppStoreVersions(ctx, appID)
	if err == nil && len(versions) > 0 {
		fmt.Println()
		ui.PrintInfo("App Store Versions")
		for _, v := range versions {
			stateIcon := "  "
			switch v.Attributes.AppStoreState {
			case asc.AppStoreStateReadyForSale:
				stateIcon = "✓ "
			case asc.AppStoreStateWaitingForReview, asc.AppStoreStateInReview:
				stateIcon = "⏳"
			case asc.AppStoreStateRejected:
				stateIcon = "✗ "
			}
			fmt.Printf("  %s %s (%s) - %s\n", stateIcon, v.Attributes.VersionString, v.Attributes.Platform, v.Attributes.AppStoreState)
		}
	}

	fmt.Println()
	return nil
}

// --- Helper functions ---

// printBuildStatus displays a build's status.
func printBuildStatus(build *asc.Build) {
	stateIcon := "  "
	switch build.Attributes.ProcessingState {
	case asc.ProcessingStateValid:
		stateIcon = "✓ "
	case asc.ProcessingStateProcessing:
		stateIcon = "⏳"
	case asc.ProcessingStateFailed, asc.ProcessingStateInvalid:
		stateIcon = "✗ "
	}

	uploadDate := "unknown"
	if build.Attributes.UploadedDate != nil {
		uploadDate = build.Attributes.UploadedDate.Format("2006-01-02 15:04")
	}

	fmt.Printf("  %s Build %s (v%s) - %s - Uploaded: %s\n",
		stateIcon, build.ID, build.Attributes.Version,
		build.Attributes.ProcessingState, uploadDate)
}

// resolveASCAppID tries to find the ASC app ID from project config.
func resolveASCAppID() string {
	cfg, err := config.LoadProjectConfig(".revyl/config.yaml")
	if err != nil {
		return ""
	}
	return cfg.Publish.IOS.ASCAppID
}

// resolveTestFlightGroups resolves TestFlight group names from flag or config.
func resolveTestFlightGroups(cmd *cobra.Command) []string {
	groupFlag, _ := cmd.Flags().GetString("group")
	if groupFlag != "" {
		return strings.Split(groupFlag, ",")
	}

	// Fall back to config
	cfg, err := config.LoadProjectConfig(".revyl/config.yaml")
	if err != nil {
		return nil
	}
	return cfg.Publish.IOS.TestFlightGroups
}

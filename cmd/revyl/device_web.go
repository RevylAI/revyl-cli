package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

func parseWebViewport(raw string) (*api.DeviceWebViewport, error) {
	parts := strings.Split(strings.TrimSpace(raw), "x")
	if len(parts) != 2 {
		return nil, fmt.Errorf("--viewport must be WIDTHxHEIGHT (width 320–3840, height 240–2160)")
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width < 320 || width > 3840 || height < 240 || height > 2160 {
		return nil, fmt.Errorf("--viewport must be WIDTHxHEIGHT (width 320–3840, height 240–2160)")
	}
	return &api.DeviceWebViewport{Width: &width, Height: &height}, nil
}

func runWebDeviceStart(cmd *cobra.Command) error {
	for _, name := range []string{"app-url", "app-link", "launch-var", "launch-arg-set", "launch-arg", "no-inherited-launch-vars", "locale", "orientation", "device", "device-model", "os-version", "device-name"} {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--%s is not available on platform web", name)
		}
	}
	appID, _ := cmd.Flags().GetString("app-id")
	app, _ := cmd.Flags().GetString("app")
	if app != "" {
		if appID != "" && appID != app {
			return fmt.Errorf("--app and --app-id must name the same app")
		}
		appID = app
	}
	if strings.TrimSpace(appID) == "" {
		configuredAppID, err := resolveConfiguredWebAppID()
		if err != nil {
			return err
		}
		if configuredAppID != "" {
			appID = configuredAppID
			recordAppliedDefault(cmd, appliedDefault{Flag: "app", Value: appID, Reason: "project_web_profile"})
		}
	}
	buildID, _ := cmd.Flags().GetString("build-version-id")
	webURL, _ := cmd.Flags().GetString("url")
	commit, _ := cmd.Flags().GetString("commit")
	if cmd.Flags().Changed("url") && strings.TrimSpace(webURL) == "" {
		return fmt.Errorf("--url requires an HTTPS URL")
	}
	viewportFlag, _ := cmd.Flags().GetString("viewport")
	viewport, err := parseWebViewport(viewportFlag)
	if err != nil {
		return err
	}
	branchName, _ := cmd.Flags().GetString("branch")
	if cmd.Flags().Changed("branch") && branchName == "" {
		return fmt.Errorf("--branch requires a Revyl branch name")
	}
	buildSelection, _ := cmd.Flags().GetString("build-selection")
	buildSelection, err = resolveCommandAllBranchesSelection(cmd, buildSelection, "branch", "build-version-id", "url")
	if err != nil {
		return err
	}
	launchEnv, secretLaunchEnv, err := parseWebLaunchEnv(getExactStringArrayFlag(cmd, "launch-env"))
	if err != nil {
		return err
	}
	opts := mcppkg.StartSessionOptions{Platform: "web", AppID: strings.TrimSpace(appID), BranchName: branchName, BuildSelection: buildSelection, BuildVersionID: strings.TrimSpace(buildID), WebURL: webURL, WebCommitSHA: strings.TrimSpace(commit), LaunchEnv: launchEnv, Web: &api.DeviceWebOptions{SecretEnvVars: secretLaunchEnv, Viewport: viewport}}
	if err := mcppkg.ValidateWebSessionStartOptions(opts); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	project, err := config.ResolveDeviceConfigContext(cwd)
	if err != nil {
		return actionableLocalConfigError(err)
	}
	sessionConfig, err := project.ReadSession()
	if err != nil {
		return actionableLocalConfigError(err)
	}
	opts.Web.SignIn = mcppkg.WebSignInFromSession(&sessionConfig)
	timeout, _ := cmd.Flags().GetInt("timeout")
	if !cmd.Flags().Changed("timeout") {
		timeout = 600
		reason := "platform_default"
		if sessionConfig.IdleTimeoutSeconds != nil {
			timeout = *sessionConfig.IdleTimeoutSeconds
			reason = "project_session"
		}
		recordAppliedDefault(cmd, appliedDefault{Flag: "timeout", Value: strconv.Itoa(timeout), Reason: reason})
	}
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be positive on platform web")
	}
	if !cmd.Flags().Changed("viewport") {
		recordAppliedDefault(cmd, appliedDefault{Flag: "viewport", Value: "1280x800", Reason: "platform_default"})
	}
	opts.IdleTimeout = time.Duration(timeout) * time.Second
	mgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_, session, err := mgr.StartSession(ctx, opts)
	if err != nil {
		return err
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(withAppliedDefaults(cmd, session)); err != nil {
			return err
		}
	} else {
		ui.PrintSuccess("Device ready! Session %d (web)", session.Index)
		ui.PrintLink("Session", session.SessionID)
		ui.PrintLink("Live View", session.ViewerURL)
	}
	openBrowser, _ := cmd.Flags().GetBool("open")
	if openBrowser {
		_ = ui.OpenBrowser(session.ViewerURL)
	}
	return nil
}

// parseWebLaunchEnv splits Web --launch-env entries into plain KEY=VALUE
// overrides and secret NAME overrides, whose values come from the local
// environment variable NAME so they never appear on argv or in output.
func parseWebLaunchEnv(entries []string) (map[string]string, map[string]string, error) {
	var plainEntries []string
	secret := map[string]string{}
	for _, entry := range entries {
		if strings.Contains(entry, "=") {
			plainEntries = append(plainEntries, entry)
			continue
		}
		name := strings.TrimSpace(entry)
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			return nil, nil, fmt.Errorf("--launch-env %s reads the local environment variable %s, which is unset or empty", name, name)
		}
		secret[name] = value
	}
	plain, err := parseLaunchEnvVars(plainEntries)
	if err != nil {
		return nil, nil, err
	}
	if len(secret) == 0 {
		secret = nil
	}
	return plain, secret, nil
}

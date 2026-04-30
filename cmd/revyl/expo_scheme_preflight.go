package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

type expoSchemePreflightError struct {
	summary string
	details []string
}

func (e *expoSchemePreflightError) Error() string {
	return e.summary
}

func printExpoSchemePreflightError(err error) {
	var schemeErr *expoSchemePreflightError
	if !errors.As(err, &schemeErr) {
		ui.PrintError("%v", err)
		return
	}

	ui.PrintError("%s", schemeErr.summary)
	for _, line := range schemeErr.details {
		if strings.TrimSpace(line) == "" {
			ui.Println()
			continue
		}
		ui.PrintDim("  %s", line)
	}
}

func ensureExpoDevClientSchemeForBuild(cwd string, cfg *config.ProjectConfig) (bool, error) {
	if cfg == nil || !isExpoBuildSystem(cfg.Build.System) {
		return false, nil
	}

	native, err := detectExpoNativeScheme(cwd)
	if err != nil {
		return false, err
	}

	expoCfg := cfg.HotReload.GetProviderConfig("expo")
	revylScheme := ""
	if expoCfg != nil {
		revylScheme = strings.TrimSpace(expoCfg.AppScheme)
	}

	if native.scheme != "" {
		if revylScheme != "" && revylScheme != native.scheme {
			return false, newExpoSchemeMismatchError(native.scheme, revylScheme)
		}
		if revylScheme == "" {
			if cfg.HotReload.Providers == nil {
				cfg.HotReload.Providers = make(map[string]*config.ProviderConfig)
			}
			if expoCfg == nil {
				expoCfg = &config.ProviderConfig{}
			}
			expoCfg.AppScheme = native.scheme
			cfg.HotReload.Providers["expo"] = expoCfg
			if strings.TrimSpace(cfg.HotReload.Default) == "" {
				cfg.HotReload.Default = "expo"
			}
			ui.PrintDim("Detected Expo scheme %q from app.json and saved it for hot reload.", native.scheme)
			return true, nil
		}
		return false, nil
	}

	if revylScheme != "" {
		if native.hasDynamicConfig {
			ui.PrintDim("Using Expo scheme %q from .revyl/config.yaml; make sure app.config.js/ts exports the same scheme before building.", revylScheme)
		}
		return false, nil
	}

	return false, newMissingExpoSchemeError(native)
}

type expoNativeScheme struct {
	scheme           string
	hasAppJSON       bool
	hasDynamicConfig bool
}

func detectExpoNativeScheme(cwd string) (expoNativeScheme, error) {
	result := expoNativeScheme{
		hasDynamicConfig: hasExpoDynamicConfig(cwd),
	}

	appJSONPath := filepath.Join(cwd, "app.json")
	data, err := os.ReadFile(appJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, &expoSchemePreflightError{
			summary: "Could not read Expo app.json before creating a dev build",
			details: []string{
				fmt.Sprintf("Revyl tried to inspect %s to find expo.scheme.", appJSONPath),
				fmt.Sprintf("Fix the file read error, or pass the scheme explicitly with: revyl init --provider expo --hotreload-app-scheme <scheme>"),
			},
		}
	}

	result.hasAppJSON = true
	var parsed struct {
		Expo struct {
			Scheme json.RawMessage `json:"scheme"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return result, &expoSchemePreflightError{
			summary: "Could not parse Expo app.json before creating a dev build",
			details: []string{
				"Revyl needs expo.scheme before building because hot reload opens the Expo dev client with a deep link.",
				fmt.Sprintf("Fix %s JSON, then run the build again.", appJSONPath),
			},
		}
	}

	result.scheme = strings.TrimSpace(parseExpoSchemeValue(parsed.Expo.Scheme))
	return result, nil
}

func hasExpoDynamicConfig(cwd string) bool {
	for _, name := range []string{"app.config.js", "app.config.ts"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
			return true
		}
	}
	return false
}

func parseExpoSchemeValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var scheme string
	if err := json.Unmarshal(raw, &scheme); err == nil {
		return scheme
	}

	var schemes []string
	if err := json.Unmarshal(raw, &schemes); err == nil {
		for _, candidate := range schemes {
			if trimmed := strings.TrimSpace(candidate); trimmed != "" {
				return trimmed
			}
		}
	}

	return ""
}

func newMissingExpoSchemeError(native expoNativeScheme) *expoSchemePreflightError {
	details := []string{
		"An Expo app URL scheme is the custom URL prefix baked into your dev client, like myapp-dev://.",
		"Revyl needs it to open the Expo dev client on a cloud device so hot reload can connect.",
		"",
	}

	if native.hasDynamicConfig {
		details = append(details,
			"Revyl could not auto-detect it because this project uses app.config.js or app.config.ts.",
			"Set the same scheme in your Expo config and in Revyl before building:",
			"  revyl init --provider expo --hotreload-app-scheme myapp-dev",
		)
	} else if native.hasAppJSON {
		details = append(details,
			"Add a scheme to app.json before creating the dev build:",
			`  { "expo": { "scheme": "myapp-dev" } }`,
			"Then re-run:",
			"  revyl init --provider expo --hotreload-app-scheme myapp-dev",
		)
	} else {
		details = append(details,
			"Revyl could not find app.json to auto-detect expo.scheme.",
			"Add a scheme to your Expo config and tell Revyl the same value:",
			"  revyl init --provider expo --hotreload-app-scheme myapp-dev",
		)
	}

	details = append(details,
		"",
		"After changing Expo native config, rebuild the dev client once. JS/TS changes can hot reload after that.",
	)

	return &expoSchemePreflightError{
		summary: "Expo app URL scheme is required before creating a dev build",
		details: details,
	}
}

func newExpoSchemeMismatchError(nativeScheme, revylScheme string) *expoSchemePreflightError {
	return &expoSchemePreflightError{
		summary: "Expo scheme mismatch before creating a dev build",
		details: []string{
			fmt.Sprintf("app.json expo.scheme is %q, but .revyl/config.yaml hotreload.providers.expo.app_scheme is %q.", nativeScheme, revylScheme),
			"These must match so the installed dev client can handle the deep link Revyl opens for hot reload.",
			fmt.Sprintf("Use one value, then run: revyl init --provider expo --hotreload-app-scheme %s", nativeScheme),
		},
	}
}

package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/devicetargets"
)

// deviceTargetCatalogClient defines the minimal API surface needed to fetch
// live device targets from the backend.
type deviceTargetCatalogClient interface {
	GetDeviceTargets(ctx context.Context) (*api.AllPlatformTargets, error)
}

// loadCommandDeviceTargetCatalog resolves the best available target catalog for
// a CLI command, preferring the live backend matrix and falling back to the
// embedded generated catalog.
//
// Parameters:
//   - ctx: Context for cancellation
//   - cmd: Cobra command providing backend/dev flag context
//
// Returns:
//   - *devicetargets.Catalog: Live-backed catalog when available, otherwise the embedded fallback
func loadCommandDeviceTargetCatalog(
	ctx context.Context,
	cmd *cobra.Command,
) *devicetargets.Catalog {
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode("", devMode)
	return loadRuntimeDeviceTargetCatalog(ctx, client)
}

// loadRuntimeDeviceTargetCatalog resolves a live target catalog from the given
// backend client, falling back to the embedded catalog on any fetch/parse
// failure.
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: Backend client used to fetch /api/v1/execution/device-targets
//
// Returns:
//   - *devicetargets.Catalog: Live-backed catalog when available, otherwise the embedded fallback
func loadRuntimeDeviceTargetCatalog(
	ctx context.Context,
	client deviceTargetCatalogClient,
) *devicetargets.Catalog {
	if client == nil {
		return devicetargets.DefaultCatalog()
	}

	liveTargets, err := client.GetDeviceTargets(ctx)
	if err != nil || liveTargets == nil || len(liveTargets.Platforms) == 0 {
		return devicetargets.DefaultCatalog()
	}

	return devicetargets.NewCatalog(convertAPIDeviceTargets(liveTargets.Platforms))
}

// convertAPIDeviceTargets converts generated API target types into the CLI's
// internal catalog representation.
//
// Parameters:
//   - platforms: Platform target configs returned by the backend
//
// Returns:
//   - map[string]*devicetargets.PlatformTargetConfig: Converted target configs keyed by platform
func convertAPIDeviceTargets(
	platforms map[string]api.PlatformTargetConfig,
) map[string]*devicetargets.PlatformTargetConfig {
	converted := make(map[string]*devicetargets.PlatformTargetConfig, len(platforms))
	for platform, cfg := range platforms {
		compatible := make(map[string][]string)
		if cfg.CompatibleRuntimes != nil {
			compatible = make(map[string][]string, len(*cfg.CompatibleRuntimes))
			for model, runtimes := range *cfg.CompatibleRuntimes {
				compatible[model] = append([]string(nil), runtimes...)
			}
		}

		converted[platform] = &devicetargets.PlatformTargetConfig{
			DefaultPair: devicetargets.DevicePair{
				Model:   cfg.DefaultPair.Model,
				Runtime: cfg.DefaultPair.Runtime,
			},
			AvailableRuntimes:  append([]string(nil), cfg.AvailableRuntimes...),
			AvailableModels:    append([]string(nil), cfg.AvailableModels...),
			CompatibleRuntimes: compatible,
		}
	}
	return converted
}

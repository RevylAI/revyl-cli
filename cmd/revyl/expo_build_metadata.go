package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

const (
	artifactBuildMetadataKey             = "artifact_metadata"
	expoDevClientBuildMetadataKey        = "expo_dev_client"
	expoDevClientSchemeMetadataKey       = "scheme"
	expoDevClientUseExpPrefixMetadataKey = "use_exp_prefix"
	maxExpoConfigOutputBytes             = 1 << 20
	maxExpoDevClientSchemeBytes          = 128
	expoConfigResolutionTimeout          = 30 * time.Second
)

// expoDevClientBuildMetadata is the build-attached runtime contract for an
// Expo development client. It deliberately contains only the URL scheme facts
// baked into the artifact; provider selection and ports remain invocation
// concerns.
type expoDevClientBuildMetadata struct {
	Scheme       string
	UseExpPrefix bool
}

type expoConfigResolver func(context.Context, string, map[string]string) ([]byte, error)

type boundedCommandOutput struct {
	bytes.Buffer
	maxBytes int
	exceeded bool
}

func (w *boundedCommandOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := w.maxBytes - w.Buffer.Len()
	if remaining <= 0 {
		w.exceeded = true
		return written, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		w.exceeded = true
	}
	_, _ = w.Buffer.Write(data)
	return written, nil
}

// deriveExpoBuildMetadata resolves the exact Expo dev-client URL scheme
// for a canonical project. Non-Expo projects do not receive Expo metadata.
func deriveExpoBuildMetadata(
	ctx context.Context,
	projectRoot string,
	framework string,
	environment map[string]string,
) (*expoDevClientBuildMetadata, error) {
	return deriveExpoBuildMetadataWithResolver(ctx, projectRoot, framework, environment, resolveExpoConfigWithTooling)
}

func deriveExpoBuildMetadataWithResolver(
	ctx context.Context,
	projectRoot string,
	framework string,
	environment map[string]string,
	resolveDynamicConfig expoConfigResolver,
) (*expoDevClientBuildMetadata, error) {
	if strings.TrimSpace(framework) != "expo" {
		return nil, nil
	}
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return nil, fmt.Errorf("project root is required to resolve Expo build metadata")
	}

	native, err := detectExpoNativeScheme(projectRoot)
	if err != nil {
		return nil, err
	}
	if !native.hasDynamicConfig {
		return newExpoDevClientBuildMetadata(native.scheme, native.useExpPrefix)
	}
	if resolveDynamicConfig == nil {
		return nil, fmt.Errorf("Expo config resolver is required for a dynamic Expo project")
	}

	resolveContext, cancel := context.WithTimeout(ctx, expoConfigResolutionTimeout)
	defer cancel()
	resolvedConfig, err := resolveDynamicConfig(resolveContext, projectRoot, environment)
	if err != nil {
		if errors.Is(resolveContext.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("resolve dynamic Expo config after %s: %w", expoConfigResolutionTimeout, context.DeadlineExceeded)
		}
		if errors.Is(resolveContext.Err(), context.Canceled) {
			return nil, fmt.Errorf("resolve dynamic Expo config: %w", context.Canceled)
		}
		return nil, fmt.Errorf("resolve dynamic Expo config with `npx expo config --json`: %w", err)
	}

	var parsed struct {
		Scheme json.RawMessage `json:"scheme"`
		Slug   string          `json:"slug"`
	}
	if err := json.Unmarshal(resolvedConfig, &parsed); err != nil {
		return nil, fmt.Errorf("parse dynamic Expo config output: %w", err)
	}
	scheme := strings.TrimSpace(parseExpoSchemeValue(parsed.Scheme))
	useExpPrefix := false
	if scheme == "" {
		scheme = strings.TrimSpace(parsed.Slug)
		useExpPrefix = scheme != ""
	}
	return newExpoDevClientBuildMetadata(scheme, useExpPrefix)
}

func resolveExpoConfigWithTooling(ctx context.Context, projectRoot string, environment map[string]string) ([]byte, error) {
	command := exec.Command("npx", "expo", "config", "--json")
	command.Dir = projectRoot
	command.Env = recipeProcessEnvironment(environment)
	configureExpoConfigCommand(command)

	stdout := boundedCommandOutput{maxBytes: maxExpoConfigOutputBytes}
	command.Stdout = &stdout
	command.Stderr = &boundedCommandOutput{maxBytes: 64 << 10}
	if err := command.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		terminateExpoConfigCommand(command)
		<-done
		return nil, ctx.Err()
	}
	if stdout.exceeded {
		return nil, fmt.Errorf("Expo config output exceeds %d bytes", maxExpoConfigOutputBytes)
	}
	return stdout.Bytes(), nil
}

// recipeProcessEnvironment mirrors build.Runner's child-process contract:
// inherit the Revyl process environment, then replace values authored by the
// resolved recipe. This keeps dynamic Expo config resolution in the exact
// environment used to produce the local artifact.
func recipeProcessEnvironment(overrides map[string]string) []string {
	effectiveOverrides := make(map[string]string, len(overrides)+2)
	for name, value := range overrides {
		effectiveOverrides[name] = value
	}
	effectiveOverrides["CI"] = "1"
	effectiveOverrides["EXPO_NO_TELEMETRY"] = "1"
	result := make([]string, 0, len(os.Environ())+len(effectiveOverrides))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := effectiveOverrides[name]; replaced {
				continue
			}
		}
		result = append(result, entry)
	}
	for name, value := range effectiveOverrides {
		result = append(result, name+"="+value)
	}
	return result
}

func newExpoDevClientBuildMetadata(scheme string, useExpPrefix bool) (*expoDevClientBuildMetadata, error) {
	metadata := &expoDevClientBuildMetadata{
		Scheme:       strings.TrimSpace(scheme),
		UseExpPrefix: useExpPrefix,
	}
	if err := metadata.validate(); err != nil {
		return nil, err
	}
	return metadata, nil
}

func (m expoDevClientBuildMetadata) attachTo(metadata map[string]interface{}) error {
	if metadata == nil {
		return fmt.Errorf("build metadata map is required")
	}
	if err := m.validate(); err != nil {
		return err
	}
	artifactMetadata := make(map[string]interface{})
	if raw, exists := metadata[artifactBuildMetadataKey]; exists && raw != nil {
		var ok bool
		artifactMetadata, ok = raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("build metadata %q must be an object", artifactBuildMetadataKey)
		}
	}
	artifactMetadata[expoDevClientBuildMetadataKey] = map[string]interface{}{
		expoDevClientSchemeMetadataKey:       strings.TrimSpace(m.Scheme),
		expoDevClientUseExpPrefixMetadataKey: m.UseExpPrefix,
	}
	metadata[artifactBuildMetadataKey] = artifactMetadata
	return nil
}

// expoProviderConfigFromBuildVersion returns nil for legacy builds that do not
// carry the build-attached contract. Present but malformed metadata fails
// closed so callers never construct a deep link from ambiguous build facts.
func expoProviderConfigFromBuildVersion(version *api.BuildVersion) (*config.ProviderConfig, error) {
	if version == nil {
		return nil, nil
	}
	return expoProviderConfigFromBuildMetadata(version.Metadata)
}

func expoProviderConfigFromBuildVersionDetail(version *api.BuildVersionDetail) (*config.ProviderConfig, error) {
	if version == nil {
		return nil, nil
	}
	return expoProviderConfigFromBuildMetadata(version.Metadata)
}

func expoProviderConfigFromBuildMetadata(metadata map[string]interface{}) (*config.ProviderConfig, error) {
	if metadata == nil {
		return nil, nil
	}
	rawArtifactMetadata, exists := metadata[artifactBuildMetadataKey]
	if !exists || rawArtifactMetadata == nil {
		return nil, nil
	}
	artifactMetadata, ok := rawArtifactMetadata.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("build metadata %q must be an object", artifactBuildMetadataKey)
	}
	raw, exists := artifactMetadata[expoDevClientBuildMetadataKey]
	if !exists || raw == nil {
		return nil, nil
	}

	values, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Expo dev-client build metadata must be an object")
	}
	rawScheme, exists := values[expoDevClientSchemeMetadataKey]
	if !exists {
		return nil, fmt.Errorf("Expo dev-client build metadata is missing %q", expoDevClientSchemeMetadataKey)
	}
	scheme, ok := rawScheme.(string)
	if !ok {
		return nil, fmt.Errorf("Expo dev-client build metadata %q must be a string", expoDevClientSchemeMetadataKey)
	}
	rawUseExpPrefix, exists := values[expoDevClientUseExpPrefixMetadataKey]
	if !exists {
		return nil, fmt.Errorf("Expo dev-client build metadata is missing %q", expoDevClientUseExpPrefixMetadataKey)
	}
	useExpPrefix, ok := rawUseExpPrefix.(bool)
	if !ok {
		return nil, fmt.Errorf("Expo dev-client build metadata %q must be a boolean", expoDevClientUseExpPrefixMetadataKey)
	}

	buildMetadata, err := newExpoDevClientBuildMetadata(scheme, useExpPrefix)
	if err != nil {
		return nil, fmt.Errorf("invalid Expo dev-client build metadata: %w", err)
	}
	return &config.ProviderConfig{
		AppScheme:    buildMetadata.Scheme,
		UseExpPrefix: buildMetadata.UseExpPrefix,
	}, nil
}

func (m expoDevClientBuildMetadata) validate() error {
	effectiveScheme := strings.TrimSpace(m.Scheme)
	if effectiveScheme == "" {
		return fmt.Errorf("Expo config must resolve expo.scheme or expo.slug before creating a dev build")
	}
	if m.UseExpPrefix {
		effectiveScheme = "exp+" + effectiveScheme
	}
	if len(effectiveScheme) > maxExpoDevClientSchemeBytes {
		return fmt.Errorf("Expo dev-client URL scheme exceeds %d bytes", maxExpoDevClientSchemeBytes)
	}
	for index, char := range effectiveScheme {
		if index == 0 {
			if !isASCIIAlpha(char) {
				return fmt.Errorf("Expo dev-client URL scheme must start with an ASCII letter")
			}
			continue
		}
		if !isASCIIAlpha(char) && (char < '0' || char > '9') && char != '+' && char != '-' && char != '.' {
			return fmt.Errorf("Expo dev-client URL scheme contains unsupported character %q", char)
		}
	}
	return nil
}

func isASCIIAlpha(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

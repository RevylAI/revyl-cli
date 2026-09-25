package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
)

func remoteBuildConfigFromResolved(appID uuid.UUID, resolved remoteBuildPlatformConfig) (api.BuildConfig, error) {
	platform := api.BuildConfigPlatform(resolved.Platform)
	steps, err := remoteBuildStepsFromCommands(remoteBuildSetupCommands(resolved), remoteBuildCommands(resolved))
	if err != nil {
		return api.BuildConfig{}, err
	}
	artifacts := remoteBuildArtifacts(defaultRemoteArtifactType(resolved.Platform), resolved.Output)

	sourceSubdir := strings.Trim(strings.TrimSpace(resolved.SourceSubdir), "/")
	if sourceSubdir == "." {
		sourceSubdir = ""
	}
	if remoteBuildUsesGitSource(resolved.Source) {
		sourceSubdir = normalizeRemoteGitSource(resolved.Source).Subdir
	}

	return api.BuildConfig{
		AppId:        appID,
		Platform:     &platform,
		Framework:    stringPtrOrNil(resolved.Framework),
		Image:        stringPtrOrNil(resolved.Image),
		SourceSubdir: stringPtrOrNil(sourceSubdir),
		Steps:        &steps,
		Artifacts:    &artifacts,
		Env:          stringMapPtrOrNil(resolved.Env),
		SecretRefs:   stringSlicePtrOrNil(resolved.Secrets),
		Caches:       remoteBuildCachesPtrOrNil(resolved.Caches),
	}, nil
}

func remoteBuildSetupCommands(resolved remoteBuildPlatformConfig) []config.BuildStepItem {
	commands := append([]config.BuildStepItem(nil), resolved.SetupCommands...)
	if len(commands) == 0 {
		if command := strings.TrimSpace(resolved.Setup); command != "" {
			commands = config.CommandStepItems([]string{command})
		}
	}
	return commands
}

func remoteBuildCommands(resolved remoteBuildPlatformConfig) []config.BuildStepItem {
	commands := append([]config.BuildStepItem(nil), resolved.Commands...)
	if len(commands) == 0 {
		if command := strings.TrimSpace(resolved.Command); command != "" {
			commands = config.CommandStepItems([]string{command})
		}
	}

	if scheme := strings.TrimSpace(resolved.Scheme); scheme != "" {
		for index, item := range commands {
			if item.IsCommand() {
				commands[index] = config.CommandStepItem(build.ApplySchemeToCommand(item.Command, scheme))
			}
		}
	}
	return commands
}

func remoteBuildStepsFromCommands(setupCommands, commands []config.BuildStepItem) ([]api.BuildStep, error) {
	checkoutName := "checkout"
	steps := []api.BuildStep{
		{Type: api.BuildStepTypeCheckout, Name: &checkoutName},
	}
	for _, phase := range []struct {
		name  string
		items []config.BuildStepItem
	}{{"setup", setupCommands}, {"build", commands}} {
		phaseSteps, err := remoteBuildStepsFromItems(phase.name, phase.items)
		if err != nil {
			return nil, err
		}
		steps = append(steps, phaseSteps...)
	}
	return steps, nil
}

func remoteBuildStepsFromItems(phase string, items []config.BuildStepItem) ([]api.BuildStep, error) {
	steps := []api.BuildStep{}
	for index, item := range items {
		positionalName := phase
		if len(items) > 1 {
			positionalName = fmt.Sprintf("%s-%d", phase, index+1)
		}
		if command, ok := item.RunCommand(); ok {
			command = strings.TrimSpace(command)
			if command == "" {
				continue
			}
			name := positionalName
			if authored := strings.TrimSpace(item.Name()); authored != "" {
				name = authored
			}
			steps = append(steps, api.BuildStep{
				Type:    api.BuildStepTypeRun,
				Name:    &name,
				Command: &command,
			})
			continue
		}
		name := item.Type()
		if authored := strings.TrimSpace(item.Name()); authored != "" {
			name = authored
		}
		inputs := api.BuildStep_Inputs{}
		encoded, err := json.Marshal(item.Inputs())
		if err != nil {
			return nil, fmt.Errorf("%s step %d: encode %s inputs: %w", phase, index+1, item.Type(), err)
		}
		if err := inputs.UnmarshalJSON(encoded); err != nil {
			return nil, fmt.Errorf("%s step %d: decode %s inputs: %w", phase, index+1, item.Type(), err)
		}
		steps = append(steps, api.BuildStep{
			Type:   api.BuildStepType(item.Type()),
			Name:   &name,
			Inputs: &inputs,
		})
	}
	return steps, nil
}

func remoteBuildArtifacts(artifactType, output string) []api.BuildArtifact {
	output = strings.TrimSpace(output)
	if output == "" {
		output = defaultRemoteArtifactPath(artifactType)
	}
	return []api.BuildArtifact{{
		Name: artifactType,
		Path: output,
		Type: artifactType,
	}}
}

func defaultRemoteArtifactPath(artifactType string) string {
	if artifactType == "apk" {
		return "**/build/outputs/apk/**/*.apk"
	}
	return "build/**/*.app"
}

func stringMapPtrOrNil(m map[string]string) *map[string]string {
	if len(m) == 0 {
		return nil
	}
	result := make(map[string]string, len(m))
	for key, value := range m {
		result[key] = value
	}
	return &result
}

func stringSlicePtrOrNil(values []string) *[]string {
	if len(values) == 0 {
		return nil
	}
	result := append([]string(nil), values...)
	return &result
}

func remoteBuildCachesPtrOrNil(caches []config.BuildCache) *[]api.BuildCache {
	if len(caches) == 0 {
		return nil
	}
	apiCaches := make([]api.BuildCache, 0, len(caches))
	for _, cache := range caches {
		apiCaches = append(apiCaches, api.BuildCache{
			Key:   cache.Key,
			Paths: append([]string(nil), cache.Paths...),
		})
	}
	return &apiCaches
}

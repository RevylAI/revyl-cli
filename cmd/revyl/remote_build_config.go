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

func remoteBuildRecipeFromResolved(appID uuid.UUID, resolved remoteBuildPlatformConfig) (api.BuildRecipe, error) {
	setupSteps, err := remoteRecipeSteps("setup", remoteBuildSetupCommands(resolved))
	if err != nil {
		return api.BuildRecipe{}, err
	}
	buildSteps, err := remoteRecipeSteps("build", remoteBuildCommands(resolved))
	if err != nil {
		return api.BuildRecipe{}, err
	}
	artifacts := remoteBuildArtifacts(defaultRemoteArtifactType(resolved.Platform), resolved.Output)

	sourceSubdir := strings.Trim(strings.TrimSpace(resolved.SourceSubdir), "/")
	if sourceSubdir == "." {
		sourceSubdir = ""
	}
	if remoteBuildUsesGitSource(resolved.Source) {
		sourceSubdir = normalizeRemoteGitSource(resolved.Source).Subdir
	}

	var setupStepsPtr *[]api.RecipeStep
	if len(setupSteps) > 0 {
		setupStepsPtr = &setupSteps
	}
	return api.BuildRecipe{
		AppId:        appID,
		Platform:     api.BuildRecipePlatform(resolved.Platform),
		Profile:      stringPtrOrNil(resolved.Profile),
		Framework:    stringPtrOrNil(resolved.Framework),
		Image:        stringPtrOrNil(resolved.Image),
		SourceSubdir: stringPtrOrNil(sourceSubdir),
		SetupSteps:   setupStepsPtr,
		BuildSteps:   buildSteps,
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

// remoteRecipeSteps converts one authored command list into recipe steps.
// Revyl adds checkout, fingerprinting, and the artifact upload itself, so only
// the customer's own steps are sent, named only when the customer named them.
func remoteRecipeSteps(phase string, items []config.BuildStepItem) ([]api.RecipeStep, error) {
	steps := []api.RecipeStep{}
	for index, item := range items {
		name := stringPtrOrNil(strings.TrimSpace(item.Name()))
		if command, ok := item.RunCommand(); ok {
			command = strings.TrimSpace(command)
			if command == "" {
				continue
			}
			steps = append(steps, api.RecipeStep{
				Type:    api.RecipeStepTypeRun,
				Name:    name,
				Command: &command,
			})
			continue
		}
		inputs := api.RecipeStep_Inputs{}
		encoded, err := json.Marshal(item.Inputs())
		if err != nil {
			return nil, fmt.Errorf("%s step %d: encode %s inputs: %w", phase, index+1, item.Type(), err)
		}
		if err := inputs.UnmarshalJSON(encoded); err != nil {
			return nil, fmt.Errorf("%s step %d: decode %s inputs: %w", phase, index+1, item.Type(), err)
		}
		steps = append(steps, api.RecipeStep{
			Type:   api.RecipeStepType(item.Type()),
			Name:   name,
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

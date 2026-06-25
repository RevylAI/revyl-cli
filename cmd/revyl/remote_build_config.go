package main

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
)

func remoteBuildConfigFromResolved(appID uuid.UUID, resolved remoteBuildPlatformConfig) api.BuildConfig {
	platform := api.BuildConfigPlatform(resolved.Platform)
	steps := remoteBuildSteps(resolved.Setup, remoteBuildCommands(resolved))
	artifacts := remoteBuildArtifacts(defaultRemoteArtifactType(resolved.Platform), resolved.Output)

	sourceSubdir := ""
	if remoteBuildUsesGitSource(resolved.Source) {
		sourceSubdir = normalizeRemoteGitSource(resolved.Source).Subdir
	}

	return api.BuildConfig{
		AppId:        appID,
		Platform:     &platform,
		Image:        stringPtrOrNil(resolved.Image),
		SourceSubdir: stringPtrOrNil(sourceSubdir),
		Steps:        &steps,
		Artifacts:    &artifacts,
		Env:          stringMapPtrOrNil(resolved.Env),
		Caches:       remoteBuildCachesPtrOrNil(resolved.Caches),
	}
}

func remoteBuildCommands(resolved remoteBuildPlatformConfig) []string {
	commands := append([]string(nil), resolved.Commands...)
	if len(commands) == 0 {
		if command := strings.TrimSpace(resolved.Command); command != "" {
			commands = []string{command}
		}
	}

	if scheme := strings.TrimSpace(resolved.Scheme); scheme != "" {
		for index, command := range commands {
			commands[index] = build.ApplySchemeToCommand(command, scheme)
		}
	}
	return commands
}

func remoteBuildSteps(setup string, commands []string) []api.BuildStep {
	checkoutName := "checkout"
	steps := []api.BuildStep{
		{Type: api.BuildStepTypeCheckout, Name: &checkoutName},
	}

	if setup = strings.TrimSpace(setup); setup != "" {
		setupName := "setup"
		steps = append(steps, api.BuildStep{
			Type:    api.BuildStepTypeRun,
			Name:    &setupName,
			Command: &setup,
		})
	}

	for index, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		name := "build"
		if len(commands) > 1 {
			name = fmt.Sprintf("build-%d", index+1)
		}
		steps = append(steps, api.BuildStep{
			Type:    api.BuildStepTypeRun,
			Name:    &name,
			Command: &command,
		})
	}
	return steps
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

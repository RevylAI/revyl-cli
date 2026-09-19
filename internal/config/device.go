package config

import (
	"errors"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type DeviceConfigContext struct {
	ProjectRoot                   string
	ConfigPath                    string
	repositoryRelativeProjectRoot string
}

func ResolveDeviceConfigContext(cwd string) (DeviceConfigContext, error) {
	effectiveDirectory, err := ResolveEffectiveDirectory(cwd, "")
	if err != nil {
		return DeviceConfigContext{}, err
	}
	root, err := resolveGitWorktreeRoot(effectiveDirectory)
	if err != nil {
		var configErr *ConfigError
		if !errors.As(err, &configErr) || configErr.Code != "git_worktree_unavailable" {
			return DeviceConfigContext{}, err
		}
		root = effectiveDirectory
	}
	context := DeviceConfigContext{ProjectRoot: effectiveDirectory, repositoryRelativeProjectRoot: "."}
	configPath, err := DiscoverConfigPath(effectiveDirectory, root)
	if err != nil {
		var configErr *ConfigError
		if errors.As(err, &configErr) && configErr.Code == "config_not_found" {
			return context, nil
		}
		return DeviceConfigContext{}, err
	}
	context.ConfigPath = configPath
	context.ProjectRoot = filepath.Dir(filepath.Dir(configPath))
	context.repositoryRelativeProjectRoot, err = repositoryRelativePath(root, context.ProjectRoot)
	if err != nil {
		return DeviceConfigContext{}, err
	}
	return context, nil
}

func (c DeviceConfigContext) ReadSession() (AuthoredSession, error) {
	if c.ConfigPath == "" {
		return AuthoredSession{}, nil
	}
	data, err := ReadConfigFile(c.ConfigPath)
	if err != nil {
		return AuthoredSession{}, err
	}
	root, document, err := loadLegacyMigrationYAMLDocument(data)
	if err != nil {
		return AuthoredSession{}, err
	}
	sessionNode := mappingValue(root, "session")
	if sessionNode != nil && hasAnyKey(document, "defaults", "before_session", "auth_bypass") {
		return AuthoredSession{}, newConfigError("classification", "mixed_config_formats", []string{"session"}, "")
	}
	if sessionNode == nil {
		if err := validateAllowedKeys(mappingValue(root, "before_session"), []string{"before_session"}, "script", "timeout_seconds"); err != nil {
			return AuthoredSession{}, err
		}
		if err := validateAllowedKeys(mappingValue(root, "auth_bypass"), []string{"auth_bypass"}, "launch_vars", "deep_link"); err != nil {
			return AuthoredSession{}, err
		}
		session, err := legacyDeviceSession(document)
		if err != nil {
			return AuthoredSession{}, err
		}
		sessionNode = &yaml.Node{}
		if err := sessionNode.Encode(session); err != nil {
			return AuthoredSession{}, newConfigError("contract", "invalid_contract", []string{"session"}, "")
		}
	}
	if err := validateSessionNode(sessionNode); err != nil {
		return AuthoredSession{}, err
	}
	var session AuthoredSession
	if err := sessionNode.Decode(&session); err != nil {
		return AuthoredSession{}, newConfigError("contract", "invalid_contract", []string{"session"}, "")
	}
	return normalizeSession(session, c.repositoryRelativeProjectRoot)
}

func legacyDeviceSession(document map[string]any) (map[string]any, error) {
	session := map[string]any{}
	defaults, err := legacyMapping(document, "defaults", []string{"defaults"})
	if err != nil {
		return nil, err
	}
	if rawTimeout, exists := defaults["timeout"]; exists && rawTimeout != nil {
		if timeout, ok := integerValue(rawTimeout); ok && timeout <= 0 {
			session["idle_timeout_seconds"] = DefaultTimeoutSeconds
		} else {
			session["idle_timeout_seconds"] = rawTimeout
		}
	}
	before, err := legacyMapping(document, "before_session", []string{"before_session"})
	if err != nil {
		return nil, err
	}
	if before != nil {
		timeout := before["timeout_seconds"]
		if parsed, ok := integerValue(timeout); timeout == nil || (ok && parsed <= 0) {
			timeout = DefaultBeforeSessionTimeoutSeconds
		}
		beforeScript := map[string]any{"timeout_seconds": timeout}
		script := before["script"]
		if value, ok := script.(string); ok {
			script = strings.TrimSpace(value)
		}
		if script != nil && script != "" {
			beforeScript["script_path"] = script
		}
		session["before_script"] = beforeScript
	}
	bypass, err := legacyMapping(document, "auth_bypass", []string{"auth_bypass"})
	if err != nil {
		return nil, err
	}
	if bypass != nil {
		authBypass := map[string]any{}
		for _, key := range []string{"launch_vars", "deep_link"} {
			if value, exists := bypass[key]; exists && value != nil {
				authBypass[key] = value
			}
		}
		session["auth_bypass"] = authBypass
	}
	return session, nil
}

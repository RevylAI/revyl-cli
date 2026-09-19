package config

import (
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

func validateSessionNode(session *yaml.Node) error {
	if session == nil || nodeIsNull(session) {
		return nil
	}
	if err := validateMappingNode(session, []string{"session"}, true); err != nil {
		return err
	}
	if err := validateAllowedKeys(session, []string{"session"}, "idle_timeout_seconds", "before_script", "auth_bypass"); err != nil {
		return err
	}
	if err := validateSecondsNode(mappingValue(session, "idle_timeout_seconds"), []string{"session", "idle_timeout_seconds"}); err != nil {
		return err
	}
	if before := mappingValue(session, "before_script"); before != nil && !nodeIsNull(before) {
		if err := validateMappingNode(before, []string{"session", "before_script"}, true); err != nil {
			return err
		}
		if err := validateAllowedKeys(before, []string{"session", "before_script"}, "script_path", "timeout_seconds"); err != nil {
			return err
		}
		if err := validateSecondsNode(mappingValue(before, "timeout_seconds"), []string{"session", "before_script", "timeout_seconds"}); err != nil {
			return err
		}
		if err := validateStringNode(mappingValue(before, "script_path"), []string{"session", "before_script", "script_path"}, true); err != nil {
			return err
		}
	}
	if bypass := mappingValue(session, "auth_bypass"); bypass != nil && !nodeIsNull(bypass) {
		if err := validateMappingNode(bypass, []string{"session", "auth_bypass"}, true); err != nil {
			return err
		}
		if err := validateAllowedKeys(bypass, []string{"session", "auth_bypass"}, "launch_vars", "deep_link"); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(bypass, "launch_vars"), []string{"session", "auth_bypass", "launch_vars"}); err != nil {
			return err
		}
		if err := validateStringNode(mappingValue(bypass, "deep_link"), []string{"session", "auth_bypass", "deep_link"}, true); err != nil {
			return err
		}
	}
	return nil
}

func normalizeSession(session AuthoredSession, repositoryRelativeProjectRoot string) (AuthoredSession, error) {
	session = cloneAuthoredSession(session)
	if session.BeforeScript == nil || session.BeforeScript.ScriptPath == nil {
		return session, nil
	}
	authoredScript := *session.BeforeScript.ScriptPath
	if authoredScript == "" || strings.TrimSpace(authoredScript) != authoredScript || strings.HasPrefix(authoredScript, "/") || strings.ContainsAny(authoredScript, "\\\x00") {
		return AuthoredSession{}, newConfigError("normalization", "invalid_before_script_path", []string{"session", "before_script", "script_path"}, "")
	}
	script := path.Clean(authoredScript)
	if script == "." {
		return AuthoredSession{}, newConfigError("normalization", "invalid_before_script_path", []string{"session", "before_script", "script_path"}, "")
	}
	repositoryScript := path.Clean(path.Join(repositoryRelativeProjectRoot, script))
	if repositoryScript == ".." || strings.HasPrefix(repositoryScript, "../") {
		return AuthoredSession{}, newConfigError("normalization", "path_escapes_repository", []string{"session", "before_script", "script_path"}, "")
	}
	session.BeforeScript.ScriptPath = &script
	return session, nil
}

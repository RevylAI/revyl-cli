package config

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// NoSessionProfile is the reserved pick meaning "start without a profile".
const NoSessionProfile = "none"

var sessionProfileNamePattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// ValidSessionProfileName reports whether name may name a session profile.
func ValidSessionProfileName(name string) bool {
	return name != NoSessionProfile && sessionProfileNamePattern.MatchString(name)
}

// SessionProfileNames returns the defined profile names in sorted order.
func (s AuthoredSession) SessionProfileNames() []string {
	names := make([]string, 0, len(s.Profiles))
	for name := range s.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateSessionNode(session *yaml.Node) error {
	if session == nil || nodeIsNull(session) {
		return nil
	}
	if err := validateMappingNode(session, []string{"session"}, true); err != nil {
		return err
	}
	if err := validateAllowedKeys(session, []string{"session"}, "idle_timeout_seconds", "before_script", "auth_bypass", "default_profile", "profiles"); err != nil {
		return err
	}
	if err := validateSecondsNode(mappingValue(session, "idle_timeout_seconds"), []string{"session", "idle_timeout_seconds"}); err != nil {
		return err
	}
	if err := validateStringNode(mappingValue(session, "default_profile"), []string{"session", "default_profile"}, true); err != nil {
		return err
	}
	if profiles := mappingValue(session, "profiles"); profiles != nil && !nodeIsNull(profiles) {
		if err := validateMappingNode(profiles, []string{"session", "profiles"}, false); err != nil {
			return err
		}
		for index := 0; index < len(profiles.Content); index += 2 {
			profilePath := []string{"session", "profiles", profiles.Content[index].Value}
			profile := profiles.Content[index+1]
			if err := validateMappingNode(profile, profilePath, false); err != nil {
				return err
			}
			if err := validateAllowedKeys(profile, profilePath, "script", "requires_repository_checkout"); err != nil {
				return err
			}
			script := mappingValue(profile, "script")
			if script == nil {
				return newConfigError("contract", "missing_field", append(profilePath, "script"), "")
			}
			if err := validateStringNode(script, append(profilePath, "script"), false); err != nil {
				return err
			}
			if err := validateBooleanNode(mappingValue(profile, "requires_repository_checkout"), append(profilePath, "requires_repository_checkout")); err != nil {
				return err
			}
		}
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
		if err := validateAllowedKeys(bypass, []string{"session", "auth_bypass"}, "launch_vars", "deep_link", "sign_in"); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(bypass, "launch_vars"), []string{"session", "auth_bypass", "launch_vars"}); err != nil {
			return err
		}
		if err := validateStringNode(mappingValue(bypass, "deep_link"), []string{"session", "auth_bypass", "deep_link"}, true); err != nil {
			return err
		}
		if err := validateSignInNode(mappingValue(bypass, "sign_in")); err != nil {
			return err
		}
	}
	return nil
}

// validateSignInNode checks each field's presence, type, and value in
// declaration order so the first reported error matches the Python contract.
func validateSignInNode(signIn *yaml.Node) error {
	path := []string{"session", "auth_bypass", "sign_in"}
	if signIn == nil || nodeIsNull(signIn) {
		return nil
	}
	if err := validateMappingNode(signIn, path, true); err != nil {
		return err
	}
	if err := validateAllowedKeys(signIn, path, "path", "secret"); err != nil {
		return err
	}
	fields := []struct {
		name     string
		required bool
		valid    func(string) bool
	}{
		{"path", true, ValidateSignInAppPath},
		{"secret", true, ValidateLaunchVariableName},
	}
	for _, field := range fields {
		fieldPath := append(append([]string{}, path...), field.name)
		node := mappingValue(signIn, field.name)
		if node == nil || (!field.required && nodeIsNull(node)) {
			if field.required {
				return newConfigError("contract", "missing_field", fieldPath, "")
			}
			continue
		}
		if err := validateStringNode(node, fieldPath, false); err != nil {
			return err
		}
		if !field.valid(node.Value) {
			return newConfigError("contract", "invalid_contract", fieldPath, "")
		}
	}
	return nil
}

// validateAuthoredSessionProfiles enforces the profile rules that span keys,
// in the order the shared Python contract (AuthoredSession) reports them:
// scripts, names, then the either-or with before_script and auth_bypass, and
// default_profile. See cognisim-docs/devices/session-profiles.mdx.
func validateAuthoredSessionProfiles(session AuthoredSession) error {
	names := session.SessionProfileNames()
	for _, name := range names {
		script := session.Profiles[name].Script
		if script == "" || strings.TrimSpace(script) != script || strings.ContainsRune(script, 0) {
			return authoredContractError([]string{"session", "profiles", name, "script"}, "session profile scripts must be non-empty, trimmed and free of NUL")
		}
	}
	if session.Profiles != nil && len(session.Profiles) == 0 {
		return authoredContractError([]string{"session", "profiles"}, "must define at least one profile")
	}
	for _, name := range names {
		if name == NoSessionProfile {
			return authoredContractError([]string{"session", "profiles"}, `"none" is reserved and means no session profile`)
		}
		if !ValidSessionProfileName(name) {
			return authoredContractError([]string{"session", "profiles"}, "Session profile names use lowercase letters, digits and dashes (1-64 characters)")
		}
	}
	defaultProfilePath := []string{"session", "default_profile"}
	switch {
	case session.Profiles == nil && session.DefaultProfile != nil:
		return newConfigError("contract", "session_default_profile_requires_profiles", defaultProfilePath, "requires session.profiles")
	case session.Profiles == nil:
		return nil
	case session.AuthBypass != nil:
		return newConfigError("contract", "session_profiles_combined_with_auth_bypass", []string{"session", "auth_bypass"}, "cannot be combined with session.profiles; write its launch values or deep link from a profile script")
	case session.BeforeScript != nil:
		return newConfigError("contract", "session_profiles_combined_with_before_script", []string{"session", "before_script"}, "cannot be combined with session.profiles; make it a profile script")
	}
	if session.DefaultProfile != nil && *session.DefaultProfile != NoSessionProfile {
		if _, ok := session.Profiles[*session.DefaultProfile]; !ok {
			return newConfigError("contract", "session_default_profile_not_defined", defaultProfilePath, `must name a profile in session.profiles, or be "none"`)
		}
	}
	return nil
}

func normalizeSession(session AuthoredSession, repositoryRelativeProjectRoot string) (AuthoredSession, error) {
	session = cloneAuthoredSession(session)
	for _, name := range session.SessionProfileNames() {
		script := session.Profiles[name].Script
		if !strings.Contains(script, "/") {
			continue
		}
		if _, err := normalizeRepositoryScriptPath(script, repositoryRelativeProjectRoot, []string{"session", "profiles", name, "script"}, "invalid_session_profile_script_path"); err != nil {
			return AuthoredSession{}, err
		}
	}
	if session.BeforeScript == nil || session.BeforeScript.ScriptPath == nil {
		return session, nil
	}
	script, err := normalizeRepositoryScriptPath(*session.BeforeScript.ScriptPath, repositoryRelativeProjectRoot, []string{"session", "before_script", "script_path"}, "invalid_before_script_path")
	if err != nil {
		return AuthoredSession{}, err
	}
	session.BeforeScript.ScriptPath = &script
	return session, nil
}

func normalizeRepositoryScriptPath(authoredScript, repositoryRelativeProjectRoot string, fieldPath []string, invalidCode string) (string, error) {
	if authoredScript == "" || strings.TrimSpace(authoredScript) != authoredScript || strings.HasPrefix(authoredScript, "/") || strings.ContainsAny(authoredScript, "\\\x00") {
		return "", newConfigError("normalization", invalidCode, fieldPath, "")
	}
	script := path.Clean(authoredScript)
	if script == "." {
		return "", newConfigError("normalization", invalidCode, fieldPath, "")
	}
	repositoryScript := path.Clean(path.Join(repositoryRelativeProjectRoot, script))
	if repositoryScript == ".." || strings.HasPrefix(repositoryScript, "../") {
		return "", newConfigError("normalization", "path_escapes_repository", fieldPath, "")
	}
	return script, nil
}

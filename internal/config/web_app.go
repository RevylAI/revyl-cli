package config

import (
	"fmt"
	"sort"
	"strings"
)

// ConfiguredWebAppID returns the Web app that the project's Web recipes name,
// or "" when none names one. Profiles that name different Web apps are
// ambiguous, so the caller must choose one explicitly.
func (b *AuthoredBuild) ConfiguredWebAppID() (string, error) {
	if b == nil {
		return "", nil
	}
	profileNames := make([]string, 0, len(b.Profiles))
	for name := range b.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	appID, appProfile := "", ""
	for _, name := range profileNames {
		web := b.Profiles[name].Web
		if web == nil || web.AppID == nil {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(*web.AppID))
		if appID != "" && candidate != appID {
			return "", fmt.Errorf("build.profiles.%s.web.app_id and build.profiles.%s.web.app_id name different Web apps", appProfile, name)
		}
		appID, appProfile = candidate, name
	}
	return appID, nil
}

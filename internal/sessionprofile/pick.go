package sessionprofile

import (
	"fmt"
	"strings"

	"github.com/revyl/cli/internal/config"
)

// PickSource says why a start applies its profile, using the values of the
// session_profile_pick analytics property.
type PickSource string

const (
	PickExplicit PickSource = "explicit"
	PickDefault  PickSource = "default"
	PickNone     PickSource = "none"
)

// Pick is the session profile a start applies. Name is empty when none does.
type Pick struct {
	Name   string
	Source PickSource
}

// ResolvePick picks the profile for one start: the explicit pick when given
// ("none" skips the default), else the project's default_profile.
func ResolvePick(session config.AuthoredSession, explicit string) (Pick, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit == config.NoSessionProfile {
		return Pick{Source: PickNone}, nil
	}
	if explicit != "" {
		if _, ok := session.Profiles[explicit]; !ok {
			return Pick{}, undefinedProfileError(session, explicit)
		}
		return Pick{Name: explicit, Source: PickExplicit}, nil
	}
	if session.DefaultProfile != nil && *session.DefaultProfile != config.NoSessionProfile {
		return Pick{Name: *session.DefaultProfile, Source: PickDefault}, nil
	}
	return Pick{Source: PickNone}, nil
}

func undefinedProfileError(session config.AuthoredSession, name string) error {
	names := session.SessionProfileNames()
	if len(names) == 0 {
		return fmt.Errorf("session profile %q isn't defined: this project has no session.profiles in .revyl/config.yaml (pick none to start without one)", name)
	}
	return fmt.Errorf("session profile %q isn't defined; this project defines: %s (or none)", name, strings.Join(names, ", "))
}

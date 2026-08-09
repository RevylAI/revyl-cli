// Package launchvars handles stored launch variables applied to app runtimes.
package launchvars

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	// InheritedIDsEnv contains comma-separated org launch-configuration UUIDs inherited
	// by child Revyl commands. Values remain in Revyl and resolve server-side.
	InheritedIDsEnv = "REVYL_INHERITED_LAUNCH_ENV_VAR_IDS"
	maxInheritedIDs = 50
)

// LoadInheritedIDs returns the validated launch-configuration IDs inherited by
// the current process. The legacy environment variable can contain both
// key/value launch variables and iOS argument sets; callers resolve each ID's
// kind through the organization-scoped API boundary.
func LoadInheritedIDs(disabled bool) ([]string, error) {
	if disabled {
		return nil, nil
	}
	rawInherited := strings.TrimSpace(os.Getenv(InheritedIDsEnv))
	if rawInherited == "" {
		return nil, nil
	}

	parts := strings.Split(rawInherited, ",")
	if len(parts) > maxInheritedIDs {
		return nil, fmt.Errorf(
			"%s contains more than %d launch configurations",
			InheritedIDsEnv,
			maxInheritedIDs,
		)
	}

	inherited := make([]string, 0, len(parts))
	seenInherited := make(map[uuid.UUID]struct{}, len(parts))
	for _, part := range parts {
		parsed, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf(
				"%s contains an invalid launch-configuration ID",
				InheritedIDsEnv,
			)
		}
		if _, exists := seenInherited[parsed]; exists {
			return nil, fmt.Errorf(
				"%s contains duplicate launch-configuration IDs",
				InheritedIDsEnv,
			)
		}
		seenInherited[parsed] = struct{}{}
		inherited = append(inherited, parsed.String())
	}
	return inherited, nil
}

// MergeInherited prepends validated inherited IDs to explicit launch variables.
// Deprecated callers can continue using this helper; new typed launch-
// configuration paths should load inherited IDs separately so iOS argument sets
// are not incorrectly validated as key/value variables.
func MergeInherited(explicit []string, disabled bool) ([]string, error) {
	inherited, err := LoadInheritedIDs(disabled)
	if err != nil {
		return nil, err
	}
	return append(inherited, explicit...), nil
}

// HasInherited reports whether inherited launch variables are active.
func HasInherited(disabled bool) bool {
	return !disabled && strings.TrimSpace(os.Getenv(InheritedIDsEnv)) != ""
}

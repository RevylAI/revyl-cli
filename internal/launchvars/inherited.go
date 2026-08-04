// Package launchvars handles stored launch variables applied to app runtimes.
package launchvars

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	// InheritedIDsEnv contains comma-separated org launch-variable UUIDs inherited
	// by child Revyl commands. Values remain in Revyl and resolve server-side.
	InheritedIDsEnv = "REVYL_INHERITED_LAUNCH_ENV_VAR_IDS"
	maxInheritedIDs = 50
)

// MergeInherited prepends validated inherited IDs to explicit launch variables.
// Explicit keys and IDs remain additive and resolve through the existing
// organization-scoped API boundary. A caller can disable only inherited inputs
// without affecting explicit inputs.
func MergeInherited(explicit []string, disabled bool) ([]string, error) {
	if disabled {
		return append([]string(nil), explicit...), nil
	}
	rawInherited := strings.TrimSpace(os.Getenv(InheritedIDsEnv))
	if rawInherited == "" {
		return append([]string(nil), explicit...), nil
	}

	parts := strings.Split(rawInherited, ",")
	if len(parts) > maxInheritedIDs {
		return nil, fmt.Errorf(
			"%s contains more than %d launch variables",
			InheritedIDsEnv,
			maxInheritedIDs,
		)
	}

	merged := make([]string, 0, len(parts)+len(explicit))
	seenInherited := make(map[uuid.UUID]struct{}, len(parts))
	for _, part := range parts {
		parsed, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf(
				"%s contains an invalid launch-variable ID",
				InheritedIDsEnv,
			)
		}
		if _, exists := seenInherited[parsed]; exists {
			return nil, fmt.Errorf(
				"%s contains duplicate launch-variable IDs",
				InheritedIDsEnv,
			)
		}
		seenInherited[parsed] = struct{}{}
		merged = append(merged, parsed.String())
	}
	return append(merged, explicit...), nil
}

// HasInherited reports whether inherited launch variables are active.
func HasInherited(disabled bool) bool {
	return !disabled && strings.TrimSpace(os.Getenv(InheritedIDsEnv)) != ""
}

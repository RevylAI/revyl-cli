// Package launchenv owns validation shared by every CLI and MCP path that
// sends inline launch environment variables.
package launchenv

import (
	"fmt"
	"slices"
	"strings"
)

// KeyError explains why key cannot be an inline launch environment key, or
// returns nil when it can. Keys must match [A-Za-z_][A-Za-z0-9_.-]* and avoid
// the reserved prefixes. The device start API does not check inline keys, so
// every caller must.
func KeyError(key string) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if !isValidKey(key) {
		return fmt.Errorf("key %q must match [A-Za-z_][A-Za-z0-9_.-]*", key)
	}
	if isReservedKey(key) {
		return fmt.Errorf(
			"key %q uses a reserved prefix (DYLD_, SIMCTL_CHILD_, COGNISIM_) and is not allowed",
			key,
		)
	}
	return nil
}

// Validate checks every key of env, in sorted order so the error is stable.
func Validate(env map[string]string) error {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := KeyError(key); err != nil {
			return fmt.Errorf("invalid launch environment variable: %w", err)
		}
	}
	return nil
}

func isValidKey(key string) bool {
	for i := 0; i < len(key); i++ {
		ch := key[i]
		isLetter := (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
		isUnderscore := ch == '_'
		if i == 0 {
			if !isLetter && !isUnderscore {
				return false
			}
			continue
		}
		isDigit := ch >= '0' && ch <= '9'
		isDotOrHyphen := ch == '.' || ch == '-'
		if !isLetter && !isDigit && !isUnderscore && !isDotOrHyphen {
			return false
		}
	}
	return true
}

// isReservedKey blocks DYLD_/SIMCTL_CHILD_/COGNISIM_ keys, which reach dyld or
// our namespaces on the device launch path. Keep in sync with
// cognisim_schemas/utils/launch_env.py.
func isReservedKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	return strings.HasPrefix(upper, "DYLD_") ||
		strings.HasPrefix(upper, "SIMCTL_CHILD_") ||
		strings.HasPrefix(upper, "COGNISIM_")
}

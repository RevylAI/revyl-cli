// Package launcharguments owns validation shared by every CLI and MCP launch path.
package launcharguments

import (
	"fmt"
	"strings"
)

// Validate rejects tokens outside the launch-argument contract.
// Tokens are otherwise preserved exactly: whitespace, duplicates, and ordering
// are all meaningful launch-argument data.
func Validate(tokens []string) error {
	for index, token := range tokens {
		if token == "" {
			return fmt.Errorf("iOS launch argument token %d cannot be empty", index+1)
		}
		if strings.ContainsRune(token, '\x00') {
			return fmt.Errorf("iOS launch argument token %d cannot contain NUL bytes", index+1)
		}
	}
	return nil
}

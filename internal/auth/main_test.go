package auth

import (
	"os"
	"testing"
)

// TestMain neutralizes an inherited REVYL_API_KEY before the suite runs.
//
// Credential resolution prefers that variable over every stored credential, so
// running these tests on a developer machine or agent that exports a real key
// would satisfy cases written to assert an unauthenticated environment. Tests
// that need a key present opt in explicitly with t.Setenv.
func TestMain(m *testing.M) {
	if err := os.Unsetenv("REVYL_API_KEY"); err != nil {
		panic("clear REVYL_API_KEY: " + err.Error())
	}
	os.Exit(m.Run())
}

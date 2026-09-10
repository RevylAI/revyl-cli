package mcp

import (
	"os"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

// TestMain neutralizes an inherited REVYL_API_KEY before the suite runs.
//
// Authentication state is resolved from that variable first, so running these
// tests on a developer machine or agent that exports a real key would report an
// authenticated server to cases written to assert a setup failure. Tests that
// need a key present opt in explicitly with t.Setenv.
func TestMain(m *testing.M) {
	if err := os.Unsetenv("REVYL_API_KEY"); err != nil {
		panic("clear REVYL_API_KEY: " + err.Error())
	}
	api.DefaultRetryBaseDelay = time.Millisecond
	os.Exit(m.Run())
}

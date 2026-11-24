package mcp

import (
	"os"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

// TestMain clears inherited REVYL_API_KEY and CI env vars; tests opt in with t.Setenv.
func TestMain(m *testing.M) {
	if err := os.Unsetenv("REVYL_API_KEY"); err != nil {
		panic("clear REVYL_API_KEY: " + err.Error())
	}
	testutil.ClearCIEnvironment()
	api.DefaultRetryBaseDelay = time.Millisecond
	os.Exit(m.Run())
}

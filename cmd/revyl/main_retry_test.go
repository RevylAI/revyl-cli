package main

import (
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

// TestMain runs the suite without its runner's CI identity and shrinks the API
// client retry backoff so cases that stub a failing response pay their request
// count rather than production wall clock.
func TestMain(m *testing.M) {
	testutil.ClearCIEnvironment()
	api.DefaultRetryBaseDelay = time.Millisecond
	m.Run()
}

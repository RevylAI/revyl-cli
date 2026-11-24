package tui

import (
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.ClearCIEnvironment()
	api.DefaultRetryBaseDelay = time.Millisecond
	m.Run()
}

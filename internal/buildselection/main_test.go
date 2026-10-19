package buildselection

import (
	"testing"

	"github.com/revyl/cli/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.ClearCIEnvironment()
	m.Run()
}

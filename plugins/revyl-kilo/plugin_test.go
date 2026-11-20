package revylkilo_test

import (
	"os"
	"os/exec"
	"testing"
)

func TestPluginBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is required to test this plugin in CI")
		}
		t.Skip("node is not installed")
	}
	output, err := exec.Command(node, "--test", "server.test.ts").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test server.test.ts: %v\n%s", err, output)
	}
}

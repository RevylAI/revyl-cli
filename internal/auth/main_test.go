package auth

import (
	"os"
	"testing"
)

// TestMain clears an inherited REVYL_API_KEY so tests opt in to environment credentials.
func TestMain(m *testing.M) {
	if err := os.Unsetenv("REVYL_API_KEY"); err != nil {
		panic("clear REVYL_API_KEY: " + err.Error())
	}
	os.Exit(m.Run())
}

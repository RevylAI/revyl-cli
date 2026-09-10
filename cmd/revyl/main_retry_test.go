package main

import (
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

// TestMain shrinks the API client retry backoff so cases that stub a failing
// response pay their request count rather than production wall clock.
func TestMain(m *testing.M) {
	api.DefaultRetryBaseDelay = time.Millisecond
	m.Run()
}

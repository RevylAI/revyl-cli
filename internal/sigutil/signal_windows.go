//go:build windows

// Package sigutil provides platform-specific signal helpers.
package sigutil

import "os"

// RebuildSignal is nil on Windows because SIGUSR1 does not exist.
// Callers must check for nil before use and fall back to an alternative
// rebuild mechanism (e.g. file-based or named-pipe trigger).
var RebuildSignal os.Signal

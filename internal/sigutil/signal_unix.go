//go:build !windows

// Package sigutil provides platform-specific signal helpers.
package sigutil

import (
	"os"
	"syscall"
)

// RebuildSignal is the signal sent to a running `revyl dev` process to trigger
// a hot-reload rebuild. On Unix this is SIGUSR1; on Windows signals are not
// supported so the value is nil.
var RebuildSignal os.Signal = syscall.SIGUSR1

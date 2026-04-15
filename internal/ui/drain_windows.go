//go:build windows

package ui

// drainStdin is a no-op on Windows where syscall.SetNonblock is not
// compatible with os.Stdin file descriptors (Windows uses HANDLEs, not
// integer fds). Stale keypresses are rare on Windows terminals, so
// skipping the drain is an acceptable trade-off.
func drainStdin() {}

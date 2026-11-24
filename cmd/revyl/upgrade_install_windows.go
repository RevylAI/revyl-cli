//go:build windows

package main

// executableReplaceBlocker reports no blocker on Windows, where sudo is not
// the elevation path; the pre-download writability check in performSelfUpdate
// still stops an upgrade into a directory this user cannot write.
func executableReplaceBlocker(string) error {
	return nil
}

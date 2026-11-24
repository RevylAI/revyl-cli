package api

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/Masterminds/semver/v3"
)

// UpcomingMinimumVersionHeader names the response header the backend sets when
// this CLI is older than the minimum version an announced compatibility
// cutoff will require.
const UpcomingMinimumVersionHeader = "X-Revyl-CLI-Upcoming-Minimum-Version"

var upcomingMinimumVersion atomic.Value

// recordUpcomingMinimumVersion keeps a well-formed announced minimum from a
// backend response. A missing or malformed header announces nothing.
func recordUpcomingMinimumVersion(resp *http.Response) {
	if resp == nil {
		return
	}
	announced := strings.TrimPrefix(strings.TrimSpace(resp.Header.Get(UpcomingMinimumVersionHeader)), "v")
	if announced == "" {
		return
	}
	if _, err := semver.StrictNewVersion(announced); err != nil {
		return
	}
	upcomingMinimumVersion.Store(announced)
}

// UpcomingMinimumVersion returns the minimum CLI version (without a "v"
// prefix) that a backend response announced during this invocation, or ""
// when none did.
func UpcomingMinimumVersion() string {
	announced, _ := upcomingMinimumVersion.Load().(string)
	return announced
}

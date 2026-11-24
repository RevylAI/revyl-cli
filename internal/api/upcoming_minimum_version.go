package api

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/Masterminds/semver/v3"
)

const UpcomingMinimumVersionHeader = "X-Revyl-CLI-Upcoming-Minimum-Version"

var upcomingMinimumVersion atomic.Value

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

func UpcomingMinimumVersion() string {
	announced, _ := upcomingMinimumVersion.Load().(string)
	return announced
}

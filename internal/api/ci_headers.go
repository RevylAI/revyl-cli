package api

import (
	"net/http"
	"strconv"

	"github.com/revyl/cli/internal/build"
)

// setCIHeaders attaches X-CI-* headers when the CLI is invoked from a supported
// CI runner. Missing values are omitted rather than sent as empty headers.
func setCIHeaders(req *http.Request) {
	context, ok := build.CIContextFromContext(req.Context())
	if !ok {
		return
	}

	setIf(req, "X-CI-System", context.System)
	setIf(req, "X-CI-Commit-SHA", context.CommitSHA)
	setIf(req, "X-CI-Branch", context.Branch)
	setIf(req, "X-CI-Run-ID", context.RunID)
	setIf(req, "X-CI-Run-URL", context.RunURL)
	setIf(req, "X-CI-Repository", context.Repository)
	setIf(req, "X-CI-Actor", context.Actor)
	setIf(req, "X-CI-Actor-URL", context.ActorURL)
	setIf(req, "X-CI-PR-URL", context.ReviewURL)
	if context.ReviewNumber > 0 {
		setIf(req, "X-CI-PR-Number", strconv.Itoa(context.ReviewNumber))
	}
}

func setIf(req *http.Request, key, value string) {
	if value != "" {
		req.Header.Set(key, value)
	}
}

package sessionprofile

import (
	"errors"
	"fmt"

	"github.com/revyl/cli/internal/api"
)

var refusalNextSteps = map[string]string{
	"session_profile_undefined":           "define it under session.profiles in .revyl/config.yaml, or pick another profile or none",
	"session_profile_script_unavailable":  "check that the profile's script exists in .revyl/scripts, at its repository path, or in the Library",
	"session_profile_output_invalid":      "fix the output the message names in the profile's script",
	"session_profile_workers_unavailable": "start with --session-profile none until Revyl's device workers update",
	"session_profile_runners_unavailable": "start with --session-profile none until Revyl's hosted runners update",
	"project_not_found":                   "check that project.id in .revyl/config.yaml names a project in this organization",
	"repository_connection_missing":       "connect the project's Git repository to Revyl, or pick a profile that doesn't need it",
	"repository_unavailable":              "Revyl couldn't read the repository just now; retry in a minute",
}

// RefusalError carries a backend diagnostic for a profile the start couldn't
// apply. Refusal details must omit script output and secret values.
type RefusalError struct {
	Code   string
	Detail string
}

func (e *RefusalError) Error() string {
	return fmt.Sprintf("Revyl refused the session profile (%s): %s; %s", e.Code, e.Detail, refusalNextSteps[e.Code])
}

func AsRefusal(err error) (*RefusalError, bool) {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return nil, false
	}
	if _, ok := refusalNextSteps[apiErr.Code]; !ok {
		return nil, false
	}
	detail := apiErr.Detail
	if detail == "" {
		detail = apiErr.Message
	}
	return &RefusalError{Code: apiErr.Code, Detail: detail}, true
}

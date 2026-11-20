// Package sessionprofile runs a project's session profile script before a CLI
// session start and turns its output files into the start request's applied
// session_profile instruction. The product rules live in the public guide,
// cognisim-docs/devices/session-profiles.mdx.
package sessionprofile

import (
	"time"
)

// ScriptTimeout bounds one profile script run, matching the hosted runner's
// SESSION_PROFILE_SCRIPT_TIMEOUT_SECONDS.
const ScriptTimeout = 300 * time.Second

// Outcome classifies how preparing a session profile ended, using the values
// of the session_profile_outcome analytics property.
type Outcome string

const (
	OutcomeApplied            Outcome = "applied"
	OutcomeScriptFailed       Outcome = "script_failed"
	OutcomeScriptTimedOut     Outcome = "script_timed_out"
	OutcomeOutputInvalid      Outcome = "output_invalid"
	OutcomeScriptUnavailable  Outcome = "script_unavailable"
	OutcomeInterpreterMissing Outcome = "interpreter_missing"
	OutcomeBackendUnsupported Outcome = "backend_unsupported"
	OutcomeDeepLinkFailed     Outcome = "deep_link_failed"
)

// Error is a session profile failure whose Reason never holds a script's
// output or an output value, so it is safe in CLI, MCP and status output.
type Error struct {
	Outcome Outcome
	Reason  string
}

func (e *Error) Error() string {
	return e.Reason
}

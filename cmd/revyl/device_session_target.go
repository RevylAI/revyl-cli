package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

const (
	sessionFlagName   = "s"
	sessionIDFlagName = "session-id"

	// sessionIDEnvVar scopes a whole shell to one device session so parallel
	// batch workers do not have to repeat the flag on every command.
	sessionIDEnvVar = "REVYL_SESSION_ID"

	sessionTargetUsage = "Session to target: local index (e.g. 0) or server-issued session ID. Defaults to the active session."
	sessionIDUsage     = "Server-issued session ID to target (parallel-safe; ignores local session indexes)"
)

// sessionUUIDPattern matches the server-issued session ID format. Session IDs
// are UUIDs and can never parse as an integer, which is what makes a single
// -s flag able to carry either form unambiguously.
var sessionUUIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// registerSessionTargetFlags adds session targeting to a single command.
//
// Parameters:
//   - cmd: The command to receive -s and --session-id.
func registerSessionTargetFlags(cmd *cobra.Command) {
	cmd.Flags().StringP(sessionFlagName, "s", "", sessionTargetUsage)
	cmd.Flags().String(sessionIDFlagName, "", sessionIDUsage)
}

// registerPersistentSessionTargetFlags adds session targeting to a parent
// command so every subcommand inherits it.
//
// Parameters:
//   - cmd: The parent command whose subcommands need session targeting.
func registerPersistentSessionTargetFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringP(sessionFlagName, "s", "", sessionTargetUsage)
	cmd.PersistentFlags().String(sessionIDFlagName, "", sessionIDUsage)
}

// sessionTarget is the resolved intent of a command's session selection, before
// any backend or local lookup happens.
type sessionTarget struct {
	// SessionID is the durable server-issued ID to target, or empty.
	SessionID string

	// Index is the local session index to target. Only meaningful when
	// SessionID is empty. -1 means "the active session".
	Index int
}

// ByDurableID reports whether this target uses a server-issued session ID, which
// selects the stateless resolution path.
func (t sessionTarget) ByDurableID() bool {
	return t.SessionID != ""
}

// sessionTargetFromCommand determines which session a command should act on.
//
// Precedence is explicit --session-id, then explicit -s, then the
// REVYL_SESSION_ID environment variable, then the active local session. A -s
// value that parses as an integer is a local index; anything else must be a
// server-issued session ID.
//
// Commands without session flags registered (for example the `dev` commands
// that build a manager for their own purposes) always resolve to the active
// session. They cannot receive the flags, and REVYL_SESSION_ID is scoped to the
// commands that opted into targeting, so exporting it for a parallel batch does
// not silently retarget an unrelated command.
//
// Parameters:
//   - cmd: The running command, used for flag lookup.
//
// Returns:
//   - sessionTarget: The requested target.
//   - error: If --session-id and -s name different sessions, or if -s is
//     neither an integer nor a session ID.
func sessionTargetFromCommand(cmd *cobra.Command) (sessionTarget, error) {
	// --session-id and REVYL_SESSION_ID are unambiguous by name, so their values
	// pass through unvalidated and the backend reports unknown IDs. Only -s needs
	// shape checking, because it has to tell an index from an ID.
	explicitID := strings.TrimSpace(lookupSessionFlag(cmd, sessionIDFlagName))

	rawSelector := strings.TrimSpace(lookupSessionFlag(cmd, sessionFlagName))
	selectorID := ""
	selectorIndex := -1
	if rawSelector != "" {
		if idx, convErr := strconv.Atoi(rawSelector); convErr == nil {
			selectorIndex = idx
		} else if sessionUUIDPattern.MatchString(rawSelector) {
			selectorID = rawSelector
		} else {
			return sessionTarget{}, fmt.Errorf(
				"invalid -%s %q: expected a session index (e.g. 0) or a server-issued session ID (run '%s device list' to see active sessions)",
				sessionFlagName, rawSelector, deviceCommandPrefix(cmd),
			)
		}
	}

	if explicitID != "" {
		if selectorID != "" && !strings.EqualFold(selectorID, explicitID) {
			return sessionTarget{}, fmt.Errorf(
				"conflicting session targets: --%s %s and -%s %s name different sessions",
				sessionIDFlagName, explicitID, sessionFlagName, selectorID,
			)
		}
		if selectorIndex >= 0 {
			return sessionTarget{}, fmt.Errorf(
				"conflicting session targets: --%s selects a session ID while -%s selects index %d",
				sessionIDFlagName, sessionFlagName, selectorIndex,
			)
		}
		return sessionTarget{SessionID: explicitID}, nil
	}

	if selectorID != "" {
		return sessionTarget{SessionID: selectorID}, nil
	}
	if rawSelector != "" {
		return sessionTarget{Index: selectorIndex}, nil
	}

	if commandSupportsSessionTargeting(cmd) {
		if envID := strings.TrimSpace(os.Getenv(sessionIDEnvVar)); envID != "" {
			return sessionTarget{SessionID: envID}, nil
		}
	}

	return sessionTarget{Index: -1}, nil
}

// sessionDisplayLabel formats a session for human-readable output.
//
// Sessions resolved by durable ID have no local index, so they are identified
// by session ID alone rather than by a meaningless sentinel index.
//
// Parameters:
//   - session: The resolved session, which may be nil.
//
// Returns:
//   - string: A label suitable for progress and status messages.
func sessionDisplayLabel(session *mcppkg.DeviceSession) string {
	if session == nil {
		return "unknown"
	}
	if session.Index == mcppkg.UnattachedSessionIndex {
		return session.SessionID
	}
	return fmt.Sprintf("%d (%s)", session.Index, session.SessionID)
}

// commandNeedsSessionInventory reports whether this command must hydrate the
// local session list from the backend and cache, even when a durable session
// ID is also present.
//
// `--all` is inventory teardown: it walks every session StopAllSessions knows
// about. A durable ID (including REVYL_SESSION_ID) would otherwise skip
// SyncSessions and leave that map empty, so the command would report success
// without cancelling anything.
//
// Parameters:
//   - cmd: The running command.
//
// Returns:
//   - bool: True when --all is set on this command.
func commandNeedsSessionInventory(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	all, err := cmd.Flags().GetBool("all")
	return err == nil && all
}

// commandSupportsSessionTargeting reports whether this command opted into
// session targeting by registering the flags.
//
// This gates the environment variable. A command that never registered the
// flags, such as the `dev` commands, manages its own session selection through
// its dev context; letting an exported REVYL_SESSION_ID reach it would send it
// down the stateless path, skipping local-session hydration and suppressing the
// writes that record its session.
//
// Cobra merges inherited persistent flags before RunE, so subcommands that
// inherit the flags from a parent qualify too.
//
// Parameters:
//   - cmd: The running command.
//
// Returns:
//   - bool: True when -s and --session-id apply to this command.
func commandSupportsSessionTargeting(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	return cmd.Flags().Lookup(sessionIDFlagName) != nil
}

// lookupSessionFlag reads a session flag, tolerating commands that never
// registered it. Cobra returns an error rather than a value in that case, and
// the correct behavior is to fall through to the next precedence rule.
func lookupSessionFlag(cmd *cobra.Command, name string) string {
	if cmd == nil {
		return ""
	}
	value, err := cmd.Flags().GetString(name)
	if err != nil {
		return ""
	}
	return value
}

// resolveSessionTarget resolves a command's session selection into a usable
// session.
//
// Durable-ID targets go through the stateless resolver, which never reads or
// writes .revyl/device-sessions.json, so parallel CLI processes sharing a
// worktree cannot corrupt each other's session list. Index targets keep the
// existing local-store behavior, with one announced fallback: when -s names
// an index that no longer exists and exactly one session is live, that
// session is used. The fallback is refused when the stale index's own session
// is known to have ended after the live one started, because the live session
// then ran alongside it and likely belongs to another caller.
//
// Parameters:
//   - cmd: The running command.
//   - mgr: The device session manager.
//
// Returns:
//   - *mcppkg.DeviceSession: The resolved session.
//   - error: A humanized resolution failure naming the next useful action.
func resolveSessionTarget(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager) (*mcppkg.DeviceSession, error) {
	target, err := sessionTargetFromCommand(cmd)
	if err != nil {
		return nil, err
	}
	if !target.ByDurableID() && target.Index < 0 {
		return resolveUntargetedSession(cmd, mgr)
	}

	session, err := resolveExactSessionTarget(cmd, mgr)
	var lookupErr *mcppkg.SessionLookupError
	if err == nil || !errors.As(err, &lookupErr) || lookupErr.Reason != mcppkg.SessionLookupIndexNotFound {
		return session, err
	}

	live := mgr.ListSessions()
	if len(live) != 1 {
		return nil, err
	}
	only := live[0]
	label := fmt.Sprintf("%d (%s %s)", only.Index, only.Platform, truncatePrefix(only.SessionID, 8))
	if starting := mgr.UnreachableSessions(); len(starting) > 0 {
		return nil, fmt.Errorf(
			"%w. Session %s is still starting, so %s is not the only live session and was not used in its place; pass -s with the session ID you want",
			err, truncatePrefix(starting[0].SessionID, 8), label,
		)
	}
	ended, indexEnded := mgr.EndedSessionAtIndex(lookupErr.Index)
	if indexEnded && only.StartedAt.Before(ended.EndedAt) {
		return nil, fmt.Errorf(
			"%w. The only active session, %s, was already running before that session ended, so it was not used in its place; pass -s %d if it is yours",
			err, label, only.Index,
		)
	}

	ui.PrintInfo("Using session %d, %s %s (index %d is stale and this is the only active session). Pass -s <index or session ID> to choose another.",
		only.Index, only.Platform, truncatePrefix(only.SessionID, 8), lookupErr.Index)
	recordAppliedDefault(cmd, appliedDefault{Flag: sessionFlagName, Value: strconv.Itoa(only.Index), Reason: "stale_index_only_active_session"})
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain:       "device_session_target",
		DomainStatus: "adopted_only_active_session",
	})
	return only, nil
}

// resolveUntargetedSession picks the session for a command that named none,
// preferring the sessions this directory started or selected (see
// DeviceSessionManager.ResolveUntargetedSession). It announces the choice when
// it changes from the previous untargeted command here, and refuses when
// several sessions are live and none belongs to this directory.
func resolveUntargetedSession(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager) (*mcppkg.DeviceSession, error) {
	choice, err := mgr.ResolveUntargetedSession()
	var lookupErr *mcppkg.SessionLookupError
	if errors.As(err, &lookupErr) && lookupErr.Reason == mcppkg.SessionLookupNoneStartedHere {
		analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
			Domain:       "device_session_target",
			DomainStatus: "refused_none_started_here",
		})
		return nil, untargetedSessionRefusal(cmd, mgr)
	}
	if err != nil {
		return nil, humanizeDeviceSessionResolveError(cmd, err)
	}

	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Domain:       "device_session_target",
		DomainStatus: string(choice.Rule),
	})
	if !choice.Changed {
		return choice.Session, nil
	}
	mgr.RecordUntargetedChoice(choice.Session)
	if !choice.HadPrevious && choice.StartedHere < 2 {
		return choice.Session, nil
	}

	why := map[mcppkg.UntargetedRule]string{
		mcppkg.UntargetedSelected:    "last started or selected in this directory",
		mcppkg.UntargetedStartedHere: fmt.Sprintf("most recently started of %d live sessions started here", choice.StartedHere),
		mcppkg.UntargetedOnlyLive:    "the only live session",
	}[choice.Rule]
	flag, selector := sessionTargetFlagFor(cmd, choice.Session)
	ui.PrintInfo("Using session %d, %s %s (%s). Pass %s to choose another.",
		choice.Session.Index, choice.Session.Platform, truncatePrefix(choice.Session.SessionID, 8), why, sessionTargetFlagUsage(flag))
	recordAppliedDefault(cmd, appliedDefault{Flag: flag, Value: selector, Reason: string(choice.Rule)})
	return choice.Session, nil
}

// sessionTargetFlagFor names the session-targeting flag this command accepts
// and the value that selects the session with it. Commands with -s take an
// index or session ID; commands with only --session-id take the ID.
func sessionTargetFlagFor(cmd *cobra.Command, session *mcppkg.DeviceSession) (flag, value string) {
	if cmd.Flags().Lookup(sessionFlagName) != nil {
		if session.SessionID == "" {
			return sessionFlagName, strconv.Itoa(session.Index)
		}
		return sessionFlagName, session.SessionID
	}
	return sessionIDFlagName, session.SessionID
}

func sessionTargetFlagUsage(flag string) string {
	if flag == sessionFlagName {
		return "-s <index or session ID>"
	}
	return "--session-id <session ID>"
}

// untargetedSessionRefusal lists the live sessions with the command that
// targets each one, for an untargeted command that found several live
// sessions and none started or selected in this directory.
func untargetedSessionRefusal(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager) error {
	live := mgr.ListSessions()
	for _, unreachable := range mgr.UnreachableSessions() {
		live = append(live, &unreachable)
	}
	commandPath := cmd.CommandPath()
	var list strings.Builder
	flagSpelling := "-s"
	for _, session := range live {
		flag, selector := sessionTargetFlagFor(cmd, session)
		if flag == sessionIDFlagName {
			flagSpelling = "--" + sessionIDFlagName
		}
		age := "age unknown"
		if !session.StartedAt.IsZero() {
			age = "started " + mcppkg.FormatAge(time.Since(session.StartedAt)) + " ago"
		}
		if session.Index == mcppkg.UnattachedSessionIndex {
			age = "still starting"
		}
		fmt.Fprintf(&list, "\n  %-8s  %-7s  %-16s  %s %s %s", truncatePrefix(session.SessionID, 8), session.Platform, age, commandPath, flagSpelling, selector)
	}
	return fmt.Errorf(
		"multiple device sessions are live and none was started or selected in this directory, so '%s' did not pick one. Rerun it with %s for your session (keep your other flags), or choose one for this directory with '%s use <index>':%s",
		commandPath, flagSpelling, deviceCommandPrefix(cmd)+" device", list.String(),
	)
}

// resolveExactSessionTarget resolves the requested session and never
// substitutes another one for a stale index. Teardown uses it so a stop can
// only ever end the session the caller named.
func resolveExactSessionTarget(cmd *cobra.Command, mgr *mcppkg.DeviceSessionManager) (*mcppkg.DeviceSession, error) {
	target, err := sessionTargetFromCommand(cmd)
	if err != nil {
		return nil, err
	}

	if target.ByDurableID() {
		session, resolveErr := mgr.ResolveSessionByID(cmd.Context(), target.SessionID)
		if resolveErr != nil {
			return nil, humanizeDeviceSessionResolveError(cmd, resolveErr)
		}
		return session, nil
	}

	session, resolveErr := mgr.ResolveSession(target.Index)
	if resolveErr != nil {
		return nil, humanizeDeviceSessionResolveError(cmd, resolveErr)
	}
	return session, nil
}

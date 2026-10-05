package mcp

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/revyl/cli/internal/api"
)

// EndedSession is the local record SyncSessions keeps when it prunes a session
// the backend confirmed has ended. It lets the next "no session" error say
// which session ended, when, and why, instead of only that nothing is running.
type EndedSession struct {
	Index              int                `json:"index"`
	SessionID          string             `json:"session_id"`
	WorkflowRunID      string             `json:"workflow_run_id,omitempty"`
	Platform           string             `json:"platform"`
	EndedAt            time.Time          `json:"ended_at"`
	Reason             EndedSessionReason `json:"reason"`
	IdleTimeoutSeconds int                `json:"idle_timeout_seconds,omitempty"`
}

// EndedSessionReason is the bounded cause recorded for an ended session.
type EndedSessionReason string

const (
	EndedSessionIdleTimeout EndedSessionReason = "idle_timeout"
	EndedSessionStopped     EndedSessionReason = "stopped"
	EndedSessionCancelled   EndedSessionReason = "cancelled"
	EndedSessionFailed      EndedSessionReason = "failed"
	EndedSessionTimedOut    EndedSessionReason = "timed_out"
	EndedSessionUnknown     EndedSessionReason = "unknown"
)

const (
	endedSessionRetention = 24 * time.Hour
	endedSessionLimit     = 10
)

// endedSessionFromPrune builds the record for a session the backend confirmed
// ended. detail is nil when the backend no longer knows the session at all.
// The idle timeout is only taken from the backend's record of the session, so
// a locally guessed value is never reported as the one Revyl enforced.
func endedSessionFromPrune(session *DeviceSession, detail *api.DeviceSessionDetail, now time.Time) *EndedSession {
	ended := &EndedSession{
		Index:         session.Index,
		SessionID:     session.SessionID,
		WorkflowRunID: session.WorkflowRunID,
		Platform:      session.Platform,
		EndedAt:       now,
		Reason:        EndedSessionUnknown,
	}
	if detail == nil {
		return ended
	}
	if detail.EndedAt != nil {
		if endedAt, err := time.Parse(time.RFC3339Nano, *detail.EndedAt); err == nil {
			ended.EndedAt = endedAt
		}
	}
	if seconds, ok := detail.SourceMetadata["idle_timeout_seconds"].(float64); ok && seconds > 0 {
		ended.IdleTimeoutSeconds = int(seconds)
	}

	status := strings.ToLower(detail.Status)
	errorMessage := ""
	if detail.ErrorMessage != nil {
		errorMessage = strings.ToLower(*detail.ErrorMessage)
	}
	_, stopRequested := detail.SourceMetadata["stop_request"]
	idleTimeout := time.Duration(ended.IdleTimeoutSeconds) * time.Second
	switch {
	case status == "failed":
		ended.Reason = EndedSessionFailed
	case strings.Contains(errorMessage, "idle"):
		ended.Reason = EndedSessionIdleTimeout
	case status == "cancelled":
		ended.Reason = EndedSessionCancelled
	case stopRequested:
		ended.Reason = EndedSessionStopped
	case status == "timeout":
		ended.Reason = EndedSessionTimedOut
	case idleTimeout > 0 && !session.LastActivity.IsZero() && ended.EndedAt.Sub(session.LastActivity) >= idleTimeout:
		ended.Reason = EndedSessionIdleTimeout
	}
	return ended
}

// locallyEndedSession records a session this manager stopped itself, so a
// later stale index can see that its session ended and when. An idle stop
// reports the manager's own configured timeout, which it enforced.
func locallyEndedSession(session *DeviceSession, index int, reason EndedSessionReason) *EndedSession {
	ended := &EndedSession{
		Index:         index,
		SessionID:     session.SessionID,
		WorkflowRunID: session.WorkflowRunID,
		Platform:      session.Platform,
		EndedAt:       time.Now(),
		Reason:        reason,
	}
	if reason == EndedSessionIdleTimeout {
		ended.IdleTimeoutSeconds = int(session.IdleTimeout / time.Second)
	}
	return ended
}

// describe renders what ended, how long ago, and why, for example
// "session 0 (ios 9f3c1a2b) ended 6m ago after 300s without device activity (idle timeout)".
func (e *EndedSession) describe(now time.Time) string {
	return fmt.Sprintf("session %d (%s %s) %s", e.Index, e.Platform, shortPrefix(e.identity(), 8), e.endedPhrase(now))
}

// identity is the session ID, or the workflow run ID for a session that
// ended before the backend issued its session ID.
func (e *EndedSession) identity() string {
	if e.SessionID != "" {
		return e.SessionID
	}
	return e.WorkflowRunID
}

// endedPhrase renders when and why the session ended, for example
// "ended 6m ago after 300s without device activity (idle timeout)".
func (e *EndedSession) endedPhrase(now time.Time) string {
	why := ""
	switch e.Reason {
	case EndedSessionIdleTimeout:
		why = " (idle timeout)"
		if e.IdleTimeoutSeconds > 0 {
			why = fmt.Sprintf(" after %ds without device activity (idle timeout)", e.IdleTimeoutSeconds)
		}
	case EndedSessionStopped:
		why = " because a stop was requested"
	case EndedSessionCancelled:
		why = " because it was cancelled"
	case EndedSessionFailed:
		why = " because it failed on the device worker"
	case EndedSessionTimedOut:
		why = " because it timed out"
	}
	return fmt.Sprintf("ended %s ago%s", FormatAge(now.Sub(e.EndedAt)), why)
}

// FormatAge renders an elapsed time compactly, for example "45s", "6m", "3h", or "4d".
func FormatAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(age.Seconds())))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

// mergeEndedSessions combines persisted and newly pruned records, keeping the
// newest record per session and only recent, bounded history.
func mergeEndedSessions(existing, added []*EndedSession, now time.Time) []*EndedSession {
	byIdentity := make(map[string]*EndedSession, len(existing)+len(added))
	for _, ended := range append(append([]*EndedSession(nil), existing...), added...) {
		if ended == nil || ended.identity() == "" || now.Sub(ended.EndedAt) > endedSessionRetention {
			continue
		}
		if previous, ok := byIdentity[ended.identity()]; ok && previous.EndedAt.After(ended.EndedAt) {
			continue
		}
		byIdentity[ended.identity()] = ended
	}
	merged := make([]*EndedSession, 0, len(byIdentity))
	for _, ended := range byIdentity {
		merged = append(merged, ended)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].EndedAt.After(merged[j].EndedAt)
	})
	if len(merged) > endedSessionLimit {
		merged = merged[:endedSessionLimit]
	}
	return merged
}

// EndedSessionAtIndex returns the most recent ended session that held a local
// index, if one is still remembered.
func (m *DeviceSessionManager) EndedSessionAtIndex(index int) (EndedSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ended := m.endedSessionAtIndexLocked(index)
	if ended == nil {
		return EndedSession{}, false
	}
	return *ended, true
}

func (m *DeviceSessionManager) endedSessionAtIndexLocked(index int) *EndedSession {
	for _, ended := range m.endedSessions {
		if ended.Index == index {
			return ended
		}
	}
	return nil
}

// endedSessionGuidance explains the ended session and names how to start a
// replacement on the same platform.
func (m *DeviceSessionManager) endedSessionGuidance(ended *EndedSession) string {
	start := m.nextStep(nextStepStartSession)
	if ended.Platform == "ios" || ended.Platform == "android" {
		start = fmt.Sprintf(m.nextStep(nextStepStartPlatformSession), ended.Platform)
	}
	return fmt.Sprintf("%s. %s", capitalizeFirst(ended.describe(time.Now())), start)
}

func capitalizeFirst(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

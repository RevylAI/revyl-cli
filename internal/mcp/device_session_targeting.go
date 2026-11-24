package mcp

import "sort"

type persistedSessionRef struct {
	SessionID     string `json:"session_id,omitempty"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
}

func (r persistedSessionRef) identity() sessionCacheIdentity {
	return sessionCacheIdentity{sessionID: r.SessionID, workflowRunID: r.WorkflowRunID}
}

func sessionRefFor(session *DeviceSession) persistedSessionRef {
	return persistedSessionRef{SessionID: session.SessionID, WorkflowRunID: session.WorkflowRunID}
}

func liveSessionRef(ref persistedSessionRef, sessions []*DeviceSession) (persistedSessionRef, bool) {
	index, ok := persistedSessionIndex(sessions, ref.identity())
	if !ok {
		return persistedSessionRef{}, false
	}
	return sessionRefFor(sessions[index]), true
}

func liveSessionRefs(refs []persistedSessionRef, sessions []*DeviceSession) []persistedSessionRef {
	kept := make([]persistedSessionRef, 0, len(refs))
	for _, ref := range refs {
		live, ok := liveSessionRef(ref, sessions)
		if !ok {
			continue
		}
		duplicate := false
		for _, existing := range kept {
			if sessionCacheIdentitiesMatch(existing.identity(), live.identity()) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, live)
		}
	}
	return kept
}

func optionalLiveSessionRef(ref *persistedSessionRef, sessions []*DeviceSession) *persistedSessionRef {
	if ref == nil {
		return nil
	}
	live, ok := liveSessionRef(*ref, sessions)
	if !ok {
		return nil
	}
	return &live
}

type UntargetedRule string

const (
	UntargetedSelected    UntargetedRule = "selected_here"
	UntargetedStartedHere UntargetedRule = "started_here"
	UntargetedOnlyLive    UntargetedRule = "only_live_session"
)

type UntargetedSessionChoice struct {
	Session     *DeviceSession
	Rule        UntargetedRule
	StartedHere int
	Changed     bool
	HadPrevious bool
}

// ResolveUntargetedSession chooses the session for a command that named none,
// preferring sessions this directory started or selected so that parallel
// agents on one account stay on their own devices:
//
//  1. The session last started or selected in this directory, while live.
//  2. Otherwise the most recently started live session started here.
//  3. Otherwise the account's only live session. When several are live and
//     none belongs to this directory, it returns a SessionLookupError with
//     reason SessionLookupNoneStartedHere instead of guessing.
//
// It reads local state only; callers sync first.
func (m *DeviceSessionManager) ResolveUntargetedSession() (UntargetedSessionChoice, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*DeviceSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Index < sessions[j].Index })

	choice := UntargetedSessionChoice{}
	var startedHere []*DeviceSession
	for _, session := range sessions {
		identity := sessionCacheIdentityFor(session)
		if m.selected != nil && sessionCacheIdentitiesMatch(*m.selected, identity) {
			choice.Session, choice.Rule = session, UntargetedSelected
		}
		for _, started := range m.startedHere {
			if sessionCacheIdentitiesMatch(started, identity) {
				startedHere = append(startedHere, session)
				break
			}
		}
	}
	choice.StartedHere = len(startedHere)
	choice.HadPrevious = m.lastUntargeted != nil

	switch {
	case choice.Session != nil:
	case len(startedHere) > 0:
		newest := startedHere[0]
		for _, session := range startedHere[1:] {
			if !session.StartedAt.Before(newest.StartedAt) {
				newest = session
			}
		}
		choice.Session, choice.Rule = newest, UntargetedStartedHere
	case len(sessions) == 1 && len(m.unreachableSessions) == 0:
		choice.Session, choice.Rule = sessions[0], UntargetedOnlyLive
	case len(sessions)+len(m.unreachableSessions) > 1:
		return UntargetedSessionChoice{}, &SessionLookupError{
			Reason:  SessionLookupNoneStartedHere,
			Index:   -1,
			message: "multiple device sessions are live and none was started or selected in this directory",
		}
	default:
		_, err := m.resolveSessionLocked(-1)
		return UntargetedSessionChoice{}, err
	}

	choice.Changed = m.lastUntargeted == nil || !sessionCacheIdentitiesMatch(*m.lastUntargeted, sessionCacheIdentityFor(choice.Session))
	return choice, nil
}

func (m *DeviceSessionManager) RecordUntargetedChoice(session *DeviceSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.persistSessionsWithMutation(sessionCacheMutation{untargetedChoice: session})
}

func (m *DeviceSessionManager) loadDirectoryTargeting(state persistedState) {
	m.startedHere = make([]sessionCacheIdentity, 0, len(state.StartedHere))
	for _, ref := range state.StartedHere {
		m.startedHere = append(m.startedHere, ref.identity())
	}
	m.selected, m.lastUntargeted = nil, nil
	if state.Selected != nil {
		identity := state.Selected.identity()
		m.selected = &identity
	}
	if state.LastUntargeted != nil {
		identity := state.LastUntargeted.identity()
		m.lastUntargeted = &identity
	}
}

func (m *DeviceSessionManager) directoryTargetingRefs() ([]persistedSessionRef, *persistedSessionRef, *persistedSessionRef) {
	ref := func(identity sessionCacheIdentity) persistedSessionRef {
		return persistedSessionRef{SessionID: identity.sessionID, WorkflowRunID: identity.workflowRunID}
	}
	startedHere := make([]persistedSessionRef, 0, len(m.startedHere))
	for _, identity := range m.startedHere {
		startedHere = append(startedHere, ref(identity))
	}
	var selected, lastUntargeted *persistedSessionRef
	if m.selected != nil {
		value := ref(*m.selected)
		selected = &value
	}
	if m.lastUntargeted != nil {
		value := ref(*m.lastUntargeted)
		lastUntargeted = &value
	}
	return startedHere, selected, lastUntargeted
}

func (m *DeviceSessionManager) applyDirectoryTargeting(mutation sessionCacheMutation) {
	if mutation.startedSession != nil {
		m.startedHere = append(m.startedHere, sessionCacheIdentityFor(mutation.startedSession))
	}
	if mutation.selectedSession != nil {
		identity := sessionCacheIdentityFor(mutation.selectedSession)
		m.selected = &identity
	}
	if mutation.untargetedChoice != nil {
		identity := sessionCacheIdentityFor(mutation.untargetedChoice)
		m.lastUntargeted = &identity
	}
}

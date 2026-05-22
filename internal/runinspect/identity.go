package runinspect

import (
	"strings"
)

// IdentityKind classifies what an identity field represents.
type IdentityKind string

const (
	IdentityKindUser     IdentityKind = "user"
	IdentityKindEmail    IdentityKind = "email"
	IdentityKindUsername IdentityKind = "username"
	IdentityKindRole     IdentityKind = "role"
	IdentityKindAccount  IdentityKind = "account"
	IdentityKindOrg      IdentityKind = "org"
	IdentityKindVendor   IdentityKind = "vendor"
)

// IdentityField is one detected identity-bearing value. Multiple
// observations of the same source key over the run are deduped into a
// single field, with `first_seen_step` / `last_seen_step` / `rotated`
// indicating whether the value was stable.
type IdentityField struct {
	Kind          IdentityKind `json:"kind"`
	Label         string       `json:"label"`
	Value         interface{}  `json:"value"`
	Source        FieldSource  `json:"source"`
	FirstSeenStep int          `json:"first_seen_step"`
	LastSeenStep  int          `json:"last_seen_step"`
	Rotated       bool         `json:"rotated"`
	// Vendor names a recognised SaaS for vendor-kind fields
	// (e.g. "Statsig", "Segment", "Sentry"). Empty for app-internal.
	Vendor string `json:"vendor,omitempty"`
}

// IdentityOptions tunes the identity detector.
type IdentityOptions struct {
	// AtStep, when non-zero, restricts resolution to lines whose step
	// index is ≤ AtStep.
	AtStep int
}

// DetectIdentity scans the recorded JSONL lines for identity-bearing
// values and returns one field per unique (source, key) location.
// Floor heartbeat lines are ignored.
func DetectIdentity(
	lines []DeviceStateLine,
	indexer StepIndexer,
	opts IdentityOptions,
) []IdentityField {
	bucket := make(map[string]*identityAcc)
	keyOf := func(s FieldSource) string {
		return s.Kind + "|" + s.Path + "|" + s.KeyPath
	}

	for i, line := range lines {
		if line.IsFloor() {
			continue
		}
		stepIdx := indexer.IndexFor(line.StepID, i+1)
		if opts.AtStep > 0 && stepIdx > opts.AtStep {
			continue
		}
		for path, change := range line.Changed {
			if change.Kind != "plist" {
				// Identity is overwhelmingly in UserDefaults. SQLite
				// could in principle hold a session row, but the
				// false-positive risk is high and the detector kept
				// simpler-by-design.
				continue
			}
			walkPlist(change.Values, "", func(keyPath string, value interface{}) {
				field := classifyIdentity(keyPath, value)
				if field == nil {
					return
				}
				field.Source = FieldSource{
					Kind:    "plist",
					Path:    path,
					KeyPath: keyPath,
				}
				key := keyOf(field.Source)
				if existing, ok := bucket[key]; ok {
					updateIdentityAcc(existing, field, stepIdx)
					return
				}
				bucket[key] = &identityAcc{
					field:      *field,
					firstStep:  stepIdx,
					lastStep:   stepIdx,
					firstValue: field.Value,
					lastValue:  field.Value,
				}
			})
		}
	}

	out := make([]IdentityField, 0, len(bucket))
	for _, a := range bucket {
		f := a.field
		f.Value = a.lastValue
		f.FirstSeenStep = a.firstStep
		f.LastSeenStep = a.lastStep
		f.Rotated = !equalValues(a.firstValue, a.lastValue)
		out = append(out, f)
	}
	return out
}

// identityAcc tracks per-source dedup state across observations.
type identityAcc struct {
	field      IdentityField
	firstStep  int
	lastStep   int
	firstValue interface{}
	lastValue  interface{}
}

func updateIdentityAcc(a *identityAcc, f *IdentityField, stepIdx int) {
	if stepIdx < a.firstStep {
		a.firstStep = stepIdx
		a.firstValue = f.Value
	}
	if stepIdx > a.lastStep {
		a.lastStep = stepIdx
		a.lastValue = f.Value
	}
}

func equalValues(a, b interface{}) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case nil:
		return b == nil
	}
	return false
}

// --- classifier -----------------------------------------------------------

// vendorRule is one exact-key vendor lookup.
type vendorRule struct {
	keySuffix string // case-insensitive suffix match on the key path
	kind      IdentityKind
	vendor    string
	label     string
}

// vendorRules covers known SaaS keys, ordered so longer matches win.
// Suffix match (case-insensitive) lets the same rule cover both
// "com.Statsig.InternalStore.stableIDKey" and the bare "stableIDKey".
var vendorRules = []vendorRule{
	{keySuffix: "com.statsig.internalstore.stableidkey", kind: IdentityKindVendor, vendor: "Statsig", label: "Statsig stable ID"},
	{keySuffix: "segment.anonymousid", kind: IdentityKindVendor, vendor: "Segment", label: "Segment anonymous ID"},
	{keySuffix: "segmentanonymousid", kind: IdentityKindVendor, vendor: "Segment", label: "Segment anonymous ID"},
	{keySuffix: "sardinesessionkey", kind: IdentityKindVendor, vendor: "Sardine", label: "Sardine session key"},
	{keySuffix: "viewerintercomhash", kind: IdentityKindVendor, vendor: "Intercom", label: "Intercom user identity hash"},
	{keySuffix: "intercom_user_id", kind: IdentityKindVendor, vendor: "Intercom", label: "Intercom user ID"},
	{keySuffix: "amplitude_user_id", kind: IdentityKindVendor, vendor: "Amplitude", label: "Amplitude user ID"},
	{keySuffix: "amplitude_device_id", kind: IdentityKindVendor, vendor: "Amplitude", label: "Amplitude device ID"},
	{keySuffix: "mp_distinct_id", kind: IdentityKindVendor, vendor: "Mixpanel", label: "Mixpanel distinct ID"},
	{keySuffix: "mp_user_id", kind: IdentityKindVendor, vendor: "Mixpanel", label: "Mixpanel user ID"},
	{keySuffix: "posthog_distinct_id", kind: IdentityKindVendor, vendor: "PostHog", label: "PostHog distinct ID"},
	{keySuffix: "posthog_anonymous_id", kind: IdentityKindVendor, vendor: "PostHog", label: "PostHog anonymous ID"},
	{keySuffix: "launchdarkly_user_id", kind: IdentityKindVendor, vendor: "LaunchDarkly", label: "LaunchDarkly user ID"},
	{keySuffix: "_ldid", kind: IdentityKindVendor, vendor: "LaunchDarkly", label: "LaunchDarkly device ID"},
	{keySuffix: "exponea_telemetry_install_id", kind: IdentityKindVendor, vendor: "Exponea", label: "Exponea install ID"},
	{keySuffix: "firinstallationsiid", kind: IdentityKindVendor, vendor: "Firebase", label: "Firebase Installations ID"},
	{keySuffix: "firinstallations.iid", kind: IdentityKindVendor, vendor: "Firebase", label: "Firebase Installations ID"},
	{keySuffix: "sentry_user_id", kind: IdentityKindVendor, vendor: "Sentry", label: "Sentry user ID"},
	{keySuffix: "sentry_session_id", kind: IdentityKindVendor, vendor: "Sentry", label: "Sentry session ID"},
	{keySuffix: "datadog_user_id", kind: IdentityKindVendor, vendor: "Datadog", label: "Datadog user ID"},
	{keySuffix: "datadog_session_id", kind: IdentityKindVendor, vendor: "Datadog", label: "Datadog session ID"},
	{keySuffix: "newrelic_user_id", kind: IdentityKindVendor, vendor: "New Relic", label: "New Relic user ID"},
}

// roleFlagKeys are exact (last-segment, lowercase) keys whose boolean
// values are surfaced as role flags. Tightly scoped to avoid surfacing
// every is-* flag in a plist.
var roleFlagKeys = map[string]string{
	"viewerisadmin":         "isAdmin",
	"viewerismanager":       "isManager",
	"viewerisowner":         "isOwner",
	"viewerissupport":       "isSupport",
	"viewerissuspended":     "isSuspended",
	"viewerisinvestigating": "isInvestigating",
	"viewerisstaff":         "isStaff",
	"viewerismember":        "isMember",
	"isadmin":               "isAdmin",
	"ismanager":             "isManager",
	"isowner":               "isOwner",
	"issupport":             "isSupport",
	"issuspended":           "isSuspended",
	"isstaff":               "isStaff",
	"ismember":              "isMember",
	"is_admin":              "isAdmin",
	"is_manager":            "isManager",
	"is_owner":              "isOwner",
	"is_staff":              "isStaff",
}

// localizationSegmentTokens are dotted-path segments whose presence
// anywhere in the key path indicates a localized UI string table,
// NOT a user-facing identity field. Apps that ship their string
// table inside a plist (rare but real — e.g. Chatbooks/Moments
// stashes them under `chatty-strings.ios.checkout.reviewOrder.*.
// displayName`) would otherwise pollute the identity dump.
//
// Match rule: split the (lowercased) key path on `.`, then check
// whether any segment is in this set OR has a known strings suffix
// (e.g. `chatty-strings`, `error-strings`). Avoids the Contains-form
// false-positive where `mystringsthatarefine.userId` would get
// caught by a bare "strings" substring.
var localizationSegmentTokens = map[string]bool{
	"strings":       true,
	"i18n":          true,
	"l10n":          true,
	"localized":     true,
	"localization":  true,
	"localizations": true,
	"translation":   true,
	"translations":  true,
	"string_table":  true,
	"stringtable":   true,
}

func pathIndicatesLocalization(keyPathLower string) bool {
	for _, seg := range strings.Split(keyPathLower, ".") {
		if localizationSegmentTokens[seg] {
			return true
		}
		// Suffix match for hyphenated `chatty-strings`, `error-strings`,
		// `app-strings`, etc.
		if strings.HasSuffix(seg, "-strings") || strings.HasSuffix(seg, "-i18n") {
			return true
		}
	}
	return false
}

// classifyIdentity returns a partially-populated IdentityField (Kind,
// Label, Value, Vendor) or nil. The caller fills in Source / step
// fields after dedup.
func classifyIdentity(keyPath string, value interface{}) *IdentityField {
	if value == nil {
		return nil
	}
	keyPathLower := strings.ToLower(keyPath)
	last := lastSegment(keyPathLower)

	// Suppress any classification when the path indicates a localized
	// string table — those `displayName` / `userName` entries are
	// product-string labels, not identity values. (A vendor key whose
	// path happens to also live under a strings-shaped segment is
	// rare enough that we'd rather lose that hypothetical positive
	// than maintain a list of vendor-exemptions.)
	if pathIndicatesLocalization(keyPathLower) {
		return nil
	}

	// 1. Exact vendor-key matches (suffix on full path).
	for _, rule := range vendorRules {
		if strings.HasSuffix(keyPathLower, rule.keySuffix) {
			s, ok := value.(string)
			if !ok || s == "" {
				continue
			}
			return &IdentityField{
				Kind:   rule.kind,
				Label:  rule.label,
				Value:  s,
				Vendor: rule.vendor,
			}
		}
	}

	// 2. Role flags (boolean values under known role-keying conventions).
	if roleLabel, ok := roleFlagKeys[last]; ok {
		if b, ok := value.(bool); ok {
			return &IdentityField{
				Kind:  IdentityKindRole,
				Label: roleLabel,
				Value: b,
			}
		}
	}

	// 3. User / org / account / email / username heuristics — only fire
	// on string values (skip bytes-encoded blobs, arrays, dicts).
	s, isStr := value.(string)
	if !isStr || s == "" {
		return nil
	}

	// Email / username conventions are very specific.
	if endsWith(last, "email") || isExactKey(last, "vieweremail", "useremail") {
		if looksLikeEmail(s) {
			return &IdentityField{Kind: IdentityKindEmail, Label: identityLabel(keyPath), Value: s}
		}
	}
	if endsWith(last, "username") || endsWith(last, "displayname") ||
		isExactKey(last, "viewerusername", "viewerdisplayname", "username", "displayname") {
		return &IdentityField{Kind: IdentityKindUsername, Label: identityLabel(keyPath), Value: s}
	}

	// User ID — `viewerUserID`, `currentUserId`, anything ending in
	// `userid` or `userId` (case-insensitive).
	if endsWith(last, "userid") || endsWith(last, "userids") ||
		isExactKey(last, "uid", "viewer_user_id") {
		return &IdentityField{Kind: IdentityKindUser, Label: identityLabel(keyPath), Value: s}
	}

	// Active account.
	if strings.Contains(last, "activeaccountid") || strings.Contains(last, "activeaccount") ||
		endsWith(last, "accountid") {
		return &IdentityField{Kind: IdentityKindAccount, Label: identityLabel(keyPath), Value: s}
	}

	// Org / workspace / team / business / company / tenant ID.
	if endsWith(last, "orgid") || endsWith(last, "organizationid") ||
		endsWith(last, "workspaceid") || endsWith(last, "teamid") ||
		endsWith(last, "tenantid") || endsWith(last, "companyid") ||
		endsWith(last, "businessid") || endsWith(last, "bizid") ||
		isExactKey(last, "selectedorgid", "currentorgid", "activeorgid",
			"selectedteamid", "currentworkspaceid") {
		return &IdentityField{Kind: IdentityKindOrg, Label: identityLabel(keyPath), Value: s}
	}

	return nil
}

func lastSegment(keyPathLower string) string {
	idx := strings.LastIndex(keyPathLower, ".")
	if idx < 0 {
		return keyPathLower
	}
	return keyPathLower[idx+1:]
}

func endsWith(s, suffix string) bool { return strings.HasSuffix(s, suffix) }

func isExactKey(s string, keys ...string) bool {
	for _, k := range keys {
		if s == k {
			return true
		}
	}
	return false
}

// identityLabel converts a key path like "viewerUserID" or
// "session.userId" into a friendly label. We just use the original
// key-path segment; the source field carries the full path for
// disambiguation.
func identityLabel(keyPath string) string {
	if idx := strings.LastIndex(keyPath, "."); idx >= 0 {
		return keyPath[idx+1:]
	}
	return keyPath
}

func looksLikeEmail(s string) bool {
	// Minimal sanity check — we don't need to handle all the RFC edge
	// cases, just rule out random tokens.
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	dot := strings.IndexByte(s[at:], '.')
	return dot > 0 && dot < len(s[at:])-1
}

// SummaryHighlights picks the most useful 4-6 identity fields for the
// `revyl run summary` view: the logged-in user, role flags, the active
// org. Drops the vendor-ID bucket entirely (those go to
// `revyl run identity`).
func SummaryHighlights(all []IdentityField) []IdentityField {
	keep := func(f IdentityField) bool {
		switch f.Kind {
		case IdentityKindUser, IdentityKindEmail, IdentityKindUsername,
			IdentityKindOrg, IdentityKindAccount:
			return true
		case IdentityKindRole:
			// Only the role flags that are TRUE (or any suspended/
			// investigating flag — those are interesting either way).
			if b, ok := f.Value.(bool); ok {
				if b {
					return true
				}
				if strings.Contains(strings.ToLower(f.Label), "suspended") ||
					strings.Contains(strings.ToLower(f.Label), "investigating") {
					return true
				}
			}
		}
		return false
	}
	out := make([]IdentityField, 0, len(all))
	for _, f := range all {
		if keep(f) {
			out = append(out, f)
		}
	}
	return out
}

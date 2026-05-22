package runinspect

import (
	"sort"
	"testing"
)

// --- classifyIdentity unit tests ------------------------------------------

func TestClassifyIdentity_VendorKeys(t *testing.T) {
	cases := []struct {
		key       string
		value     interface{}
		wantKind  IdentityKind
		wantVend  string
		wantLabel string
	}{
		{
			key:       "com.Statsig.InternalStore.stableIDKey",
			value:     "C6A4E049-2CF7-490F-91EC-F44C8587D5BC",
			wantKind:  IdentityKindVendor,
			wantVend:  "Statsig",
			wantLabel: "Statsig stable ID",
		},
		{
			key:       "segmentAnonymousId",
			value:     "6BE13940-5DC8-4D5B-A598-A9D63D47629E",
			wantKind:  IdentityKindVendor,
			wantVend:  "Segment",
			wantLabel: "Segment anonymous ID",
		},
		{
			key:       "sardineSessionKey",
			value:     "0978d678-3966-4fb1-853e-62fc7ae782f1",
			wantKind:  IdentityKindVendor,
			wantVend:  "Sardine",
			wantLabel: "Sardine session key",
		},
		{
			key:       "viewerIntercomHash",
			value:     "ded197e251b8b498d3476ff8dc4f433a27a2ec5976d74570b931a74a3edaaf92",
			wantKind:  IdentityKindVendor,
			wantVend:  "Intercom",
			wantLabel: "Intercom user identity hash",
		},
		{
			key:       "EXPONEA_TELEMETRY_INSTALL_ID",
			value:     "D67DB8A4-285A-41C2-85D6-B9A057E58B43",
			wantKind:  IdentityKindVendor,
			wantVend:  "Exponea",
			wantLabel: "Exponea install ID",
		},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			f := classifyIdentity(tc.key, tc.value)
			if f == nil {
				t.Fatalf("expected hit; got nil")
			}
			if f.Kind != tc.wantKind {
				t.Errorf("kind = %s, want %s", f.Kind, tc.wantKind)
			}
			if f.Vendor != tc.wantVend {
				t.Errorf("vendor = %s, want %s", f.Vendor, tc.wantVend)
			}
			if f.Label != tc.wantLabel {
				t.Errorf("label = %s, want %s", f.Label, tc.wantLabel)
			}
		})
	}
}

func TestClassifyIdentity_AppInternalUserKey(t *testing.T) {
	cases := []struct {
		key  string
		want IdentityKind
	}{
		{"viewerUserID", IdentityKindUser},
		{"currentUserId", IdentityKindUser},
		{"session.userId", IdentityKindUser},
		{"uid", IdentityKindUser},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			f := classifyIdentity(tc.key, "user_abc123")
			if f == nil {
				t.Fatalf("expected hit")
			}
			if f.Kind != tc.want {
				t.Errorf("kind = %s, want %s", f.Kind, tc.want)
			}
		})
	}
}

func TestClassifyIdentity_OrgKey(t *testing.T) {
	cases := []string{
		"selectedOrgId",
		"currentWorkspaceId",
		"activeOrgId",
		"selectedTeamId",
		"businessId",
		"bizId",
		"session.tenantId",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			f := classifyIdentity(key, "biz_xyz")
			if f == nil {
				t.Fatalf("expected hit on %q", key)
			}
			if f.Kind != IdentityKindOrg {
				t.Errorf("kind = %s, want org", f.Kind)
			}
		})
	}
}

func TestClassifyIdentity_AccountKey(t *testing.T) {
	cases := []string{
		"com.whop.accountManager.activeAccountId",
		"activeAccountId",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			f := classifyIdentity(key, "user_abc")
			if f == nil {
				t.Fatalf("expected hit on %q", key)
			}
			if f.Kind != IdentityKindAccount {
				t.Errorf("kind = %s, want account", f.Kind)
			}
		})
	}
}

func TestClassifyIdentity_RoleFlags(t *testing.T) {
	cases := []struct {
		key   string
		value bool
		label string
	}{
		{"viewerIsAdmin", false, "isAdmin"},
		{"viewerIsManager", true, "isManager"},
		{"viewerIsSupport", false, "isSupport"},
		{"viewerIsSuspended", true, "isSuspended"},
		{"is_admin", false, "isAdmin"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			f := classifyIdentity(tc.key, tc.value)
			if f == nil {
				t.Fatalf("expected hit")
			}
			if f.Kind != IdentityKindRole {
				t.Errorf("kind = %s, want role", f.Kind)
			}
			if f.Label != tc.label {
				t.Errorf("label = %s, want %s", f.Label, tc.label)
			}
			if got, _ := f.Value.(bool); got != tc.value {
				t.Errorf("value = %v, want %v", got, tc.value)
			}
		})
	}
}

func TestClassifyIdentity_RoleFlagOnlyForBooleans(t *testing.T) {
	// A string under a role-flag key shouldn't be classified as a role.
	f := classifyIdentity("viewerIsAdmin", "true")
	if f != nil {
		t.Errorf("expected nil for non-bool role-keyed value; got %+v", f)
	}
}

func TestClassifyIdentity_EmailRequiresEmailShape(t *testing.T) {
	cases := []struct {
		key   string
		value string
		want  bool
	}{
		{"viewerEmail", "test@example.com", true},
		{"userEmail", "iostestbot@proton.me", true},
		{"viewerEmail", "not-an-email", false},
		{"viewerEmail", "@only-after", false},
		{"viewerEmail", "before@", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			f := classifyIdentity(tc.key, tc.value)
			if (f != nil) != tc.want {
				t.Errorf("got hit=%v, want %v", f != nil, tc.want)
			}
			if f != nil && f.Kind != IdentityKindEmail {
				t.Errorf("kind = %s, want email", f.Kind)
			}
		})
	}
}

func TestClassifyIdentity_Username(t *testing.T) {
	f := classifyIdentity("viewerUsername", "iostestbot")
	if f == nil || f.Kind != IdentityKindUsername {
		t.Fatalf("expected username kind, got %+v", f)
	}
	f = classifyIdentity("viewerDisplayName", "Testbot")
	if f == nil || f.Kind != IdentityKindUsername {
		t.Fatalf("expected username kind from displayName, got %+v", f)
	}
}

func TestClassifyIdentity_LocalizationPathsSuppressed(t *testing.T) {
	// These are the false positives observed against a real Chatbooks
	// run — localized string-table entries under "*-strings.*.displayName"
	// must not be classified as identity.
	cases := []struct {
		key   string
		value string
	}{
		{"chatty-strings.ios.checkout.reviewOrder.express.displayName", "Express"},
		{"strings.profile.userName", "Anonymous"},
		{"i18n.user.displayName", "Guest"},
		{"localized.checkout.userEmail", "support@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if f := classifyIdentity(tc.key, tc.value); f != nil {
				t.Errorf("expected nil for localization-path key; got %+v", f)
			}
		})
	}
	// Sanity: a clean `viewerDisplayName` (no strings-table fragment)
	// still classifies.
	if f := classifyIdentity("viewerDisplayName", "Testbot"); f == nil {
		t.Error("expected hit on clean viewerDisplayName")
	}
}

func TestClassifyIdentity_BytesEncodedSkipped(t *testing.T) {
	// Plist `__bytes__` blobs come through as a map; ensure we skip
	// them rather than coercing to string and getting a false hit.
	bytesBlob := map[string]interface{}{
		"__bytes__":   true,
		"len":         float64(66),
		"b64_preview": "eyJoYXMi...",
	}
	if f := classifyIdentity("viewerUserID", bytesBlob); f != nil {
		t.Errorf("expected nil for bytes-encoded value; got %+v", f)
	}
}

// --- DetectIdentity integration ------------------------------------------

func TestDetectIdentity_RealisticWhopFixture(t *testing.T) {
	// Mirrors the actual values we pulled out of the Whop run earlier
	// in the conversation. Confirms the detector surfaces the right
	// fields without us needing to enumerate every key.
	lines := []DeviceStateLine{
		{
			StepID: "step-1",
			Changed: map[string]DeviceStateChange{
				"Library/Preferences/com.whop.whopapp.plist": {
					Kind: "plist",
					Values: map[string]interface{}{
						"viewerUserID":      "user_8W9oSSqmk5gqa",
						"viewerEmail":       "iostestbot@proton.me",
						"viewerUsername":    "iostestbot",
						"viewerDisplayName": "Testbot",
						"viewerIsAdmin":     false,
						"viewerIsManager":   false,
						"viewerIsSupport":   false,
						"viewerIsSuspended": false,
						"selectedOrgId":     "biz_be0uqMpB3Y9ghB",
						"com.whop.accountManager.activeAccountId": "user_8W9oSSqmk5gqa",
						"com.Statsig.InternalStore.stableIDKey":   "C6A4E049-2CF7-490F-91EC-F44C8587D5BC",
						"segmentAnonymousId":                      "6BE13940-5DC8-4D5B-A598-A9D63D47629E",
						"sardineSessionKey":                       "0978d678-3966-4fb1-853e-62fc7ae782f1",
						"viewerIntercomHash":                      "ded197e251b8b498d3476ff8dc4f433a27a2ec5976d74570b931a74a3edaaf92",
						// noise that shouldn't surface
						"lastViewedTab":             float64(0),
						"isDMsEnabled":              false,
						"hasTrackedOnboardingEvent": true,
					},
				},
			},
		},
	}
	idx := MapIndexer{"step-1": 1}
	fields := DetectIdentity(lines, idx, IdentityOptions{})

	kinds := make(map[IdentityKind]int)
	labels := make(map[string]bool)
	for _, f := range fields {
		kinds[f.Kind]++
		labels[f.Label] = true
	}
	// We want all four of: user, email, username, org, account, plus
	// at least 3 role flags + 4 vendor IDs.
	want := map[IdentityKind]int{
		IdentityKindUser:     1,
		IdentityKindEmail:    1,
		IdentityKindUsername: 2, // username + displayName
		IdentityKindOrg:      1,
		IdentityKindAccount:  1,
		IdentityKindRole:     4,
		IdentityKindVendor:   4,
	}
	for k, n := range want {
		if kinds[k] < n {
			t.Errorf("kind %s: got %d, want ≥%d", k, kinds[k], n)
		}
	}

	// Noise keys (`lastViewedTab`, `isDMsEnabled`, etc.) must not surface.
	if labels["lastViewedTab"] || labels["isDMsEnabled"] || labels["hasTrackedOnboardingEvent"] {
		t.Errorf("noise keys leaked: labels=%v", labels)
	}
}

func TestDetectIdentity_DedupAndRotation(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-a",
			Changed: map[string]DeviceStateChange{
				"Library/Preferences/com.x.plist": {
					Kind: "plist",
					Values: map[string]interface{}{
						"viewerUserID": "user_old",
					},
				},
			},
		},
		{
			StepID: "step-b",
			Changed: map[string]DeviceStateChange{
				"Library/Preferences/com.x.plist": {
					Kind: "plist",
					Values: map[string]interface{}{
						"viewerUserID": "user_new", // rotated mid-run
					},
				},
			},
		},
	}
	idx := MapIndexer{"step-a": 1, "step-b": 2}
	fields := DetectIdentity(lines, idx, IdentityOptions{})

	var userField *IdentityField
	for i := range fields {
		if fields[i].Kind == IdentityKindUser {
			userField = &fields[i]
			break
		}
	}
	if userField == nil {
		t.Fatal("expected one user-kind field")
	}
	if userField.Value != "user_new" {
		t.Errorf("value = %v, want user_new (latest)", userField.Value)
	}
	if userField.FirstSeenStep != 1 || userField.LastSeenStep != 2 {
		t.Errorf("step range = %d..%d, want 1..2", userField.FirstSeenStep, userField.LastSeenStep)
	}
	if !userField.Rotated {
		t.Error("expected rotated=true")
	}
}

func TestDetectIdentity_FloorLinesIgnored(t *testing.T) {
	floorType := "floor"
	lines := []DeviceStateLine{
		{
			StepID:     "__live_floor__",
			ActionType: &floorType,
			Changed: map[string]DeviceStateChange{
				"Library/Preferences/com.x.plist": {
					Kind:   "plist",
					Values: map[string]interface{}{"viewerUserID": "user_abc"},
				},
			},
		},
	}
	fields := DetectIdentity(lines, MapIndexer{}, IdentityOptions{})
	if len(fields) != 0 {
		t.Errorf("expected floor lines to be ignored; got %d fields", len(fields))
	}
}

func TestDetectIdentity_AtStepClips(t *testing.T) {
	lines := []DeviceStateLine{
		{
			StepID: "step-1",
			Changed: map[string]DeviceStateChange{
				"Library/Preferences/com.x.plist": {
					Kind:   "plist",
					Values: map[string]interface{}{"viewerUserID": "user_step1"},
				},
			},
		},
		{
			StepID: "step-3",
			Changed: map[string]DeviceStateChange{
				"Library/Preferences/com.x.plist": {
					Kind:   "plist",
					Values: map[string]interface{}{"viewerUserID": "user_step3"},
				},
			},
		},
	}
	idx := MapIndexer{"step-1": 1, "step-3": 3}
	fields := DetectIdentity(lines, idx, IdentityOptions{AtStep: 2})
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Value != "user_step1" {
		t.Errorf("value = %v, want user_step1 (AtStep=2 clip)", fields[0].Value)
	}
}

// --- SummaryHighlights ----------------------------------------------------

func TestSummaryHighlights_KeepsCoreDropsVendor(t *testing.T) {
	all := []IdentityField{
		{Kind: IdentityKindUser, Label: "viewerUserID", Value: "user_a"},
		{Kind: IdentityKindEmail, Label: "viewerEmail", Value: "a@b.com"},
		{Kind: IdentityKindOrg, Label: "selectedOrgId", Value: "biz_a"},
		{Kind: IdentityKindRole, Label: "isAdmin", Value: false},                // dropped (false + not suspended)
		{Kind: IdentityKindRole, Label: "isOwner", Value: true},                 // kept
		{Kind: IdentityKindRole, Label: "isSuspended", Value: false},            // kept regardless
		{Kind: IdentityKindVendor, Label: "Statsig stable ID", Value: "uuid-1"}, // dropped
	}
	out := SummaryHighlights(all)
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })

	labels := make([]string, 0, len(out))
	for _, f := range out {
		labels = append(labels, f.Label)
	}
	want := []string{"isOwner", "isSuspended", "selectedOrgId", "viewerEmail", "viewerUserID"}
	if !equalStringSlices(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package devicetargets

import "testing"

func TestNewestRuntimePrefersNewestStableRelease(t *testing.T) {
	catalog := NewCatalog(map[string]*PlatformTargetConfig{
		"ios": {
			AvailableRuntimes: []string{"iOS 27.0", "iOS 26.5", "iOS 26.2", "iOS 18.5", "iOS 27-beta-4"},
			AvailableModels:   []string{"iPhone 16", "iPhone 15", "iPad Pro"},
			CompatibleRuntimes: map[string][]string{
				"iPhone 16": {"iOS 27-beta-4", "iOS 26.5", "iOS 27.0", "iOS 18.5"},
				"iPhone 15": {"iOS 18.5", "iOS 26.2"},
			},
		},
		"android": {
			AvailableRuntimes: []string{"Android 14"},
			AvailableModels:   []string{"Pixel 7"},
		},
	})

	testCases := []struct {
		platform, model, want string
	}{
		{"ios", "iPhone 16", "iOS 27.0"},
		{"ios", "iPhone 15", "iOS 26.2"},
		{"ios", "iPad Pro", "iOS 27.0"},
		{"android", "Pixel 7", "Android 14"},
	}
	for _, tc := range testCases {
		got, err := catalog.NewestRuntime(tc.platform, tc.model)
		if err != nil || got != tc.want {
			t.Fatalf("NewestRuntime(%q, %q) = %q, %v; want %q", tc.platform, tc.model, got, err, tc.want)
		}
	}
	if _, err := catalog.NewestRuntime("ios", "iPhone 99"); err == nil {
		t.Fatal("NewestRuntime accepted an unknown model")
	}
}

func TestCompareRuntimeVersions(t *testing.T) {
	testCases := []struct {
		left, right string
		want        int
	}{
		{"iOS 27.0", "iOS 26.5", 1},
		{"iOS 26.10", "iOS 26.9", 1},
		{"iOS 27.0", "iOS 27-beta-4", 1},
		{"iOS 27-beta-10", "iOS 27-beta-4", 1},
		{"iOS 27", "iOS 27.0", 0},
		{"iOS 28-rc-4", "iOS 28-beta-4", 1},
		{"Android 13", "Android 14", -1},
	}
	for _, tc := range testCases {
		if got := compareRuntimeVersions(tc.left, tc.right); got != tc.want {
			t.Fatalf("compareRuntimeVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

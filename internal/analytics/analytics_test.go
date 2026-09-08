package analytics

import (
	"os"
	"testing"
)

func TestAnalyticsDisabled(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })

	for _, binary := range []struct {
		name       string
		path       string
		testBinary bool
	}{
		{name: "unix test", path: "/tmp/revyl.test", testBinary: true},
		{name: "windows test", path: `C:\Temp\revyl.test.exe`, testBinary: true},
		{name: "unix cli", path: "/usr/local/bin/revyl"},
		{name: "windows cli", path: `C:\Programs\revyl.exe`},
	} {
		t.Run(binary.name, func(t *testing.T) {
			os.Args = []string{binary.path}
			for _, setting := range []struct {
				name              string
				analyticsTest     string
				telemetryDisabled string
				doNotTrack        string
				wantDisabled      bool
			}{
				{name: "default", wantDisabled: binary.testBinary},
				{name: "test opt-in", analyticsTest: "1"},
				{
					name:              "telemetry opt-out overrides test opt-in",
					analyticsTest:     "1",
					telemetryDisabled: "1",
					wantDisabled:      true,
				},
				{
					name:          "do-not-track overrides test opt-in",
					analyticsTest: "1",
					doNotTrack:    "1",
					wantDisabled:  true,
				},
			} {
				t.Run(setting.name, func(t *testing.T) {
					t.Setenv("REVYL_ANALYTICS_TEST", setting.analyticsTest)
					t.Setenv("REVYL_TELEMETRY_DISABLED", setting.telemetryDisabled)
					t.Setenv("DO_NOT_TRACK", setting.doNotTrack)

					if disabled := analyticsDisabled(); disabled != setting.wantDisabled {
						t.Fatalf("analyticsDisabled() = %v, want %v", disabled, setting.wantDisabled)
					}
				})
			}
		})
	}
}

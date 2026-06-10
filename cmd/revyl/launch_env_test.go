package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

// TestParseLaunchEnvVars covers the --launch-env KEY=VALUE parsing used by both
// `test run` and `device start`.
func TestParseLaunchEnvVars(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "nil input yields nil map (field omitted)",
			in:   nil,
			want: nil,
		},
		{
			name: "single pair",
			in:   []string{"API_URL=https://staging.example.com"},
			want: map[string]string{"API_URL": "https://staging.example.com"},
		},
		{
			name: "multiple pairs",
			in:   []string{"API_URL=https://x", "DEBUG=1"},
			want: map[string]string{"API_URL": "https://x", "DEBUG": "1"},
		},
		{
			name: "value may contain '=' (only first splits)",
			in:   []string{"QUERY=a=b&c=d"},
			want: map[string]string{"QUERY": "a=b&c=d"},
		},
		{
			name: "empty value is allowed",
			in:   []string{"FLAG="},
			want: map[string]string{"FLAG": ""},
		},
		{
			name: "key is trimmed; value preserved verbatim",
			in:   []string{"  KEY =  spaced value "},
			want: map[string]string{"KEY": "  spaced value "},
		},
		{
			name:    "missing '=' is rejected",
			in:      []string{"NOEQUALS"},
			wantErr: true,
		},
		{
			name:    "empty key is rejected",
			in:      []string{"=value"},
			wantErr: true,
		},
		{
			name:    "key with spaces is rejected",
			in:      []string{"my key=value"},
			wantErr: true,
		},
		{
			name:    "key starting with number is rejected",
			in:      []string{"1KEY=value"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLaunchEnvVars(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestExecuteTestRequestLaunchEnvWire verifies the test-run wire contract: inline
// launch env vars serialize as `launch_env_vars`, and are omitted when unset.
func TestExecuteTestRequestLaunchEnvWire(t *testing.T) {
	withVars, err := json.Marshal(&api.ExecuteTestRequest{
		TestID:        "abc",
		LaunchEnvVars: map[string]string{"API_URL": "https://x"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withVars), `"launch_env_vars":{"API_URL":"https://x"}`) {
		t.Errorf("expected launch_env_vars in body, got: %s", withVars)
	}

	without, err := json.Marshal(&api.ExecuteTestRequest{TestID: "abc"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(without), "launch_env_vars") {
		t.Errorf("expected launch_env_vars omitted when unset, got: %s", without)
	}
}

// TestStartDeviceRequestEnvVarsWire verifies the device-start wire contract:
// inline launch env vars serialize as `env_vars`, and are omitted when unset.
func TestStartDeviceRequestEnvVarsWire(t *testing.T) {
	withVars, err := json.Marshal(&api.StartDeviceRequest{
		Platform: "ios",
		EnvVars:  map[string]string{"API_URL": "https://x"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withVars), `"env_vars":{"API_URL":"https://x"}`) {
		t.Errorf("expected env_vars in body, got: %s", withVars)
	}

	without, err := json.Marshal(&api.StartDeviceRequest{Platform: "ios"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(without), "env_vars") {
		t.Errorf("expected env_vars omitted when unset, got: %s", without)
	}
}

// TestStartDeviceRequestRuntimeConfigWire verifies the device-start runtime
// override contract used by `device start --locale/--orientation`.
func TestStartDeviceRequestRuntimeConfigWire(t *testing.T) {
	withRuntimeConfig, err := json.Marshal(&api.StartDeviceRequest{
		Platform: "ios",
		RunConfig: &api.DeviceRunConfig{
			ExecutionMode: &api.DeviceExecutionModeConfig{
				InitialLocale:      "fr_FR",
				InitialOrientation: "landscape",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(withRuntimeConfig)
	if !strings.Contains(body, `"run_config"`) {
		t.Errorf("expected run_config in body, got: %s", body)
	}
	if !strings.Contains(body, `"initial_locale":"fr_FR"`) {
		t.Errorf("expected initial_locale in body, got: %s", body)
	}
	if !strings.Contains(body, `"initial_orientation":"landscape"`) {
		t.Errorf("expected initial_orientation in body, got: %s", body)
	}

	without, err := json.Marshal(&api.StartDeviceRequest{Platform: "ios"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(without), "run_config") {
		t.Errorf("expected run_config omitted when unset, got: %s", without)
	}
}

package launchenv

import (
	"strings"
	"testing"
)

func TestKeyError(t *testing.T) {
	for _, key := range []string{"API_URL", "_private", "hp-skip-onboarding", "com.example.flag", "a1"} {
		if err := KeyError(key); err != nil {
			t.Errorf("KeyError(%q) = %v, want nil", key, err)
		}
	}
	for _, key := range []string{
		"", "1KEY", "-skip", ".hidden", "my key",
		"DYLD_INSERT_LIBRARIES", "dyld_library_path", "SIMCTL_CHILD_FOO",
		"COGNISIM_PASTE_SOCK", "COGNISIM_STOREKIT_CONFIG_JSON",
	} {
		if err := KeyError(key); err == nil {
			t.Errorf("KeyError(%q) = nil, want an error", key)
		}
	}
}

func TestValidateNamesTheFirstBadKeyInOrder(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Fatalf("Validate(nil) = %v", err)
	}
	err := Validate(map[string]string{"OK": "1", "SIMCTL_CHILD_X": "", "DYLD_X": ""})
	if err == nil || !strings.Contains(err.Error(), `"DYLD_X"`) {
		t.Fatalf("Validate() = %v, want the DYLD_X key named", err)
	}
}

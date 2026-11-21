package sessionprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/revyl/cli/internal/api"
)

// CookiesFileProfileName names the applied session profile a --cookies-file
// start sends when no project profile applies.
const CookiesFileProfileName = "cookies-file"

// BackendCapabilityWebTestSessionProfiles is the backend capability that
// accepts an applied session_profile on a Web test run.
const BackendCapabilityWebTestSessionProfiles = "web_test_session_profiles"

var cookiesFile = outputFile{kind: browserStorageStateFile.kind, envName: "--cookies-file"}

// ReadCookiesFile reads a --cookies-file, which holds Playwright storage state
// or a bare JSON array of its cookies, and returns the canonical storage
// state. Errors never echo a cookie or localStorage value.
func ReadCookiesFile(path string) (json.RawMessage, error) {
	handle, err := os.Open(path) // #nosec G304 -- the caller names this local file
	if err != nil {
		return nil, fmt.Errorf("--cookies-file could not be read: %w", err)
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, maxBrowserStorageStateBytes+1))
	if err != nil {
		return nil, fmt.Errorf("--cookies-file could not be read: %w", err)
	}
	if len(data) > maxBrowserStorageStateBytes {
		return nil, invalidOutput("%s is larger than %d bytes", cookiesFile.envName, maxBrowserStorageStateBytes)
	}
	data = bytes.TrimPrefix(data, utf8ByteOrderMark)
	if !utf8.Valid(data) {
		return nil, invalidOutput("%s is not valid UTF-8", cookiesFile.envName)
	}
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '[' {
		data = slices.Concat([]byte(`{"cookies":`), data, []byte("}"))
	}
	state, err := parseBrowserStorageState(cookiesFile, string(data))
	if err != nil {
		return nil, err
	}
	var parsed canonicalStorageState
	if err := json.Unmarshal(state, &parsed); err != nil || len(parsed.Cookies)+len(parsed.Origins) == 0 {
		return nil, invalidOutput("%s holds no cookies or origins", cookiesFile.envName)
	}
	return state, nil
}

// WithBrowserStorageState adds a --cookies-file's storage state to the start's
// applied profile, after any storage state the profile wrote so the file wins
// where both set the same cookie or localStorage item. Without a profile it
// applies the file alone as the cookies-file profile.
func WithBrowserStorageState(instruction *api.AppliedSessionProfile, state json.RawMessage) (*api.AppliedSessionProfile, error) {
	if instruction == nil {
		return &api.AppliedSessionProfile{
			Instruction: api.SessionProfileInstructionKindApplied,
			Name:        CookiesFileProfileName,
			Outputs:     api.AppliedSessionProfileOutputs{BrowserStorageState: state},
		}, nil
	}
	merged := *instruction
	if merged.Outputs.BrowserStorageState != nil {
		var profileState, fileState canonicalStorageState
		if err := json.Unmarshal(merged.Outputs.BrowserStorageState, &profileState); err != nil {
			return nil, fmt.Errorf("session profile %q: its browser storage state could not be read", merged.Name)
		}
		if err := json.Unmarshal(state, &fileState); err != nil {
			return nil, fmt.Errorf("%s: its browser storage state could not be read", cookiesFile.envName)
		}
		profileState.Cookies = append(profileState.Cookies, fileState.Cookies...)
		profileState.Origins = append(profileState.Origins, fileState.Origins...)
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(profileState); err != nil {
			return nil, fmt.Errorf("%s: could not be combined with session profile %q", cookiesFile.envName, merged.Name)
		}
		state = bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
		if pythonJSONSize(state) > maxBrowserStorageStateBytes {
			return nil, invalidOutput("%s and session profile %q together hold more than %d bytes of browser storage state", cookiesFile.envName, merged.Name, maxBrowserStorageStateBytes)
		}
	}
	merged.Outputs.BrowserStorageState = state
	return &merged, nil
}

// RequireBackendCapability fails unless the backend lists capability, so a
// request never sends a field an older backend would silently ignore. feature
// names what needs it in the error.
func RequireBackendCapability(ctx context.Context, backend interface {
	GetBackendCapabilities(ctx context.Context) ([]string, error)
}, capability, feature string) error {
	capabilities, err := backend.GetBackendCapabilities(ctx)
	var apiErr *api.APIError
	if err != nil && !(errors.As(err, &apiErr) && apiErr.StatusCode == 404) {
		return fmt.Errorf("%s: could not check whether the Revyl backend supports it: %w", feature, err)
	}
	if !slices.Contains(capabilities, capability) {
		return fmt.Errorf("%s needs a newer Revyl backend", feature)
	}
	return nil
}

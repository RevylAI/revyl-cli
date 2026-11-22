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

// BackendCapabilitySessionProfiles is the backend capability that accepts an
// applied session_profile on a start request.
const BackendCapabilitySessionProfiles = "session_profiles"

// BackendCapabilityWebTestSessionProfiles is the backend capability that
// accepts an applied session_profile on a Web test run.
const BackendCapabilityWebTestSessionProfiles = "web_test_session_profiles"

var cookiesFile = outputFile{envName: "--cookies-file"}

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

// CookiesFileProfile is the applied cookies-file profile that carries a
// --cookies-file's storage state.
func CookiesFileProfile(state json.RawMessage) *api.SessionProfileRequest {
	return &api.SessionProfileRequest{
		Instruction: api.SessionProfileInstructionKindApplied,
		Name:        CookiesFileProfileName,
		Outputs:     &api.AppliedSessionProfileOutputs{BrowserStorageState: state},
	}
}

// WithCookiesFile makes start apply a --cookies-file's storage state. A start
// that runs a profile can't also apply the file: Revyl runs that profile on
// its hosted runner and applies its outputs there.
func WithCookiesFile(start Start, state json.RawMessage) (Start, error) {
	if start.Runs() {
		return start, fmt.Errorf("--cookies-file can't combine with session profile %s, which Revyl runs on its hosted runner; pass --session-profile none to start with the file's cookies alone", start.Name())
	}
	return Start{Pick: start.Pick, Instruction: CookiesFileProfile(state)}, nil
}

// CapabilityLister lists the optional behaviors a Revyl backend supports.
type CapabilityLister interface {
	GetBackendCapabilities(ctx context.Context) ([]string, error)
}

// RequireBackendCapability fails unless the backend lists capability, so a
// request never sends a field an older backend would silently ignore. feature
// names what needs it in the error.
func RequireBackendCapability(ctx context.Context, backend CapabilityLister, capability, feature string) error {
	supported, err := backendSupports(ctx, backend, capability)
	if err != nil {
		return fmt.Errorf("%s: could not check whether the Revyl backend supports it: %w", feature, err)
	}
	if !supported {
		return fmt.Errorf("%s needs a newer Revyl backend", feature)
	}
	return nil
}

// backendSupports reports whether the backend lists capability. A backend
// older than the capabilities route lists none.
func backendSupports(ctx context.Context, backend CapabilityLister, capability string) (bool, error) {
	capabilities, err := backend.GetBackendCapabilities(ctx)
	var apiErr *api.APIError
	if err != nil && !(errors.As(err, &apiErr) && apiErr.StatusCode == 404) {
		return false, err
	}
	return slices.Contains(capabilities, capability), nil
}

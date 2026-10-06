package analytics

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/orgguard"
	"github.com/revyl/cli/internal/ui"
)

// FailureClass is the bounded cause category on cli_command_failed. It is
// derived only from error types, HTTP status codes, and bounded command
// metadata, never from error text, arguments, paths, or values.
type FailureClass string

const (
	FailureClassAuth      FailureClass = "auth"
	FailureClassQuota     FailureClass = "quota"
	FailureClassConfig    FailureClass = "config"
	FailureClassUsage     FailureClass = "usage"
	FailureClassNetwork   FailureClass = "network"
	FailureClassServer    FailureClass = "server"
	FailureClassTimeout   FailureClass = "timeout"
	FailureClassCancelled FailureClass = "cancelled"
	FailureClassBuild     FailureClass = "build"
	FailureClassUpload    FailureClass = "upload"
	FailureClassDevice    FailureClass = "device"
	FailureClassGitHub    FailureClass = "github"
	FailureClassInternal  FailureClass = "internal"
	FailureClassUnknown   FailureClass = "unknown"
)

type classifiedError struct {
	err   error
	class FailureClass
}

// WithFailureClass marks a known exit path whose error no longer carries a
// classifiable type, such as an actionable message rebuilt from a typed cause.
// The returned error keeps the original message and unwraps to it.
func WithFailureClass(err error, class FailureClass) error {
	if err == nil {
		return nil
	}
	return &classifiedError{err: err, class: class}
}

func (e *classifiedError) Error() string {
	return e.err.Error()
}

func (e *classifiedError) Unwrap() error {
	return e.err
}

var buildFailureStageClasses = map[string]FailureClass{
	"validation":          FailureClassUsage,
	"authentication":      FailureClassAuth,
	"configuration":       FailureClassConfig,
	"secret_validation":   FailureClassConfig,
	"app_resolution":      FailureClassConfig,
	"setup":               FailureClassBuild,
	"build":               FailureClassBuild,
	"artifact_resolution": FailureClassBuild,
	"enqueue":             FailureClassBuild,
	"poll":                FailureClassBuild,
	"upload":              FailureClassUpload,
	"source_archive":      FailureClassUpload,
	"source_upload":       FailureClassUpload,
}

// classifyFailure checks the cause before the stage: a typed cause anywhere in
// the chain wins, and the build stage only classifies otherwise untyped
// failures.
func classifyFailure(err error, completion CommandCompletion) FailureClass {
	var classified *classifiedError
	if errors.As(err, &classified) {
		return classified.class
	}
	if errors.Is(err, ErrCommandPanicked) {
		return FailureClassInternal
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ui.ErrSelectionCancelled) {
		return FailureClassCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureClassTimeout
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		switch status := apiErr.StatusCode; {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			return FailureClassAuth
		case status == http.StatusPaymentRequired || status == http.StatusTooManyRequests:
			return FailureClassQuota
		case status >= http.StatusInternalServerError:
			return FailureClassServer
		case status >= http.StatusBadRequest:
			return FailureClassUsage
		}
	}
	var orgMismatchErr *orgguard.MismatchError
	if errors.Is(err, auth.ErrDeviceAuthorizationDenied) || errors.Is(err, auth.ErrDeviceAuthorizationExpired) ||
		errors.As(err, &orgMismatchErr) {
		return FailureClassAuth
	}
	var (
		configErr         *config.ConfigError
		missingRootErr    *config.MissingProjectRootError
		ambiguousRootsErr *config.AmbiguousProjectRootsError
		buildToolErr      *build.BuildToolError
		uploadErr         *api.UploadHTTPError
		deviceStopErr     *api.DeviceSessionStopPendingError
		networkErr        net.Error
	)
	switch {
	case errors.As(err, &configErr), errors.As(err, &missingRootErr), errors.As(err, &ambiguousRootsErr):
		return FailureClassConfig
	case errors.As(err, &buildToolErr):
		return FailureClassBuild
	case errors.As(err, &uploadErr):
		return FailureClassUpload
	case errors.As(err, &deviceStopErr):
		return FailureClassDevice
	case errors.As(err, &networkErr):
		return FailureClassNetwork
	}
	if stage, ok := completion.Properties["build_failure_stage"].(string); ok {
		if class, known := buildFailureStageClasses[stage]; known {
			return class
		}
	}
	return FailureClassUnknown
}

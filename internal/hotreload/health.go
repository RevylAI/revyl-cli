package hotreload

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// RuntimeFailureKind classifies hot-reload failures into user-actionable causes.
type RuntimeFailureKind string

const (
	RuntimeFailureLocalDevServerDown RuntimeFailureKind = "local_dev_server_down"
	RuntimeFailureRelayUnreachable   RuntimeFailureKind = "relay_unreachable"
	RuntimeFailureRelayLeaseExpired  RuntimeFailureKind = "relay_lease_expired"
	RuntimeFailureManifestUnhealthy  RuntimeFailureKind = "manifest_unhealthy"
	RuntimeFailureBundleSlow         RuntimeFailureKind = "bundle_slow"
	RuntimeFailureMetro500           RuntimeFailureKind = "metro_500"
	RuntimeFailureDeviceSessionEnded RuntimeFailureKind = "device_session_ended"
)

// RuntimeFailure describes one root cause detected during a hot-reload session.
type RuntimeFailure struct {
	Kind     RuntimeFailureKind
	Provider string
	Port     int
	RelayID  string
	Detail   string
	Fatal    bool
	Err      error
}

func (f RuntimeFailure) Error() string {
	if f.Detail != "" {
		return f.Detail
	}
	if f.Err != nil {
		return f.Err.Error()
	}
	return string(f.Kind)
}

func (f RuntimeFailure) Message() string {
	provider := strings.TrimSpace(f.Provider)
	if provider == "" {
		provider = "dev server"
	}

	switch f.Kind {
	case RuntimeFailureLocalDevServerDown:
		if f.Port > 0 {
			return fmt.Sprintf("Local %s stopped responding on 127.0.0.1:%d", provider, f.Port)
		}
		return fmt.Sprintf("Local %s stopped responding", provider)
	case RuntimeFailureRelayUnreachable:
		return "Hot reload relay became unreachable"
	case RuntimeFailureRelayLeaseExpired:
		return "Hot reload relay session expired"
	case RuntimeFailureManifestUnhealthy:
		return "Expo manifest became unhealthy"
	case RuntimeFailureBundleSlow:
		return "Expo bundle is too slow to load"
	case RuntimeFailureMetro500:
		return "Metro returned an internal server error"
	case RuntimeFailureDeviceSessionEnded:
		return "Device session ended"
	default:
		return "Hot reload runtime failure"
	}
}

func (f RuntimeFailure) Hint() string {
	switch f.Kind {
	case RuntimeFailureLocalDevServerDown:
		return "Restart `revyl dev`; run with `--debug` to capture Expo/Metro stdout and stderr."
	case RuntimeFailureRelayUnreachable:
		return "Check your network/VPN connection and restart `revyl dev` if the relay cannot reconnect."
	case RuntimeFailureRelayLeaseExpired:
		return "Restart `revyl dev` to create a fresh relay URL."
	case RuntimeFailureManifestUnhealthy:
		return "Check Expo manifest output and app scheme configuration; run with `--debug` for details."
	case RuntimeFailureBundleSlow:
		return "Wait for Metro to finish bundling, or rerun with `--debug` to inspect bundle output."
	case RuntimeFailureMetro500:
		return "Check Metro logs for the JavaScript or bundler error; run with `--debug` to stream them."
	case RuntimeFailureDeviceSessionEnded:
		return "Start a new dev session with `revyl dev`."
	default:
		return "Run with `--debug` for more detail."
	}
}

func (f RuntimeFailure) WithDefaults(provider string, port int, relayID string) RuntimeFailure {
	if strings.TrimSpace(f.Provider) == "" {
		f.Provider = provider
	}
	if f.Port == 0 {
		f.Port = port
	}
	if strings.TrimSpace(f.RelayID) == "" {
		f.RelayID = relayID
	}
	return f
}

func isLocalConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return isConnectionRefusedMessage(err.Error())
}

func isConnectionRefusedMessage(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "actively refused")
}

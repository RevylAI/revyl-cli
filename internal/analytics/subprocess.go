package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/config"
)

const (
	telemetryHelperEnv      = "REVYL_TELEMETRY_HELPER"
	telemetryBackendURLEnv  = "REVYL_TELEMETRY_BACKEND_URL"
	maxTelemetryPayloadSize = 64 * 1024
	telemetrySendTimeout    = 2 * time.Second
)

func IsTelemetryHelper() bool {
	return os.Getenv(telemetryHelperEnv) == "1"
}

// Use a detached helper so analytics never holds up the user command.
func SpawnTelemetry(payload TelemetryPayload, backendURL string) {
	if len(payload.Events) == 0 {
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil || len(payloadBytes) == 0 || len(payloadBytes) > maxTelemetryPayloadSize {
		return
	}

	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return
	}

	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), telemetryHelperEnv+"=1")
	if backendURL = strings.TrimSpace(backendURL); backendURL != "" {
		cmd.Env = append(cmd.Env, telemetryBackendURLEnv+"="+backendURL)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}

	_, _ = stdin.Write(payloadBytes)
	_ = stdin.Close()
	_ = cmd.Process.Release()
}

func RunTelemetryHelper(r io.Reader) {
	if r == nil {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r, maxTelemetryPayloadSize+1))
	if err != nil || len(body) == 0 || len(body) > maxTelemetryPayloadSize {
		return
	}

	var payload TelemetryPayload
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Events) == 0 {
		return
	}

	token, err := auth.NewManager().GetActiveToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), telemetrySendTimeout)
	defer cancel()

	_ = postTelemetryPayload(ctx, body, token, helperBackendURL())
}

func postTelemetryPayload(ctx context.Context, body []byte, token, backendURL string) error {
	backendURL = strings.TrimRight(strings.TrimSpace(backendURL), "/")
	if backendURL == "" {
		backendURL = config.ProdBackendURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL+defaultBackendAnalyticsPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "revyl-cli")
	req.Header.Set("X-Revyl-Client", "cli")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func helperBackendURL() string {
	if url := strings.TrimSpace(os.Getenv(telemetryBackendURLEnv)); url != "" {
		return url
	}
	return config.GetBackendURL(false)
}

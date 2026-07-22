package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	devCockpitDefaultHost       = "127.0.0.1"
	devCockpitDefaultPort       = 3232
	devCockpitPortSearchSpan    = 20
	devCockpitShutdownTimeout   = 2 * time.Second
	devCockpitControlTokenBytes = 16
)

var errDevCockpitRebuildQueued = errors.New("rebuild already queued")

type devCockpitOptions struct {
	Host           string
	Port           int
	PortSearchSpan int
	CWD            string
	ContextName    string
	ViewerURL      string
	BuildEnabled   bool
	TriggerRebuild func() error
	Stop           func()
}

type devCockpitServer struct {
	server   *http.Server
	listener net.Listener
	URL      string
	Port     int
	token    string
	options  devCockpitOptions
}

type devCockpitSnapshot struct {
	Running             bool                 `json:"running"`
	PID                 int                  `json:"pid,omitempty"`
	Context             string               `json:"context"`
	Platform            string               `json:"platform,omitempty"`
	PlatformKey         string               `json:"platform_key,omitempty"`
	Provider            string               `json:"provider,omitempty"`
	SessionID           string               `json:"session_id,omitempty"`
	SessionIndex        int                  `json:"session_index,omitempty"`
	SessionOwned        bool                 `json:"session_owned"`
	ViewerURL           string               `json:"viewer_url"`
	CockpitURL          string               `json:"cockpit_url"`
	TunnelURL           string               `json:"tunnel_url,omitempty"`
	DeepLinkURL         string               `json:"deep_link_url,omitempty"`
	Transport           string               `json:"transport,omitempty"`
	RelayID             string               `json:"relay_id,omitempty"`
	LocalPort           int                  `json:"local_port,omitempty"`
	State               string               `json:"state,omitempty"`
	BuildMode           string               `json:"build_mode,omitempty"`
	DeltaCacheWarm      bool                 `json:"delta_cache_warm"`
	RebuildCount        int                  `json:"rebuild_count"`
	RebuildAvailable    bool                 `json:"rebuild_available"`
	BuildEnabled        bool                 `json:"build_enabled"`
	NextAction          string               `json:"next_action"`
	FailureSummary      string               `json:"failure_summary,omitempty"`
	AgentContext        string               `json:"agent_context,omitempty"`
	LastRebuild         *devRebuildInfo      `json:"last_rebuild,omitempty"`
	LastRebuildStatus   string               `json:"last_rebuild_status,omitempty"`
	LastRebuildError    string               `json:"last_rebuild_error,omitempty"`
	LastRebuildDuration int64                `json:"last_rebuild_duration_ms,omitempty"`
	RemoteJobID         string               `json:"remote_job_id,omitempty"`
	RemoteVersionID     string               `json:"remote_build_version_id,omitempty"`
	RebuildLogs         []devRebuildLogEntry `json:"rebuild_logs,omitempty"`
	Commands            map[string]string    `json:"commands"`
	UpdatedAt           string               `json:"updated_at"`
}

type devCockpitPageData struct {
	Title string
}

func startDevCockpitForContext(
	ctx context.Context,
	cwd string,
	ctxName string,
	viewerURL string,
	buildEnabled bool,
	rebuildCh chan<- struct{},
	stop func(),
) (*devCockpitServer, error) {
	return startDevCockpitServer(ctx, devCockpitOptions{
		Host:           devCockpitDefaultHost,
		Port:           devCockpitDefaultPort,
		PortSearchSpan: devCockpitPortSearchSpan,
		CWD:            cwd,
		ContextName:    ctxName,
		ViewerURL:      viewerURL,
		BuildEnabled:   buildEnabled,
		TriggerRebuild: func() error {
			return queueDevCockpitRebuild(rebuildCh)
		},
		Stop: stop,
	})
}

func queueDevCockpitRebuild(ch chan<- struct{}) error {
	if ch == nil {
		return fmt.Errorf("rebuild controls are unavailable")
	}
	select {
	case ch <- struct{}{}:
		return nil
	default:
		return errDevCockpitRebuildQueued
	}
}

func startDevCockpitServer(ctx context.Context, options devCockpitOptions) (*devCockpitServer, error) {
	host := strings.TrimSpace(options.Host)
	if host == "" {
		host = devCockpitDefaultHost
	}
	port := options.Port
	if port < 0 {
		port = devCockpitDefaultPort
	}
	span := options.PortSearchSpan
	if span < 0 {
		span = 0
	}

	token, err := newDevCockpitToken()
	if err != nil {
		return nil, err
	}

	var ln net.Listener
	var selectedPort int
	if port == 0 {
		ln, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return nil, err
		}
		selectedPort = ln.Addr().(*net.TCPAddr).Port
	} else {
		for candidate := port; candidate <= port+span; candidate++ {
			ln, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(candidate)))
			if err == nil {
				selectedPort = candidate
				break
			}
		}
		if ln == nil {
			return nil, fmt.Errorf("no available local cockpit port in %d-%d", port, port+span)
		}
	}

	options.Host = host
	options.Port = selectedPort
	options.PortSearchSpan = span

	cockpit := &devCockpitServer{
		listener: ln,
		URL:      fmt.Sprintf("http://%s:%d", host, selectedPort),
		Port:     selectedPort,
		token:    token,
		options:  options,
	}
	mux := http.NewServeMux()
	cockpit.registerRoutes(mux)
	cockpit.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if serveErr := cockpit.server.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "revyl dev cockpit server stopped: %v\n", serveErr)
		}
	}()

	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), devCockpitShutdownTimeout)
			defer cancel()
			_ = cockpit.Close(shutdownCtx)
		}()
	}

	return cockpit, nil
}

func (c *devCockpitServer) Close(ctx context.Context) error {
	if c == nil || c.server == nil {
		return nil
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), devCockpitShutdownTimeout)
		defer cancel()
	}
	return c.server.Shutdown(ctx)
}

func (c *devCockpitServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", c.handleIndex)
	mux.HandleFunc("/api", c.handleAPI)
	mux.HandleFunc("/rebuild", c.handleRebuild)
	mux.HandleFunc("/stop", c.handleStop)
	mux.HandleFunc("/viewer", c.handleViewer)
}

func (c *devCockpitServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeDevCockpitMethodNotAllowed(w, http.MethodGet)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = devCockpitTemplate.Execute(w, devCockpitPageData{
		Title: "Revyl Dev",
	})
}

func (c *devCockpitServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeDevCockpitMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeDevCockpitJSON(w, c.snapshot())
}

func (c *devCockpitServer) handleRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDevCockpitMethodNotAllowed(w, http.MethodPost)
		return
	}
	if !c.authorizeMutation(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !c.options.BuildEnabled {
		writeDevCockpitJSONStatus(w, http.StatusConflict, map[string]interface{}{
			"ok":      false,
			"message": "rebuild is disabled for this dev loop",
		})
		return
	}
	if c.options.TriggerRebuild == nil {
		writeDevCockpitJSONStatus(w, http.StatusConflict, map[string]interface{}{
			"ok":      false,
			"message": "rebuild controls are unavailable",
		})
		return
	}
	if err := c.options.TriggerRebuild(); err != nil {
		status := http.StatusConflict
		if !errors.Is(err, errDevCockpitRebuildQueued) {
			status = http.StatusInternalServerError
		}
		writeDevCockpitJSONStatus(w, status, map[string]interface{}{
			"ok":      false,
			"message": err.Error(),
		})
		return
	}
	writeDevCockpitJSON(w, map[string]interface{}{
		"ok":      true,
		"message": "rebuild queued",
	})
}

func (c *devCockpitServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDevCockpitMethodNotAllowed(w, http.MethodPost)
		return
	}
	if !c.authorizeMutation(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if c.options.Stop != nil {
		c.options.Stop()
	}
	writeDevCockpitJSON(w, map[string]interface{}{
		"ok":      true,
		"message": "stop requested",
	})
}

func (c *devCockpitServer) handleViewer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeDevCockpitMethodNotAllowed(w, http.MethodGet)
		return
	}
	viewerURL := c.viewerURL()
	if viewerURL == "" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, viewerURL, http.StatusFound)
}

func (c *devCockpitServer) authorizeMutation(r *http.Request) bool {
	return c != nil && c.token != "" && r.Header.Get("X-Revyl-Cockpit-Token") == c.token
}

func (c *devCockpitServer) viewerURL() string {
	if c == nil {
		return ""
	}
	if viewer := strings.TrimSpace(c.options.ViewerURL); viewer != "" {
		return devCockpitViewerURL(viewer)
	}
	if ctxMeta, err := loadDevContext(c.options.CWD, c.options.ContextName); err == nil && ctxMeta != nil {
		return devCockpitViewerURL(ctxMeta.ViewerURL)
	}
	return ""
}

func devCockpitViewerURL(viewer string) string {
	return strings.TrimSpace(viewer)
}

func (c *devCockpitServer) snapshot() devCockpitSnapshot {
	ctxName := strings.TrimSpace(c.options.ContextName)
	if ctxName == "" {
		ctxName = defaultDevContextName
	}

	pidPath := devCtxPIDPath(c.options.CWD, ctxName)
	statusPath := devCtxStatusPath(c.options.CWD, ctxName)
	pid, nonce := readDevCtxPIDFile(pidPath)
	ctxMeta, _ := loadDevContext(c.options.CWD, ctxName)
	if pid == 0 && ctxMeta != nil {
		pid = ctxMeta.PID
	}
	expectedNonce := nonce
	if ctxMeta != nil && ctxMeta.StartedAtNano != 0 {
		expectedNonce = ctxMeta.StartedAtNano
	}

	running := true
	if pid != 0 {
		running, _ = isDevCtxProcessAlive(pid, expectedNonce, pidPath)
	}

	var ds *devStatus
	if data, err := os.ReadFile(statusPath); err == nil {
		var parsed devStatus
		if json.Unmarshal(data, &parsed) == nil {
			ds = &parsed
		}
	}

	output := buildDevStatusOutput(ctxName, pid, ctxMeta, ds)
	snapshot := devCockpitSnapshot{
		Running:             running,
		PID:                 pid,
		Context:             ctxName,
		SessionOwned:        boolFromStatusOutput(output, "session_owned"),
		ViewerURL:           devCockpitViewerURL(stringFromStatusOutput(output, "viewer_url")),
		CockpitURL:          c.URL,
		Platform:            stringFromStatusOutput(output, "platform"),
		SessionID:           stringFromStatusOutput(output, "session_id"),
		TunnelURL:           stringFromStatusOutput(output, "tunnel_url"),
		DeepLinkURL:         stringFromStatusOutput(output, "deep_link_url"),
		Transport:           stringFromStatusOutput(output, "transport"),
		RelayID:             stringFromStatusOutput(output, "relay_id"),
		State:               stringFromStatusOutput(output, "state"),
		BuildMode:           stringFromStatusOutput(output, "build_mode"),
		DeltaCacheWarm:      boolFromStatusOutput(output, "delta_cache_warm"),
		RebuildCount:        intFromStatusOutput(output, "rebuild_count"),
		RebuildAvailable:    c.options.BuildEnabled && c.options.TriggerRebuild != nil,
		BuildEnabled:        c.options.BuildEnabled,
		LastRebuildStatus:   stringFromStatusOutput(output, "last_rebuild_status"),
		LastRebuildError:    stringFromStatusOutput(output, "last_rebuild_error"),
		LastRebuildDuration: int64FromStatusOutput(output, "last_rebuild_duration_ms"),
		RemoteJobID:         stringFromStatusOutput(output, "remote_job_id"),
		RemoteVersionID:     stringFromStatusOutput(output, "remote_build_version_id"),
		UpdatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	if snapshot.ViewerURL == "" {
		snapshot.ViewerURL = c.viewerURL()
	}
	if ctxMeta != nil {
		snapshot.PlatformKey = strings.TrimSpace(ctxMeta.PlatformKey)
		snapshot.Provider = strings.TrimSpace(ctxMeta.Provider)
		snapshot.SessionIndex = ctxMeta.SessionIndex
		snapshot.LocalPort = ctxMeta.Port
	}
	if ds != nil {
		snapshot.LastRebuild = ds.LastRebuild
		if ds.LastRebuild != nil {
			snapshot.RebuildLogs = ds.LastRebuild.Logs
		}
	}
	snapshot.Commands = devCockpitCommands(ctxName, snapshot.SessionIndex)
	snapshot.FailureSummary = devCockpitFailureSummary(snapshot)
	snapshot.NextAction = devCockpitNextAction(snapshot)
	snapshot.AgentContext = devCockpitAgentContext(snapshot)
	return snapshot
}

func devCockpitCommands(ctxName string, _ int) map[string]string {
	ctxName = strings.TrimSpace(ctxName)
	if ctxName == "" {
		ctxName = defaultDevContextName
	}
	return map[string]string{
		"use":         "revyl dev use " + ctxName,
		"status":      "revyl dev status",
		"rebuild":     "revyl dev rebuild --wait",
		"rebuildJSON": "revyl dev rebuild --wait --json",
		"stop":        "revyl dev stop",
	}
}

func devCockpitNextAction(snapshot devCockpitSnapshot) string {
	if !snapshot.Running {
		return "Restart revyl dev or run revyl dev status to inspect this stopped context."
	}
	if devCockpitRebuildRunningStatus(snapshot.LastRebuildStatus) {
		return "Rebuild is running. Watch the rebuild log, then verify the hosted viewer when it finishes."
	}
	if snapshot.FailureSummary != "" || devCockpitFailureStatus(snapshot.LastRebuildStatus) {
		return "Fix the build error, then run revyl dev rebuild --wait --json."
	}
	if !snapshot.RebuildAvailable {
		return "Use the hosted viewer; this dev loop does not expose native rebuild controls."
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.State)) {
	case "building", "rebuilding", "installing":
		return "A rebuild is running. Wait for the next status event before acting."
	}
	if strings.EqualFold(snapshot.LastRebuildStatus, "skipped") {
		return "No native changes were detected. Keep editing or use the hosted viewer."
	}
	if snapshot.RebuildCount == 0 {
		return "Dev loop is ready. Make a native change, then click Rebuild or run revyl dev rebuild --wait."
	}
	return "Dev loop is ready. Make a change, then rebuild when you need a fresh install."
}

func devCockpitFailureSummary(snapshot devCockpitSnapshot) string {
	if errText := strings.TrimSpace(snapshot.LastRebuildError); errText != "" {
		return errText
	}
	if snapshot.LastRebuild != nil {
		if errText := strings.TrimSpace(snapshot.LastRebuild.Error); errText != "" {
			return errText
		}
		if buildErr := devCockpitFirstBuildError(snapshot.LastRebuild); buildErr != "" {
			return buildErr
		}
	}
	if devCockpitFailureStatus(snapshot.LastRebuildStatus) {
		return snapshot.LastRebuildStatus
	}
	return ""
}

func devCockpitFirstBuildError(rb *devRebuildInfo) string {
	if rb == nil {
		return ""
	}
	for _, buildErr := range rb.BuildErrors {
		location := strings.TrimSpace(buildErr.File)
		if buildErr.Line > 0 {
			location += fmt.Sprintf(":%d", buildErr.Line)
			if buildErr.Column > 0 {
				location += fmt.Sprintf(":%d", buildErr.Column)
			}
		}
		message := strings.TrimSpace(buildErr.Message)
		severity := strings.TrimSpace(buildErr.Severity)
		if severity != "" && message != "" {
			message = severity + ": " + message
		}
		switch {
		case location != "" && message != "":
			return location + " - " + message
		case message != "":
			return message
		case location != "":
			return location
		}
	}
	return ""
}

func devCockpitFailureStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "success", "skipped", "ready", "idle", "running", "building", "rebuilding", "installing", "uploading", "pushing", "pending", "queued", "requested":
		return false
	default:
		return true
	}
}

func devCockpitRebuildRunningStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "preparing", "running", "building", "rebuilding", "installing", "uploading", "pushing", "pending", "queued", "requested":
		return true
	default:
		return false
	}
}

func devCockpitAgentContext(snapshot devCockpitSnapshot) string {
	rebuildCommand := strings.TrimSpace(snapshot.Commands["rebuildJSON"])
	if rebuildCommand == "" {
		rebuildCommand = "revyl dev rebuild --wait --json"
	}
	platform := strings.TrimSpace(snapshot.Platform)
	if snapshot.PlatformKey != "" {
		if platform == "" {
			platform = snapshot.PlatformKey
		} else {
			platform += " / " + snapshot.PlatformKey
		}
	}
	latestError := strings.TrimSpace(snapshot.FailureSummary)
	if latestError == "" {
		latestError = "none"
	}

	lines := []string{
		"Revyl dev context: " + devCockpitDisplayValue(snapshot.Context, "default"),
		"Session: " + devCockpitDisplayValue(snapshot.SessionID, "none"),
		"Platform: " + devCockpitDisplayValue(platform, "unknown"),
		"Provider: " + devCockpitDisplayValue(snapshot.Provider, "unknown"),
		"Rebuild command: " + rebuildCommand,
		"Latest rebuild: " + devCockpitDisplayValue(snapshot.LastRebuildStatus, "none"),
		"Latest error: " + latestError,
	}
	if snapshot.TunnelURL != "" {
		lines = append(lines, "Tunnel: "+snapshot.TunnelURL)
	}
	if snapshot.DeepLinkURL != "" {
		lines = append(lines, "Deep link: "+snapshot.DeepLinkURL)
	}
	if snapshot.ViewerURL != "" {
		lines = append(lines, "Hosted viewer: "+snapshot.ViewerURL)
	}
	return strings.Join(lines, "\n")
}

func devCockpitDisplayValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func stringFromStatusOutput(output map[string]interface{}, key string) string {
	if value, ok := output[key].(string); ok {
		return value
	}
	return ""
}

func boolFromStatusOutput(output map[string]interface{}, key string) bool {
	if value, ok := output[key].(bool); ok {
		return value
	}
	return false
}

func intFromStatusOutput(output map[string]interface{}, key string) int {
	switch value := output[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func int64FromStatusOutput(output map[string]interface{}, key string) int64 {
	switch value := output[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func newDevCockpitToken() (string, error) {
	var raw [devCockpitControlTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("failed to generate cockpit token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func writeDevCockpitMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func writeDevCockpitJSON(w http.ResponseWriter, value interface{}) {
	writeDevCockpitJSONStatus(w, http.StatusOK, value)
}

func writeDevCockpitJSONStatus(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var devCockpitTemplate = template.Must(template.New("dev-cockpit").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #050505;
      --bar: #151515;
      --text: #f3f4f5;
      --muted: #a6a8ab;
      --border: #303236;
      --button: #1f2023;
      --button-hover: #292b2f;
    }

    @media (prefers-color-scheme: light) {
      :root {
        --bg: #f6f6f7;
        --bar: #ffffff;
        --text: #151515;
        --muted: #5f6368;
        --border: #dcdfe4;
        --button: #ffffff;
        --button-hover: #f2f3f5;
      }
    }

    * { box-sizing: border-box; }
    html, body { height: 100%; margin: 0; }
    body {
      background: var(--bg);
      color: var(--text);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      font-size: 13px;
      line-height: 1.4;
      letter-spacing: 0;
    }

    .shell {
      min-height: 100vh;
      display: grid;
      grid-template-rows: 44px minmax(0, 1fr);
    }

    .topbar {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      align-items: center;
      gap: 12px;
      padding: 8px 10px;
      border-bottom: 1px solid var(--border);
      background: var(--bar);
    }

    .brand {
      min-width: 0;
      display: flex;
      align-items: center;
      gap: 8px;
      font-weight: 600;
    }

    .mark {
      width: 18px;
      height: 18px;
      display: inline-grid;
      place-items: center;
      border: 1px solid var(--text);
      color: var(--text);
      font-size: 10px;
      line-height: 1;
      flex: 0 0 auto;
    }

    .title-block {
      min-width: 0;
      display: grid;
      gap: 1px;
    }

    .title {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .subtitle {
      color: var(--muted);
      font-size: 11px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .btn {
      height: 28px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: 6px;
      border: 1px solid var(--border);
      background: var(--button);
      color: var(--text);
      text-decoration: none;
      padding: 0 9px;
      font: inherit;
      white-space: nowrap;
    }

    .btn:hover { background: var(--button-hover); }
    .btn svg {
      width: 15px;
      height: 15px;
      stroke-width: 1.8;
    }

    .viewer {
      min-width: 0;
      min-height: 0;
      background: var(--bg);
    }

    .viewer iframe {
      display: block;
      width: 100%;
      height: calc(100vh - 44px);
      border: 0;
      background: var(--bg);
    }

    @media (max-width: 520px) {
      .topbar { grid-template-columns: 1fr; height: auto; }
      .shell { grid-template-rows: auto minmax(0, 1fr); }
      .btn { width: 100%; }
      .viewer iframe { height: calc(100vh - 76px); }
    }
  </style>
</head>
<body>
  <div class="shell">
    <header class="topbar">
      <div class="brand">
        <div class="mark" aria-hidden="true">R</div>
        <div class="title-block">
          <div class="title">Revyl dev</div>
          <div class="subtitle">session viewer</div>
        </div>
      </div>
      <a class="btn" id="openRevyl" href="/viewer" target="_blank" rel="noreferrer" title="Open in Revyl">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" aria-hidden="true"><path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/></svg>
        <span>Open in Revyl</span>
      </a>
    </header>

    <main class="viewer" aria-label="Hosted Revyl session">
      <iframe id="sessionFrame" src="/viewer" title="Revyl session viewer" allow="fullscreen; clipboard-read; clipboard-write"></iframe>
    </main>
  </div>
</body>
</html>`))

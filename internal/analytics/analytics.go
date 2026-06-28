package analytics

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/revyl/cli/internal/agentinfo"
	"github.com/revyl/cli/internal/auth"
)

const defaultBackendAnalyticsPath = "/api/v1/telemetry/cli-analytics"

type Config struct {
	Version    string
	Commit     string
	Date       string
	BackendURL string
}

type Recorder struct {
	enabled bool
	flush   func(TelemetryPayload)

	mu        sync.Mutex
	userID    string
	orgID     string
	baseProps map[string]interface{}
	events    []TelemetryEvent
}

type identityInfo struct {
	ClientInstanceID string
	UserID           string
	OrgID            string
	AuthMethod       string
}

func NewFromEnv(cfg Config) *Recorder {
	if analyticsDisabled() {
		return NewNoop()
	}
	return NewWithFlusher(cfg, func(payload TelemetryPayload) {
		SpawnTelemetry(payload, cfg.BackendURL)
	})
}

func NewWithFlusher(cfg Config, flush func(TelemetryPayload)) *Recorder {
	if flush == nil {
		return NewNoop()
	}

	identity := loadIdentity()
	cliVersion := strings.TrimSpace(cfg.Version)
	if cliVersion == "" {
		cliVersion = "dev"
	}

	baseProps := map[string]interface{}{
		"cli_version": cliVersion,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"service":     "revyl-cli",
	}
	if commit := strings.TrimSpace(cfg.Commit); commit != "" {
		baseProps["cli_commit"] = commit
	}
	if date := strings.TrimSpace(cfg.Date); date != "" {
		baseProps["cli_build_date"] = date
	}
	if identity.ClientInstanceID != "" {
		baseProps["client_instance_id"] = identity.ClientInstanceID
	}
	if ciProvider := detectCIProvider(); ciProvider != "" {
		baseProps["ci_provider"] = ciProvider
	}
	if agent := agentinfo.Detect(); agent.Name != "" {
		baseProps["agent"] = agent.Name
		if agent.SessionID != "" {
			baseProps["agent_session_id"] = sanitizeString(agent.SessionID)
		}
		if agent.Originator != "" {
			baseProps["agent_originator"] = sanitizeString(agent.Originator)
		}
		if agent.Remote {
			baseProps["agent_remote"] = true
		}
	}
	if identity.AuthMethod != "" {
		baseProps["auth_method"] = identity.AuthMethod
	}

	return &Recorder{
		enabled:   true,
		flush:     flush,
		userID:    identity.UserID,
		orgID:     identity.OrgID,
		baseProps: baseProps,
	}
}

func NewNoop() *Recorder {
	return &Recorder{}
}

func (r *Recorder) Enabled() bool {
	return r != nil && r.enabled && r.flush != nil
}

func (r *Recorder) Flush() {
	if !r.Enabled() {
		return
	}

	r.mu.Lock()
	events := make([]TelemetryEvent, len(r.events))
	copy(events, r.events)
	r.events = nil
	r.mu.Unlock()

	if len(events) == 0 {
		return
	}
	r.flush(TelemetryPayload{Events: events})
}

func (r *Recorder) eventProps(run *CommandRun) map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	props := map[string]interface{}{}
	for key, value := range r.baseProps {
		props[key] = value
	}
	if r.userID != "" {
		props["user_id"] = r.userID
	}
	if r.orgID != "" {
		props["org_id"] = r.orgID
	}
	if run != nil {
		for key, value := range run.props {
			props[key] = value
		}
	}
	return props
}

func loadIdentity() identityInfo {
	mgr := auth.NewManager()
	clientID, err := mgr.GetOrCreateClientInstanceID()
	if err != nil {
		clientID = ""
	}

	info := identityInfo{ClientInstanceID: clientID}
	creds, err := mgr.GetCredentials()
	if err != nil || creds == nil {
		return info
	}
	info.UserID = strings.TrimSpace(creds.UserID)
	info.OrgID = strings.TrimSpace(creds.OrgID)
	info.AuthMethod = strings.TrimSpace(creds.AuthMethod)
	return info
}

func analyticsDisabled() bool {
	telemetryDisabled, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("REVYL_TELEMETRY_DISABLED")))
	doNotTrack, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("DO_NOT_TRACK")))
	if telemetryDisabled || doNotTrack {
		return true
	}

	analyticsTest, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("REVYL_ANALYTICS_TEST")))
	if strings.HasSuffix(os.Args[0], ".test") && !analyticsTest {
		return true
	}
	return false
}

func detectCIProvider() string {
	switch {
	case os.Getenv("GITHUB_ACTIONS") == "true":
		return "github_actions"
	case os.Getenv("GITLAB_CI") != "":
		return "gitlab"
	case os.Getenv("CIRCLECI") != "":
		return "circleci"
	case os.Getenv("BUILDKITE") != "":
		return "buildkite"
	case os.Getenv("JENKINS_URL") != "":
		return "jenkins"
	case os.Getenv("CI") != "":
		return "generic"
	default:
		return ""
	}
}

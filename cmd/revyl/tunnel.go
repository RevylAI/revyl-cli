package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/hotreload"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

//go:embed tunnel_forwarder.cjs
var tunnelForwarderScript string

const (
	tunnelMaxSelectors      = 10
	tunnelDetachTimeout     = 30 * time.Second
	computerTunnelNode      = "/opt/revyl/node/bin/node"
	computerTunnelLifetime  = 58 * time.Minute
	computerTunnelExecSlack = 90 * time.Second
	computerTunnelDrain     = 2 * time.Second
	computerTunnelReady     = 30 * time.Second
	computerTunnelStartBy   = computerTunnelReady + 30*time.Second
	tunnelSessionCheck      = 30 * time.Second
	tunnelActivityReport    = time.Minute
	tunnelReattachAttempts  = 3
)

var (
	errTunnelReplaced       = errors.New("another tunnel replaced this one")
	errTunnelReattachFailed = errors.New("the tunnel could not be re-attached")
)

var tunnelHostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// tunnelSelector is one exact TCP destination a tunnel carries, resolved and
// dialed on this machine.
type tunnelSelector struct {
	host string
	port int
}

// destination is the selector as tunnel streams name it; the session's
// forwarder sends exactly this string.
func (s tunnelSelector) destination() string {
	return net.JoinHostPort(s.host, strconv.Itoa(s.port))
}

func (s tunnelSelector) loopback() bool {
	if s.host == "localhost" {
		return true
	}
	ip := net.ParseIP(s.host)
	return ip != nil && ip.IsLoopback()
}

func parseTunnelSelectors(values []string) ([]tunnelSelector, error) {
	if len(values) == 0 {
		return nil, errors.New("pass a port such as 8081, or a host:port such as api.internal:443")
	}
	if len(values) > tunnelMaxSelectors {
		return nil, fmt.Errorf("a tunnel carries at most %d selectors", tunnelMaxSelectors)
	}
	selectors := make([]tunnelSelector, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if _, err := strconv.Atoi(normalized); err == nil {
			normalized = net.JoinHostPort("localhost", normalized)
		}
		host, portText, err := net.SplitHostPort(normalized)
		port, portErr := strconv.Atoi(portText)
		if err != nil || portErr != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%q isn't a port or host:port; use forms like 3000, localhost:3000, 10.0.0.5:8080, [::1]:3000 or api.internal:443", value)
		}
		if port == 53 {
			return nil, fmt.Errorf("%q: port 53 can't be tunneled", value)
		}
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		} else if len(host) > 253 || !tunnelHostnamePattern.MatchString(host) {
			return nil, fmt.Errorf("%q: the host must be a hostname or an IP address", value)
		}
		selector := tunnelSelector{host: host, port: port}
		if seen[selector.destination()] {
			return nil, fmt.Errorf("%q is declared twice", value)
		}
		seen[selector.destination()] = true
		selectors = append(selectors, selector)
	}
	return selectors, nil
}

func tunnelDestinations(selectors []tunnelSelector) []string {
	destinations := make([]string, len(selectors))
	for i, selector := range selectors {
		destinations[i] = selector.destination()
	}
	return destinations
}

func newTunnelKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("could not create a tunnel key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// tunnelRun is one attached tunnel's relay and its bounded analytics.
type tunnelRun struct {
	backend   *hotreload.RelayTunnelBackend
	bridge    *hotreload.TCPBridge
	key       string
	relayURL  string
	sku       string
	selectors []tunnelSelector
	attached  time.Time
	mgr       *mcppkg.DeviceSessionManager
	session   *mcppkg.DeviceSession
	tunnelID  string
}

func startTunnelRelay(ctx context.Context, client *api.Client, sku string, selectors []tunnelSelector) (*tunnelRun, error) {
	key, err := newTunnelKey()
	if err != nil {
		return nil, err
	}
	bridge := hotreload.NewTCPBridge(key, tunnelDestinations(selectors))
	backend := hotreload.NewRelayTCPTunnelBackend(client, bridge)
	backend.SetLogCallback(func(message string) { ui.PrintDebug("%s", message) })
	relayURL, err := backend.Start(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("could not open the tunnel relay: %w", err)
	}
	backend.StartHealthMonitor(ctx)
	return &tunnelRun{backend: backend, bridge: bridge, key: key, relayURL: relayURL, sku: sku, selectors: selectors}, nil
}

// wait blocks until the person stops the tunnel or the relay fails for good.
func (t *tunnelRun) wait(ctx context.Context, sessionEnded <-chan error) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "detached", nil
		case err := <-sessionEnded:
			switch {
			case errors.Is(err, errTunnelReplaced):
				return "replaced", err
			case errors.Is(err, errTunnelReattachFailed):
				return "reattach_failed", err
			}
			return "session_ended", err
		case failure := <-t.backend.Failures():
			if failure.Fatal {
				return "relay_lost", fmt.Errorf("the tunnel relay failed: %s", failure.Detail)
			}
		}
	}
}

func (t *tunnelRun) recordCompletion(cmd *cobra.Command, outcome, attachFailure string) {
	loopback := 0
	for _, selector := range t.selectorsOrEmpty() {
		if selector.loopback() {
			loopback++
		}
	}
	properties := map[string]interface{}{
		"tunnel_sku":                     t.sku,
		"tunnel_outcome":                 outcome,
		"tunnel_loopback_selector_count": loopback,
		"tunnel_remote_selector_count":   len(t.selectorsOrEmpty()) - loopback,
	}
	if attachFailure != "" {
		properties["tunnel_attach_failure"] = attachFailure
	}
	if t != nil && t.bridge != nil {
		counts := t.bridge.Counts()
		properties["tunnel_streams_opened"] = counts.StreamsOpened
		properties["tunnel_streams_refused"] = counts.StreamsRefused
		properties["tunnel_dial_failures"] = counts.DialFailures
	}
	if t != nil && !t.attached.IsZero() {
		properties["tunnel_connected_ms"] = time.Since(t.attached).Milliseconds()
	}
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{Domain: "tunnel", DomainStatus: outcome, Properties: properties})
}

func (t *tunnelRun) selectorsOrEmpty() []tunnelSelector {
	if t == nil {
		return nil
	}
	return t.selectors
}

func (t *tunnelRun) stop() {
	if err := t.backend.Stop(); err != nil {
		ui.PrintDebug("tunnel relay revoke: %v", err)
	}
}

func (t *tunnelRun) summary() string {
	counts := t.bridge.Counts()
	return fmt.Sprintf("Tunnel stopped after %s: %d connections carried, %d refused, %d destinations not answering on this machine.",
		time.Since(t.attached).Round(time.Second), counts.StreamsOpened, counts.StreamsRefused, counts.DialFailures)
}

type tunnelStatusOutput struct {
	Status    string           `json:"status"`
	SessionID string           `json:"session_id,omitempty"`
	Computer  string           `json:"computer_id,omitempty"`
	Platform  string           `json:"platform,omitempty"`
	Selectors []string         `json:"selectors"`
	Endpoints []tunnelEndpoint `json:"endpoints"`
}

// tunnelEndpoint pairs a tunneled destination on this machine with the
// address the app in the session opens to reach it.
type tunnelEndpoint struct {
	Selector   string `json:"selector"`
	AppAddress string `json:"app_address"`
	AppURL     string `json:"app_url,omitempty"`
}

func tunnelEndpoints(selectors []tunnelSelector) []tunnelEndpoint {
	endpoints := make([]tunnelEndpoint, len(selectors))
	for i, selector := range selectors {
		endpoints[i] = tunnelEndpoint{Selector: selector.destination(), AppAddress: selector.destination()}
		if selector.loopback() {
			endpoints[i].AppURL = "http://" + selector.destination()
		}
	}
	return endpoints
}

func tunnelSelectorValues(cmd *cobra.Command, positional []string) []string {
	values, _ := cmd.Flags().GetStringArray("selector")
	return append(append([]string{}, positional...), values...)
}

func printTunnelActive(cmd *cobra.Command, output tunnelStatusOutput, target string) {
	if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
		data, _ := json.Marshal(output)
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	}
	ui.PrintSuccess("Tunnel active: %s reaches %s on this machine.", target, strings.Join(output.Selectors, ", "))
	for _, endpoint := range output.Endpoints {
		address := endpoint.AppAddress
		if endpoint.AppURL != "" {
			address = endpoint.AppURL
		}
		ui.PrintInfo("In the app, %s reaches %s here.", address, endpoint.Selector)
	}
	ui.PrintInfo("Connections the app opens from now on use the tunnel; relaunch the app if it connected earlier. Press Ctrl+C to stop.")
}

func tunnelAttachFailure(err error) string {
	var workerErr *mcppkg.WorkerHTTPError
	if errors.As(err, &workerErr) {
		switch workerErr.StatusCode {
		case http.StatusNotFound:
			return "worker_unsupported"
		case http.StatusNotImplemented:
			return "device_unsupported"
		case http.StatusUnprocessableEntity:
			return "invalid_selectors"
		}
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return "worker_unsupported"
	}
	return "worker_error"
}

var deviceTunnelCmd = &cobra.Command{
	Use:   "tunnel [PORT | HOST:PORT]...",
	Short: "Route a session's connections to localhost or private hosts through this machine",
	Long: `Route the declared connections of a running iOS simulator or Android
emulator session through this machine, until you press Ctrl+C.

revyl device tunnel 8081 makes localhost:8081 in the active session reach
localhost:8081 here. The app keeps using the address it already has. Each
PORT means localhost:PORT, and each HOST:PORT (or --selector) is an exact
destination that the CLI dials from this machine, so this machine's /etc/hosts,
DNS and VPN apply: localhost:3000 reaches port 3000 here, and
api.internal:443 reaches the host your network resolves. Nothing else on this
machine is reachable through the tunnel.

Start the tunnel before the app opens its connections, or relaunch the app
afterwards. A tunnel carries TCP only. On Android, a localhost selector needs
a port of 1024 or higher, and any other host reaches this machine through the
emulator's HTTP proxy, so connections that ignore the system proxy aren't
routed. On iOS, only the declared port of a host is routed and its other ports
keep their usual route; a public hostname is routed through the simulator's
HTTP proxy, so connections that ignore the system proxy keep their usual route.

Traffic through the tunnel keeps the session from idling out. If the session
loses the tunnel, the CLI attaches it again; if another tunnel replaces it on
the session, this one stops with an error.

For a Mac computer, and iOS or Web sessions running inside one, use
revyl computer tunnel.`,
	Example: `  revyl device tunnel 8081
  revyl device tunnel -s 1 8080 api.staging.internal:443`,
	Args: cobra.MaximumNArgs(tunnelMaxSelectors),
	RunE: runDeviceTunnel,
}

func runDeviceTunnel(cmd *cobra.Command, args []string) error {
	selectors, err := parseTunnelSelectors(tunnelSelectorValues(cmd, args))
	if err != nil {
		return err
	}
	mgr, err := getDeviceSessionMgr(cmd)
	if err != nil {
		return err
	}
	session, err := resolveSessionTarget(cmd, mgr)
	if err != nil {
		return err
	}
	if session.Platform != "ios" && session.Platform != "android" {
		return fmt.Errorf("device tunnels support iOS and Android sessions; for a Web session inside a Mac computer use revyl computer tunnel")
	}
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	run, err := attachSessionTunnel(ctx, mgr, session, selectors)
	if err != nil {
		failure := "worker_error"
		var attachErr *tunnelAttachError
		if errors.As(err, &attachErr) {
			failure = attachErr.Failure
		}
		(&tunnelRun{sku: session.Platform, selectors: selectors}).recordCompletion(cmd, "attach_failed", failure)
		return err
	}
	printTunnelActive(cmd, tunnelStatusOutput{Status: "active", SessionID: session.SessionID, Platform: session.Platform, Selectors: tunnelDestinations(selectors), Endpoints: tunnelEndpoints(selectors)}, "session "+session.SessionID)

	outcome, waitErr := run.wait(ctx, run.watchSession(ctx))
	if outcome == "session_ended" || outcome == "replaced" {
		run.stop()
	} else {
		run.detachSession(cmd.Context())
	}
	run.recordCompletion(cmd, outcome, "")
	ui.PrintInfo("%s", run.summary())
	return waitErr
}

// tunnelAttachError names why a tunnel could not attach, for analytics.
type tunnelAttachError struct {
	Failure string
	Err     error
}

func (e *tunnelAttachError) Error() string { return e.Err.Error() }
func (e *tunnelAttachError) Unwrap() error { return e.Err }

// attachSessionTunnel starts a relay and attaches it to a running iOS or
// Android session, so the session's selectors reach this machine. The caller
// owns the returned run and releases it with detachSession.
func attachSessionTunnel(ctx context.Context, mgr *mcppkg.DeviceSessionManager, session *mcppkg.DeviceSession, selectors []tunnelSelector) (*tunnelRun, error) {
	run, err := startTunnelRelay(ctx, mgr.APIClient(), session.Platform, selectors)
	if err != nil {
		return nil, &tunnelAttachError{Failure: "relay_unavailable", Err: err}
	}
	run.tunnelID = uuid.NewString()
	if _, err := mgr.WorkerRequestOnSession(ctx, session, "/tunnel_start", run.startRequest()); err != nil {
		run.stop()
		failure := tunnelAttachFailure(err)
		if failure == "worker_unsupported" {
			return nil, &tunnelAttachError{Failure: failure, Err: errors.New("this session's device doesn't support tunnels yet; start a new session and try again")}
		}
		return nil, &tunnelAttachError{Failure: failure, Err: fmt.Errorf("could not attach the tunnel to session %s: %w", session.SessionID, err)}
	}
	run.mgr, run.session, run.attached = mgr, session, time.Now()
	return run, nil
}

// detachSession removes the tunnel from its session and revokes its relay.
// A tunnel that another attach has since replaced is left in place.
func (t *tunnelRun) detachSession(ctx context.Context) {
	detachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunnelDetachTimeout)
	defer cancel()
	if _, err := t.mgr.WorkerRequestOnSession(detachCtx, t.session, "/tunnel_stop", map[string]interface{}{"tunnel_id": t.tunnelID}); err != nil {
		ui.PrintDebug("tunnel detach: %v", err)
	}
	t.stop()
}

func (t *tunnelRun) startRequest() map[string]interface{} {
	return map[string]interface{}{"relay_url": t.relayURL, "key": t.key, "selectors": tunnelDestinations(t.selectors), "tunnel_id": t.tunnelID}
}

// watchSession reports when the tunnel's session stops running, checking
// every tunnelSessionCheck; a state it can't read counts as running. Every
// tunnelActivityReport it also tells the worker whether the tunnel carried
// traffic, which keeps the session alive, re-attaches the tunnel when the
// worker lost it, and ends with errTunnelReplaced when another attach took
// the session's tunnel over.
func (t *tunnelRun) watchSession(ctx context.Context) <-chan error {
	ended := make(chan error, 1)
	go func() {
		sessionCheck := time.NewTicker(tunnelSessionCheck)
		defer sessionCheck.Stop()
		activityReport := time.NewTicker(tunnelActivityReport)
		defer activityReport.Stop()
		reporting := true
		var reportedBytes int64
		reattachFailures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-sessionCheck.C:
				if alive, reason := t.mgr.CheckSessionAlive(ctx, t.session); !alive {
					ended <- fmt.Errorf("the tunnel stopped because session %s ended: %s", t.session.SessionID, reason)
					return
				}
			case <-activityReport.C:
				if !reporting {
					continue
				}
				counts := t.bridge.Counts()
				movedBytes := counts.BytesUp + counts.BytesDown
				respBody, err := t.mgr.WorkerRequestOnSession(ctx, t.session, "/tunnel_activity", map[string]interface{}{"tunnel_id": t.tunnelID, "carried_traffic": movedBytes != reportedBytes})
				if err != nil {
					if tunnelAttachFailure(err) == "worker_unsupported" {
						reporting = false
					}
					ui.PrintDebug("tunnel activity report: %v", err)
					continue
				}
				reportedBytes = movedBytes
				var status struct {
					Active   bool   `json:"active"`
					TunnelID string `json:"tunnel_id"`
				}
				if err := json.Unmarshal(respBody, &status); err != nil {
					ui.PrintDebug("tunnel activity report: %v", err)
					continue
				}
				if status.TunnelID != "" && status.TunnelID != t.tunnelID {
					if !status.Active {
						continue
					}
					ended <- fmt.Errorf("the tunnel stopped because %w on session %s", errTunnelReplaced, t.session.SessionID)
					return
				}
				if status.Active {
					reattachFailures = 0
					continue
				}
				if _, err := t.mgr.WorkerRequestOnSession(ctx, t.session, "/tunnel_start", t.startRequest()); err != nil {
					reattachFailures++
					ui.PrintDebug("tunnel re-attach: %v", err)
					if reattachFailures >= tunnelReattachAttempts {
						ended <- fmt.Errorf("%w to session %s: %v", errTunnelReattachFailed, t.session.SessionID, err)
						return
					}
					continue
				}
				reattachFailures = 0
				ui.PrintInfo("The session lost the tunnel; it is re-attached.")
			}
		}
	}()
	return ended
}

func newComputerTunnelCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "tunnel C1 [PORT]...",
		Short: "Route a computer's localhost ports to this machine",
		Long: `Make localhost ports inside a running Mac computer reach the same ports on
this machine, until you press Ctrl+C. Safari, Chrome, and the iOS and Web
sessions running inside the computer all reach them at their usual address,
such as http://localhost:3000.

Each PORT means localhost:PORT. A --selector is a loopback host:port:
localhost:PORT, 127.0.0.1:PORT or [::1]:PORT. Each needs a port of 1024 or higher that nothing in the computer
already listens on. The CLI dials it on this machine; nothing else here is
reachable through the tunnel. A tunnel carries TCP only.`,
		Example: `  revyl computer tunnel 4f9c2b1e-... 3000`,
		Args:    cobra.RangeArgs(1, tunnelMaxSelectors+1),
		RunE:    runComputerTunnel,
	}
	command.Flags().StringArray("selector", nil, "Loopback host:port to route to this machine, such as localhost:3000 (repeatable)")
	command.Flags().Bool("json", false, "Print the active tunnel as JSON")
	return command
}

func runComputerTunnel(cmd *cobra.Command, args []string) error {
	id, err := parseComputerUUID(args[0])
	if err != nil {
		return err
	}
	selectors, err := parseTunnelSelectors(tunnelSelectorValues(cmd, args[1:]))
	if err != nil {
		return err
	}
	for _, selector := range selectors {
		if !selector.loopback() || selector.port < 1024 {
			return fmt.Errorf("--selector %s: a computer tunnel carries localhost ports from 1024 up; other hosts aren't supported yet", selector.destination())
		}
	}
	client, err := computerClient(cmd)
	if err != nil {
		return err
	}
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	run, err := startTunnelRelay(ctx, client, "computer", selectors)
	if err != nil {
		(&tunnelRun{sku: "computer", selectors: selectors}).recordCompletion(cmd, "attach_failed", "relay_unavailable")
		return err
	}
	defer run.stop()

	forwarder := &computerForwarder{client: client, computer: id, run: run, name: "revyl-tunnel-" + uuid.NewString()[:8] + ".cjs"}
	sessionEnded, err := forwarder.start(ctx)
	defer forwarder.stop(cmd.Context())
	if err != nil {
		run.recordCompletion(cmd, "attach_failed", "computer_unsupported")
		return err
	}
	run.attached = time.Now()
	printTunnelActive(cmd, tunnelStatusOutput{Status: "active", Computer: id.String(), Platform: "computer", Selectors: tunnelDestinations(selectors), Endpoints: tunnelEndpoints(selectors)}, "computer "+id.String())

	outcome, waitErr := run.wait(ctx, sessionEnded)
	run.recordCompletion(cmd, outcome, "")
	ui.PrintInfo("%s", run.summary())
	return waitErr
}

// computerForwarder runs the tunnel forwarder inside a computer as its
// computer account, one exec at a time. An exec lasts at most an hour, so
// each one stops accepting before it ends and the next takes over the ports.
type computerForwarder struct {
	client   *api.Client
	computer uuid.UUID
	run      *tunnelRun
	name     string

	mu         sync.Mutex
	known      map[uuid.UUID]bool
	owned      []api.ComputerProcessHandle
	generation int
}

type computerForwarderListener struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Selector string `json:"selector"`
}

func (f *computerForwarder) listeners() []computerForwarderListener {
	var listeners []computerForwarderListener
	for _, selector := range f.run.selectors {
		hosts := []string{selector.host}
		if selector.host == "localhost" {
			hosts = []string{"127.0.0.1", "::1"}
		}
		for _, host := range hosts {
			listeners = append(listeners, computerForwarderListener{Host: host, Port: selector.port, Selector: selector.destination()})
		}
	}
	return listeners
}

func (f *computerForwarder) statusPath() string {
	return f.name + ".status"
}

func (f *computerForwarder) operation(ctx context.Context, timeout time.Duration, build func(*api.ComputerCommandRequest, int) error) (*api.ComputerCommandResponse, error) {
	request := api.ComputerCommandRequest{ComputerSessionId: f.computer, RequestId: uuid.New()}
	if err := build(&request, int(timeout/time.Millisecond)); err != nil {
		return nil, err
	}
	return f.client.SubmitComputerOperation(ctx, request)
}

// exec runs argv in the computer; a nonzero exit is a result, not an error.
func (f *computerForwarder) exec(ctx context.Context, timeout time.Duration, stdin []byte, argv ...string) (api.ComputerExecution, error) {
	response, err := f.operation(ctx, timeout, func(request *api.ComputerCommandRequest, timeoutMs int) error {
		encoded := api.EncodeComputerBytes(stdin)
		return request.Command.FromComputerExec(api.ComputerExec{Argv: argv, Stdin: &encoded, TimeoutMs: &timeoutMs})
	})
	if err != nil {
		return api.ComputerExecution{}, err
	}
	info, err := api.InspectComputerExecution(*response)
	if info.ErrorCode == "exec_nonzero" && info.ExitCode != nil {
		return info, nil
	}
	if err != nil || info.Outcome != "succeeded" {
		return info, fmt.Errorf("the computer reported %s %s", info.Outcome, info.ErrorCode)
	}
	return info, nil
}

// runningProcesses lists the run-owned processes; file reads and process
// operations answer while the forwarder's exec holds the computer.
func (f *computerForwarder) runningProcesses(ctx context.Context) ([]api.ComputerProcessHandle, error) {
	response, err := f.operation(ctx, 10*time.Second, func(request *api.ComputerCommandRequest, timeoutMs int) error {
		limit := api.ComputerMaxItems
		return request.Command.FromComputerProcessList(api.ComputerProcessList{MaxItems: &limit, TimeoutMs: &timeoutMs})
	})
	if err != nil {
		return nil, err
	}
	succeeded, err := response.Execution.AsComputerSucceeded()
	if err != nil || succeeded.Outcome != "succeeded" {
		return nil, errors.New("the computer did not list its processes")
	}
	result, err := succeeded.Result.AsComputerProcessListResult()
	if err != nil {
		return nil, err
	}
	var running []api.ComputerProcessHandle
	for _, process := range result.Processes {
		if process.State == "running" {
			running = append(running, process.Handle)
		}
	}
	return running, nil
}

// claimForwarderProcess records the forwarder's process while its current
// generation listens. A computer runs one exec at a time, so the forwarder is
// then the one running process no earlier listing showed; with any other
// count it claims nothing rather than guess.
func (f *computerForwarder) claimForwarderProcess(ctx context.Context) {
	running, err := f.runningProcesses(ctx)
	if err != nil {
		ui.PrintDebug("tunnel forwarder process list: %v", err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var unknown []api.ComputerProcessHandle
	for _, handle := range running {
		if !f.known[handle.HandleId] {
			unknown = append(unknown, handle)
		}
	}
	if len(unknown) != 1 {
		ui.PrintDebug("tunnel forwarder process not identified among %d new processes", len(unknown))
		return
	}
	f.known[unknown[0].HandleId] = true
	f.owned = append(f.owned, unknown[0])
}

// awaitListening waits until the given forwarder generation listens, fails,
// or misses computerTunnelReady.
func (f *computerForwarder) awaitListening(ctx context.Context, generation int, ended <-chan error) error {
	deadline := time.Now().Add(computerTunnelReady)
	for {
		select {
		case err := <-ended:
			if err == nil {
				err = errors.New("it exited before listening")
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if reply := lastForwarderReply(f.forwarderStatus(ctx)); reply.Generation == generation {
			if err := reply.err(); err != nil {
				return err
			}
			if reply.Listening > 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("it didn't start listening; check that another long exec isn't running in the computer")
		}
	}
}

// forwarderStatus reads the forwarder's latest reply; it is empty until the
// forwarder has listened or failed.
func (f *computerForwarder) forwarderStatus(ctx context.Context) []byte {
	response, err := f.operation(ctx, 10*time.Second, func(request *api.ComputerCommandRequest, timeoutMs int) error {
		limit := 4096
		return request.Command.FromComputerFileRead(api.ComputerFileRead{Path: f.statusPath(), MaxBytes: &limit, TimeoutMs: &timeoutMs})
	})
	if err != nil {
		return nil
	}
	info, err := api.InspectComputerExecution(*response)
	if err != nil {
		return nil
	}
	return info.Data
}

// start writes the forwarder into the computer, launches it, and returns once
// it listens; the channel reports a forwarder that stops for good.
func (f *computerForwarder) start(ctx context.Context) (<-chan error, error) {
	if _, err := f.operation(ctx, 30*time.Second, func(request *api.ComputerCommandRequest, timeoutMs int) error {
		return request.Command.FromComputerFileWrite(api.ComputerFileWrite{Path: f.name, Data: api.EncodeComputerBytes([]byte(tunnelForwarderScript)), TimeoutMs: &timeoutMs})
	}); err != nil {
		return nil, fmt.Errorf("could not write the tunnel forwarder into the computer: %w", err)
	}
	existing, err := f.runningProcesses(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list the computer's processes: %w", err)
	}
	f.known = map[uuid.UUID]bool{}
	for _, handle := range existing {
		f.known[handle.HandleId] = true
	}
	generation, generationEnded := f.launch(ctx)
	if err := f.awaitListening(ctx, generation, generationEnded); err != nil {
		return nil, fmt.Errorf("the tunnel forwarder didn't start in the computer: %w", err)
	}
	f.claimForwarderProcess(ctx)
	ended := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-generationEnded:
				if err != nil {
					ended <- fmt.Errorf("the tunnel forwarder stopped in the computer: %w", err)
					return
				}
				generation, generationEnded = f.launch(ctx)
				if err := f.awaitListening(ctx, generation, generationEnded); err != nil {
					if ctx.Err() == nil {
						ended <- fmt.Errorf("the tunnel forwarder's hourly handoff failed in the computer: %w", err)
					}
					return
				}
				f.claimForwarderProcess(ctx)
			}
		}
	}()
	return ended, nil
}

// launch starts the next forwarder generation as one exec. Its channel
// reports nil when the forwarder exited cleanly at the end of its lifetime,
// so the caller replaces it. A generation the computer runs after
// computerTunnelStartBy exits at once, so a late exec can't hold the
// computer for a tunnel that has already given up on it.
func (f *computerForwarder) launch(ctx context.Context) (int, <-chan error) {
	f.mu.Lock()
	f.generation++
	generation := f.generation
	f.mu.Unlock()
	now := time.Now()
	config, _ := json.Marshal(map[string]interface{}{
		"relay_host":           strings.TrimPrefix(f.run.relayURL, "https://"),
		"key":                  f.run.key,
		"listeners":            f.listeners(),
		"status_path":          f.statusPath(),
		"generation":           generation,
		"start_by_ms":          now.Add(computerTunnelStartBy).UnixMilli(),
		"stop_accepting_at_ms": now.Add(computerTunnelLifetime).UnixMilli(),
		"exit_at_ms":           now.Add(computerTunnelLifetime + computerTunnelDrain).UnixMilli(),
	})
	ended := make(chan error, 1)
	go func() {
		result, err := f.exec(ctx, computerTunnelLifetime+computerTunnelExecSlack, append(config, '\n'), computerTunnelNode, f.name)
		if ctx.Err() != nil {
			return
		}
		if err == nil && result.ExitCode != nil && *result.ExitCode == 0 {
			ended <- nil
			return
		}
		if err == nil {
			err = forwarderReplyError(result.Stdout)
		}
		if err == nil {
			err = forwarderExitError(result)
		}
		ended <- err
	}()
	return generation, ended
}

type forwarderReply struct {
	Error      string `json:"error"`
	Port       int    `json:"port"`
	Listening  int    `json:"listening"`
	Generation int    `json:"generation"`
}

func (r forwarderReply) err() error {
	switch r.Error {
	case "":
		return nil
	case "address_in_use":
		return fmt.Errorf("port %d is already in use inside the computer", r.Port)
	default:
		return errors.New(r.Error)
	}
}

func lastForwarderReply(output []byte) forwarderReply {
	var last forwarderReply
	for _, line := range strings.Split(string(output), "\n") {
		var reply forwarderReply
		if json.Unmarshal([]byte(line), &reply) == nil {
			last = reply
		}
	}
	return last
}

func forwarderReplyError(output []byte) error {
	for _, line := range strings.Split(string(output), "\n") {
		var reply forwarderReply
		if json.Unmarshal([]byte(line), &reply) == nil && reply.Error != "" {
			return reply.err()
		}
	}
	return nil
}

func forwarderExitError(result api.ComputerExecution) error {
	if result.ExitCode == nil {
		return errors.New("it ended without an exit code")
	}
	return fmt.Errorf("it exited with code %d", *result.ExitCode)
}

func (f *computerForwarder) stop(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunnelDetachTimeout)
	defer cancel()
	f.mu.Lock()
	generation := f.generation
	f.mu.Unlock()
	if reply := lastForwarderReply(f.forwarderStatus(ctx)); f.known != nil && reply.Generation == generation && reply.Listening > 0 {
		f.claimForwarderProcess(ctx)
	}
	f.mu.Lock()
	owned := append([]api.ComputerProcessHandle{}, f.owned...)
	f.mu.Unlock()
	for _, handle := range owned {
		if _, err := f.operation(ctx, 10*time.Second, func(request *api.ComputerCommandRequest, timeoutMs int) error {
			return request.Command.FromComputerProcessTerminate(api.ComputerProcessTerminate{Handle: handle, TimeoutMs: &timeoutMs})
		}); err != nil {
			ui.PrintDebug("tunnel forwarder terminate: %v", err)
		}
	}
	if _, err := f.exec(ctx, 10*time.Second, nil, "/bin/rm", "-f", f.name, f.statusPath()); err != nil {
		ui.PrintDebug("tunnel forwarder cleanup: %v", err)
	}
}

func init() {
	deviceTunnelCmd.Flags().StringArray("selector", nil, "Exact host:port to route through this machine, such as localhost:3000 or api.internal:443 (repeatable)")
	deviceTunnelCmd.Flags().Bool("json", false, "Print the active tunnel as JSON")
	registerSessionTargetFlags(deviceTunnelCmd)
	deviceCmd.AddCommand(deviceTunnelCmd)
}

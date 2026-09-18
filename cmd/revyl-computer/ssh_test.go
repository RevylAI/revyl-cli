package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	communicatormocks "github.com/aws/session-manager-plugin/src/communicator/mocks"
	datachannelmocks "github.com/aws/session-manager-plugin/src/datachannel/mocks"
	smlog "github.com/aws/session-manager-plugin/src/log"
	smsession "github.com/aws/session-manager-plugin/src/sessionmanagerplugin/session"
	"github.com/stretchr/testify/mock"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

type testShellPlugin struct {
	smsession.ISessionPlugin
	initialize func()
	run        func() error
}

func (plugin testShellPlugin) Initialize(smlog.T, *smsession.Session) {
	plugin.initialize()
}

func (plugin testShellPlugin) SetSessionHandlers(smlog.T) error {
	return plugin.run()
}

func shellHandshakeFixture(t *testing.T, ready chan bool, open func(), waiting func()) (*smsession.Session, <-chan struct{}) {
	t.Helper()
	t.Setenv("SSM_PLUGIN_SKIP_CLIENT_CONFIGURE", "true")
	channel := new(datachannelmocks.IDataChannel)
	socket := new(communicatormocks.IWebSocketChannel)
	closed := make(chan struct{})
	channel.On("Initialize", mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).Return()
	channel.On("SetWebsocket", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	channel.On("GetWsChannel").Return(socket)
	socket.On("SetOnMessage", mock.Anything).Return()
	socket.On("SetOnError", mock.Anything).Return()
	channel.On("RegisterOutputStreamHandler", mock.Anything, false).Return()
	channel.On("Open", mock.Anything).Run(func(mock.Arguments) { open() }).Return(nil)
	channel.On("ResendStreamDataMessageScheduler", mock.Anything).Return(nil)
	channel.On("IsSessionTypeSet").Run(func(mock.Arguments) { waiting() }).Return(ready)
	channel.On("GetSessionType").Return("Standard_Stream")
	channel.On("GetSessionProperties").Return("test-properties")
	channel.On("Close", mock.Anything).Run(func(mock.Arguments) { close(closed) }).Return(nil).Once()
	return &smsession.Session{DataChannel: channel}, closed
}

func TestShellHandshakeTimeoutClosesChannel(t *testing.T) {
	shell, closed := shellHandshakeFixture(t, make(chan bool), func() {}, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := awaitBrokeredShell(ctx, smlog.NewMockLog(), shell); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake error = %v, want deadline exceeded", err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed-out handshake did not close the data channel")
	}
}

func TestShellHandshakeCancellationClosesChannel(t *testing.T) {
	waiting := make(chan struct{})
	shell, closed := shellHandshakeFixture(t, make(chan bool), func() {}, func() { close(waiting) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- awaitBrokeredShell(ctx, smlog.NewMockLog(), shell) }()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("handshake did not begin")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handshake error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handshake ignored cancellation")
	}
	select {
	case <-closed:
	default:
		t.Fatal("canceled handshake did not close the data channel")
	}
}

func TestShellHandshakeRejectsUnreadyOrClosedChannel(t *testing.T) {
	for _, closeReady := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "closed"}[closeReady], func(t *testing.T) {
			ready := make(chan bool, 1)
			if closeReady {
				close(ready)
			} else {
				ready <- false
			}
			shell, closed := shellHandshakeFixture(t, ready, func() {}, func() {})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := awaitBrokeredShell(ctx, smlog.NewMockLog(), shell); err == nil || !strings.Contains(err.Error(), "did not start a shell") {
				t.Fatalf("handshake error = %v", err)
			}
			select {
			case <-closed:
			default:
				t.Fatal("failed handshake did not close the data channel")
			}
		})
	}
}

func TestShellHandshakeReadyTransfersChannelOwnership(t *testing.T) {
	ready := make(chan bool, 1)
	ready <- true
	shell, closed := shellHandshakeFixture(t, ready, func() {}, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := awaitBrokeredShell(ctx, smlog.NewMockLog(), shell); err != nil {
		t.Fatal(err)
	}
	if shell.SessionType != "Standard_Stream" || shell.SessionProperties != "test-properties" {
		t.Fatalf("handshake did not copy the negotiated session metadata")
	}
	cancel()
	select {
	case <-closed:
		t.Fatal("startup cancellation closed an established interactive session")
	default:
	}
	closeBrokeredShellChannel(smlog.NewMockLog(), shell)
}

func TestShellConnectionTimeoutClosesLateConnection(t *testing.T) {
	releaseOpen := make(chan struct{})
	defer close(releaseOpen)
	shell, closed := shellHandshakeFixture(t, make(chan bool), func() { <-releaseOpen }, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := awaitBrokeredShell(ctx, smlog.NewMockLog(), shell); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connection error = %v, want deadline exceeded", err)
	}
	select {
	case <-closed:
		t.Fatal("connection was closed while it was still being initialized")
	default:
	}
	releaseOpen <- struct{}{}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection finishing after cancellation was not closed")
	}
}

func TestShellStartupAlreadyCanceledDoesNotConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitBrokeredShell(ctx, smlog.NewMockLog(), &smsession.Session{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("connection error = %v, want cancellation", err)
	}
}

func TestShellWelcomeRequiresCompletedHandshake(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		t.Run(map[bool]string{false: "stalled", true: "ready"}[succeeds], func(t *testing.T) {
			stderr, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			originalStderr := os.Stderr
			os.Stderr = stderr
			ui.SetQuietMode(false)
			t.Cleanup(func() {
				os.Stderr = originalStderr
				_ = stderr.Close()
			})
			readOutput := func() string {
				output, err := os.ReadFile(stderr.Name())
				if err != nil {
					t.Fatal(err)
				}
				return string(output)
			}
			ready := make(chan bool, 1)
			if succeeds {
				ready <- true
			}
			shell, closed := shellHandshakeFixture(t, ready, func() {}, func() {})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			previous := smsession.SessionRegistry["Standard_Stream"]
			t.Cleanup(func() { smsession.SessionRegistry["Standard_Stream"] = previous })
			initialized, running, notified := false, false, false
			smsession.SessionRegistry["Standard_Stream"] = testShellPlugin{
				initialize: func() {
					initialized = true
					if strings.Contains(readOutput(), "You are on the machine") {
						t.Error("welcome was printed before plugin initialization")
					}
				},
				run: func() error {
					running = true
					if !initialized || !notified || !strings.Contains(readOutput(), "You are on the machine") {
						t.Error("interactive shell started without readiness and welcome")
					}
					<-ctx.Done()
					return io.EOF
				},
			}
			err = runBrokeredShell(ctx, smlog.NewMockLog(), shell, func() {
				notified = true
				cancel()
			})
			if succeeds {
				if err != nil || !running {
					t.Fatalf("ready shell = %v; startup cancellation must not interrupt the interactive shell", err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || initialized || running || notified || strings.Contains(readOutput(), "You are on the machine") {
				t.Fatalf("stalled shell printed welcome or started a handler: err=%v, output=%q", err, readOutput())
			}
			select {
			case <-closed:
			default:
				t.Fatal("returned shell did not close its data channel")
			}
		})
	}
}

func TestBrokeredShellTerminatesAfterStartupFailureOrNormalReturn(t *testing.T) {
	startupFailure := errors.New("handshake failed")
	for _, testCase := range []struct {
		name          string
		startupError  error
		cancelParent  bool
		cleanupStatus int
	}{
		{"failed handshake", startupFailure, false, http.StatusNoContent},
		{"canceled startup", context.Canceled, true, http.StatusNoContent},
		{"normal return", nil, false, http.StatusNoContent},
		{"unconfirmed or unavailable cleanup endpoint", nil, false, http.StatusNotFound},
		{"cleanup failure", startupFailure, false, http.StatusForbidden},
		{"cleanup failure after normal return", nil, false, http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var terminations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-api-key" {
					t.Error("broker call did not use Revyl credentials")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /api/v1/execution/mac/shell-sessions":
					_ = json.NewEncoder(w).Encode(api.MacShellSession{SessionId: "test-session"})
				case "DELETE /api/v1/execution/mac/shell-sessions/test-session":
					terminations.Add(1)
					if r.Context().Err() != nil {
						t.Error("cleanup inherited the canceled startup context")
					}
					w.WriteHeader(testCase.cleanupStatus)
					if testCase.cleanupStatus >= 400 {
						_, _ = w.Write([]byte(`{"detail":"private provider diagnostic"}`))
					}
				default:
					t.Errorf("unexpected broker call: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := runBrokeredShellSession(ctx, api.NewClientWithBaseURL("test-api-key", server.URL), func(startupCtx context.Context, session *api.MacShellSession, onReady func()) error {
				deadline, ok := startupCtx.Deadline()
				if !ok || time.Until(deadline) > 30*time.Second {
					t.Fatal("startup context has no bounded deadline")
				}
				if session.SessionId != "test-session" {
					t.Fatal("wrong session passed to shell")
				}
				if testCase.cancelParent {
					cancel()
				}
				if testCase.startupError == nil {
					onReady()
					if startupCtx.Err() != context.Canceled {
						t.Fatal("successful startup did not release its deadline and signal subscription")
					}
				}
				return testCase.startupError
			})
			if terminations.Load() != 1 {
				t.Fatalf("termination requests = %d, want 1", terminations.Load())
			}
			if testCase.startupError != nil && !errors.Is(err, testCase.startupError) {
				t.Fatalf("original startup failure lost: %v", err)
			}
			if testCase.cleanupStatus >= 400 {
				if err == nil || !strings.Contains(err.Error(), "could not confirm remote shell termination") || strings.Contains(err.Error(), "private provider diagnostic") {
					t.Fatalf("cleanup failure was not safely surfaced: %v", err)
				}
			} else if testCase.startupError == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBrokeredShellDoesNotTerminateWhenMintingFails(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	err := runBrokeredShellSession(context.Background(), api.NewClientWithBaseURL("test-api-key", server.URL), func(context.Context, *api.MacShellSession, func()) error {
		t.Fatal("shell started after failed minting")
		return nil
	})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("failed minting: err=%v, calls=%d", err, calls.Load())
	}
}

// Package auth provides authentication management for the Revyl CLI.
//
// This file implements browser-based OAuth authentication using a local
// callback server pattern. The CLI starts a temporary HTTP server,
// opens the browser to the auth page, and waits for the callback with
// the access token.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/revyl/cli/internal/ui"
)

const (
	// DefaultAuthTimeout is the maximum time to wait for browser authentication.
	DefaultAuthTimeout = 5 * time.Minute

	// StateTokenLength is the length of the random state token in bytes.
	// Results in 64 hex characters.
	StateTokenLength = 32
)

// BrowserAuthResult contains the result of browser-based authentication.
type BrowserAuthResult struct {
	// Token is the access token or API key received from the auth callback.
	Token string

	// Email is the user's email address (optional).
	Email string

	// OrgID is the user's organization ID (optional).
	OrgID string

	// UserID is the user's ID (optional).
	UserID string

	// APIKeyID is the PropelAuth-assigned key ID when a persistent key was created (optional).
	// Empty when the callback fell back to a short-lived access token.
	APIKeyID string

	// AuthMethod indicates how the token was generated ("api_key" for persistent key, empty for access token).
	AuthMethod string

	// Error contains any error message from the auth flow.
	Error string
}

// BrowserAuthConfig contains configuration for browser-based authentication.
type BrowserAuthConfig struct {
	// AppURL is the base URL of the frontend app (e.g., https://app.revyl.ai).
	AppURL string

	// Timeout is the maximum time to wait for authentication.
	// Defaults to DefaultAuthTimeout if zero.
	Timeout time.Duration
}

// BrowserAuth handles browser-based OAuth authentication.
type BrowserAuth struct {
	config BrowserAuthConfig
}

// NewBrowserAuth creates a new browser authentication handler.
//
// Parameters:
//   - config: Configuration for the auth flow
//
// Returns:
//   - *BrowserAuth: A new browser auth handler
func NewBrowserAuth(config BrowserAuthConfig) *BrowserAuth {
	if config.Timeout == 0 {
		config.Timeout = DefaultAuthTimeout
	}
	return &BrowserAuth{config: config}
}

// generateStateToken creates a cryptographically secure random state token.
//
// Returns:
//   - string: A hex-encoded random token
//   - error: Any error during token generation
func generateStateToken() (string, error) {
	bytes := make([]byte, StateTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate state token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// findAvailablePort finds an available TCP port on localhost and returns the listener.
// The caller is responsible for using or closing the returned listener.
// This avoids a race condition where another process could claim the port
// between finding it and starting the server.
//
// Returns:
//   - net.Listener: A listener bound to an available port
//   - int: The port number
//   - error: Any error during port discovery
func findAvailablePort() (net.Listener, int, error) {
	// Listen on port 0 to get an available port from the OS
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to find available port: %w", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	return listener, addr.Port, nil
}

// Authenticate performs browser-based authentication.
//
// This method:
// 1. Starts a local HTTP server on a random available port
// 2. Generates a cryptographic state token for CSRF protection
// 3. Opens the browser to the auth page with port and state parameters
// 4. Waits for the callback with the access token
// 5. Returns the authentication result
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *BrowserAuthResult: The authentication result
//   - error: Any error during authentication
func (b *BrowserAuth) Authenticate(ctx context.Context) (*BrowserAuthResult, error) {
	// Generate state token for CSRF protection
	state, err := generateStateToken()
	if err != nil {
		return nil, err
	}

	// Find an available port and get the listener
	// The listener is passed to the server to avoid race conditions
	listener, port, err := findAvailablePort()
	if err != nil {
		return nil, err
	}

	// Create result channel
	resultCh := make(chan *BrowserAuthResult, 1)
	errCh := make(chan error, 1)

	// Create HTTP server with the pre-bound listener
	server := &callbackServer{
		port:     port,
		state:    state,
		listener: listener,
		resultCh: resultCh,
		errCh:    errCh,
	}

	// Start the server
	if err := server.Start(); err != nil {
		listener.Close() // Clean up listener on error
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	defer server.Stop()

	// Build auth URL
	authURL, err := url.Parse(b.config.AppURL)
	if err != nil {
		return nil, fmt.Errorf("invalid app URL: %w", err)
	}
	authURL.Path = "/cli/auth"
	query := authURL.Query()
	query.Set("port", fmt.Sprintf("%d", port))
	query.Set("state", state)
	authURL.RawQuery = query.Encode()

	// Open browser
	if err := ui.OpenBrowser(authURL.String()); err != nil {
		return nil, fmt.Errorf("failed to open browser: %w", err)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, b.config.Timeout)
	defer cancel()

	// Wait for result
	select {
	case result := <-resultCh:
		if result.Error != "" {
			return nil, fmt.Errorf("authentication failed: %s", result.Error)
		}
		return result, nil
	case err := <-errCh:
		return nil, err
	case <-timeoutCtx.Done():
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("authentication timed out after %v", b.config.Timeout)
		}
		return nil, timeoutCtx.Err()
	}
}

// GetAuthURL returns the URL that would be opened for authentication.
// Useful for displaying to users who need to manually open the URL.
//
// Parameters:
//   - port: The local server port
//   - state: The state token
//
// Returns:
//   - string: The full auth URL, or empty string if AppURL is invalid
func (b *BrowserAuth) GetAuthURL(port int, state string) string {
	authURL, err := url.Parse(b.config.AppURL)
	if err != nil || authURL == nil {
		// Return empty string for invalid URLs rather than panicking
		return ""
	}
	authURL.Path = "/cli/auth"
	query := authURL.Query()
	query.Set("port", fmt.Sprintf("%d", port))
	query.Set("state", state)
	authURL.RawQuery = query.Encode()
	return authURL.String()
}

// callbackServer is a temporary HTTP server that handles the OAuth callback.
type callbackServer struct {
	port     int
	state    string
	listener net.Listener // Pre-bound listener to avoid race conditions
	resultCh chan *BrowserAuthResult
	errCh    chan error
	server   *http.Server
	wg       sync.WaitGroup
}

// Start starts the callback server using the pre-bound listener.
//
// Returns:
//   - error: Any error during server startup
func (s *callbackServer) Start() error {
	if s.listener == nil {
		return fmt.Errorf("listener not set: use findAvailablePort() to get a listener")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler: mux,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errCh <- fmt.Errorf("server error: %w", err)
		}
	}()

	return nil
}

// Stop gracefully stops the callback server.
func (s *callbackServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
		s.wg.Wait()
	}
}

// handleCallback handles the OAuth callback request.
func (s *callbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// Validate state parameter
	receivedState := query.Get("state")
	if receivedState != s.state {
		s.sendErrorResponse(w, "Invalid state parameter")
		s.resultCh <- &BrowserAuthResult{Error: "invalid_state"}
		return
	}

	// Check for error
	if errMsg := query.Get("error"); errMsg != "" {
		s.sendErrorResponse(w, fmt.Sprintf("Authentication error: %s", errMsg))
		s.resultCh <- &BrowserAuthResult{Error: errMsg}
		return
	}

	// Get token
	token := query.Get("token")
	if token == "" {
		s.sendErrorResponse(w, "Missing token")
		s.resultCh <- &BrowserAuthResult{Error: "missing_token"}
		return
	}

	// Build result
	result := &BrowserAuthResult{
		Token:      token,
		Email:      query.Get("email"),
		OrgID:      query.Get("org_id"),
		UserID:     query.Get("user_id"),
		APIKeyID:   query.Get("api_key_id"),
		AuthMethod: query.Get("auth_method"),
	}

	// Send success response
	s.sendSuccessResponse(w)

	// Send result
	s.resultCh <- result
}

// sendSuccessResponse sends an HTML success page to the browser.
func (s *callbackServer) sendSuccessResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Revyl CLI - Authentication Successful</title>
    <style>
        * {
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: #0a0a0a;
        }
        .container {
            background: #0d0d0d;
            border: 1px solid rgba(157, 97, 255, 0.2);
            padding: 48px 56px;
            text-align: center;
            max-width: 400px;
            position: relative;
        }
        .corner {
            position: absolute;
            width: 10px;
            height: 10px;
        }
        .corner-tl {
            top: -1px;
            left: -1px;
            border-top: 2px solid #9D61FF;
            border-left: 2px solid #9D61FF;
        }
        .corner-tr {
            top: -1px;
            right: -1px;
            border-top: 2px solid #9D61FF;
            border-right: 2px solid #9D61FF;
        }
        .corner-bl {
            bottom: -1px;
            left: -1px;
            border-bottom: 2px solid #9D61FF;
            border-left: 2px solid #9D61FF;
        }
        .corner-br {
            bottom: -1px;
            right: -1px;
            border-bottom: 2px solid #9D61FF;
            border-right: 2px solid #9D61FF;
        }
        .icon {
            width: 48px;
            height: 48px;
            background: rgba(157, 97, 255, 0.1);
            display: flex;
            align-items: center;
            justify-content: center;
            margin: 0 auto 20px;
        }
        .icon svg {
            width: 24px;
            height: 24px;
            color: #9D61FF;
        }
        h1 {
            color: #ffffff;
            font-size: 20px;
            margin: 0 0 8px;
            font-weight: 600;
        }
        p {
            color: #888;
            margin: 0;
            font-size: 14px;
            line-height: 1.5;
        }
        .close-hint {
            margin-top: 20px;
            font-size: 13px;
            color: #555;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="corner corner-tl"></div>
        <div class="corner corner-tr"></div>
        <div class="corner corner-bl"></div>
        <div class="corner corner-br"></div>
        <div class="icon">
            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path>
            </svg>
        </div>
        <h1>Authentication Successful</h1>
        <p>You have successfully authenticated with the Revyl CLI.</p>
        <p class="close-hint">You can close this window and return to your terminal.</p>
    </div>
</body>
</html>`))
}

// sendErrorResponse sends an HTML error page to the browser.
// The message is HTML-escaped to prevent XSS attacks.
func (s *callbackServer) sendErrorResponse(w http.ResponseWriter, message string) {
	// Escape HTML to prevent XSS from malicious error messages
	safeMessage := html.EscapeString(message)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>Revyl CLI - Authentication Error</title>
    <style>
        * {
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            margin: 0;
            background: #0a0a0a;
        }
        .container {
            background: #0d0d0d;
            border: 1px solid rgba(239, 68, 68, 0.2);
            padding: 48px 56px;
            text-align: center;
            max-width: 400px;
            position: relative;
        }
        .corner {
            position: absolute;
            width: 10px;
            height: 10px;
        }
        .corner-tl {
            top: -1px;
            left: -1px;
            border-top: 2px solid #ef4444;
            border-left: 2px solid #ef4444;
        }
        .corner-tr {
            top: -1px;
            right: -1px;
            border-top: 2px solid #ef4444;
            border-right: 2px solid #ef4444;
        }
        .corner-bl {
            bottom: -1px;
            left: -1px;
            border-bottom: 2px solid #ef4444;
            border-left: 2px solid #ef4444;
        }
        .corner-br {
            bottom: -1px;
            right: -1px;
            border-bottom: 2px solid #ef4444;
            border-right: 2px solid #ef4444;
        }
        .icon {
            width: 48px;
            height: 48px;
            background: rgba(239, 68, 68, 0.1);
            display: flex;
            align-items: center;
            justify-content: center;
            margin: 0 auto 20px;
        }
        .icon svg {
            width: 24px;
            height: 24px;
            color: #ef4444;
        }
        h1 {
            color: #ffffff;
            font-size: 20px;
            margin: 0 0 8px;
            font-weight: 600;
        }
        p {
            color: #888;
            margin: 0;
            font-size: 14px;
            line-height: 1.5;
        }
        .error-message {
            background: rgba(239, 68, 68, 0.1);
            border: 1px solid rgba(239, 68, 68, 0.2);
            padding: 12px 16px;
            margin-top: 16px;
            color: #f87171;
            font-size: 13px;
            text-align: left;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="corner corner-tl"></div>
        <div class="corner corner-tr"></div>
        <div class="corner corner-bl"></div>
        <div class="corner corner-br"></div>
        <div class="icon">
            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path>
            </svg>
        </div>
        <h1>Authentication Failed</h1>
        <p>There was a problem authenticating with the Revyl CLI.</p>
        <div class="error-message">%s</div>
    </div>
</body>
</html>`, safeMessage)))
}

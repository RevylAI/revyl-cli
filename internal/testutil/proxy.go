package testutil

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func RunInSubprocess(t *testing.T, environment map[string]string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^"+strings.ReplaceAll(regexp.QuoteMeta(t.Name()), "/", "$/^")+"$", "-test.v") // #nosec G204 -- Only re-executes os.Executable(), with escaped test names and no shell or user-supplied executable.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "REQUEST_METHOD", "SSL_CERT_FILE", "SSL_CERT_DIR", "REVYL_API_KEY", "INFISICAL_CLIENT_ID", "INFISICAL_CLIENT_SECRET":
			continue
		}
		if _, overridden := environment[key]; !overridden {
			command.Env = append(command.Env, entry)
		}
	}
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Env = append(command.Env, "REVYL_TEST_SUBPROCESS="+t.Name())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated proxy test failed: %v\n%s", err, output)
	}
}

func NewConnectProxy(t *testing.T, upstreamURL string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodConnect || r.Host != "sandbox.invalid:443" {
			http.Error(w, "destination is not allowed", http.StatusForbidden)
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:fixture")) {
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		connection, err := net.DialTimeout("tcp", upstream.Host, time.Second)
		if err != nil {
			t.Error(err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer connection.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer client.Close()
		deadline := time.Now().Add(10 * time.Second)
		_ = client.SetDeadline(deadline)
		_ = connection.SetDeadline(deadline)
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(connection, buffered)
			_ = connection.Close()
			close(copied)
		}()
		_, _ = io.Copy(client, connection)
		_ = client.Close()
		<-copied
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func StartTrustedTLSServer(t *testing.T, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sandbox.invalid"},
		DNSNames:              []string{"sandbox.invalid"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	certificatePath := filepath.Join(t.TempDir(), "proxy-ca.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return server, certificatePath
}

func TestDirectWebSocket(t *testing.T, handler http.Handler, connect func(context.Context, string) error) {
	t.Helper()
	for _, name := range []string{"direct_ws", "direct_wss", "loopback_proxy_bypass"} {
		t.Run(name, func(t *testing.T) {
			if name == "direct_wss" && runtime.GOOS != "linux" {
				t.Skip("SSL_CERT_FILE configures the Linux system trust store")
			}
			if os.Getenv("REVYL_TEST_SUBPROCESS") == t.Name() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := connect(ctx, os.Getenv("REVYL_TEST_TARGET")); err != nil {
					t.Fatal(err)
				}
				return
			}
			environment := map[string]string{}
			var server *httptest.Server
			if name == "direct_wss" {
				var certificatePath string
				server, certificatePath = StartTrustedTLSServer(t, handler)
				environment["SSL_CERT_FILE"] = certificatePath
			} else {
				server = httptest.NewServer(handler)
				t.Cleanup(server.Close)
			}
			if name == "loopback_proxy_bypass" {
				environment["HTTP_PROXY"] = "http://127.0.0.1:1"
				environment["HTTPS_PROXY"] = "http://127.0.0.1:1"
			}
			environment["REVYL_TEST_TARGET"] = "ws" + strings.TrimPrefix(server.URL, "http")
			RunInSubprocess(t, environment)
		})
	}
}

func TestWebSocketProxy(t *testing.T, connect func(context.Context, string) error) {
	t.Helper()
	for _, name := range []string{"http_proxy", "https_proxy_trusted_ca", "https_proxy_untrusted_ca", "no_proxy", "denied", "proxy_auth_required", "timeout", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			if name == "https_proxy_trusted_ca" && runtime.GOOS != "linux" {
				t.Skip("SSL_CERT_FILE configures the Linux system trust store")
			}
			if os.Getenv("REVYL_TEST_SUBPROCESS") == t.Name() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if name == "cancelled" {
					cancel()
				}
				err := connect(ctx, os.Getenv("REVYL_TEST_TARGET"))
				if name == "http_proxy" || name == "https_proxy_trusted_ca" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("expected connection to fail")
				} else if !strings.Contains(err.Error(), "sandbox network permissions") {
					t.Fatalf("connection failure has no network setup guidance: %v", err)
				}
				switch name {
				case "https_proxy_untrusted_ca":
					if !strings.Contains(err.Error(), "certificate") {
						t.Fatalf("expected certificate verification failure, got %v", err)
					}
				case "denied":
					if !strings.Contains(err.Error(), "Forbidden") {
						t.Fatalf("expected proxy denial, got %v", err)
					}
				case "proxy_auth_required":
					if !strings.Contains(err.Error(), "Proxy Authentication Required") {
						t.Fatalf("expected proxy authentication failure, got %v", err)
					}
				case "timeout":
					var networkError net.Error
					if !errors.As(err, &networkError) || !networkError.Timeout() {
						t.Fatalf("expected a bounded network timeout, got %v", err)
					}
				case "cancelled":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancellation, got %v", err)
					}
				}
				return
			}
			upgrader := websocket.Upgrader{}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials leaked to the destination")
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			})
			scheme := "ws"
			proxyVariable := "HTTP_PROXY"
			environment := map[string]string{}
			var upstream *httptest.Server
			if strings.HasPrefix(name, "https_proxy") {
				var certificatePath string
				upstream, certificatePath = StartTrustedTLSServer(t, handler)
				if name == "https_proxy_trusted_ca" {
					environment["SSL_CERT_FILE"] = certificatePath
				}
				scheme, proxyVariable = "wss", "HTTPS_PROXY"
			} else {
				upstream = httptest.NewServer(handler)
				t.Cleanup(upstream.Close)
			}
			proxy, requests := NewConnectProxy(t, upstream.URL)
			if name == "timeout" {
				release := make(chan struct{})
				proxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					<-release
				}))
				t.Cleanup(proxy.Close)
				t.Cleanup(func() { close(release) })
			}
			environment[proxyVariable] = strings.Replace(proxy.URL, "http://", "http://fixture:fixture@", 1)
			if name == "proxy_auth_required" {
				environment[proxyVariable] = proxy.URL
			}
			environment["REVYL_TEST_TARGET"] = scheme + "://sandbox.invalid:443/socket"
			if name == "no_proxy" {
				environment["NO_PROXY"] = "sandbox.invalid"
			}
			if name == "denied" {
				environment["REVYL_TEST_TARGET"] = "ws://denied.invalid:443/socket"
			}
			RunInSubprocess(t, environment)
			if name == "no_proxy" || name == "cancelled" {
				if requests.Load() != 0 {
					t.Fatal("bypassed or cancelled connection reached the proxy")
				}
			} else if requests.Load() == 0 {
				t.Fatalf("%s connection did not use the proxy", scheme)
			}
		})
	}
}

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

func computerHarborFixture(t *testing.T, id string) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, contents := range map[string]string{
		"config.json":           `{}`,
		"result.json":           `{}`,
		"agent/trajectory.json": `{"steps":[],"fixture":"private-trajectory-canary"}`,
	} {
		file, err := archive.Create("revyl-computers/computer-" + id + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestComputerExportSavesHarborBundle(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id := uuid.NewString()
	body := computerHarborFixture(t, id)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/computer-runs/"+id+"/export/harbor" {
			t.Error("unexpected export request")
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			directory := t.TempDir()
			t.Chdir(directory)
			path := "./revyl-computer-" + id + ".zip"
			args := []string{"computer", "export", id}
			if jsonOutput {
				path = filepath.Join(directory, "private-export-path.zip")
				args = append(args, "--format", "harbor", "--out", path, "--json")
			}
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("saved archive changed: %v", err)
			}
			archive, err := zip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			trajectory, err := archive.Open("revyl-computers/computer-" + id + "/agent/trajectory.json")
			if err != nil {
				t.Fatal(err)
			}
			contents, err := io.ReadAll(trajectory)
			trajectory.Close()
			if err != nil || !bytes.Contains(contents, []byte("private-trajectory-canary")) {
				t.Fatal("saved archive lost its trajectory")
			}
			info, err := os.Stat(path)
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Fatal("saved archive is not private")
			}
			if jsonOutput {
				var result computerExportResult
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Path != path || result.SizeBytes != int64(len(body)) || result.ComputerSessionID != id || result.Format != "harbor" || stderr.Len() != 0 {
					t.Fatalf("JSON export result = %+v, stderr = %q", result, stderr.String())
				}
			} else {
				want := fmt.Sprintf("Saved %s (%d bytes)\nView: unzip %s -d jobs && harbor view jobs\n", path, len(body), path)
				if stdout.Len() != 0 || stderr.String() != want {
					t.Fatalf("export output = %q, %q; want stderr %q", stdout.String(), stderr.String(), want)
				}
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 {
				t.Fatalf("export left temporary files: %v, %v", entries, err)
			}
		})
	}
}

func TestComputerExportFailurePreservesDestinationAndCleansTemp(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id := uuid.NewString()
	body := computerHarborFixture(t, id)
	for _, failure := range []string{"truncated", "disconnected", "wrong_session", "inaccessible"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", failure, existing), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch failure {
					case "inaccessible":
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"detail":"inaccessible"}`)
					case "wrong_session":
						_, _ = w.Write(computerHarborFixture(t, uuid.NewString()))
					default:
						if failure == "disconnected" {
							w.Header().Set("Content-Length", fmt.Sprint(len(body)))
						}
						_, _ = w.Write(body[:len(body)-22])
					}
				}))
				defer server.Close()
				t.Setenv("REVYL_BACKEND_URL", server.URL)
				t.Setenv("REVYL_API_KEY", "fixture-key")
				directory := t.TempDir()
				path := filepath.Join(directory, "bundle.zip")
				if existing {
					if err := os.WriteFile(path, []byte("original archive"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
				root.AddCommand(newComputerCommand())
				var output bytes.Buffer
				root.SetOut(&output)
				root.SetErr(&bytes.Buffer{})
				root.SetArgs([]string{"computer", "export", id, "--out", path, "--json"})
				err := root.Execute()
				if err == nil || !json.Valid(output.Bytes()) {
					t.Fatalf("export did not fail with JSON output: %v, %q", err, output.String())
				}
				if failure == "truncated" || failure == "disconnected" {
					if !errors.Is(err, api.ErrComputerExportCutOff) || !strings.Contains(err.Error(), "export was cut off") {
						t.Fatalf("truncated export error = %v", err)
					}
				} else if failure == "inaccessible" {
					var refusal *api.APIError
					if !errors.As(err, &refusal) || refusal.Code != "inaccessible" {
						t.Fatalf("inaccessible error = %v", err)
					}
				} else if !strings.Contains(err.Error(), "missing the expected Harbor trajectory") {
					t.Fatalf("wrong-session error = %v", err)
				}
				entries, err := os.ReadDir(directory)
				if err != nil || !existing && len(entries) != 0 || existing && len(entries) != 1 {
					t.Fatalf("export left unexpected files: %v, %v", entries, err)
				}
				if existing {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != "original archive" {
						t.Fatal("failed export replaced the original destination")
					}
				}
			})
		}
	}
}

func TestComputerExportRejectsInvalidArgumentsBeforeDispatch(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	for _, args := range [][]string{
		{"--format", "atif", uuid.NewString()},
		{"not-a-uuid"},
		{uuid.Nil.String()},
		{},
		{uuid.NewString(), uuid.NewString()},
	} {
		root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(newComputerCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append([]string{"computer", "export"}, args...))
		err := root.Execute()
		if err == nil || strings.Contains(err.Error(), "authenticated") {
			t.Fatalf("arguments were not rejected before auth: %v", err)
		}
		if len(args) > 0 && args[0] == "--format" && err.Error() != "export format must be harbor" {
			t.Fatalf("format error = %v", err)
		}
	}
}

func TestComputerExportLifecycleKeepsFlagValuesPrivate(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	id := uuid.NewString()
	body := computerHarborFixture(t, id)
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write(body)
				} else {
					_, _ = io.WriteString(w, `{"detail":"inaccessible"}`)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "")
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			path := filepath.Join(t.TempDir(), "private-path-canary.zip")
			args := []string{"computer", "export", id, "--format", "harbor", "--out", path, "--json"}
			leaf, _, err := root.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			if err := leaf.ParseFlags(args[2:]); err != nil {
				t.Fatal(err)
			}
			var payload analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(got analytics.TelemetryPayload) { payload = got })
			run := recorder.StartCommand(leaf, []string{id})
			root.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
			t.Setenv("REVYL_API_KEY", "fixture-key")
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			err = root.Execute()
			run.Complete(err)
			run.Flush()
			if (err == nil) != (status == http.StatusOK) || len(payload.Events) != 2 {
				t.Fatalf("lifecycle result = %v, events = %d", err, len(payload.Events))
			}
			if payload.Events[0].Event != analytics.CliCommandStartedEvent {
				t.Fatal("export lost centralized invocation instrumentation")
			}
			terminal := payload.Events[1]
			wantEvent := analytics.CliCommandCompletedEvent
			if status != http.StatusOK {
				wantEvent = analytics.CliCommandFailedEvent
			}
			if terminal.Event != wantEvent || terminal.Properties["command"] != "revyl computer export" || terminal.Properties["domain"] != "computer" {
				t.Fatalf("unexpected lifecycle event: %+v", terminal)
			}
			if status == http.StatusOK && terminal.Properties["domain_status"] != "exported" {
				t.Fatal("export completion was not recorded")
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{path, "private-path-canary", "private-trajectory-canary", "fixture-key", "harbor", server.URL} {
				if bytes.Contains(encoded, []byte(private)) {
					t.Fatalf("export analytics captured private value %q", private)
				}
			}
			for _, event := range payload.Events {
				if _, ok := event.Properties["flag_values"]; ok {
					t.Fatal("export analytics captured flag values")
				}
			}
		})
	}
}

func TestComputerExportReplacesExistingFileAfterVerification(t *testing.T) {
	id := uuid.New()
	body := computerHarborFixture(t, id.String())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(path, []byte("original archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := saveComputerHarbor(context.Background(), api.NewClientWithBaseURL("fixture-key", server.URL), id, path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, body) || result.SizeBytes != int64(len(body)) {
		t.Fatalf("replacement archive changed: %v", err)
	}
}

func TestComputerExportCancellationRemovesTemporaryFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := saveComputerHarbor(ctx, api.NewClientWithBaseURL("fixture-key", server.URL), uuid.New(), filepath.Join(directory, "bundle.zip"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation error = %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancellation left files: %v, %v", entries, err)
	}
}

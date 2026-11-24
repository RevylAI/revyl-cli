package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const buildDownloadTestSignature = "X-Amz-Signature=fixture-signature"

type fakeBuildArtifactServer struct {
	artifact           []byte
	recordedSHA256     string
	contentDisposition string
	artifactStatus     int
	server             *httptest.Server
}

func newFakeBuildArtifactServer(t *testing.T, artifact []byte) *fakeBuildArtifactServer {
	t.Helper()
	sum := sha256.Sum256(artifact)
	fake := &fakeBuildArtifactServer{
		artifact:           artifact,
		recordedSHA256:     hex.EncodeToString(sum[:]),
		contentDisposition: "attachment; filename*=UTF-8''app-release.apk",
		artifactStatus:     http.StatusOK,
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/builds/bv-1":
			if r.URL.Query().Get("include_download_url") != "true" {
				t.Errorf("include_download_url = %q, want true", r.URL.Query().Get("include_download_url"))
			}
			metadata := map[string]any{}
			if fake.recordedSHA256 != "" {
				metadata["artifact_sha256"] = fake.recordedSHA256
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           "bv-1",
				"version":      "1.2.3",
				"download_url": fake.server.URL + "/artifacts/org/app/stored-key.apk?" + buildDownloadTestSignature,
				"metadata":     metadata,
			})
		case "/artifacts/org/app/stored-key.apk":
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" {
				t.Errorf("artifact request carried Revyl credentials")
			}
			if fake.artifactStatus != http.StatusOK {
				w.WriteHeader(fake.artifactStatus)
				return
			}
			if fake.contentDisposition != "" {
				w.Header().Set("Content-Disposition", fake.contentDisposition)
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(fake.artifact)))
			_, _ = w.Write(fake.artifact)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Build not found"}`))
		}
	}))
	t.Cleanup(fake.server.Close)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", fake.server.URL)
	return fake
}

func runBuildDownloadForTest(t *testing.T, versionID string, args ...string) (string, string, error) {
	t.Helper()
	cmd := &cobra.Command{Use: "download"}
	cmd.SetContext(context.Background())
	addBuildDownloadFlags(cmd)
	cmd.Flags().Bool("dev", false, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	var err error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
		err = runBuildDownload(cmd, []string{versionID})
	})
	return stdout, stderr, err
}

func TestBuildDownloadIsRegisteredUnderBuild(t *testing.T) {
	found, _, err := buildCmd.Find([]string{"download"})
	if err != nil || found != buildDownloadCmd {
		t.Fatalf("revyl build download is not registered: %v", err)
	}
}

func TestBuildDownloadSavesVerifiedArtifactUnderItsFileName(t *testing.T) {
	fake := newFakeBuildArtifactServer(t, []byte("apk-bytes"))
	t.Chdir(t.TempDir())

	stdout, stderr, err := runBuildDownloadForTest(t, "bv-1", "--json")

	if err != nil {
		t.Fatalf("build download: %v", err)
	}
	output := parseJSON(t, stdout)
	assertJSONString(t, output, "build_version_id", "bv-1")
	assertJSONString(t, output, "version", "1.2.3")
	assertJSONString(t, output, "path", "app-release.apk")
	assertJSONString(t, output, "sha256", fake.recordedSHA256)
	if output["sha256_verified"] != true || output["bytes"] != float64(len("apk-bytes")) {
		t.Fatalf("sha256_verified/bytes = %v/%v, want true/%d", output["sha256_verified"], output["bytes"], len("apk-bytes"))
	}
	if _, present := output["download_url"]; present {
		t.Fatal("download_url was printed without --print-url")
	}
	if strings.Contains(stdout+stderr, "fixture-signature") {
		t.Fatalf("output exposes the signed URL:\n%s%s", stdout, stderr)
	}
	if data, readErr := os.ReadFile("app-release.apk"); readErr != nil || string(data) != "apk-bytes" {
		t.Fatalf("saved artifact = %q, %v", data, readErr)
	}
	assertNoPartialDownloads(t, ".")
}

func TestBuildDownloadKeepsOnlyTheBaseNameOfAHostileFileName(t *testing.T) {
	fake := newFakeBuildArtifactServer(t, []byte("apk-bytes"))
	fake.contentDisposition = `attachment; filename="../../escape\\..\\evil.apk"`
	workDir := filepath.Join(t.TempDir(), "work")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workDir)

	_, _, err := runBuildDownloadForTest(t, "bv-1", "--json")

	if err != nil {
		t.Fatalf("build download: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(workDir, "evil.apk")); statErr != nil {
		t.Fatalf("artifact was not saved as evil.apk in the working directory: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(workDir), "evil.apk")); statErr == nil {
		t.Fatal("artifact escaped the working directory")
	}
}

func TestBuildDownloadIntoExistingDirectoryUsesTheArtifactName(t *testing.T) {
	newFakeBuildArtifactServer(t, []byte("apk-bytes"))
	dir := t.TempDir()

	stdout, _, err := runBuildDownloadForTest(t, "bv-1", "--out", dir, "--json")

	if err != nil {
		t.Fatalf("build download: %v", err)
	}
	assertJSONString(t, parseJSON(t, stdout), "path", filepath.Join(dir, "app-release.apk"))
}

func TestBuildDownloadDiscardsAnArtifactWhoseSHA256DoesNotMatch(t *testing.T) {
	fake := newFakeBuildArtifactServer(t, []byte("tampered-bytes"))
	fake.recordedSHA256 = strings.Repeat("ab", 32)
	dir := t.TempDir()
	out := filepath.Join(dir, "app.apk")

	_, _, err := runBuildDownloadForTest(t, "bv-1", "--out", out)

	if err == nil || !strings.Contains(err.Error(), "does not match its recorded SHA-256") {
		t.Fatalf("error = %v, want a SHA-256 mismatch", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched artifact was kept at %s", out)
	}
	assertNoPartialDownloads(t, dir)
}

func TestBuildDownloadWithoutRecordedChecksumReportsItUnverified(t *testing.T) {
	fake := newFakeBuildArtifactServer(t, []byte("apk-bytes"))
	fake.recordedSHA256 = ""
	out := filepath.Join(t.TempDir(), "app.apk")

	stdout, _, err := runBuildDownloadForTest(t, "bv-1", "--out", out, "--json")

	if err != nil {
		t.Fatalf("build download: %v", err)
	}
	if verified := parseJSON(t, stdout)["sha256_verified"]; verified != false {
		t.Fatalf("sha256_verified = %v, want false", verified)
	}
}

func TestBuildDownloadRefusesToOverwriteWithoutForce(t *testing.T) {
	newFakeBuildArtifactServer(t, []byte("new-bytes"))
	out := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(out, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runBuildDownloadForTest(t, "bv-1", "--out", out)

	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v, want a refusal naming --force", err)
	}
	if data, _ := os.ReadFile(out); string(data) != "existing" {
		t.Fatalf("existing file was changed to %q", data)
	}

	if _, _, err := runBuildDownloadForTest(t, "bv-1", "--out", out, "--force"); err != nil {
		t.Fatalf("build download --force: %v", err)
	}
	if data, _ := os.ReadFile(out); string(data) != "new-bytes" {
		t.Fatalf("--force left %q, want the new artifact", data)
	}
}

func TestBuildDownloadPrintURLWarnsAndIncludesTheURL(t *testing.T) {
	newFakeBuildArtifactServer(t, []byte("apk-bytes"))
	out := filepath.Join(t.TempDir(), "app.apk")

	stdout, stderr, err := runBuildDownloadForTest(t, "bv-1", "--out", out, "--print-url", "--json")

	if err != nil {
		t.Fatalf("build download: %v", err)
	}
	downloadURL, _ := parseJSON(t, stdout)["download_url"].(string)
	if !strings.Contains(downloadURL, buildDownloadTestSignature) {
		t.Fatalf("download_url = %q, want the signed URL", downloadURL)
	}
	if !strings.Contains(stderr, "temporary credential") || strings.Contains(stderr, "fixture-signature") {
		t.Fatalf("stderr should warn without repeating the URL:\n%s", stderr)
	}
}

func TestBuildDownloadArtifactErrorDoesNotExposeTheSignedURL(t *testing.T) {
	fake := newFakeBuildArtifactServer(t, []byte("apk-bytes"))
	fake.artifactStatus = http.StatusForbidden
	dir := t.TempDir()

	stdout, stderr, err := runBuildDownloadForTest(t, "bv-1", "--out", filepath.Join(dir, "app.apk"))

	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("error = %v, want HTTP 403", err)
	}
	if strings.Contains(err.Error()+stdout+stderr, "fixture-signature") {
		t.Fatalf("failure exposes the signed URL: %v", err)
	}
	assertNoPartialDownloads(t, dir)
}

func TestBuildDownloadUnknownVersionNamesTheNextStep(t *testing.T) {
	newFakeBuildArtifactServer(t, []byte("apk-bytes"))

	_, _, err := runBuildDownloadForTest(t, "missing", "--out", filepath.Join(t.TempDir(), "app.apk"))

	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "revyl build list") {
		t.Fatalf("error = %v, want not found with the build list hint", err)
	}
}

func TestSanitizeArtifactFileName(t *testing.T) {
	tests := map[string]string{
		"app-release.apk":           "app-release.apk",
		"../../evil.apk":            "evil.apk",
		`..\..\evil.ipa`:            "evil.ipa",
		"/etc/passwd":               "passwd",
		"..":                        "",
		" . ":                       "",
		"name:with*bad?chars.zip":   "name_with_bad_chars.zip",
		"line\nbreak.apk":           "line_break.apk",
		"MyApp Simulator Build.zip": "MyApp Simulator Build.zip",
	}
	for input, want := range tests {
		if got := sanitizeArtifactFileName(input); got != want {
			t.Errorf("sanitizeArtifactFileName(%q) = %q, want %q", input, got, want)
		}
	}
}

func assertNoPartialDownloads(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			t.Fatalf("partial download %s was left behind", entry.Name())
		}
	}
}

package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

func writeWebOutput(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readWebArchive(t *testing.T, archive string) map[string]string {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	files := map[string]string{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(content)
	}
}

func TestPackWebOutputStaticAndSSR(t *testing.T) {
	for _, files := range []map[string]string{
		{"index.html": "<h1>static</h1>", "assets/app.js": "app"},
		{"server.js": "console.log('server')", "node_modules/example/index.js": "module.exports = {}", ".next/server/app.js": "app"},
	} {
		root := t.TempDir()
		writeWebOutput(t, root, "source.ts", "not an artifact")
		writeWebOutput(t, root, ".env.local", "not an artifact")
		for name, content := range files {
			writeWebOutput(t, root, "dist/"+name, content)
		}
		archive, metadata, err := packWebBuildOutput(context.Background(), root, "dist")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(archive)
		if got := readWebArchive(t, archive); !reflect.DeepEqual(got, files) {
			t.Fatalf("archive = %#v, want %#v", got, files)
		}
		data, err := os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if metadata.SHA256 != hex.EncodeToString(hash[:]) || metadata.SizeBytes != int64(len(data)) || metadata.Artifact != "tarball" {
			t.Fatalf("metadata = %#v", metadata)
		}
	}
}

func TestPackWebOutputRejectsUnsafePaths(t *testing.T) {
	for _, output := range []string{"", ".", "..", "../dist", "dist/../../outside", "/tmp/dist", "dist/*", `dist\other`} {
		t.Run(output, func(t *testing.T) {
			if _, _, err := packWebBuildOutput(context.Background(), t.TempDir(), output); err == nil {
				t.Fatal("unsafe output path accepted")
			}
		})
	}
}

func TestPackWebOutputRejectsSensitiveFiles(t *testing.T) {
	for _, name := range []string{".env", ".env.production", ".git/config", ".gitconfig", ".git-credentials", ".hg/store", ".svn/entries", ".revyl/config.yaml", ".dev.vars", ".dev.vars.preview", ".envrc", ".npmrc", ".netrc", ".pypirc", ".aws/credentials", ".ssh/id_rsa", ".gnupg/secring.gpg"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWebOutput(t, root, "dist/"+name, "private")
			if _, _, err := packWebBuildOutput(context.Background(), root, "dist"); err == nil {
				t.Fatal("sensitive output accepted")
			}
		})
	}
}

func TestPackWebOutputRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires Windows privileges")
	}
	for _, target := range []string{"index.html", "../../secret"} {
		root := t.TempDir()
		writeWebOutput(t, root, "dist/index.html", "static")
		if err := os.Symlink(target, filepath.Join(root, "dist", "linked")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := packWebBuildOutput(context.Background(), root, "dist"); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	root := t.TempDir()
	writeWebOutput(t, root, "actual/index.html", "static")
	if err := os.Symlink("actual", filepath.Join(root, "dist")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := packWebBuildOutput(context.Background(), root, "dist"); err == nil {
		t.Fatal("output directory symlink accepted")
	}
}

func TestPackWebOutputLimitsAndCancellation(t *testing.T) {
	root := t.TempDir()
	writeWebOutput(t, root, "dist/index.html", strings.Repeat("large", 100))
	writeWebOutput(t, root, "dist/app.js", "app")
	for _, test := range []struct {
		maxBytes   int64
		maxEntries int
		message    string
	}{
		{100, 20, "web bundle exceeds its unpacked size limit of 100 bytes"},
		{10000, 1, "exceeds the 1-entry archive limit"},
	} {
		if _, _, err := packWebBuildOutputWithLimits(context.Background(), root, "dist", test.maxBytes, test.maxEntries); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("archive limit error = %v, want %q", err, test.message)
		}
	}
	compressed := &webBundleLimitedWriter{writer: io.Discard, measure: "compressed", limit: 20, remaining: 20}
	if _, err := compressed.Write(make([]byte, 21)); err == nil || err.Error() != "web bundle exceeds its compressed size limit of 20 bytes" {
		t.Fatalf("compressed limit not enforced: %v", err)
	}
	headers := t.TempDir()
	writeWebOutput(t, headers, "dist/index.html", strings.Repeat("x", 100))
	if _, _, err := packWebBuildOutputWithLimits(context.Background(), headers, "dist", 600, 10); err == nil || !strings.Contains(err.Error(), "web bundle exceeds its unpacked size limit of 600 bytes") {
		t.Fatalf("tar header bytes not counted toward the unpacked limit: %v", err)
	}
	if maxWebBundleBytes != 1<<30 || formatWebBundleLimit(maxWebBundleBytes) != "1 GiB" || formatWebBundleLimit(200<<20) != "200 MiB" {
		t.Fatalf("web bundle limit = %d (%s)", maxWebBundleBytes, formatWebBundleLimit(maxWebBundleBytes))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := packWebBuildOutput(ctx, root, "dist"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled packing = %v", err)
	}
	empty := t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := packWebBuildOutput(context.Background(), empty, "dist"); err == nil {
		t.Fatal("empty archive accepted")
	}
}

func TestManagedWebStaticMissingIndexFailsBeforeUpload(t *testing.T) {
	root := t.TempDir()
	gitInitBuildRepository(t, root)
	writeProjectBuildConfig(t, root, webManagedConfig("true", ""))
	writeWebOutput(t, root, "dist/server.js", "server")
	invocation, err := resolveBuildInvocation(root, "", "preview", "web", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := newPublicBuildTestCommand(t, "--platform=web", "--local")
	_, err = uploadLocalWebBuild(cmd, invocation, filepath.Join(invocation.ProjectRoot, "dist"), 0, nil, true)
	if err == nil || !strings.Contains(err.Error(), "set run.start") {
		t.Fatalf("missing static index error = %v", err)
	}
}

func webManagedConfig(command, run string) string {
	return "project: {id: " + buildTestProjectID + "}\nbuild:\n  profiles:\n    preview:\n      web:\n        app_id: 00000000-0000-4000-8000-000000000001\n        build_commands: ['" + command + "']\n        output_path: dist\n" + run
}

func TestWebURLOnlyBuildReturnsActionableError(t *testing.T) {
	for _, test := range []struct {
		name, profiles string
		explicit       [][2]string
	}{
		{
			name:     "URL-only profile",
			profiles: "    preview:\n      web: {app_id: 00000000-0000-4000-8000-000000000001}\n",
			explicit: [][2]string{{"", ""}, {"preview", ""}, {"preview", "web"}, {"", "web"}},
		},
		{
			name:     "URL-only Web beside a phone recipe",
			profiles: "    preview:\n      android: {build_commands: [./gradlew assembleDebug], output_path: app.apk}\n      web: {app_id: 00000000-0000-4000-8000-000000000001}\n",
			explicit: [][2]string{{"preview", "web"}, {"", "web"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			gitInitBuildRepository(t, root)
			writeProjectBuildConfig(t, root, "project: {id: "+buildTestProjectID+"}\nbuild:\n  framework: react_native\n  profiles:\n"+test.profiles)
			for _, explicit := range test.explicit {
				_, err := resolveBuildInvocation(root, "", explicit[0], explicit[1], false, nil)
				if err == nil || !strings.Contains(err.Error(), config.URLOnlyProfileMessage("preview")) {
					t.Fatalf("revyl build --profile %q --platform %q error = %v", explicit[0], explicit[1], err)
				}
			}
		})
	}
}

func TestManagedWebRemoteJobUsesExistingBuildPipeline(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-key")
	root := t.TempDir()
	gitInitBuildRepository(t, root)
	runGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "Web build test fixture")
	writeProjectBuildConfig(t, root, webManagedConfig("touch local-must-not-run", "        run: {start: node server.js, env_keys: [APP_URL], secret_names: [RUNTIME_TOKEN]}\n"))
	withWorkingDir(t, root)
	var request api.RemoteBuildRequest
	var calls []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/00000000-0000-4000-8000-000000000001":
			io.WriteString(w, `{"id":"00000000-0000-4000-8000-000000000001","platform":"web","latest_version":"v1","versions_count":1}`)
		case "/api/v1/apps/remote/upload-url":
			json.NewEncoder(w).Encode(api.RemoteBuildSourceUploadResponse{UploadUrl: server.URL + "/source", SourceKey: "source.tar.gz"})
		case "/source":
			io.Copy(io.Discard, r.Body)
		case "/api/v1/apps/remote":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			io.WriteString(w, `{"build_job_id":"web-job"}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := newPublicBuildTestCommand(t, "--platform=web", "--remote", "--detach", "--json")
	output := captureStdout(t, func() {
		if err := runBuild(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	result := parseJSON(t, output)
	assertJSONString(t, result, "platform", "web")
	assertJSONString(t, result, "build_job_id", "web-job")
	if request.Recipe == nil || request.Recipe.Run == nil || request.Recipe.Platform != "web" || request.Recipe.Run.Start == nil || *request.Recipe.Run.Start != "node server.js" || request.Recipe.Run.Port == nil || *request.Recipe.Run.Port != 3000 {
		t.Fatalf("recipe = %#v", request.Recipe)
	}
	if request.Recipe.Artifacts == nil || len(*request.Recipe.Artifacts) != 1 || (*request.Recipe.Artifacts)[0].Type != "web_bundle" || (*request.Recipe.Artifacts)[0].Path != "dist" {
		t.Fatalf("artifacts = %#v", request.Recipe.Artifacts)
	}
	if request.Recipe.Run.SecretNames == nil || !reflect.DeepEqual(*request.Recipe.Run.SecretNames, []string{"RUNTIME_TOKEN"}) || request.BuildDefinitionHash == nil {
		t.Fatalf("runtime names/hash missing: %#v", request)
	}
	source, sourceErr := request.Source.AsRemoteBuildArchiveSource()
	if sourceErr != nil || source.CommitSha == nil || !webBuildCommitSHA.MatchString(*source.CommitSha) {
		t.Fatalf("source revision missing: %#v", request.Source)
	}
	if _, err := os.Stat(filepath.Join(root, "local-must-not-run")); !os.IsNotExist(err) {
		t.Fatalf("remote build executed locally: %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("unexpected build request count: %v", calls)
	}
}

func TestManagedWebLocalStaticAndSSRUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local builds use a Unix shell")
	}
	for _, serverRendered := range []bool{false, true} {
		t.Run(map[bool]string{false: "static", true: "ssr"}[serverRendered], func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "test-key")
			t.Setenv("RUNTIME_TOKEN", "runtime-only-secret-value")
			root := t.TempDir()
			gitInitBuildRepository(t, root)
			command, run, file := "mkdir -p dist; printf static > dist/index.html", "", "index.html"
			if serverRendered {
				command, run, file = "mkdir -p dist; printf server > dist/server.js", "        run: {start: node server.js, env_keys: [APP_URL], secret_names: [RUNTIME_TOKEN]}\n", "server.js"
			}
			writeProjectBuildConfig(t, root, webManagedConfig(command, run))
			writeWebOutput(t, root, ".env.local", "not in artifact")
			withWorkingDir(t, root)
			var completed api.BuildUploadCompleteRequest
			var uploaded []byte
			var calls []string
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/apps/00000000-0000-4000-8000-000000000001/builds/upload-url":
					if r.URL.Query().Get("file_name") != "bundle.tar.zst" || r.URL.Query().Get("source") != "cli_upload" {
						t.Errorf("upload query = %s", r.URL.RawQuery)
					}
					json.NewEncoder(w).Encode(map[string]string{"version_id": "00000000-0000-4000-8000-000000000002", "version": "v1", "upload_url": server.URL + "/bundle", "content_type": "application/zstd"})
				case "/bundle":
					uploaded, _ = io.ReadAll(r.Body)
				case "/api/v1/apps/builds/00000000-0000-4000-8000-000000000002/complete-upload":
					if err := json.NewDecoder(r.Body).Decode(&completed); err != nil {
						t.Error(err)
					}
					io.WriteString(w, `{"version_id":"00000000-0000-4000-8000-000000000002","version":"v1"}`)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			cmd := newPublicBuildTestCommand(t, "--platform=web", "--local", "--json")
			commitSHA := strings.Repeat("a", 40)
			if serverRendered {
				commitSHA = strings.Repeat("b", 64)
			}
			ctx, err := build.WithUploadSCMContext(context.Background(), "example/web", commitSHA, 0)
			if err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(ctx)
			output := captureStdout(t, func() {
				if err := runBuild(cmd, nil); err != nil {
					t.Fatal(err)
				}
			})
			parseJSON(t, output)
			hash := sha256.Sum256(uploaded)
			if completed.Web == nil || completed.Web.Artifact == nil || *completed.Web.Artifact != "tarball" || completed.Web.Sha256 != hex.EncodeToString(hash[:]) || completed.Web.SizeBytes != len(uploaded) {
				t.Fatalf("completion = %#v", completed)
			}
			if completed.Metadata == nil || (*completed.Metadata)["commit_sha"] != commitSHA || completed.Web.Run == nil || completed.Web.Run.Start != nil != serverRendered {
				t.Fatalf("runtime/revision missing: %#v", completed)
			}
			completionJSON, err := json.Marshal(completed)
			if err != nil || strings.Contains(string(completionJSON), "runtime-only-secret-value") {
				t.Fatalf("runtime values leaked or metadata failed to encode: %v", err)
			}
			archive := filepath.Join(t.TempDir(), "bundle.tar.zst")
			os.WriteFile(archive, uploaded, 0o600)
			files := readWebArchive(t, archive)
			if len(files) != 1 || files[file] == "" || len(calls) != 3 {
				t.Fatalf("files/calls = %#v, %v", files, calls)
			}
		})
	}
}

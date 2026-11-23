package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fakeSchema = `{"cli_schema":{"global_flags":[{"name":"chdir","type":"string"},{"name":"json","type":"bool"}],"commands":[{"path":"feedback","flags":[{"name":"type","type":"string","description":"Feedback type: bug or other"},{"name":"body","type":"string"}]},{"path":"device","subcommands":[{"path":"device start","flags":[{"name":"platform","type":"string"},{"name":"open","type":"bool"}]}]}]}}`

func fakeRelease(t *testing.T, checksumOverride string) *httptest.Server {
	t.Helper()
	binary := []byte("#!/bin/sh\ncat <<'EOF'\n" + fakeSchema + "\nEOF\n")
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	if checksumOverride != "" {
		checksum = checksumOverride
	}
	asset := fmt.Sprintf("revyl-%s-%s", runtime.GOOS, runtime.GOARCH)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v9.9.9/checksums.txt":
			fmt.Fprintf(writer, "%s  %s\n", checksum, asset)
		case "/v9.9.9/" + asset:
			_, _ = writer.Write(binary)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

const usableRecord = "global --json\nfeedback-type Feedback type: bug or other\ncommand device start --open\n"

func writeRecord(t *testing.T, dir, tag, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, tag+".txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordCommandTreesRecordsMissingPinsAndRemovesUnpinnedOnes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake release binary is a shell script")
	}
	dir := t.TempDir()
	writeRecord(t, dir, "v1.0.0", usableRecord)
	writeRecord(t, dir, "v0.9.0", usableRecord)
	server := fakeRelease(t, "")

	if err := recordCommandTrees(context.Background(), server.Client(), server.URL, dir, []string{"v9.9.9", "v1.0.0"}); err != nil {
		t.Fatal(err)
	}

	recorded, err := os.ReadFile(filepath.Join(dir, "v9.9.9.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "global --chdir= --json\nfeedback-type Feedback type: bug or other\ncommand feedback --type= --body=\ncommand device\ncommand device start --platform= --open\n"
	if string(recorded) != want {
		t.Errorf("v9.9.9.txt = %q, want %q", recorded, want)
	}
	if kept, _ := os.ReadFile(filepath.Join(dir, "v1.0.0.txt")); string(kept) != usableRecord {
		t.Errorf("v1.0.0.txt = %q, want it unchanged", kept)
	}
	if _, err := os.Stat(filepath.Join(dir, "v0.9.0.txt")); !os.IsNotExist(err) {
		t.Errorf("v0.9.0.txt must be removed once no plugin pins it: %v", err)
	}
}

func TestRecordCommandTreesRecordsAnEmptyOrMalformedRecordAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake release binary is a shell script")
	}
	server := fakeRelease(t, "")
	for name, content := range map[string]string{"empty": "", "malformed": "not a command tree\n"} {
		dir := t.TempDir()
		writeRecord(t, dir, "v9.9.9", content)

		if err := recordCommandTrees(context.Background(), server.Client(), server.URL, dir, []string{"v9.9.9"}); err != nil {
			t.Fatal(err)
		}

		if recorded, _ := os.ReadFile(filepath.Join(dir, "v9.9.9.txt")); !strings.HasPrefix(string(recorded), "global --chdir= --json\n") {
			t.Errorf("%s v9.9.9.txt = %q, want the release's tree", name, recorded)
		}
	}
}

func TestRecordCommandTreesRefusesABinaryThatFailsItsChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake release binary is a shell script")
	}
	dir := t.TempDir()
	server := fakeRelease(t, strings.Repeat("0", 64))

	err := recordCommandTrees(context.Background(), server.Client(), server.URL, dir, []string{"v9.9.9"})

	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("recordCommandTrees() error = %v, want a checksum mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "v9.9.9.txt")); !os.IsNotExist(statErr) {
		t.Errorf("a record was written for an unverified binary: %v", statErr)
	}
}

func TestRecordCommandTreesRejectsANameThatIsNotAReleaseTag(t *testing.T) {
	if err := recordCommandTrees(context.Background(), http.DefaultClient, "http://unused.invalid", t.TempDir(), []string{"../v1.0.0"}); err == nil {
		t.Fatal("recordCommandTrees() error = nil, want the bad tag rejected")
	}
}

func TestCheckCommandTreesNamesMissingAndUnpinnedRecords(t *testing.T) {
	dir := t.TempDir()
	writeRecord(t, dir, "v1.0.0", usableRecord)
	writeRecord(t, dir, "v0.9.0", usableRecord)

	err := checkCommandTrees(dir, []string{"v1.0.0", "v2.0.0"})

	if err == nil || !strings.Contains(err.Error(), "no record for pinned release v2.0.0") || !strings.Contains(err.Error(), "v0.9.0, which no plugin pins") {
		t.Fatalf("checkCommandTrees() error = %v, want the missing v2.0.0 and unpinned v0.9.0 named", err)
	}
	if err := checkCommandTrees(dir, []string{"v1.0.0", "v0.9.0"}); err != nil {
		t.Fatalf("checkCommandTrees() error = %v, want none when records match the pins", err)
	}
}

func TestCheckCommandTreesRejectsAnEmptyOrMalformedRecord(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "malformed": "not a command tree\n"} {
		dir := t.TempDir()
		writeRecord(t, dir, "v1.0.0", content)

		err := checkCommandTrees(dir, []string{"v1.0.0"})

		if err == nil || !strings.Contains(err.Error(), "an unusable record for pinned release v1.0.0") {
			t.Errorf("checkCommandTrees(%s record) error = %v, want the record rejected", name, err)
		}
	}
}

func TestPinnedReleaseTagsReadsBothPluginManifests(t *testing.T) {
	root := t.TempDir()
	for path, tag := range map[string]string{"plugins/revyl/runtime-manifest.json": "v0.2.34", "cursor-plugin/runtime-manifest.json": "v0.1.210"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(fmt.Sprintf("{\"release_tag\": %q}", tag)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tags, err := pinnedReleaseTags(root)

	if err != nil || strings.Join(tags, " ") != "v0.2.34 v0.1.210" {
		t.Fatalf("pinnedReleaseTags() = %q, %v, want both pins", tags, err)
	}
}

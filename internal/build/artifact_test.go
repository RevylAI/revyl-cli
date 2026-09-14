package build

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const clipPlist = `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.Clip</string><key>NSAppClip</key><dict/></dict></plist>`
const hostPlist = `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.Host</string></dict></plist>`

type iosArchiveEntry struct {
	name, data string
	mode       os.FileMode
	hardlink   bool
}

func TestIOSArchiveRoundTrip(t *testing.T) {
	for _, tc := range []struct{ format, filename, app string }{
		{"gzip-tar", "build.gz", "Clip.app"},
		{"zip", "build.GZ", "Clip.app"},
		{"gzip-tar", "build.tgz", "Host.app"},
		{"zip", "build.tar.gz", "Host.app"},
	} {
		t.Run(tc.format+"/"+tc.app, func(t *testing.T) {
			entries := []iosArchiveEntry{
				{name: "Clip.app/Info.plist", data: clipPlist},
				{name: tc.app + "/Alias/Executable", data: "executable", mode: 0o755},
				{name: tc.app + "/Alias", data: "Resources", mode: os.ModeSymlink | 0o777},
			}
			want := []iosArchiveEntry{
				{name: tc.app + "/Info.plist", data: clipPlist, mode: 0o644},
				{name: tc.app + "/Resources/Executable", data: "executable", mode: 0o755},
				entries[2],
			}
			if tc.app == "Host.app" {
				want[0].data = hostPlist
				embedded := iosArchiveEntry{name: "Host.app/AppClips/Clip.app/Info.plist", data: clipPlist, mode: 0o644}
				entries = append(entries, want[0], embedded)
				want = append(want, embedded)
			}
			if tc.format == "gzip-tar" {
				entries = append(entries, iosArchiveEntry{name: tc.app + "/LinkedExecutable", data: tc.app + "/Resources/Executable", hardlink: true})
				want = append(want, iosArchiveEntry{name: tc.app + "/LinkedExecutable", data: "executable", mode: 0o755})
			}
			input := writeIOSArchive(t, tc.format, tc.filename, entries)
			if PlatformFromFilePath(input) != "ios" || !IsTarGz(input) {
				t.Fatal("compressed iOS artifact was not recognized")
			}
			output, err := ExtractAppFromArchive(input)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(output) })
			assertIOSArchiveEntries(t, output, tc.app, want)
		})
	}
}

func TestArchiveExtractionKeepsSymlinksOffDisk(t *testing.T) {
	members := []archiveMember{
		{name: "Clip.app/Resources/Info.plist", mode: 0o644},
		{name: "Clip.app/Info.plist", target: "Resources/Info.plist", isLink: true, mode: os.ModeSymlink | 0o777},
		{name: "Clip.app/Alias", target: "Resources", isLink: true, mode: os.ModeSymlink | 0o777},
	}
	layout, err := validateArchiveMembers(members)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	extractor, err := newArchiveExtractor(root, layout, &archiveWorkspaceBudget{limitBytes: archiveWorkspaceLimitBytes})
	if err != nil {
		t.Fatal(err)
	}
	if err := extractor.extractMember(members[0], strings.NewReader(clipPlist)); err != nil {
		t.Fatal(err)
	}
	if err := extractor.stageLinks(); err != nil {
		t.Fatal(err)
	}
	for _, member := range members[1:] {
		linkPath := filepath.Join(root, filepath.FromSlash(member.name))
		if _, err := os.Lstat(linkPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("symlink was materialized on disk: %s, error = %v", member.name, err)
		}
		metadata, found, err := extractor.metadata.lookup(linkPath)
		if err != nil || !found || metadata.mode != member.mode || metadata.linkTarget != member.target {
			t.Fatalf("symlink metadata was lost: %s, metadata = %+v, found = %v, error = %v", member.name, metadata, found, err)
		}
	}
}

func TestIOSArchiveFrameworkSymlinks(t *testing.T) {
	entries := []iosArchiveEntry{
		{name: "Host.app/Info.plist", data: "Resources/Info.plist", mode: os.ModeSymlink | 0o777},
		{name: "Host.app/Resources/Info.plist", data: hostPlist, mode: 0o644},
		{name: "Host.app/Frameworks/Example.framework/Example", data: "Versions/Current/Example", mode: os.ModeSymlink | 0o777},
		{name: "Host.app/Frameworks/Example.framework/Versions/Current", data: "A", mode: os.ModeSymlink | 0o777},
		{name: "Host.app/Frameworks/Example.framework/Versions/A/Example", data: "framework executable", mode: 0o755},
		{name: "Host.app/Empty/Link", data: "../Resources", mode: os.ModeSymlink | 0o777},
		{name: "Clip.app/Info.plist", data: clipPlist, mode: 0o644},
		{name: "Clip.app/Alias", data: ".", mode: os.ModeSymlink | 0o777},
	}
	for _, format := range []string{"zip", "gzip-tar"} {
		for _, order := range []string{"forward", "reverse"} {
			t.Run(format+"/"+order, func(t *testing.T) {
				ordered := slices.Clone(entries)
				if order == "reverse" {
					slices.Reverse(ordered)
				}
				input := writeIOSArchive(t, format, "build.gz", ordered)
				output, err := ExtractAppFromArchive(input)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(output) })
				assertIOSArchiveEntries(t, output, "Host.app", entries[:6])
			})
		}
	}
}

func TestIOSArchiveRejectionLeavesWorkspaceClean(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		entries      []iosArchiveEntry
		limits       archiveLimits
		wantError    string
	}{
		{name: "escaping link", format: "zip", wantError: "unsafe archive", entries: []iosArchiveEntry{
			{name: "outside", data: "..", mode: os.ModeSymlink | 0o777},
			{name: "outside/marker", data: "overwritten"},
		}},
		{name: "normalized collision", format: "gzip-tar", wantError: "duplicate", entries: []iosArchiveEntry{
			{name: "Clip.app/é", data: "first"},
			{name: "CLIP.APP/e\u0301", data: "second"},
		}},
		{name: "cyclic symlinks", format: "zip", wantError: "cyclic", entries: []iosArchiveEntry{
			{name: "Clip.app/First", data: "Second", mode: os.ModeSymlink | 0o777},
			{name: "Clip.app/Second", data: "First", mode: os.ModeSymlink | 0o777},
		}},
		{name: "symlink path collision", format: "gzip-tar", wantError: "duplicate", entries: []iosArchiveEntry{
			{name: "Clip.app/Alias", data: "Resources", mode: os.ModeSymlink | 0o777},
			{name: "Clip.app/alias", data: "file"},
		}},
		{name: "expanded workspace", format: "gzip-tar", wantError: "workspace limit",
			limits:  archiveLimits{workspaceBytes: 16 << 10, artifactBytes: 1 << 20},
			entries: []iosArchiveEntry{{name: "Clip.app/large", data: strings.Repeat("x", 32<<10)}},
		},
		{name: "output limit", format: "zip", wantError: "artifact limit",
			limits: archiveLimits{workspaceBytes: 1 << 20, artifactBytes: 128},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := append([]iosArchiveEntry{{name: "Clip.app/Info.plist", data: clipPlist}}, tc.entries...)
			input := writeIOSArchive(t, tc.format, "build.gz", entries)
			workspace := t.TempDir()
			for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(variable, workspace)
			}
			marker := filepath.Join(workspace, "marker")
			if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			limits := tc.limits
			if limits == (archiveLimits{}) {
				limits = defaultArchiveLimits()
			}
			output, err := extractAppFromArchive(input, limits)
			if output != "" || err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("output = %q, error = %v; want %q", output, err, tc.wantError)
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "unchanged" {
				t.Fatalf("outside marker changed: %q, error = %v", data, err)
			}
			remaining, err := os.ReadDir(workspace)
			if err != nil || len(remaining) != 1 || remaining[0].Name() != "marker" {
				t.Fatalf("temporary files remain: %v, error = %v", remaining, err)
			}
		})
	}
}

func assertIOSArchiveEntries(t *testing.T, filename, app string, expectedEntries []iosArchiveEntry) {
	t.Helper()
	archive, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if _, err := validateZipArchive(&archive.Reader); err != nil {
		t.Fatalf("normalized ZIP failed safety validation: %v", err)
	}
	files := make(map[string]*zip.File)
	for _, entry := range archive.File {
		if !strings.HasPrefix(entry.Name, app+"/") {
			t.Fatalf("unexpected sibling in normalized ZIP: %s", entry.Name)
		}
		files[entry.Name] = entry
	}
	for _, expected := range expectedEntries {
		entry := files[expected.name]
		if entry == nil || entry.Mode() != expected.mode {
			t.Fatalf("missing entry or incorrect mode: %s", expected.name)
		}
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(stream)
		if err := errors.Join(readErr, stream.Close()); err != nil || string(data) != expected.data {
			t.Fatalf("%s content = %q, error = %v", expected.name, data, err)
		}
	}
}

func writeIOSArchive(t *testing.T, format, filename string, entries []iosArchiveEntry) string {
	t.Helper()
	var content bytes.Buffer
	if format == "zip" {
		archive := zip.NewWriter(&content)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
			mode := entry.mode
			if mode == 0 {
				mode = 0o644
			}
			header.SetMode(mode)
			writer, err := archive.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(writer, entry.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&content)
		archive := tar.NewWriter(compressed)
		for _, entry := range entries {
			mode := entry.mode.Perm()
			if mode == 0 {
				mode = 0o644
			}
			header := &tar.Header{Name: entry.name, Mode: int64(mode), Size: int64(len(entry.data))}
			if entry.mode&os.ModeSymlink != 0 || entry.hardlink {
				header.Size, header.Linkname, header.Typeflag = 0, entry.data, tar.TypeSymlink
				if entry.hardlink {
					header.Typeflag = tar.TypeLink
				}
			}
			if err := archive.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Size > 0 {
				if _, err := io.WriteString(archive, entry.data); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
			t.Fatal(err)
		}
	}
	archivePath := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(archivePath, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

package build

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "MyApp.app", "MyApp"), "binary-content")
	writeFile(t, filepath.Join(dir, "MyApp.app", "Info.plist"), "<plist/>")

	m, err := BuildManifest(filepath.Join(dir, "MyApp.app"))
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if len(m.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(m.Files))
	}
	if m.Hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if _, ok := m.Files["MyApp"]; !ok {
		t.Fatal("expected MyApp in manifest")
	}
	if _, ok := m.Files["Info.plist"]; !ok {
		t.Fatal("expected Info.plist in manifest")
	}
}

func TestDiffManifest_EmptyDiff(t *testing.T) {
	m := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 100, Mtime: 1000},
			"Info.plist": {Size: 50, Mtime: 1000},
		},
	}
	diff := DiffManifest(m, m)
	if len(diff.Changed) != 0 {
		t.Fatalf("expected empty diff, got %d changed", len(diff.Changed))
	}
	if len(diff.Deleted) != 0 {
		t.Fatalf("expected no deletions, got %d", len(diff.Deleted))
	}
}

func TestDiffManifest_SingleFileChange(t *testing.T) {
	old := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 100, Mtime: 1000},
			"Info.plist": {Size: 50, Mtime: 1000},
		},
	}
	cur := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 120, Mtime: 2000},
			"Info.plist": {Size: 50, Mtime: 1000},
		},
	}
	diff := DiffManifest(old, cur)
	if len(diff.Changed) != 1 || diff.Changed[0] != "MyApp" {
		t.Fatalf("expected [MyApp], got %v", diff.Changed)
	}
}

func TestDiffManifest_NewFile(t *testing.T) {
	old := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp": {Size: 100, Mtime: 1000},
		},
	}
	cur := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 100, Mtime: 1000},
			"Assets.car": {Size: 500, Mtime: 2000},
		},
	}
	diff := DiffManifest(old, cur)
	if len(diff.Changed) != 1 || diff.Changed[0] != "Assets.car" {
		t.Fatalf("expected [Assets.car], got %v", diff.Changed)
	}
}

func TestDiffManifest_NilOldReturnsAll(t *testing.T) {
	cur := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 100, Mtime: 1000},
			"Info.plist": {Size: 50, Mtime: 1000},
		},
	}
	diff := DiffManifest(nil, cur)
	if len(diff.Changed) != 2 {
		t.Fatalf("expected 2 files, got %d", len(diff.Changed))
	}
}

func TestDiffManifest_DeletedFiles(t *testing.T) {
	old := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 100, Mtime: 1000},
			"OldAsset":   {Size: 200, Mtime: 1000},
			"Info.plist": {Size: 50, Mtime: 1000},
		},
	}
	cur := &AppManifest{
		Files: map[string]ManifestEntry{
			"MyApp":      {Size: 100, Mtime: 1000},
			"Info.plist": {Size: 50, Mtime: 1000},
		},
	}
	diff := DiffManifest(old, cur)
	if len(diff.Changed) != 0 {
		t.Fatalf("expected no changes, got %v", diff.Changed)
	}
	if len(diff.Deleted) != 1 || diff.Deleted[0] != "OldAsset" {
		t.Fatalf("expected [OldAsset] deleted, got %v", diff.Deleted)
	}
}

func TestCreateDeltaZip(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "MyApp.app")
	writeFile(t, filepath.Join(appDir, "MyApp"), "new-binary")
	writeFile(t, filepath.Join(appDir, "Info.plist"), "<plist/>")

	data, err := CreateDeltaZip(appDir, []string{"MyApp"})
	if err != nil {
		t.Fatalf("CreateDeltaZip: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty zip")
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	if len(r.File) != 1 {
		t.Fatalf("expected 1 file in zip, got %d", len(r.File))
	}
	if r.File[0].Name != "MyApp" {
		t.Fatalf("expected MyApp in zip, got %s", r.File[0].Name)
	}
}

func TestDeltaSize(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "MyApp.app")
	writeFile(t, filepath.Join(appDir, "MyApp"), "binary-content-12345")
	writeFile(t, filepath.Join(appDir, "Info.plist"), "<plist/>")

	size := DeltaSize(appDir, []string{"MyApp"})
	if size != 20 {
		t.Fatalf("expected size 20, got %d", size)
	}
}

func TestSaveLoadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	m := &AppManifest{
		AppPath: "/tmp/MyApp.app",
		Hash:    "abc123",
		Files: map[string]ManifestEntry{
			"MyApp": {Size: 100, Mtime: 1000},
		},
	}

	if err := SaveManifest(m, path); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if loaded.Hash != "abc123" {
		t.Fatalf("expected hash abc123, got %s", loaded.Hash)
	}
	if len(loaded.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(loaded.Files))
	}
}

func TestLoadManifest_Missing(t *testing.T) {
	m, err := LoadManifest("/nonexistent/path")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if m != nil {
		t.Fatal("expected nil manifest for missing file")
	}
}

func TestLargeDeltaFallback(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "MyApp.app")
	largeContent := make([]byte, 25*1024*1024)
	writeFileBytes(t, filepath.Join(appDir, "MyApp"), largeContent)

	size := DeltaSize(appDir, []string{"MyApp"})
	if size < 20*1024*1024 {
		t.Fatalf("expected size > 20MB, got %d", size)
	}
}

func TestParseXcodeBuildErrors(t *testing.T) {
	output := `/path/to/LoginView.swift:42:15: error: Cannot convert value of type 'String'
/path/to/AppDelegate.swift:10:1: warning: unused variable 'x'
some other line that should be ignored
`
	errors := ParseXcodeBuildErrors(output)
	if len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errors))
	}
	if errors[0].File != "/path/to/LoginView.swift" {
		t.Fatalf("expected LoginView.swift, got %s", errors[0].File)
	}
	if errors[0].Line != 42 || errors[0].Column != 15 {
		t.Fatalf("expected line 42 col 15, got %d:%d", errors[0].Line, errors[0].Column)
	}
	if errors[0].Severity != "error" {
		t.Fatalf("expected error, got %s", errors[0].Severity)
	}
	if errors[1].Severity != "warning" {
		t.Fatalf("expected warning, got %s", errors[1].Severity)
	}
}

func TestParseGradleBuildErrors(t *testing.T) {
	output := `e: file:///path/to/File.kt:42:15 Unresolved reference: foo
some other gradle output
`
	errors := ParseGradleBuildErrors(output)
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}
	if errors[0].File != "/path/to/File.kt" {
		t.Fatalf("expected /path/to/File.kt, got %s", errors[0].File)
	}
	if errors[0].Line != 42 {
		t.Fatalf("expected line 42, got %d", errors[0].Line)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeFileBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
}

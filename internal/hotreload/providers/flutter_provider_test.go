package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFlutterDetect_ValidFlutterProject(t *testing.T) {
	dir := t.TempDir()

	pubspec := `name: flutter_minimal
description: A minimal Flutter app.
environment:
  sdk: ^3.0.0
dependencies:
  flutter:
    sdk: flutter
flutter:
  uses-material-design: true
`
	os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(pubspec), 0644)
	os.MkdirAll(filepath.Join(dir, "lib"), 0755)
	os.WriteFile(filepath.Join(dir, "lib", "main.dart"), []byte("void main() {}"), 0644)
	os.MkdirAll(filepath.Join(dir, "android"), 0755)
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)

	p := &FlutterProvider{}
	result, err := p.Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected detection result, got nil")
	}
	if result.Provider != "flutter" {
		t.Fatalf("expected provider 'flutter', got %q", result.Provider)
	}
	if result.Confidence != 0.75 {
		t.Fatalf("expected confidence 0.75, got %f", result.Confidence)
	}
	if result.Platform != "cross-platform" {
		t.Fatalf("expected 'cross-platform', got %q", result.Platform)
	}
	if len(result.Indicators) < 2 {
		t.Fatalf("expected at least 2 indicators, got %d: %v", len(result.Indicators), result.Indicators)
	}
}

func TestFlutterDetect_NotFlutterProject(t *testing.T) {
	dir := t.TempDir()

	// pubspec.yaml without flutter SDK
	pubspec := `name: dart_package
dependencies:
  http: ^1.0.0
`
	os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(pubspec), 0644)

	p := &FlutterProvider{}
	result, _ := p.Detect(dir)
	if result != nil {
		t.Fatal("expected nil for non-flutter pubspec.yaml")
	}
}

func TestFlutterDetect_NoPubspec(t *testing.T) {
	dir := t.TempDir()

	p := &FlutterProvider{}
	result, _ := p.Detect(dir)
	if result != nil {
		t.Fatal("expected nil when no pubspec.yaml exists")
	}
}

func TestFlutterDetect_AndroidOnly(t *testing.T) {
	dir := t.TempDir()

	pubspec := `name: flutter_android
dependencies:
  flutter:
    sdk: flutter
`
	os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(pubspec), 0644)
	os.MkdirAll(filepath.Join(dir, "android"), 0755)

	p := &FlutterProvider{}
	result, _ := p.Detect(dir)
	if result == nil {
		t.Fatal("expected detection result")
	}
	if result.Platform != "android" {
		t.Fatalf("expected 'android', got %q", result.Platform)
	}
}

func TestFlutterGetProjectInfo(t *testing.T) {
	dir := t.TempDir()

	pubspec := `name: my_flutter_app
dependencies:
  flutter:
    sdk: flutter
`
	os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(pubspec), 0644)
	os.MkdirAll(filepath.Join(dir, "android"), 0755)
	os.MkdirAll(filepath.Join(dir, "ios"), 0755)

	p := &FlutterProvider{}
	info, err := p.GetProjectInfo(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "my_flutter_app" {
		t.Fatalf("expected name 'my_flutter_app', got %q", info.Name)
	}
	if info.Flutter == nil {
		t.Fatal("expected Flutter info to be non-nil")
	}
	if info.Flutter.Name != "my_flutter_app" {
		t.Fatalf("expected flutter name 'my_flutter_app', got %q", info.Flutter.Name)
	}
	if info.Platform != "cross-platform" {
		t.Fatalf("expected 'cross-platform', got %q", info.Platform)
	}
}

func TestFlutterProvider_IsNotSupported(t *testing.T) {
	p := &FlutterProvider{}
	if p.IsSupported() {
		t.Fatal("flutter should not be supported (uses rebuild loop, not dev server)")
	}
}

func TestFlutterProvider_Name(t *testing.T) {
	p := &FlutterProvider{}
	if p.Name() != "flutter" {
		t.Fatalf("expected 'flutter', got %q", p.Name())
	}
}

func TestFlutterProvider_DisplayName(t *testing.T) {
	p := &FlutterProvider{}
	if p.DisplayName() != "Flutter" {
		t.Fatalf("expected 'Flutter', got %q", p.DisplayName())
	}
}

func TestFlutterDetect_RealFlutterMinimal(t *testing.T) {
	// Test against the actual internal-apps/flutter-minimal if it exists.
	dir := filepath.Join("..", "..", "..", "..", "internal-apps", "flutter-minimal")
	if _, err := os.Stat(filepath.Join(dir, "pubspec.yaml")); err != nil {
		t.Skipf("flutter-minimal not found at %s, skipping", dir)
	}

	p := &FlutterProvider{}
	result, err := p.Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected detection result for flutter-minimal")
	}
	if result.Provider != "flutter" {
		t.Fatalf("expected 'flutter', got %q", result.Provider)
	}
	if result.Platform != "cross-platform" {
		t.Fatalf("expected 'cross-platform', got %q", result.Platform)
	}

	info, err := p.GetProjectInfo(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "flutter_minimal" {
		t.Fatalf("expected name 'flutter_minimal', got %q", info.Name)
	}
}

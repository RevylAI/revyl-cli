package build

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseBuildToolError_InvalidNpxInvocation(t *testing.T) {
	stderr := `npm error could not determine executable to run
npm error A complete log of this run can be found in: /tmp/npm.log
npm ERR! npm exec eas build --platform ios`

	err := parseBuildToolError(stderr, "npx eas build --platform ios")
	if err == nil {
		t.Fatalf("parseBuildToolError() returned nil")
	}
	if err.Message != "invalid npx eas invocation" {
		t.Fatalf("Message = %q, want %q", err.Message, "invalid npx eas invocation")
	}
	if !strings.Contains(err.Guidance, "npx --yes eas-cli build ...") {
		t.Fatalf("Guidance = %q, expected npx --yes eas-cli guidance", err.Guidance)
	}
}

func TestParseBuildToolError_LoginRequiredFromNonInteractivePrompt(t *testing.T) {
	stderr := `An Expo user account is required to proceed.

Log in to EAS with email or username (exit and run eas login --help to see other login options)
Input is required, but stdin is not readable. Failed to display prompt: Email or username`

	err := parseBuildToolError(stderr, "npx --yes eas-cli build --platform ios")
	if err == nil {
		t.Fatalf("parseBuildToolError() returned nil")
	}
	if err.Message != "Not logged in to EAS" {
		t.Fatalf("Message = %q, want %q", err.Message, "Not logged in to EAS")
	}
	if !strings.Contains(err.Guidance, "npx --yes eas-cli login") {
		t.Fatalf("Guidance = %q, expected npx --yes eas-cli login guidance", err.Guidance)
	}
}

func TestParseBuildToolError_BazelNotFound(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		command string
	}{
		{
			name:    "sh style bazel",
			stderr:  "/bin/sh: bazel: command not found",
			command: "bazel build //app:target -c dbg",
		},
		{
			name:    "zsh style bazel",
			stderr:  "command not found: bazel",
			command: "bazel build //app:target -c dbg",
		},
		{
			name:    "sh style bazelisk",
			stderr:  "/bin/sh: bazelisk: command not found",
			command: "bazelisk build //app:target -c dbg",
		},
		{
			name:    "zsh style bazelisk",
			stderr:  "command not found: bazelisk",
			command: "bazelisk build //ios:MyApp -c dbg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseBuildToolError(tt.stderr, tt.command)
			if err == nil {
				t.Fatalf("parseBuildToolError() returned nil for stderr=%q", tt.stderr)
			}
			if err.Message != "bazel not found" {
				t.Fatalf("Message = %q, want %q", err.Message, "bazel not found")
			}
			if !strings.Contains(err.Guidance, "brew install bazelisk") {
				t.Fatalf("Guidance = %q, expected bazelisk install guidance", err.Guidance)
			}
		})
	}
}

func TestParseBuildToolError_FlutterNotFound(t *testing.T) {
	err := parseBuildToolError("/bin/sh: flutter: command not found", "flutter build apk --debug")
	if err == nil {
		t.Fatalf("parseBuildToolError() returned nil")
	}
	if err.Message != "flutter not found" {
		t.Fatalf("Message = %q, want %q", err.Message, "flutter not found")
	}
	if !strings.Contains(err.Guidance, "flutter.dev") {
		t.Fatalf("Guidance = %q, expected Flutter install link", err.Guidance)
	}
}

func TestParseBuildToolError_GradleNotFound(t *testing.T) {
	err := parseBuildToolError("/bin/sh: ./gradlew: command not found", "./gradlew :app:assembleDebug")
	if err == nil {
		t.Fatalf("parseBuildToolError() returned nil")
	}
	if err.Message != "gradle not found" {
		t.Fatalf("Message = %q, want %q", err.Message, "gradle not found")
	}
}

func TestParseBuildToolError_NoMatch(t *testing.T) {
	err := parseBuildToolError("some random build output\nwarning: deprecated API", "bazel build //app:target")
	if err != nil {
		t.Fatalf("parseBuildToolError() = %v, want nil for unrecognized stderr", err)
	}
}

func TestShouldShowBuildLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		// Xcode build phases
		{"xcode compile", "Compiling AppDelegate.swift", true},
		{"xcode link", "Linking MyApp", true},
		{"xcode sign", "Signing MyApp.app", true},
		{"xcode codesign", "CodeSign /path/to/MyApp.app", true},
		{"xcode ld", "Ld /path/to/binary", true},
		{"swift compile", "SwiftCompile normal arm64 /src/File.swift", true},
		{"swift emit module", "SwiftEmitModule normal arm64", true},
		{"build succeeded", "Build Succeeded", true},
		{"build failed", "Build Failed", true},
		{"build all caps succeeded", "BUILD SUCCEEDED", true},
		{"build all caps failed", "BUILD FAILED", true},
		{"xcode build marker", "** BUILD SUCCEEDED **", true},

		// Gradle phases
		{"gradle app task", ":app:compileDebugKotlin", true},
		{"gradle task", "> Task :app:bundleRelease", true},

		// EAS / Expo lifecycle milestones
		{"eas creating build", "Creating build for simulator", true},
		{"eas resolving", "Resolving iOS build settings", true},
		{"eas install deps", "Installing dependencies", true},
		{"eas prebuild", "Running prebuild", true},
		{"eas sourcemaps", "Generating sourcemaps", true},
		{"eas bundling", "Bundling with Metro", true},
		{"eas building", "Building iOS project", true},
		{"eas archiving", "Archiving app", true},
		{"eas packaging", "Packaging build artifacts", true},
		{"eas downloading", "Downloading build", true},
		{"eas pod install", "Installing pods", true},
		{"eas pod install run", "Running pod install", true},

		// Mid-line EAS markers (case-insensitive)
		{"mid-line creating build", "  > Creating build for iOS simulator", true},
		{"mid-line prebuild", "  [expo] Running expo prebuild --platform ios", true},
		{"mid-line pod install", "  [expo] Running pod install in ios directory", true},

		// Warning / error lines
		{"warning prefix", "warning: unused variable", true},
		{"error prefix", "error: cannot find module", true},
		{"mid-line warning", "MyFile.swift:42: warning: deprecated API", true},
		{"mid-line error", "MyFile.swift:10: error: type mismatch", true},

		// Lines that should be filtered out
		{"empty line", "", false},
		{"whitespace only", "   ", false},
		{"cd command", "cd /path/to/project", false},
		{"verbose compiler invocation", "    /usr/bin/clang -x c++ -target arm64-apple-ios15.0 ...", false},
		{"destination list", "    { platform:iOS Simulator, id:ABC123, OS:16.4, name:iPhone 14 }", false},
		{"xcode multiple destination warning", "--- xcodebuild: WARNING: Using the first of multiple matching destinations:", false},
		{"xcode command line build settings header", "Build settings from command line:", false},
		{"xcode sdkroot build setting", "    SDKROOT = iphonesimulator26.2", false},
		{"export command", "export DEVELOPER_DIR=/Applications/Xcode.app", false},
		{"note targets line", "note: Run script build phase", false},

		// Indented versions of valid lines
		{"indented compile", "  Compiling ViewController.m", true},
		{"tab indented build succeeded", "\tBuild Succeeded", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldShowBuildLine(tt.line)
			if got != tt.want {
				t.Errorf("shouldShowBuildLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestScanCRLF(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		tokens []string
	}{
		{
			name:   "unix newlines",
			input:  "line1\nline2\nline3\n",
			tokens: []string{"line1", "line2", "line3"},
		},
		{
			name:   "windows newlines",
			input:  "line1\r\nline2\r\nline3\r\n",
			tokens: []string{"line1", "line2", "line3"},
		},
		{
			name:   "bare carriage returns",
			input:  "progress 10%\rprogress 50%\rprogress 100%\n",
			tokens: []string{"progress 10%", "progress 50%", "progress 100%"},
		},
		{
			name:   "mixed line endings",
			input:  "first\rsecond\r\nthird\n",
			tokens: []string{"first", "second", "third"},
		},
		{
			name:   "trailing content without newline",
			input:  "final line",
			tokens: []string{"final line"},
		},
		{
			name:   "empty input",
			input:  "",
			tokens: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.input)
			var tokens []string
			for len(data) > 0 {
				advance, token, _ := scanCRLF(data, false)
				if advance == 0 {
					// Not enough data; pass remainder as EOF
					_, token, _ = scanCRLF(data, true)
					if token != nil {
						tokens = append(tokens, string(token))
					}
					break
				}
				if token != nil {
					tokens = append(tokens, string(token))
				}
				data = data[advance:]
			}

			if len(tokens) != len(tt.tokens) {
				t.Fatalf("got %d tokens %v, want %d tokens %v", len(tokens), tokens, len(tt.tokens), tt.tokens)
			}
			for i, want := range tt.tokens {
				if tokens[i] != want {
					t.Errorf("token[%d] = %q, want %q", i, tokens[i], want)
				}
			}
		})
	}
}

func TestShortenBuildLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "swift compile with path",
			input: "SwiftCompile normal arm64 /Users/dev/project/Sources/AppDelegate.swift",
			want:  "Compiling AppDelegate.swift",
		},
		{
			name:  "compile c with path",
			input: "CompileC /Users/dev/project/Sources/main.c normal arm64",
			want:  "Compiling main.c",
		},
		{
			name:  "swift emit module with target",
			input: "SwiftEmitModule normal arm64 in target 'MyFramework'",
			want:  "Emitting module MyFramework",
		},
		{
			name:  "swift emit module no target",
			input: "SwiftEmitModule normal arm64",
			want:  "Emitting module",
		},
		{
			name:  "ld with target",
			input: "Ld /path/to/binary normal arm64 in target 'MyApp'",
			want:  "Linking MyApp",
		},
		{
			name:  "codesign",
			input: "CodeSign /path/to/MyApp.app",
			want:  "Signing MyApp.app",
		},
		{
			name:  "passthrough unknown line",
			input: "Some other build output",
			want:  "Some other build output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortenBuildLine(tt.input)
			if got != tt.want {
				t.Errorf("shortenBuildLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilterBuildOutputLine(t *testing.T) {
	line, ok := FilterBuildOutputLine("  SwiftCompile normal arm64 /Users/dev/project/Sources/AppDelegate.swift")
	if !ok {
		t.Fatal("FilterBuildOutputLine() did not display compile line")
	}
	if line != "Compiling AppDelegate.swift" {
		t.Fatalf("FilterBuildOutputLine() = %q, want shortened compile line", line)
	}

	if line, ok := FilterBuildOutputLine("--- xcodebuild: WARNING: Using the first of multiple matching destinations:"); ok {
		t.Fatalf("FilterBuildOutputLine() displayed noisy destination warning: %q", line)
	}
}

func TestScanCRLF_XcodebuildProgress(t *testing.T) {
	input := bytes.Join([][]byte{
		[]byte("Compiling file1.swift"),
		[]byte("Compiling file2.swift"),
		[]byte("Build Succeeded"),
	}, []byte{'\r'})
	input = append(input, '\n')

	data := input
	var tokens []string
	for len(data) > 0 {
		advance, token, _ := scanCRLF(data, false)
		if advance == 0 {
			_, token, _ = scanCRLF(data, true)
			if token != nil {
				tokens = append(tokens, string(token))
			}
			break
		}
		if token != nil {
			tokens = append(tokens, string(token))
		}
		data = data[advance:]
	}

	if len(tokens) != 3 {
		t.Fatalf("got %d tokens, want 3: %v", len(tokens), tokens)
	}
	if tokens[0] != "Compiling file1.swift" {
		t.Errorf("tokens[0] = %q, want %q", tokens[0], "Compiling file1.swift")
	}
	if tokens[2] != "Build Succeeded" {
		t.Errorf("tokens[2] = %q, want %q", tokens[2], "Build Succeeded")
	}
}

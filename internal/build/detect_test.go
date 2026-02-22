package build

import (
	"testing"
)

func TestParseXcodebuildListOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "single scheme",
			input: `Information about project "MyApp":
    Targets:
        MyApp

    Build Configurations:
        Debug
        Release

    Schemes:
        MyApp
`,
			expected: []string{"MyApp"},
		},
		{
			name: "multiple schemes",
			input: `Information about project "MyApp":
    Targets:
        MyApp
        MyAppTests

    Build Configurations:
        Debug
        Release

    Schemes:
        MyApp
        MyApp-Staging
        MyAppTests
`,
			expected: []string{"MyApp", "MyApp-Staging", "MyAppTests"},
		},
		{
			name:     "no schemes section",
			input:    `Information about project "MyApp":`,
			expected: nil,
		},
		{
			name:     "empty output",
			input:    "",
			expected: nil,
		},
		{
			name: "schemes followed by another section",
			input: `Information about workspace "MyApp":
    Schemes:
        MyApp
        MyAppUITests

    Build Configurations:
        Debug
`,
			expected: []string{"MyApp", "MyAppUITests"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseXcodebuildListOutput(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d schemes, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, scheme := range result {
				if scheme != tt.expected[i] {
					t.Errorf("scheme[%d]: expected %q, got %q", i, tt.expected[i], scheme)
				}
			}
		})
	}
}

func TestApplySchemeToCommand(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		scheme   string
		expected string
	}{
		{
			name:     "normal scheme name",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "MyApp",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme 'MyApp' -configuration Debug",
		},
		{
			name:     "scheme with spaces gets quoted",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "My App",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme 'My App' -configuration Debug",
		},
		{
			name:     "scheme with single quote gets escaped",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "My'App",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme 'My'\\''App' -configuration Debug",
		},
		{
			name:     "empty scheme is no-op",
			command:  "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
			scheme:   "",
			expected: "xcodebuild -workspace MyApp.xcworkspace -scheme * -configuration Debug",
		},
		{
			name:     "command without -scheme * is no-op",
			command:  "./gradlew assembleDebug",
			scheme:   "MyApp",
			expected: "./gradlew assembleDebug",
		},
		{
			name:     "react native ios command",
			command:  "cd ios && xcodebuild -workspace *.xcworkspace -scheme * -configuration Debug -sdk iphonesimulator -derivedDataPath build",
			scheme:   "MyRNApp",
			expected: "cd ios && xcodebuild -workspace *.xcworkspace -scheme 'MyRNApp' -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplySchemeToCommand(tt.command, tt.scheme)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

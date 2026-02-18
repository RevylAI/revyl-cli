package main

import (
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestSelectExactNameApp(t *testing.T) {
	tests := []struct {
		name      string
		apps      []api.App
		target    string
		wantID    string
		wantError string
	}{
		{
			name: "single exact match",
			apps: []api.App{
				{ID: "a1", Name: "My App", Platform: "android"},
				{ID: "a2", Name: "Other", Platform: "ios"},
			},
			target: "My App",
			wantID: "a1",
		},
		{
			name: "not found",
			apps: []api.App{
				{ID: "a1", Name: "My App", Platform: "android"},
			},
			target:    "Unknown",
			wantError: `app "Unknown" not found`,
		},
		{
			name: "duplicate names require disambiguation",
			apps: []api.App{
				{ID: "id-2", Name: "Shared", Platform: "ios"},
				{ID: "id-1", Name: "Shared", Platform: "android"},
			},
			target:    "Shared",
			wantError: `multiple apps named "Shared" found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectExactNameApp(tt.apps, tt.target)
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantError)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID != tt.wantID {
				t.Fatalf("app ID = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

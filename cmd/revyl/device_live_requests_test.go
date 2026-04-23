package main

import "testing"

func TestValidateLiveNetworkPlatform(t *testing.T) {
	t.Parallel()

	if err := validateLiveNetworkPlatform("ios"); err != nil {
		t.Fatalf("validateLiveNetworkPlatform(ios) error = %v, want nil", err)
	}

	err := validateLiveNetworkPlatform("android")
	if err == nil {
		t.Fatal("validateLiveNetworkPlatform(android) error = nil, want non-nil")
	}
	if got := err.Error(); got != "live network requests are currently supported only for iOS sessions (got android)" {
		t.Fatalf("validateLiveNetworkPlatform(android) = %q", got)
	}
}

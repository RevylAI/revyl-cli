package main

import (
	"errors"
	"testing"
)

func TestClassifyEASAuthPreflight_Authenticated(t *testing.T) {
	status := classifyEASAuthPreflight("revyl-admin", nil)
	if status != easAuthPreflightAuthenticated {
		t.Fatalf("classifyEASAuthPreflight() = %q, want %q", status, easAuthPreflightAuthenticated)
	}
}

func TestClassifyEASAuthPreflight_NeedsLogin(t *testing.T) {
	status := classifyEASAuthPreflight("Not logged in", errors.New("exit status 1"))
	if status != easAuthPreflightNeedsLogin {
		t.Fatalf("classifyEASAuthPreflight() = %q, want %q", status, easAuthPreflightNeedsLogin)
	}
}

func TestClassifyEASAuthPreflight_ToolingIssue(t *testing.T) {
	status := classifyEASAuthPreflight("npm error could not determine executable to run", errors.New("exit status 1"))
	if status != easAuthPreflightTooling {
		t.Fatalf("classifyEASAuthPreflight() = %q, want %q", status, easAuthPreflightTooling)
	}
}

func TestClassifyEASAuthPreflight_Transient(t *testing.T) {
	status := classifyEASAuthPreflight("network timeout", errors.New("exit status 1"))
	if status != easAuthPreflightTransient {
		t.Fatalf("classifyEASAuthPreflight() = %q, want %q", status, easAuthPreflightTransient)
	}
}

func TestClassifyEASAuthPreflight_ToolingFromExecError(t *testing.T) {
	status := classifyEASAuthPreflight("", errors.New("exec: \"npx\": executable file not found in $PATH"))
	if status != easAuthPreflightTooling {
		t.Fatalf("classifyEASAuthPreflight() = %q, want %q", status, easAuthPreflightTooling)
	}
}

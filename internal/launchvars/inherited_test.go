package launchvars

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMergeInherited(t *testing.T) {
	t.Setenv(
		InheritedIDsEnv,
		"0d60e7e3-6548-476b-91c6-c48b6d620d0e, 37159693-B91E-4E99-A0CB-E8A812387986",
	)

	got, err := MergeInherited([]string{"EXPLICIT_KEY"}, false)
	if err != nil {
		t.Fatalf("MergeInherited() error = %v", err)
	}
	want := []string{
		"0d60e7e3-6548-476b-91c6-c48b6d620d0e",
		"37159693-b91e-4e99-a0cb-e8a812387986",
		"EXPLICIT_KEY",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeInherited() = %v, want %v", got, want)
	}
}

func TestMergeInheritedRejectsInvalidInput(t *testing.T) {
	tests := map[string]string{
		"invalid ID": "not-a-uuid",
		"duplicate ID": strings.Join([]string{
			"0d60e7e3-6548-476b-91c6-c48b6d620d0e",
			"0d60e7e3-6548-476b-91c6-c48b6d620d0e",
		}, ","),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(InheritedIDsEnv, value)
			_, err := MergeInherited(nil, false)
			if err == nil || !strings.Contains(err.Error(), InheritedIDsEnv) {
				t.Fatalf("MergeInherited() error = %v", err)
			}
		})
	}
}

func TestMergeInheritedLeavesExplicitValuesAloneWithoutEnvironment(t *testing.T) {
	t.Setenv(InheritedIDsEnv, "")
	explicit := []string{"KEY"}
	got, err := MergeInherited(explicit, false)
	if err != nil {
		t.Fatalf("MergeInherited() error = %v", err)
	}
	if !reflect.DeepEqual(got, explicit) {
		t.Fatalf("MergeInherited() = %v, want %v", got, explicit)
	}
	got[0] = "CHANGED"
	if explicit[0] != "KEY" {
		t.Fatal("MergeInherited() returned the caller's backing array")
	}
}

func TestMergeInheritedRejectsMoreThanFifty(t *testing.T) {
	ids := make([]string, 51)
	for index := range ids {
		ids[index] = fmt.Sprintf("00000000-0000-0000-0000-%012d", index)
	}
	t.Setenv(InheritedIDsEnv, strings.Join(ids, ","))
	if _, err := MergeInherited(nil, false); err == nil {
		t.Fatal("MergeInherited() error = nil, want limit error")
	}
}

func TestMergeInheritedCanBeDisabledPerRun(t *testing.T) {
	t.Setenv(InheritedIDsEnv, "not-a-uuid")
	explicit := []string{"ALTERNATE_AUTH"}

	got, err := MergeInherited(explicit, true)
	if err != nil {
		t.Fatalf("MergeInherited() error = %v", err)
	}
	if !reflect.DeepEqual(got, explicit) {
		t.Fatalf("MergeInherited() = %v, want %v", got, explicit)
	}
	if HasInherited(true) {
		t.Fatal("HasInherited(true) = true, want false")
	}
}

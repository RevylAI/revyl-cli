//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/revyl/cli/internal/privatefs"
)

func windowsFileDACL(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount == 0 {
		t.Fatalf("expected access rules on %s: DACL=%v error=%v", path, dacl, err)
	}
	sddl := descriptor.String()
	if sddl == "" {
		t.Fatalf("could not serialize DACL for %s", path)
	}
	return sddl
}

func TestPrivateRuntimeFilesPreserveWindowsACLInheritance(t *testing.T) {
	dir := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("create private directory DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	directoryDACL := windowsFileDACL(t, dir)
	root, err := privatefs.CreateDirectory(filepath.Dir(dir), filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if got := windowsFileDACL(t, dir); got != directoryDACL {
		t.Fatalf("directory DACL changed: %s", got)
	}
	referencePath := filepath.Join(dir, "reference.txt")
	if err := os.WriteFile(referencePath, []byte("reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	inheritedDACL := windowsFileDACL(t, referencePath)
	for _, writer := range []struct {
		name  string
		write func(string, []byte) error
	}{
		{"artifact", writePrivateRuntimeFile},
		{"status", func(path string, data []byte) error { return writeDevStatusFile(testRuntimePath(path), data) }},
	} {
		t.Run(writer.name, func(t *testing.T) {
			path := filepath.Join(dir, writer.name+".json")
			for _, contents := range []string{"legacy contents", "new"} {
				if err := writer.write(path, []byte(contents)); err != nil {
					t.Fatal(err)
				}
				if got := windowsFileDACL(t, path); got != inheritedDACL {
					t.Fatalf("file DACL = %s, want inherited %s", got, inheritedDACL)
				}
				data, err := readPrivateRuntimeFile(path)
				if err != nil || string(data) != contents {
					t.Fatalf("read after write: contents=%q error=%v", data, err)
				}
			}
		})
	}
}

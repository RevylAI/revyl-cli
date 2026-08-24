//go:build windows

package build

import (
	"reflect"
	"syscall"
	"testing"
)

func TestNewShellCommandUsesWindowsCommandProcessor(t *testing.T) {
	command := newShellCommand(`"C:\Program Files\Revyl\helper.exe" --mode output`)
	configureCommandProcessGroup(command)
	if want := []string{"cmd.exe"}; !reflect.DeepEqual(command.Args, want) {
		t.Fatalf("shell command arguments = %q, want %q", command.Args, want)
	}
	if command.SysProcAttr == nil {
		t.Fatal("shell command process attributes are nil")
	}
	if want := `cmd.exe /D /S /C ""C:\Program Files\Revyl\helper.exe" --mode output"`; command.SysProcAttr.CmdLine != want {
		t.Fatalf("shell command line = %q, want %q", command.SysProcAttr.CmdLine, want)
	}
	if command.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("shell command process-group flag was not configured")
	}
}

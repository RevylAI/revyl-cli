package buildselection

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	branchDetectionTimeout = 5 * time.Second
	gitOutputLimit         = 64 * 1024
)

type boundedGitOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedGitOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := gitOutputLimit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func runContextGit(ctx context.Context, dir string, args ...string) (string, bool, bool) {
	if dir != "" {
		args = append([]string{"-C", filepath.Clean(dir)}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_OPTIONAL_LOCKS", "GIT_SSH", "GIT_SSH_COMMAND":
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_OPTIONAL_LOCKS=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes -oConnectTimeout=3 -oConnectionAttempts=1")
	cmd.WaitDelay = 200 * time.Millisecond
	var output boundedGitOutput
	cmd.Stdout = &output
	err := cmd.Run()
	return output.String(), err == nil, output.overflow
}

package main

import (
	"runtime"
	"strings"
)

func cliRecoveryCommand(arguments ...string) string {
	selectedRoot, _ := rootCmd.PersistentFlags().GetString("chdir")
	return cliRecoveryCommandInDirectory(selectedRoot, arguments...)
}

func cliRecoveryCommandInDirectory(directory string, arguments ...string) string {
	parts := []string{"revyl"}
	if strings.TrimSpace(directory) != "" {
		parts = append(parts, "-C", quoteCLIRecoveryArgument(directory))
	}
	parts = append(parts, arguments...)
	return strings.Join(parts, " ")
}

func gitRecoveryCommand(worktreeRoot string, arguments ...string) string {
	parts := []string{"git"}
	if strings.TrimSpace(worktreeRoot) != "" {
		parts = append(parts, "-C", quoteCLIRecoveryArgument(worktreeRoot))
	}
	parts = append(parts, arguments...)
	return strings.Join(parts, " ")
}

func quoteCLIRecoveryArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(character rune) bool {
		return !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("_@%+=:,./-", character))
	}) == -1 {
		return value
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

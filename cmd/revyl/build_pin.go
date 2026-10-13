package main

import (
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/ui"
)

const (
	buildPinFlagName          = "pin"
	buildSetCurrentFlagName   = "set-current"
	buildNoSetCurrentFlagName = "no-set-current"
)

// registerBuildPinFlags adds --pin and the deprecated --set-current and
// --no-set-current flags. The deprecated flags stay registered so old scripts
// keep working; they are hidden from help.
func registerBuildPinFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&buildPinFlag, buildPinFlagName, false, "Pin the branch to the new build")
	cmd.Flags().BoolVar(&buildSetCurr, buildSetCurrentFlagName, false, "Deprecated: use --pin")
	cmd.Flags().BoolVar(&buildNoSetCurrent, buildNoSetCurrentFlagName, false, "Deprecated: uploads don't pin their branch unless you pass --pin")
	_ = cmd.Flags().MarkHidden(buildSetCurrentFlagName)
	_ = cmd.Flags().MarkHidden(buildNoSetCurrentFlagName)
	for _, name := range []string{buildPinFlagName, buildSetCurrentFlagName, buildNoSetCurrentFlagName} {
		analytics.MarkFlagValue(cmd, name)
	}
}

// buildPinStatement is one explicitly passed flag's request for the branch's pin.
type buildPinStatement struct {
	spelling string
	pin      bool
}

// resolveBuildPin reports whether a build or upload pins its branch to the new
// build. Only --pin, or the deprecated --set-current, pins it; the deprecated
// --set-current=false and --no-set-current do nothing. Flags asking for
// opposite outcomes are refused. Warnings go to stderr even in --json mode.
func resolveBuildPin(cmd *cobra.Command) (bool, error) {
	flags := cmd.Flags()
	var statements []buildPinStatement
	var deprecated []buildPinStatement
	if flags.Changed(buildPinFlagName) {
		value, _ := flags.GetBool(buildPinFlagName)
		statements = append(statements, buildPinStatement{boolFlagSpelling(buildPinFlagName, value), value})
	}
	if flags.Changed(buildSetCurrentFlagName) {
		value, _ := flags.GetBool(buildSetCurrentFlagName)
		statement := buildPinStatement{boolFlagSpelling(buildSetCurrentFlagName, value), value}
		statements = append(statements, statement)
		deprecated = append(deprecated, statement)
	}
	if flags.Changed(buildNoSetCurrentFlagName) {
		value, _ := flags.GetBool(buildNoSetCurrentFlagName)
		statement := buildPinStatement{boolFlagSpelling(buildNoSetCurrentFlagName, value), false}
		if value {
			statements = append(statements, statement)
		}
		deprecated = append(deprecated, statement)
	}
	for i := 1; i < len(statements); i++ {
		if statements[i].pin != statements[0].pin {
			return false, usageErrorf("%s and %s conflict; use only --pin to pin the branch to the new build, or drop both so the upload doesn't pin the branch", statements[0].spelling, statements[i].spelling)
		}
	}
	for _, statement := range deprecated {
		ui.PrintWarning("%s", buildPinDeprecationWarning(statement))
	}
	return len(statements) > 0 && statements[0].pin, nil
}

func buildPinDeprecationWarning(statement buildPinStatement) string {
	if statement.pin {
		return statement.spelling + " is deprecated and will be removed; use --pin to pin the branch to the new build"
	}
	return statement.spelling + " is deprecated and does nothing; uploads don't pin their branch unless you pass --pin"
}

func boolFlagSpelling(name string, value bool) string {
	if value {
		return "--" + name
	}
	return "--" + name + "=false"
}

package main

import (
	"fmt"

	"github.com/spf13/pflag"
)

// renamedStringFlagName is the pflag value behind one name of a renamed string
// flag. Both names write one variable, and a value that differs from what the
// other name already set is refused.
type renamedStringFlagName struct {
	flags     *pflag.FlagSet
	target    *string
	newName   string
	oldName   string
	otherName string
}

func (n *renamedStringFlagName) Set(value string) error {
	if other := n.flags.Lookup(n.otherName); other != nil && other.Changed && *n.target != value {
		return fmt.Errorf("--%s and --%s differ; pass only --%s", n.oldName, n.newName, n.newName)
	}
	*n.target = value
	return nil
}

func (n *renamedStringFlagName) String() string {
	if n == nil || n.target == nil {
		return ""
	}
	return *n.target
}

func (n *renamedStringFlagName) Type() string {
	return "string"
}

// registerRenamedStringFlag registers newName and keeps oldName working as a
// hidden alias that prints a one-line rename notice on stderr when used.
func registerRenamedStringFlag(flags *pflag.FlagSet, target *string, newName, oldName, usage string) {
	flags.Var(&renamedStringFlagName{flags: flags, target: target, newName: newName, oldName: oldName, otherName: oldName}, newName, usage)
	flags.Var(&renamedStringFlagName{flags: flags, target: target, newName: newName, oldName: oldName, otherName: newName}, oldName, usage)
	_ = flags.MarkDeprecated(oldName, fmt.Sprintf("use --%s instead", newName))
}

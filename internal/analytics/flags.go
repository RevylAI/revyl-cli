package analytics

import (
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	flagAnnotationSafeValue = "revyl.analytics.safe_value"
)

// Unmarked flags report presence only.
func MarkFlagValue(cmd *cobra.Command, name string) {
	if cmd == nil {
		return
	}
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		flag = cmd.PersistentFlags().Lookup(name)
	}
	if flag == nil {
		return
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[flagAnnotationSafeValue] = []string{"true"}
}

func (r *Recorder) commandProps(cmd *cobra.Command, args []string, commandID string) map[string]interface{} {
	props := map[string]interface{}{
		"command":          cmd.CommandPath(),
		"command_use":      cmd.Use,
		"command_id":       commandID,
		"positional_count": len(args),
	}
	if len(args) > 0 {
		props["positional_args_present"] = true
	}
	flagNames, flagValues := changedFlags(cmd)
	if len(flagNames) > 0 {
		props["flag_names"] = flagNames
	}
	if len(flagValues) > 0 {
		props["flag_values"] = flagValues
	}
	return props
}

func changedFlags(cmd *cobra.Command) ([]string, map[string]interface{}) {
	names := map[string]struct{}{}
	values := map[string]interface{}{}
	visit := func(flag *pflag.Flag) {
		if flag != nil && flag.Changed {
			names[flag.Name] = struct{}{}
			if capturesFlagValue(flag) {
				values[flag.Name] = flag.Value.String()
			}
		}
	}

	cmd.Flags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, values
}

func capturesFlagValue(flag *pflag.Flag) bool {
	return flag != nil && len(flag.Annotations[flagAnnotationSafeValue]) > 0
}

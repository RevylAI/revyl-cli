package analytics

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const flagAnnotationSafeValue = "revyl.analytics.safe_value"

// Deprecated compatibility hook. Analytics now reports flag names only, but
// command construction still calls this while downstream integrations migrate.
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
	flagNames := changedFlagNames(cmd)
	if len(flagNames) > 0 {
		props["flag_names"] = flagNames
	}
	return props
}

func changedFlagNames(cmd *cobra.Command) []string {
	names := map[string]struct{}{}
	visit := func(flag *pflag.Flag) {
		if flag != nil && flag.Changed {
			names[flag.Name] = struct{}{}
		}
	}

	cmd.Flags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func commandDiagnosticRedactions(cmd *cobra.Command, args []string) []string {
	values := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			values[value] = struct{}{}
		}
	}
	for _, arg := range args {
		add(arg)
	}

	visit := func(flag *pflag.Flag) {
		if flag == nil || !flag.Changed || flag.Value == nil || flag.Value.Type() == "bool" {
			return
		}
		if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
			for _, value := range sliceValue.GetSlice() {
				add(value)
			}
			return
		}
		add(flag.Value.String())
	}
	cmd.Flags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)

	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) == len(out[j]) {
			return out[i] < out[j]
		}
		return len(out[i]) > len(out[j])
	})
	return out
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
)

// appliedDefault records one value the CLI chose because the caller left it
// unset. JSON outputs list these under an additive "defaults_applied" key;
// Reason is a stable snake_case code agents can branch on.
type appliedDefault struct {
	Flag   string `json:"flag"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// announceAppliedDefault prints the one-line stderr notice for a default the
// CLI applied instead of asking, and marks the invocation's selection as
// inferred for analytics. The notice ignores --quiet because the caller never
// saw the choice being made.
func announceAppliedDefault(cmd *cobra.Command, choice, why, alternative string) {
	fmt.Fprintf(cmd.ErrOrStderr(), "Using %s (%s). %s\n", choice, why, alternative)
	analytics.SetCommandCompletion(cmd.Context(), analytics.CommandCompletion{
		Properties: map[string]interface{}{"selection_source": "inferred"},
	})
}

func appliedDefaultList(applied *appliedDefault) []appliedDefault {
	if applied == nil {
		return nil
	}
	return []appliedDefault{*applied}
}

type appliedDefaultsKey struct{}

// recordAppliedDefault remembers a default this invocation applied so
// withAppliedDefaults can report it. The caller prints the matching stderr
// notice.
func recordAppliedDefault(cmd *cobra.Command, applied appliedDefault) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if defaults, ok := ctx.Value(appliedDefaultsKey{}).(*[]appliedDefault); ok {
		*defaults = append(*defaults, applied)
		return
	}
	cmd.SetContext(context.WithValue(ctx, appliedDefaultsKey{}, &[]appliedDefault{applied}))
}

// withAppliedDefaults adds defaults_applied to a JSON object payload when this
// invocation recorded a default, leaving every existing key unchanged. Payloads
// that are not JSON objects, and invocations without defaults, pass through.
func withAppliedDefaults(cmd *cobra.Command, payload interface{}) interface{} {
	ctx := cmd.Context()
	if ctx == nil {
		return payload
	}
	defaults, _ := ctx.Value(appliedDefaultsKey{}).(*[]appliedDefault)
	if defaults == nil || len(*defaults) == 0 {
		return payload
	}
	encoded, err := marshalWithoutHTMLEscape(payload)
	if err != nil {
		return payload
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(encoded, &object) != nil || object == nil {
		return payload
	}
	appliedJSON, err := marshalWithoutHTMLEscape(*defaults)
	if err != nil {
		return payload
	}
	object["defaults_applied"] = appliedJSON
	return object
}

// marshalWithoutHTMLEscape encodes compact JSON that keeps &, < and > literal,
// so an output printed without HTML escaping stays that way once it carries
// defaults_applied.
func marshalWithoutHTMLEscape(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

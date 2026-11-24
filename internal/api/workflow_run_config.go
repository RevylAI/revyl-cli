package api

import "encoding/json"

// WorkflowRunConfigPatch preserves omitted versus null concurrency and unknown
// saved run-config keys, which the generated optional pointers cannot represent.
type WorkflowRunConfigPatch struct {
	RunConfig               map[string]interface{}
	OverrideRunConfig       bool
	DefaultConcurrencyLimit **int
}

func (p WorkflowRunConfigPatch) MarshalJSON() ([]byte, error) {
	var runConfig *map[string]interface{}
	var override *bool
	if p.RunConfig != nil {
		runConfig = &p.RunConfig
		override = &p.OverrideRunConfig
	}
	return json.Marshal(struct {
		RunConfig               *map[string]interface{} `json:"run_config,omitempty"`
		OverrideRunConfig       *bool                   `json:"override_run_config,omitempty"`
		DefaultConcurrencyLimit **int                   `json:"default_concurrency_limit,omitempty"`
	}{runConfig, override, p.DefaultConcurrencyLimit})
}

func (p *WorkflowRunConfigPatch) SetConcurrencyLimit(limit *int) {
	p.DefaultConcurrencyLimit = &limit
}

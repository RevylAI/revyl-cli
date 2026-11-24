package api

import "encoding/json"

// WorkflowRunConfigPatch carries a full-map RunConfig/OverrideRunConfig pair alongside a
// double-pointer DefaultConcurrencyLimit so omitted and explicit null concurrency stay distinct.
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

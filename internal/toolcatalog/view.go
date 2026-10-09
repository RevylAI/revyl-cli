package toolcatalog

import (
	"fmt"
	"strings"
)

type Query struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

type View struct {
	Version  int    `json:"version"`
	Renderer string `json:"renderer"`
	Query    Query  `json:"query"`
}

var viewRenderers = map[string]string{
	"sessions.list":        "execution_table",
	"sessions.report":      "report",
	"sessions.logs":        "device_logs",
	"sessions.network":     "network_requests",
	"sessions.performance": "performance",
	"atlas.search":         "atlas_graph",
	"atlas.neighbors":      "atlas_graph",
	"atlas.graph":          "atlas_graph",
	"atlas.area":           "atlas_graph",
	"atlas.from_reports":   "atlas_report_projection",
	"atlas.compare_builds": "atlas_build_comparison",
	"reports.get":          "report",
	"reports.logs":         "device_logs",
	"reports.network":      "network_requests",
	"reports.performance":  "performance",
	"builds.runs":          "execution_table",
}

func (d Definition) View(input map[string]any) (View, error) {
	renderer, ok := viewRenderers[d.Name]
	if !d.ReadOnly || !ok {
		return View{}, fmt.Errorf("this tool has no native view contract")
	}
	if _, err := d.Arguments(input); err != nil {
		return View{}, err
	}
	arguments := map[string]any{}
	for key, parameter := range d.InputSchema.Properties {
		if parameter.Default != nil {
			arguments[key] = parameter.Default
		}
	}
	for key, value := range input {
		arguments[key] = value
	}
	if _, relative := arguments["since"].(string); relative && strings.HasPrefix(d.Name, "atlas.") {
		return View{}, fmt.Errorf("replayable views require absolute from/to timestamps instead of since")
	}
	if build, ok := arguments["build"].(string); ok && build != "all" {
		if err := validate(Parameter{Type: "string", Format: "uuid", MaxLength: 36}, build); err != nil {
			return View{}, fmt.Errorf("replayable views require an exact build UUID or all")
		}
	}
	return View{Version: 1, Renderer: renderer, Query: Query{Tool: d.Name, Arguments: arguments}}, nil
}

func (d Definition) ValidateView(view View) error {
	if view.Version != 1 || view.Query.Tool != d.Name {
		return fmt.Errorf("unsupported view version or query")
	}
	expected, err := d.View(view.Query.Arguments)
	if err != nil {
		return err
	}
	if expected.Renderer != view.Renderer {
		return fmt.Errorf("renderer does not match the query's native view contract")
	}
	return nil
}

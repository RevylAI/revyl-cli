package main

import "github.com/revyl/cli/internal/toolcatalog"

func investigationToolSpecs() []toolcatalog.Spec {
	scope := []string{"app", "build", "from", "to", "since", "report-id", "test-id", "source-kind", "device-model", "runtime", "include-variants", "limit", "screenshots"}
	specs := []toolcatalog.Spec{
		{Name: "explorations.launch", Command: "explore run", Description: "Launch an exploration after the user authorizes its app/build, objective and resource scope. Returns immediately with the run identity; use explorations.status to check progress. A timeout is not proof the launch failed: inspect existing runs before retrying.", Positionals: []string{"app_id"}, Flags: []string{"build-id", "platform", "explorers", "strategy", "instructions", "device-model", "os-version", "max-duration"}, Required: []string{"instructions"}, Defaults: map[string]any{"explorers": 1, "max_duration": "10m"}, FixedArguments: []string{"--no-wait"}, Write: true},
		{Name: "explorations.status", Command: "explore status", Description: "Read exploration progress and its report by run ID. A queued or running exploration is not completed evidence.", Positionals: []string{"run_id"}},
		{Name: "tests.run", Command: "test run", Description: "Launch an existing test after the user authorizes the test and build. Returns immediately with the execution identity. Read test history and reports for outcomes; do not blindly retry an uncertain launch.", Positionals: []string{"test_id"}, Flags: []string{"build-id"}, FixedArguments: []string{"--no-wait"}, Write: true},
		{Name: "sessions.list", Command: "device history", Description: "Discover app sessions including Explore and tests, newest first. Filter app/build/source/status/from/to before pagination. Defaults to all statuses, including active sessions; use status running for only active sessions. Follow offset until offset+len(sessions) reaches total. Empty build test executions do not mean there are no Explore reports.", Flags: []string{"app", "build", "source", "status", "platform", "from", "to", "limit", "offset"}, Defaults: map[string]any{"limit": 20, "offset": 0, "status": "all"}},
		{Name: "sessions.report", Command: "device report", Description: "Read a session-backed report, including Explore steps, report identity and capture availability. Use session_id from sessions.list or Atlas provenance; no test execution is required.", Flags: []string{"session-id", "no-steps", "no-actions"}, Required: []string{"session_id"}, Defaults: map[string]any{"no_actions": true}},
		{Name: "atlas.from_reports", Command: "atlas from-reports", Description: "Work upwards from 1-5 exact report IDs into Atlas. Preserves per-report membership, truncation and unmapped reports. Does not localize a failure to every screen in its run.", Flags: []string{"app", "reports", "limit"}, Required: []string{"app", "reports"}, Defaults: map[string]any{"limit": 50}},
		{Name: "atlas.compare_builds", Command: "atlas compare", Description: "Compare observed screen and transition identity sets across two builds. Missing evidence is not a removed feature; this is not a pixel comparison.", Flags: []string{"app", "base-build", "head-build", "limit"}, Required: []string{"app", "base_build", "head_build"}, Defaults: map[string]any{"limit": 100}},
		{Name: "builds.runs", Command: "build runs", Description: "Page through executions exactly attributed to a build. Filter returned statuses locally and follow has_next; an empty failure subset on one page does not mean there are no failures.", Positionals: []string{"build_id"}, Flags: []string{"page", "limit"}, Defaults: map[string]any{"page": 1, "limit": 20}},
		{Name: "apps.list", Command: "atlas apps", Description: "Find apps by name or platform; use returned app UUIDs in subsequent calls.", Flags: []string{"search", "platform", "all", "offset", "limit"}, Defaults: map[string]any{"all": true, "limit": 25, "offset": 0}},
		{Name: "builds.list", Command: "build list", Description: "Read builds for one app. Inspect recorded version and provenance before comparing.", Flags: []string{"app", "branch", "page", "limit"}, Defaults: map[string]any{"page": 1, "limit": 20}, Required: []string{"app"}},
		{Name: "builds.status", Command: "build status", Description: "Inspect one cloud build job and its current outcome.", Positionals: []string{"build_job_id"}},
		{Name: "tests.list", Command: "test query", Description: "Find tests by app, name/ID and platform before pagination. An optional tag-name filter applies only to the fetched page; preserve unread rows. This is not execution-level failure search.", Flags: []string{"limit", "offset", "app", "search", "platform", "tag"}, Defaults: map[string]any{"limit": 25}},
		{Name: "tests.history", Command: "test history", Description: "Read recent executions of one test. Select an exact failed or successful execution before reading diagnostics.", Positionals: []string{"test_id"}, Flags: []string{"limit", "offset"}, Defaults: map[string]any{"limit": 10}},
		{Name: "tests.versions", Command: "test versions", Description: "Read saved definition versions of one test.", Positionals: []string{"test_id"}},
		{Name: "tests.get", Command: "test inspect", Description: "Read a test's current instructions, validations, saved device targets and run configuration. This is the current definition, not necessarily the version or resolved overrides used by a past execution.", Positionals: []string{"test_id"}},
		{Name: "tests.configuration", Command: "test config show", Description: "Read a test's current saved run configuration, such as fail-fast, location and orientation. An empty configuration means no saved overrides, not that the run's resolved defaults or launch arguments are known.", Positionals: []string{"test_id"}},
		{Name: "reports.get", Command: "test report", Description: "Read a report's steps, actions, capture availability, provenance and report ID. Input is an execution UUID, not a report UUID.", Positionals: []string{"execution_id"}, Flags: []string{"no-steps"}},
		{Name: "reports.summary", Command: "run summary", Description: "Orient on one execution's result, steps, and available evidence.", Positionals: []string{"execution_id"}},
		{Name: "reports.logs", Command: "run logs", Description: "Read actual device logs from one selected execution. Filter by regex or level; returns at most 100 lines. Inspect truncation before concluding no issue.", Positionals: []string{"execution_id"}, Flags: []string{"grep", "level", "tail"}, Defaults: map[string]any{"tail": 80}},
		{Name: "reports.network", Command: "run network", Description: "Inspect captured network requests for one selected execution. Narrow by host, failure, status or run-relative seconds. Use min_duration_ms and slowest_first for latency. Pages default to 50 compact requests; follow offset/has_more. Availability depends on capture.", Positionals: []string{"execution_id"}, Flags: []string{"host", "status", "failed", "grep", "since", "until", "limit", "offset", "min-duration-ms", "slowest-first", "compact"}, Defaults: map[string]any{"limit": 50, "compact": true}},
		{Name: "reports.performance", Command: "run perf", Description: "Read CPU and memory summaries for one selected execution. These are run-level metrics, not screen-attributed rankings.", Positionals: []string{"execution_id"}, Flags: []string{"timeline"}, Defaults: map[string]any{"timeline": true}},
		{Name: "reports.state", Command: "run state", Description: "Inspect captured UserDefaults/SQLite paths or a selected path at a report step. Captured values are evidence, not necessarily initial launch configuration.", Positionals: []string{"execution_id"}, Flags: []string{"path", "at-step"}},
		{Name: "annotations.list", Command: "atlas annotations list", Description: "Read grounded findings/comments for an app or exact observation, preserving pagination.", Flags: []string{"app", "observation", "status", "severity", "limit", "cursor"}, Required: []string{"app"}, Defaults: map[string]any{"limit": 25}},
		{Name: "annotations.get", Command: "atlas annotations get", Description: "Read an existing annotation thread and its supporting capture.", Flags: []string{"app"}, Required: []string{"app"}, Positionals: []string{"thread_id"}},
		{Name: "annotations.create", Command: "atlas annotations create", Description: "Create a grounded annotation on an exact observation. Use only when the user requests a write. Supply a stable client_request_id for retry recovery. Inspect the capture first.", Flags: []string{"app", "observation", "target", "body", "severity", "client-request-id"}, Required: []string{"app", "observation", "target", "body", "client_request_id"}, Write: true},
		{Name: "annotations.reply", Command: "atlas annotations reply", Description: "Add a reply to a selected thread when requested. Reuse client_request_id for retry recovery.", Flags: []string{"app", "body", "client-request-id"}, Required: []string{"app", "body", "client_request_id"}, Positionals: []string{"thread_id"}, Write: true},
	}
	for _, diagnostic := range []struct {
		name, command, description string
		flags                      []string
	}{
		{"performance", "perf", "Read CPU and memory samples and summary for an Explore/device session. Run-level measurements cannot rank individual screens.", []string{"timeline"}},
		{"logs", "logs", "Read captured device logs for an Explore/device session. Filter grep/level and inspect truncation.", []string{"grep", "level", "tail"}},
		{"network", "network", "Read captured network requests for an Explore/device session. Filter failed, host, status, min_duration_ms or run-relative seconds. Use slowest_first for latency and offset/has_more to page compact requests.", []string{"host", "status", "failed", "grep", "since", "until", "limit", "offset", "min-duration-ms", "slowest-first", "compact"}},
		{"state", "state", "Inspect captured UserDefaults/SQLite state for an Explore/device session, optionally at a step.", []string{"path", "at-step"}},
	} {
		defaults := map[string]any{}
		if diagnostic.name == "performance" {
			defaults["timeline"] = true
		}
		if diagnostic.name == "network" {
			defaults["limit"] = 50
			defaults["compact"] = true
		}
		if diagnostic.name == "logs" {
			defaults["tail"] = 80
		}
		specs = append(specs, toolcatalog.Spec{Name: "sessions." + diagnostic.name, Command: "run " + diagnostic.command, Description: diagnostic.description, Positionals: []string{"session_id"}, Flags: diagnostic.flags, FixedArguments: []string{"--id-kind=session"}, Defaults: defaults})
	}
	for _, operation := range []struct{ name, description, positional string }{
		{"brief", "Orient on graph anchors and bounded captures. Anchors are suggestions, not a hierarchy.", ""},
		{"graph", "Read a scoped graph. Filter by build, report or test. Edges are observed relationships, not proof of one executed journey.", ""},
		{"search", "Find screens by keywords in names, descriptions, actions and labels within an app/build/report scope. Every term must match; use one short concept per query, not a full question or a list of unrelated concepts.", "query"},
		{"area", "Read a product-area subgraph with boundary edges.", "product_area"},
		{"screen", "Inspect a canonical screen and its representative capture.", "screen_id"},
		{"neighbors", "Traverse incoming and outgoing relationships of a screen.", "screen_id"},
	} {
		spec := toolcatalog.Spec{Name: "atlas." + operation.name, Command: "atlas " + operation.name, Description: operation.description, Flags: append([]string{}, scope...), Required: []string{"app"}, Defaults: map[string]any{"build": "all", "surface_scope": "all", "limit": 25, "include_variants": true}}
		spec.Flags = append(spec.Flags, "workflow-execution-id", "recent-build-limit")
		if operation.positional != "" {
			spec.Positionals = []string{operation.positional}
		}
		if operation.name == "neighbors" {
			spec.Flags = append(spec.Flags, "direction")
		}
		specs = append(specs, spec)
	}
	for _, operation := range []struct{ name, description, positional string }{
		{"observation", "Read an exact capture. Use this rather than the representative for a cited screenshot.", "observation_id"},
		{"observations", "Read grouped captures/variants belonging to one screen.", "screen_id"},
		{"report", "Read the originating report for an exact observation or a screen representative.", "observation_id"},
	} {
		flags := append([]string{}, scope...)
		if operation.name == "observations" {
			flags = append(flags, "workflow-execution-id", "recent-build-limit")
		}
		if operation.name == "report" {
			flags = flags[:len(flags)-1]
		}
		specs = append(specs, toolcatalog.Spec{Name: "atlas." + operation.name, Command: "atlas " + operation.name, Description: operation.description, Flags: flags, Required: []string{"app"}, Positionals: []string{operation.positional}, Defaults: map[string]any{"limit": 25, "include_variants": true}})
	}
	specs = append(specs, toolcatalog.Spec{Name: "atlas.edge", Command: "atlas edge", Description: "Inspect an observed transition and optionally its exact run/video evidence.", Flags: append(append([]string{}, scope...), "runs", "workflow-execution-id", "recent-build-limit"), Required: []string{"app"}, Positionals: []string{"source_id", "target_id"}, Defaults: map[string]any{"limit": 10}})
	return specs
}

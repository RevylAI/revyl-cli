package toolcatalog

type CookbookStep struct {
	Tool        string `json:"tool"`
	Instruction string `json:"instruction"`
}

type Cookbook struct {
	Name       string         `json:"name"`
	Goal       string         `json:"goal"`
	Steps      []CookbookStep `json:"steps"`
	Boundaries []string       `json:"boundaries"`
}

func Cookbooks() []Cookbook {
	return []Cookbook{
		{Name: "existing-findings", Goal: "Read existing annotations and follow their exact captured evidence without writing new findings.", Steps: []CookbookStep{
			{"apps.list", "Resolve the app by name and preserve its UUID."},
			{"annotations.list", "List existing threads for this app. Narrow status or severity when requested; preserve cursor pagination. Screen keyword search does not search annotation bodies."},
			{"annotations.get", "Read selected threads and their source observation identities."},
			{"atlas.observation", "Retrieve the exact observation with screenshots before describing a visual finding; preserve the source link."},
			{"atlas.report", "Follow that observation to its report when the finding needs execution context."},
		}, Boundaries: []string{"Existing annotations are claims, not independently confirmed bugs.", "No matching threads is not proof the app has no issues.", "This workflow is read-only; do not call annotation mutations."}},
		{Name: "explore-diagnostics", Goal: "Find app activity, including Explore sessions without test executions, and trace diagnostics back into Atlas.", Steps: []CookbookStep{
			{"apps.list", "Resolve the app by name; keep its exact app UUID."},
			{"sessions.list", "Filter by app and optionally build/source/time/status before pagination. All statuses are included by default; use status=running for only active sessions. Preserve total and offset."},
			{"sessions.report", "Inspect 1-5 relevant session reports, preserving report_id and session_id even when execution_id is null."},
			{"sessions.performance", "Read CPU/memory for selected sessions. Compare samples and duration; do not claim screen-specific CPU."},
			{"sessions.logs", "Inspect selected session device logs, filtered by level or grep."},
			{"sessions.network", "Inspect captured failed requests and their timing. Missing capture is not a clean network."},
			{"atlas.from_reports", "Show the Atlas screens observed in the selected report IDs. Session report IDs work without executions."},
		}, Boundaries: []string{"An empty builds.runs result does not establish missing session reports.", "Do not read diagnostic artifacts across the entire org. Select a small cohort first.", "Use session IDs for sessions tools; use execution IDs for reports tools."}},
		{Name: "failed-build", Goal: "Find failed executions on a build, locate their Atlas evidence, and inspect diagnostics.", Steps: []CookbookStep{
			{"builds.list", "Resolve an exact build UUID in an app. Follow has_next if needed; branch filters apply to each scanned page."},
			{"builds.runs", "Page through this build's attributed executions. Select failed outcomes, retaining their exact execution_id and test_id."},
			{"reports.get", "Inspect up to five selected executions. Read effective step outcomes, validation results and capture availability; a failed execution can contain successful steps. Preserve the report ID distinct from the execution ID."},
			{"tests.get", "Inspect the source test's current instructions and configuration when useful. Do not assume the current definition or settings equal the version and resolved overrides used by the selected execution."},
			{"atlas.from_reports", "Project these report IDs into the same app. Retain unmapped reports and per-report membership; do not label every screen in a failed run as broken."},
			{"reports.logs", "Inspect each selected execution's device logs, narrowing grep and level. Preserve partial, matched_before_limit and truncated."},
			{"reports.network", "Inspect failed requests or a relevant run-relative time interval if captured. Correlation is not proof of causation."},
			{"reports.performance", "Check run-level CPU/memory summaries when relevant. These cannot rank individual screens."},
		}, Boundaries: []string{"Stop when the claim has supporting evidence or the selected evidence is insufficient.", "Do not crawl diagnostic artifacts across the organization.", "Use tools call --view on the supported read to produce a replayable native view recipe."}},
		{Name: "atlas-drilldown", Goal: "Start from an app or flow question and trace exact observations into originating reports.", Steps: []CookbookStep{
			{"apps.list", "Resolve the app UUID by name and platform, preserving pagination."},
			{"atlas.brief", "Orient on graph anchors; these are suggestions, not a hierarchy."},
			{"atlas.search", "Find candidate screens in the requested build/time scope."},
			{"atlas.observation", "Retrieve the exact observation with screenshots=true and open its image before making visual claims."},
			{"atlas.edge", "Check observed transitions and runs before claiming an executed flow. Inspect decisive video intervals."},
			{"atlas.report", "Retrieve the source report for the exact observation. Preserve report, execution and session references."},
			{"reports.logs", "Inspect device logs only for the selected source execution, if that provenance exists."},
		}, Boundaries: []string{"Generated names and OCR are navigation aids, not visual verification.", "Unobserved behavior is not a missing feature.", "If evidence belongs only to a session, use sessions.report and sessions.logs/network/performance/state with session_id."}},
		{Name: "compare-builds", Goal: "Compare the observed Atlas evidence supported by two builds.", Steps: []CookbookStep{
			{"builds.list", "Resolve two exact build UUIDs belonging to the same app."},
			{"atlas.compare_builds", "Compare screen and transition identity sets, retaining both graph scopes and partial status."},
			{"atlas.observations", "Inspect captures for question-relevant screens within each build scope."},
			{"atlas.observation", "Open exact screenshots from both builds. First verify they represent the same logical screen, then describe any visual drift. Atlas identities can over-group distinct screens."},
		}, Boundaries: []string{"Shared identities are comparison candidates, not verified semantic or pixel matches.", "Observed only in one build does not prove introduction or removal.", "Do not compare run metrics as screen metrics.", "Return the comparison with --view for a native comparison recipe."}},
		{Name: "annotate-evidence", Goal: "Write a user-requested grounded annotation on an inspected capture.", Steps: []CookbookStep{
			{"atlas.observation", "Retrieve and open the exact capture; do not ground against a screen representative substituted for it."},
			{"annotations.create", "Only after a requested write, supply app, observation, visually concrete target, body and a stable client_request_id."},
			{"annotations.get", "Read back the returned thread and verify its observation and body. If the create outcome is uncertain, preserve the idempotency key."},
		}, Boundaries: []string{"A rendered view never writes annotations by itself.", "Do not create annotations just to test access or to fill an empty canvas."}},
	}
}

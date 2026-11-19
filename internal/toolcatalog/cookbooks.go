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
			{"sessions.list", "Filter by app and optionally build/source/time/status, or a Git branch or commit with branch/git_commit_sha, before pagination. All statuses are included by default; use status=running for only active sessions. Preserve total and offset."},
			{"reports.inspect", "Start with overview using reference_id=session UUID and id_kind=session. Select 1-5 reports. Page/filter steps or actions, follow next_offset, and read omitted fields in chunks; preserve report_id and session_id even when execution_id is null."},
			{"sessions.performance", "Read CPU/memory for selected sessions. Compare samples and duration; do not claim screen-specific CPU."},
			{"sessions.logs", "Inspect selected session device logs, filtered by level or grep."},
			{"sessions.network", "Inspect captured failed requests and their timing. Missing capture is not a clean network."},
			{"atlas.from_reports", "Show the Atlas screens observed in the selected report IDs. Session report IDs work without executions."},
		}, Boundaries: []string{"An empty builds.runs result does not establish missing session reports.", "Do not read diagnostic artifacts across the entire org. Select a small cohort first.", "Use session IDs for sessions tools; use execution IDs for reports tools."}},
		{Name: "failed-build", Goal: "Find failed executions on a build, locate their Atlas evidence, and inspect diagnostics.", Steps: []CookbookStep{
			{"builds.list", "Resolve an exact build UUID in an app. Follow has_next if needed; branch filters apply to each scanned page."},
			{"builds.runs", "Page through this build's attributed executions. Select failed outcomes, retaining their exact execution_id and test_id."},
			{"reports.inspect", "Start with overview using reference_id=execution UUID and id_kind=execution for up to five executions. Page/filter steps and actions, then retrieve omitted field chunks. Read effective step outcomes and capture availability; a failed execution can contain successful steps. Preserve the report ID distinct from the execution ID."},
			{"tests.get", "Inspect the source test's current instructions and configuration when useful. Do not assume the current definition or settings equal the version and resolved overrides used by the selected execution."},
			{"atlas.from_reports", "Project these report IDs into the same app. Retain unmapped reports and per-report membership; do not label every screen in a failed run as broken."},
			{"reports.logs", "Inspect each selected execution's device logs, narrowing grep and level. Preserve partial, matched_before_limit and truncated."},
			{"reports.network", "Inspect failed requests or a relevant run-relative time interval if captured. Correlation is not proof of causation."},
			{"reports.performance", "Check run-level CPU/memory summaries when relevant. These cannot rank individual screens."},
		}, Boundaries: []string{"Stop when the claim has supporting evidence or the selected evidence is insufficient.", "Do not crawl diagnostic artifacts across the organization.", "Use tools call --view on the supported read to produce a replayable native view recipe."}},
		{Name: "branch-status", Goal: "Find which tests fail on a Git branch or at an exact commit, and trace each failure to its report and diagnostics.", Steps: []CookbookStep{
			{"tests.status", "Pass branch or git_commit_sha, and since such as 7d for pass rates over a window. Read the counts and each test's newest run, failed first. A queued row has workflow_task_id and no session yet; a quarantined failure has was_quarantined=true."},
			{"tests.history", "Pass a failing test's test_id with the same branch or git_commit_sha to compare its newest failure with earlier passes there. Check git_commit_sha and has_uncommitted_definition_changes on each run before blaming a code change."},
			{"reports.inspect", "Start with overview using reference_id=the run's task_id and id_kind=execution for up to five selected failures. Page/filter steps and actions, then read omitted field chunks."},
			{"reports.logs", "Inspect each selected execution's device logs, narrowing grep and level. Preserve truncation."},
			{"sessions.list", "With the same branch or git_commit_sha, and app to keep one app's sessions, find the branch's other sessions; one without test_execution_id ran no test."},
		}, Boundaries: []string{"A test with no run on the branch or commit isn't listed; absence is not a pass.", "tests.status puts a run on a branch by the Git branch its tests came from, else its build's Revyl branch; tests.history and sessions.list match either branch.", "A test run as saved in Revyl counts only once its session starts, so a queued workflow can hide it.", "One failing run is not proof of a regression; compare runs of the same commit and build."}},
		{Name: "atlas-drilldown", Goal: "Start from an app or flow question and trace exact observations into originating reports.", Steps: []CookbookStep{
			{"apps.list", "Resolve the app UUID by name and platform, preserving pagination."},
			{"atlas.brief", "Orient on graph anchors; these are suggestions, not a hierarchy. Its inline filters list the builds, branches, devices, OS versions and launch keys to scope later reads with; call atlas.filters only for tests, reports, workflow executions or launch values."},
			{"atlas.search", "Find candidate screens in the requested build/time scope."},
			{"atlas.observation", "Retrieve the exact observation with screenshots=true and open its image before making visual claims."},
			{"atlas.edge", "Check observed transitions and runs before claiming an executed flow. Inspect decisive video intervals."},
			{"atlas.report", "Retrieve the source report for the exact observation. Preserve report, execution and session references."},
			{"reports.logs", "Inspect device logs only for the selected source execution, if that provenance exists."},
		}, Boundaries: []string{"Generated names and OCR are navigation aids, not visual verification.", "Unobserved behavior is not a missing feature.", "If evidence belongs only to a session, use sessions.report and sessions.logs/network/performance/state with session_id."}},
		{Name: "compare-builds", Goal: "Compare the observed Atlas evidence of two builds, or of any two filter scopes (device, OS, branch, test, report or launch value), or find screens seen under only one value of a filter, in one or two calls.", Steps: []CookbookStep{
			{"atlas.brief", "Start here unless the filter values are already known; its inline filters list the exact build, branch, device and OS values with run counts. Use atlas.filters with group runs for test or report values, and builds.list only when a build is not listed."},
			{"atlas.diff", "Pass a and b terms (for example a=[\"build=<UUID>\"], b=[\"build=<UUID>\"] or device_model terms) for screens and relationships only in A, only in B and in both. For \"seen on only one device/OS/build\" questions pass partition_by instead; it reads up to 8 values in one call. Do not confirm screens one by one; check partial and values_truncated before claiming absence."},
			{"atlas.observations", "When the question needs visuals, inspect captures for question-relevant screens within each scope."},
			{"atlas.observation", "Open exact screenshots from both scopes. First verify they represent the same logical screen, then describe any visual drift. Atlas identities can over-group distinct screens."},
		}, Boundaries: []string{"Shared identities are comparison candidates, not verified semantic or pixel matches.", "Observed only in one scope does not prove introduction or removal.", "Diff counts are observations, not runs or user traffic.", "Do not compare run metrics as screen metrics.", "atlas.compare_builds returns both full build graphs and supports --view for a native comparison recipe."}},
		{Name: "annotate-evidence", Goal: "Write a user-requested grounded annotation on an inspected capture.", Steps: []CookbookStep{
			{"atlas.observation", "Retrieve and open the exact capture; do not ground against a screen representative substituted for it."},
			{"annotations.create", "Only after a requested write, supply app, observation, visually concrete target, body and a stable client_request_id."},
			{"annotations.get", "Read back the returned thread and verify its observation and body. If the create outcome is uncertain, preserve the idempotency key."},
		}, Boundaries: []string{"A rendered view never writes annotations by itself.", "Do not create annotations just to test access or to fill an empty canvas."}},
	}
}

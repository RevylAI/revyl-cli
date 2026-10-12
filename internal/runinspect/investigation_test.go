package runinspect

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInvestigationPagingAndUnabridgedSearch(t *testing.T) {
	steps := []any{}
	for i := 0; i < 90; i++ {
		steps = append(steps, map[string]any{"id": string(rune('A' + i)), "effective_status": "failed", "step_description": strings.Repeat("x", 1400) + "needle", "type_data": map[string]any{"large": strings.Repeat("z", 100000)}, "actions": []any{}})
	}
	raw, _ := json.Marshal(map[string]any{"id": "report", "steps": steps})
	seen := map[string]bool{}
	offset := 0
	for {
		page, err := InspectReport(raw, InvestigationOptions{Section: "steps", Limit: 100, Offset: offset, Grep: "needle", Status: "failed"})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > InvestigationBudgetBytes {
			t.Fatal("byte budget exceeded")
		}
		if page.Matched != 90 || page.Total != 90 {
			t.Fatalf("wrong coverage: %+v", page)
		}
		for _, item := range page.Items {
			if seen[item.StepID] {
				t.Fatal("duplicate item")
			}
			seen[item.StepID] = true
			if len(item.OmittedFields) != 2 {
				t.Fatalf("missing omitted fields: %+v", item)
			}
		}
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= offset {
			t.Fatal("no progress")
		}
		offset = *page.NextOffset
	}
	if len(seen) != 90 {
		t.Fatalf("lost items: %d", len(seen))
	}
}

func TestInvestigationFieldChunksRoundTrip(t *testing.T) {
	value := strings.Repeat("\"emoji界🙂\n", 3000)
	raw, _ := json.Marshal(map[string]any{"id": "report", "steps": []any{map[string]any{"id": "step", "actions": []any{map[string]any{"id": "action", "type_data": map[string]any{"a/b~c": value}}}}}})
	var combined strings.Builder
	offset := 0
	version := ""
	for {
		page, err := InspectReport(raw, InvestigationOptions{Section: "field", StepID: "step", ActionID: "action", Field: "/type_data/a~1b~0c", FieldVersion: version, Limit: 20, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > InvestigationBudgetBytes {
			t.Fatal("byte budget exceeded")
		}
		combined.WriteString(page.Field.Content)
		version = page.Field.Version
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	var actual string
	if err := json.Unmarshal([]byte(combined.String()), &actual); err != nil {
		t.Fatal(err)
	}
	if actual != value {
		t.Fatal("field text changed")
	}
}

func TestInvestigationIdentityAndFieldScope(t *testing.T) {
	raw := json.RawMessage(`{"id":"report","steps":[{"id":"first","effective_status":"passed","actions":[{"id":"a","reasoning":"first"}]},{"id":"second","effective_status":"failed","actions":[{"id":"b","reasoning":"second"}]}]}`)
	page, err := InspectReport(raw, InvestigationOptions{Section: "actions", Status: "failed", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Matched != 1 || page.Items[0].ActionID != "b" {
		t.Fatalf("wrong selection: %+v", page)
	}
	for _, o := range []InvestigationOptions{
		{Section: "actions", StepID: "absent", Limit: 20},
		{Section: "field", StepID: "first", ActionID: "b", Field: "/reasoning", Limit: 20},
		{Section: "field", StepID: "first", Field: "/missing", Limit: 20},
		{Section: "steps", Grep: "[", Limit: 20},
	} {
		if _, err := InspectReport(raw, o); err == nil {
			t.Fatalf("accepted invalid scope: %+v", o)
		}
	}
}

func TestLogContextAndParserCoverage(t *testing.T) {
	lines, err := ParseLogText([]byte("a\nb\nmatch\nc\nmatch\nd\ne"))
	if err != nil {
		t.Fatal(err)
	}
	context := LogContext(lines, []LogLine{lines[2], lines[4]}, 1)
	if len(context) != 5 || context[0].LineNumber != 2 || context[4].LineNumber != 6 {
		t.Fatalf("wrong context: %+v", context)
	}
	if _, err := ParseLogText([]byte("valid\n" + strings.Repeat("x", 17*1024*1024))); err == nil {
		t.Fatal("silently accepted partial log")
	}
}

func TestInvestigationRootFieldRetainsReportScope(t *testing.T) {
	raw := json.RawMessage(`{"id":"report","tldr":"summary","steps":[{"id":"step"}]}`)
	page, err := InspectReport(raw, InvestigationOptions{Section: "field", Field: "/tldr", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Field.Content != `"summary"` {
		t.Fatalf("read wrong scope: %+v", page.Field)
	}
}

func TestInvestigationFieldRejectsChangedContinuation(t *testing.T) {
	raw := json.RawMessage(`{"id":"report","tldr":"first"}`)
	page, err := InspectReport(raw, InvestigationOptions{Section: "field", Field: "/tldr", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectReport(json.RawMessage(`{"id":"report","tldr":"changed"}`), InvestigationOptions{Section: "field", Field: "/tldr", FieldVersion: page.Field.Version, Offset: 1, Limit: 20}); err != ErrReportFieldChanged {
		t.Fatalf("accepted mixed field versions: %v", err)
	}
	if _, err := InspectReport(raw, InvestigationOptions{Section: "field", Field: "/tldr", Offset: 1, Limit: 20}); err == nil {
		t.Fatal("accepted unchecked continuation")
	}
}

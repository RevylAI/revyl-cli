package runinspect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const InvestigationBudgetBytes = 48000

var ErrReportFieldChanged = errors.New("report field changed between chunks; restart at offset 0 or select a stable nested field instead of a collection containing expiring media URLs")

type InvestigationOptions struct {
	Section, StepID, ActionID, Field, Grep, Status string
	FieldVersion                                   string
	Offset, Limit                                  int
}

type OmittedReportField struct {
	Pointer string `json:"pointer"`
	Bytes   int    `json:"bytes"`
	Preview string `json:"preview,omitempty"`
}

type ReportInvestigationItem struct {
	StepID        string                     `json:"step_id,omitempty"`
	ActionID      string                     `json:"action_id,omitempty"`
	Value         map[string]json.RawMessage `json:"value"`
	OmittedFields []OmittedReportField       `json:"omitted_fields"`
}

type ReportFieldChunk struct {
	Pointer         string `json:"pointer"`
	Version         string `json:"version"`
	Encoding        string `json:"encoding"`
	Content         string `json:"content"`
	TotalCharacters int    `json:"total_characters"`
}

type ReportInvestigation struct {
	SchemaVersion  int                       `json:"schema_version"`
	ReportID       string                    `json:"report_id"`
	SessionID      string                    `json:"session_id,omitempty"`
	ExecutionID    string                    `json:"execution_id,omitempty"`
	UpdatedAt      string                    `json:"updated_at,omitempty"`
	Section        string                    `json:"section"`
	StepID         string                    `json:"step_id,omitempty"`
	ActionID       string                    `json:"action_id,omitempty"`
	Scope          string                    `json:"scope"`
	Filters        map[string]string         `json:"filters"`
	Total          int                       `json:"total"`
	Matched        int                       `json:"matched"`
	Offset         int                       `json:"offset"`
	Returned       int                       `json:"returned"`
	NextOffset     *int                      `json:"next_offset"`
	Truncated      bool                      `json:"truncated"`
	Items          []ReportInvestigationItem `json:"items"`
	Field          *ReportFieldChunk         `json:"field,omitempty"`
	ExcludedFields []string                  `json:"excluded_fields"`
	Guidance       string                    `json:"guidance"`
}

func (o InvestigationOptions) Validate() error {
	switch o.Section {
	case "overview", "steps", "actions", "field":
	default:
		return fmt.Errorf("section must be overview, steps, actions, or field")
	}
	if o.Offset < 0 || o.Limit < 1 || o.Limit > 100 {
		return fmt.Errorf("offset must be nonnegative and limit must be 1..100")
	}
	if o.Section == "overview" && (o.Offset != 0 || o.StepID != "" || o.ActionID != "" || o.Grep != "" || o.Status != "") {
		return fmt.Errorf("overview does not accept filters or offset; use steps, actions, or field")
	}
	if o.ActionID != "" && o.StepID == "" {
		return fmt.Errorf("action-id requires step-id")
	}
	if o.Section == "steps" && o.ActionID != "" {
		return fmt.Errorf("action-id requires section actions or field")
	}
	if o.FieldVersion != "" {
		decoded, err := hex.DecodeString(o.FieldVersion)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("field-version must be the SHA-256 version from a field response")
		}
	}
	if o.Section == "field" {
		if o.Offset > 0 && o.FieldVersion == "" {
			return fmt.Errorf("field continuation requires field-version from the first chunk")
		}
		if o.Field == "" || !strings.HasPrefix(o.Field, "/") {
			return fmt.Errorf("field requires an RFC 6901 JSON pointer, such as /reasoning or /type_data")
		}
		if o.Grep != "" || o.Status != "" {
			return fmt.Errorf("field reads do not accept grep or status")
		}
	} else if o.Field != "" || o.FieldVersion != "" {
		return fmt.Errorf("field requires section field")
	}
	if o.Status != "" && o.Status != "passed" && o.Status != "failed" && o.Status != "warning" && o.Status != "running" && o.Status != "pending" {
		return fmt.Errorf("status must be passed, failed, warning, running, or pending")
	}
	if _, err := regexp.Compile(o.Grep); err != nil {
		return fmt.Errorf("grep must be a valid regular expression")
	}
	return nil
}

// InspectReport projects the API envelope into the separate, bounded CLI investigation contract.
func InspectReport(raw json.RawMessage, o InvestigationOptions) (*ReportInvestigation, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("invalid report JSON")
	}
	page := &ReportInvestigation{
		SchemaVersion: 1,
		ReportID:      reportString(root, "id"),
		SessionID:     reportString(root, "session_id"),
		ExecutionID:   reportString(root, "execution_id"),
		UpdatedAt:     reportString(root, "updated_at"),
		Section:       o.Section, StepID: o.StepID, ActionID: o.ActionID, Offset: o.Offset,
		Items:          []ReportInvestigationItem{},
		ExcludedFields: []string{"llm_call", "reflection_llm_call"},
		Filters:        map[string]string{"grep": o.Grep, "status": o.Status},
		Scope:          "Captured report fields only; device logs, network, performance, state, and image pixels are separate reads. Live reports may change between pages.",
		Guidance:       "Use reports.inspect with the same reference, filters, and section and next_offset to continue. For an omitted field, use section=field, its pointer as field, and the item's step_id/action_id. Field offsets count Unicode characters in encoded JSON; pass field.version as field_version on continuation, concatenate chunks, then decode. A media URL is not visual inspection.",
	}
	if page.ReportID == "" {
		return nil, fmt.Errorf("no report found for this reference")
	}
	stepsRaw := root["steps"]
	var steps []map[string]json.RawMessage
	if data := stepsRaw; len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &steps); err != nil {
			return nil, fmt.Errorf("invalid report steps")
		}
	}
	delete(root, "steps")
	selected := root
	candidates := []ReportInvestigationItem{}
	foundStep, foundAction := o.StepID == "", o.ActionID == ""
	pattern := regexp.MustCompile(o.Grep)
	for _, step := range steps {
		stepID := reportString(step, "id")
		if o.StepID != "" && stepID != o.StepID {
			continue
		}
		foundStep = true
		if o.Section == "field" && o.StepID != "" {
			selected = step
		}
		statusMatches := o.Status == "" || reportString(step, "effective_status") == o.Status
		if o.Section == "steps" {
			page.Total++
			delete(step, "actions")
			data, _ := json.Marshal(step)
			if statusMatches && pattern.Match(data) {
				candidates = append(candidates, compactReportItem(step, stepID, ""))
			}
		}
		if o.Section == "actions" || (o.Section == "field" && o.ActionID != "") {
			var actions []map[string]json.RawMessage
			if data := step["actions"]; len(data) > 0 && string(data) != "null" {
				if err := json.Unmarshal(data, &actions); err != nil {
					return nil, fmt.Errorf("invalid report actions")
				}
			}
			for _, action := range actions {
				actionID := reportString(action, "id")
				if o.ActionID != "" && actionID != o.ActionID {
					continue
				}
				foundAction = true
				if o.Section == "field" {
					selected = action
					continue
				}
				page.Total++
				data, _ := json.Marshal(action)
				if statusMatches && pattern.Match(data) {
					candidates = append(candidates, compactReportItem(action, stepID, actionID))
				}
			}
		}
	}
	if !foundStep {
		return nil, fmt.Errorf("step not found in this report; list section=steps")
	}
	if !foundAction {
		return nil, fmt.Errorf("action not found in this step; list section=actions")
	}
	if o.Section == "field" {
		if o.StepID == "" {
			if len(stepsRaw) > 0 {
				selected["steps"] = stepsRaw
			}
		}
		value, err := reportPointer(selected, o.Field)
		if err != nil {
			return nil, err
		}
		var compact bytes.Buffer
		if err = json.Compact(&compact, value); err != nil {
			return nil, fmt.Errorf("invalid report field JSON")
		}
		version := fmt.Sprintf("%x", sha256.Sum256(compact.Bytes()))
		if o.FieldVersion != "" && o.FieldVersion != version {
			return nil, ErrReportFieldChanged
		}
		chars := []rune(compact.String())
		if o.Offset > len(chars) {
			return nil, fmt.Errorf("offset exceeds field length")
		}
		end := min(o.Offset+4000, len(chars))
		page.Total = len(chars)
		page.Matched = len(chars)
		page.Returned = end - o.Offset
		page.Field = &ReportFieldChunk{Pointer: o.Field, Version: version, Encoding: "json", Content: string(chars[o.Offset:end]), TotalCharacters: len(chars)}
		if end < len(chars) {
			page.NextOffset = &end
			page.Truncated = true
		}
		return page, nil
	}
	if o.Section == "overview" {
		page.Total = 1
		candidates = append(candidates, compactReportItem(root, "", ""))
		page.ExcludedFields = append(page.ExcludedFields, "steps")
	}
	if o.Section == "steps" {
		page.ExcludedFields = append(page.ExcludedFields, "actions")
	}
	page.Matched = len(candidates)
	if o.Offset > len(candidates) {
		return nil, fmt.Errorf("offset exceeds matching item count")
	}
	for index := o.Offset; index < len(candidates) && page.Returned < o.Limit; index++ {
		page.Items = append(page.Items, candidates[index])
		page.Returned++
		next := index + 1
		page.NextOffset = &next
		page.Truncated = true
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > InvestigationBudgetBytes {
			page.Items = page.Items[:len(page.Items)-1]
			page.Returned--
			if page.Returned == 0 {
				return nil, fmt.Errorf("report item exceeds investigation budget; use section=field with a selected step/action and field pointer")
			}
			break
		}
	}
	next := o.Offset + page.Returned
	page.Truncated = next < len(candidates)
	page.NextOffset = nil
	if page.Truncated {
		page.NextOffset = &next
	}
	return page, nil
}

func compactReportItem(source map[string]json.RawMessage, stepID, actionID string) ReportInvestigationItem {
	item := ReportInvestigationItem{StepID: stepID, ActionID: actionID, Value: map[string]json.RawMessage{}, OmittedFields: []OmittedReportField{}}
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := source[key]
		if key == "llm_call" || key == "reflection_llm_call" {
			continue
		}
		if len(value) <= 1200 {
			item.Value[key] = value
			continue
		}
		omitted := OmittedReportField{Pointer: "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), Bytes: len(value)}
		var text string
		if json.Unmarshal(value, &text) == nil {
			runes := []rune(text)
			omitted.Preview = string(runes[:min(160, len(runes))])
		}
		item.OmittedFields = append(item.OmittedFields, omitted)
	}
	return item
}

func reportString(fields map[string]json.RawMessage, key string) string {
	var result string
	_ = json.Unmarshal(fields[key], &result)
	return result
}

func reportPointer(fields map[string]json.RawMessage, pointer string) (json.RawMessage, error) {
	current, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(pointer[1:], "/") {
		for index := 0; index < len(part); index++ {
			if part[index] == '~' {
				if index+1 >= len(part) || (part[index+1] != '0' && part[index+1] != '1') {
					return nil, fmt.Errorf("invalid JSON pointer escape")
				}
				index++
			}
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		var object map[string]json.RawMessage
		if json.Unmarshal(current, &object) == nil && object != nil {
			value, ok := object[part]
			if !ok {
				return nil, fmt.Errorf("field not found; inspect the selected item's available fields")
			}
			current = value
			continue
		}
		var array []json.RawMessage
		index, indexErr := strconv.Atoi(part)
		if json.Unmarshal(current, &array) != nil || indexErr != nil || index < 0 || index >= len(array) || strconv.Itoa(index) != part {
			return nil, fmt.Errorf("field not found; inspect the selected item's available fields")
		}
		current = array[index]
	}
	return current, nil
}

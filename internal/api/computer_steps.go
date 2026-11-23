package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ComputerStepRequest asks for one plain-language step on a computer's screen.
type ComputerStepRequest struct {
	StepType    string `json:"step_type"`
	Description string `json:"description"`
	Engine      string `json:"engine"`
	Via         string `json:"via"`
}

type ComputerExtractedData struct {
	Information string `json:"information"`
}

// ComputerStepResult is what one computer step did and which engine answered it.
type ComputerStepResult struct {
	StepType         string                 `json:"step_type"`
	Success          bool                   `json:"success"`
	Status           string                 `json:"status"`
	StatusReason     *string                `json:"status_reason"`
	ValidationResult *bool                  `json:"validation_result"`
	ExtractedData    *ComputerExtractedData `json:"extracted_data"`
	Point            *ComputerScreenPoint   `json:"point"`
	Engine           string                 `json:"engine"`
	LocalEngine      *string                `json:"local_engine"`
	Attempts         int                    `json:"attempts"`
	Seconds          float64                `json:"seconds"`
}

const (
	ComputerStepDescriptionMaxChars = 2000
	computerStepTimeout             = 90 * time.Second
	computerStepMaxResponseJSON     = 64 * 1024
)

var (
	computerStepTypes    = map[string]struct{}{"instruction": {}, "validation": {}, "extract": {}}
	computerStepEngines  = map[string]struct{}{"auto": {}, "local": {}, "hosted": {}}
	computerStepStatuses = map[string]struct{}{"passed": {}, "failed": {}, "uncertain": {}}
)

func ValidateComputerStep(request ComputerStepRequest) error {
	if _, ok := computerStepTypes[request.StepType]; !ok {
		return errors.New("computer step type must be instruction, validation or extract")
	}
	if _, ok := computerStepEngines[request.Engine]; !ok {
		return errors.New("--engine must be auto, local or hosted")
	}
	if strings.TrimSpace(request.Description) == "" || utf8.RuneCountInString(request.Description) > ComputerStepDescriptionMaxChars {
		return fmt.Errorf("describe the step in 1 to %d characters", ComputerStepDescriptionMaxChars)
	}
	if request.Via != "cli" && request.Via != "mcp" && request.Via != "api" {
		return errors.New("computer step request is invalid")
	}
	return nil
}

// RunComputerStep sends one step once; a step may click, so it is never retried.
func (c *Client) RunComputerStep(ctx context.Context, id uuid.UUID, request ComputerStepRequest) (*ComputerStepResult, error) {
	if id == uuid.Nil {
		return nil, errors.New("computer step request is invalid")
	}
	if err := ValidateComputerStep(request); err != nil {
		return nil, err
	}
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/steps", id), request, id.String(), computerStepTimeout)
	body, err := readComputerScreenBody(response, err)
	if err != nil {
		return nil, err
	}
	var result ComputerStepResult
	if response.StatusCode != http.StatusOK || len(body) > computerStepMaxResponseJSON || decodeComputerJSON(body, &result, "step_type", "success", "status", "engine", "attempts", "seconds") != nil || !validComputerStepResult(result, request.StepType) {
		return nil, errors.New("computer step response is invalid")
	}
	return &result, nil
}

func validComputerStepResult(result ComputerStepResult, stepType string) bool {
	_, knownStatus := computerStepStatuses[result.Status]
	return knownStatus &&
		result.StepType == stepType &&
		result.Success == (result.Status == "passed") &&
		(result.Engine == "local" || result.Engine == "hosted") &&
		result.Attempts >= 0 && result.Seconds >= 0 &&
		validComputerScreenCursor(result.Point)
}

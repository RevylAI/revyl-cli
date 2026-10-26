package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const computerOperationWait = 310 * time.Second

var ErrComputerExportCutOff = errors.New("computer export was cut off")

var computerOperationPollInterval = 2 * time.Second

func (c *Client) CreateComputerRun(ctx context.Context, request ComputerRunCreate) (*ComputerReport, error) {
	if request.ComputerSessionId == uuid.Nil || request.Os != "macos" && request.Os != "linux" {
		return nil, fmt.Errorf("computer creation requires a nonzero UUID and macos or linux")
	}
	response, err := c.doComputerMutationOnce(ctx, "/api/v1/computer-runs", request, request.ComputerSessionId.String(), DefaultTimeout)
	return readComputerReport(response, err, request.ComputerSessionId)
}

func (c *Client) GetComputerRun(ctx context.Context, id uuid.UUID) (*ComputerReportDetail, error) {
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s", id))
	return readComputerReportDetail(response, err, id)
}

func (c *Client) ExportComputerHarbor(ctx context.Context, id uuid.UUID, destination io.Writer) error {
	if destination == nil {
		return errors.New("computer export requires a destination writer")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	client := *c.httpClient
	client.Timeout = 0
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := c.doRequestOnceWithClient(ctx, http.MethodGet, fmt.Sprintf("/api/v1/computer-runs/%s/export/harbor", id), nil, &client)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errComputerReadFailed
	}
	if response.StatusCode != http.StatusOK {
		_, readErr := readComputerBody(response, nil)
		if readErr != nil {
			return readErr
		}
		return errComputerResponse
	}
	defer response.Body.Close()
	if _, err := io.Copy(destination, response.Body); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return ErrComputerExportCutOff
		}
		return errors.New("computer export download or write failed; check the destination and retry")
	}
	return nil
}

// ComputerListMaxLimit is the most computers one list read returns.
const ComputerListMaxLimit = 100

// ListComputerRuns reads the organization's newest computers, newest first.
func (c *Client) ListComputerRuns(ctx context.Context, limit int) (*ComputerRunListResponse, error) {
	if limit < 1 || limit > ComputerListMaxLimit {
		return nil, fmt.Errorf("computer list limit must be from 1 to %d", ComputerListMaxLimit)
	}
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs?limit=%d", limit))
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, err
	}
	var list ComputerRunListResponse
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "ComputerRunListResponse") != nil || decodeComputerJSON(body, &list, "computers") != nil || list.Limit != limit || list.Offset != 0 || len(list.Computers) > limit || list.Total < len(list.Computers) {
		return nil, errComputerResponse
	}
	return &list, nil
}

func (c *Client) GetComputerReport(ctx context.Context, id uuid.UUID) (*ComputerReport, error) {
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/report", id))
	return readComputerReport(response, err, id)
}

func (c *Client) GetComputerATIF(ctx context.Context, id uuid.UUID) (*ComputerATIFTrajectory, error) {
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/report/atif", id))
	return readComputerATIF(response, err, id)
}

const ComputerPerformanceMaxSamples = 2048

type ComputerPerformancePage struct {
	ComputerPerformanceReport
	json json.RawMessage
}

func (p ComputerPerformancePage) MarshalJSON() ([]byte, error) {
	return p.json, nil
}

func (c *Client) GetComputerPerformanceReport(ctx context.Context, id uuid.UUID, cursor int) (*ComputerPerformancePage, error) {
	if id == uuid.Nil || cursor < 0 || cursor > ComputerPerformanceMaxSamples {
		return nil, errors.New("computer performance requires a nonzero UUID and cursor from 0 to 2048")
	}
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/report/performance?cursor=%d&limit=%d", id, cursor, ComputerPerformanceMaxSamples))
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, err
	}
	var page ComputerPerformancePage
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "ComputerPerformanceReport") != nil || decodeComputerJSON(body, &page.ComputerPerformanceReport) != nil || page.ComputerSessionId != id {
		return nil, errComputerResponse
	}
	next, err := strconv.Atoi(page.NextCursor)
	itemCount := 0
	if page.Items != nil {
		itemCount = len(*page.Items)
	}
	if err != nil || next < cursor || next > ComputerPerformanceMaxSamples || itemCount != next-cursor || page.Source != "none" && next > page.Summary.SampleCount {
		return nil, errComputerResponse
	}
	page.json = body
	return &page, nil
}

func (c *Client) StopComputerRun(ctx context.Context, id uuid.UUID) (*ComputerReport, error) {
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/stop", id), nil, id.String(), 310*time.Second)
	return readComputerReport(response, err, id)
}

func (c *Client) SubmitComputerOperation(ctx context.Context, request ComputerCommandRequest) (*ComputerCommandResponse, error) {
	if err := ValidateComputerCommand(request); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, computerOperationWait)
	defer cancel()
	operationID := ComputerOperationID(request.ComputerSessionId, request.RequestId)
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/operations", request.ComputerSessionId), request, request.RequestId.String(), computerOperationWait, map[string]string{"Prefer": "respond-async"})
	result, err := readComputerOperation(response, err, request.ComputerSessionId, operationID, true)
	if err == nil && result == nil {
		result, err = c.awaitComputerOperation(ctx, request.ComputerSessionId, operationID)
	}
	if err != nil {
		return nil, err
	}
	info, err := InspectComputerExecution(*result)
	kind, _ := request.Command.Discriminator()
	if err != nil || info.Kind != "" && info.Kind != kind {
		return nil, errComputerResponse
	}
	if info.Outcome == "timed_out" && kind != "exec" {
		timedOut, err := result.Execution.AsComputerTimedOut()
		if err != nil || timedOut.ObservedExitCode != nil {
			return nil, errComputerResponse
		}
	}
	if info.Outcome == "succeeded" {
		succeeded, _ := result.Execution.AsComputerSucceeded()
		command, _ := request.Command.ValueByDiscriminator()
		switch command := command.(type) {
		case ComputerFileRead:
			limit := ComputerMaxOutputBytes
			if command.MaxBytes != nil {
				limit = *command.MaxBytes
			}
			if len(info.Data) > limit {
				return nil, errComputerResponse
			}
		case ComputerFileWrite:
			data, err := DecodeComputerBytes(command.Data, ComputerMaxWriteBytes)
			written, resultErr := succeeded.Result.AsComputerFileWriteResult()
			if err != nil || resultErr != nil || written.ByteCount != len(data) {
				return nil, errComputerResponse
			}
		case ComputerProcessList:
			limit := ComputerMaxItems
			if command.MaxItems != nil {
				limit = *command.MaxItems
			}
			processes, _ := succeeded.Result.AsComputerProcessListResult()
			if len(processes.Processes) > limit {
				return nil, errComputerResponse
			}
		case ComputerProcessInspect:
			process, _ := succeeded.Result.AsComputerProcessInspectResult()
			if process.Process.Handle != command.Handle {
				return nil, errComputerResponse
			}
		}
	}
	return result, nil
}

func (c *Client) GetComputerOperation(ctx context.Context, id, operationID uuid.UUID) (*ComputerCommandResponse, error) {
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/operations/%s", id, operationID))
	return readComputerOperation(response, err, id, operationID, true)
}

// awaitComputerOperation reads a submitted operation the backend answered as
// still running until its evidence settles, never resubmitting it. A read never
// dispatches, so a failed read is retried within the bound unless it is a
// definite refusal.
func (c *Client) awaitComputerOperation(ctx context.Context, id, operationID uuid.UUID) (*ComputerCommandResponse, error) {
	var unrecorded *ComputerCommandResponse
	for {
		select {
		case <-ctx.Done():
			if unrecorded != nil {
				return unrecorded, nil
			}
			return nil, safeComputerTransportError(ctx.Err())
		case <-time.After(computerOperationPollInterval):
		}
		result, err := c.GetComputerOperation(ctx, id, operationID)
		var apiErr *APIError
		refusal := errors.As(err, &apiErr)
		switch {
		case err == nil && result != nil && result.Evidence != "pending":
			return result, nil
		case err == nil && result != nil:
			unrecorded = result
		case err == nil, ctx.Err() != nil, errors.Is(err, errComputerReadFailed),
			refusal && (apiErr.Code == "evidence_unavailable" || apiErr.Code == "" && apiErr.StatusCode >= 500):
		case refusal && apiErr.Code == "operation_not_found":
			return nil, fmt.Errorf("%w: %w", ErrComputerOutcomeUnconfirmed, err)
		default:
			return nil, err
		}
	}
}

func ComputerOperationID(id, requestID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(id, []byte(requestID.String()))
}

func (c *Client) doComputerRead(ctx context.Context, path string) (*http.Response, error) {
	client := *c.httpClient
	client.Timeout = DefaultTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c.doRequestOnceWithClient(ctx, http.MethodGet, path, nil, &client)
}

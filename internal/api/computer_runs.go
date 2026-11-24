package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const computerOperationWait = 310 * time.Second

const computerOperationWaitMargin = 30 * time.Second

var ErrComputerExportCutOff = errors.New("computer export was cut off")

var computerOperationPollInterval = 2 * time.Second

var computerStartPollInterval = 250 * time.Millisecond

var computerSessionProfilePollInterval = 2 * time.Second

var computerReleasePollInterval = time.Second

var computerPersistentPollInterval = 2 * time.Second

func (c *Client) CreateComputerRun(ctx context.Context, request ComputerRunCreate) (*ComputerReport, error) {
	if request.ComputerSessionId == uuid.Nil || request.Os != "macos" && request.Os != "linux" {
		return nil, fmt.Errorf("computer creation requires a nonzero UUID and a supported OS")
	}
	response, err := c.doComputerMutationOnce(ctx, "/api/v1/computer-runs", request, request.ComputerSessionId.String(), DefaultTimeout)
	return readComputerReport(response, err, request.ComputerSessionId)
}

func (c *Client) GetComputerRun(ctx context.Context, id uuid.UUID) (*ComputerReportDetail, error) {
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s", id))
	return readComputerReportDetail(response, err, id)
}

// AwaitComputerRunStarted re-reads a starting computer until it leaves starting
// and, when it was created with a session profile, until that profile has
// finished on the active computer, or ctx ends, returning the latest report
// either way.
func (c *Client) AwaitComputerRunStarted(ctx context.Context, report *ComputerReport) (*ComputerReport, error) {
	report, err := c.awaitComputerRun(ctx, report, computerStartPollInterval, func(report ComputerReport) bool {
		return InspectComputerReport(report).State == "starting"
	})
	if err != nil {
		return nil, err
	}
	return c.awaitComputerRun(ctx, report, computerSessionProfilePollInterval, func(report ComputerReport) bool {
		profile := report.SessionProfile
		return profile != nil && InspectComputerReport(report).State == "active" &&
			(profile.Status == ComputerSessionProfileStatusPending || profile.Status == ComputerSessionProfileStatusRunning)
	})
}

// AwaitComputerRunSettled re-reads a stopped computer while its release or
// evidence is still pending, until both settle or ctx ends, returning the
// latest report either way.
func (c *Client) AwaitComputerRunSettled(ctx context.Context, report *ComputerReport) (*ComputerReport, error) {
	return c.awaitComputerRun(ctx, report, computerReleasePollInterval, func(report ComputerReport) bool {
		return ComputerRunUnsettled(InspectComputerReport(report))
	})
}

func ComputerRunUnsettled(status ComputerReportStatus) bool {
	return status.Release == "pending" || status.Evidence == "pending"
}

// awaitComputerRun re-reads a computer while waiting holds for its report or
// until ctx ends. Reads never dispatch, so a transient read failure is retried
// within ctx and only a refusal ends the wait with an error.
func (c *Client) awaitComputerRun(ctx context.Context, report *ComputerReport, interval time.Duration, waiting func(ComputerReport) bool) (*ComputerReport, error) {
	id := report.ComputerSessionId
	for waiting(*report) {
		select {
		case <-ctx.Done():
			return report, nil
		case <-time.After(interval):
		}
		response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s", id))
		_, next, err := readComputerReportDetailAndReport(response, err, id)
		var apiErr *APIError
		switch {
		case err == nil:
			report = next
		case ctx.Err() != nil:
			return report, nil
		case errors.Is(err, errComputerReadFailed), errors.As(err, &apiErr) && (apiErr.Code == "evidence_unavailable" || apiErr.Code == "" && apiErr.StatusCode >= 500):
		default:
			return nil, err
		}
	}
	return report, nil
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

// GetComputerHosting reads which app-session platforms a running computer can
// host now. It is empty until the computer is running.
func (c *Client) GetComputerHosting(ctx context.Context, id uuid.UUID) (*ComputerHostingCapability, error) {
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/hosting?launch_values=true", id))
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, err
	}
	var hosting ComputerHostingCapability
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "ComputerHostingCapability") != nil || decodeComputerJSON(body, &hosting, "platforms", "max_children") != nil {
		return nil, errComputerResponse
	}
	return &hosting, nil
}

// ListComputerSessions reads the newest app sessions hosted inside a computer.
func (c *Client) ListComputerSessions(ctx context.Context, id uuid.UUID, limit int) (*ComputerRelatedSessionListResponse, error) {
	if limit < 1 || limit > ComputerListMaxLimit {
		return nil, fmt.Errorf("computer session list limit must be from 1 to %d", ComputerListMaxLimit)
	}
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/sessions?limit=%d", id, limit))
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, err
	}
	var list ComputerRelatedSessionListResponse
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "ComputerRelatedSessionListResponse") != nil || decodeComputerJSON(body, &list, "sessions") != nil || list.Limit != limit || list.Offset != 0 || len(list.Sessions) > limit || list.Total < len(list.Sessions) {
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

// StopComputerRun stops a computer. Stopped by a persistent computer's own ID,
// it also returns that computer's status, usually still stopping while its host
// saves the disk; the status is nil for any other computer.
func (c *Client) StopComputerRun(ctx context.Context, id uuid.UUID) (*ComputerReport, *PersistentComputerStatus, error) {
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/stop", id), nil, id.String(), 310*time.Second)
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, nil, err
	}
	var fields map[string]json.RawMessage
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &fields) != nil {
		return nil, nil, errComputerResponse
	}
	persistentBody, persistent := fields["persistent"]
	delete(fields, "persistent")
	reportBody, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, errComputerResponse
	}
	report, err := decodeComputerReport(reportBody, id)
	if err != nil || !persistent {
		return report, nil, err
	}
	var status PersistentComputerStatus
	if validateComputerResponseSchema(persistentBody, "PersistentComputerStatus") != nil || decodeComputerJSON(persistentBody, &status) != nil || status.ComputerId != id {
		return nil, nil, errComputerResponse
	}
	return report, &status, nil
}

func (c *Client) ExtendComputerRun(ctx context.Context, id uuid.UUID, timeoutSeconds int) (*ComputerReport, error) {
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/extend", id), ComputerRunExtend{TimeoutSeconds: timeoutSeconds}, id.String(), DefaultTimeout)
	return readComputerReport(response, err, id)
}

// PersistentComputerStartRequest is handwritten because persistent computers'
// routes stay off the public generated surface. A field left nil takes
// create's default.
type PersistentComputerStartRequest struct {
	Display        *ComputerDisplaySize `json:"display,omitempty"`
	TimeoutSeconds *int                 `json:"timeout_seconds,omitempty"`
}

// StartComputer boots a stopped persistent computer's saved disk as a new run
// on the host that saved it, with the request's display and lifetime. It
// answers while the new run is still starting.
func (c *Client) StartComputer(ctx context.Context, id uuid.UUID, request PersistentComputerStartRequest) (*PersistentComputerStatus, error) {
	var body any
	if request.Display != nil || request.TimeoutSeconds != nil {
		body = request
	}
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/start", id), body, id.String(), DefaultTimeout)
	return readPersistentComputerStatus(response, err, id)
}

// DeleteComputer stops a persistent computer's current run without saving it
// and deletes the computer and its saved disk.
func (c *Client) DeleteComputer(ctx context.Context, id uuid.UUID) error {
	response, err := c.doComputerRequestOnce(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/computer-runs/%s", id), nil, id.String(), 310*time.Second)
	if err == nil && response.StatusCode == http.StatusNoContent {
		response.Body.Close()
		return nil
	}
	if _, err = readComputerBody(response, err); err != nil {
		return err
	}
	return errComputerResponse
}

// AwaitPersistentComputer re-reads a persistent computer while it is in the
// given state or until ctx ends, returning the latest status either way. Reads
// never dispatch, so a transient read failure is retried within ctx.
func (c *Client) AwaitPersistentComputer(ctx context.Context, status *PersistentComputerStatus, waiting PersistentComputerStatusState) (*PersistentComputerStatus, error) {
	for status.State == waiting {
		select {
		case <-ctx.Done():
			return status, nil
		case <-time.After(computerPersistentPollInterval):
		}
		detail, err := c.GetComputerRun(ctx, status.ComputerId)
		var apiErr *APIError
		switch {
		case err == nil && detail.Persistent != nil && detail.Persistent.ComputerId == status.ComputerId:
			status = detail.Persistent
		case err == nil:
			return nil, errComputerResponse
		case ctx.Err() != nil:
			return status, nil
		case errors.Is(err, errComputerReadFailed), errors.As(err, &apiErr) && (apiErr.Code == "evidence_unavailable" || apiErr.Code == "" && apiErr.StatusCode >= 500):
		default:
			return nil, err
		}
	}
	return status, nil
}

// PersistentComputerListResponse is handwritten because persistent computers
// are an internal preview kept off the public OpenAPI surface.
type PersistentComputerListResponse struct {
	Computers []PersistentComputerStatus `json:"computers"`
	Limit     int                        `json:"limit"`
	Offset    int                        `json:"offset"`
	Total     int                        `json:"total"`
}

// ListPersistentComputers reads the organization's newest persistent
// computers, stopped ones included.
func (c *Client) ListPersistentComputers(ctx context.Context, limit int) (*PersistentComputerListResponse, error) {
	if limit < 1 || limit > ComputerListMaxLimit {
		return nil, fmt.Errorf("computer list limit must be from 1 to %d", ComputerListMaxLimit)
	}
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/persistent?limit=%d", limit))
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, err
	}
	var list PersistentComputerListResponse
	var items struct {
		Computers []json.RawMessage `json:"computers"`
	}
	if response.StatusCode != http.StatusOK || decodeComputerJSON(body, &list, "computers", "total", "limit", "offset") != nil || json.Unmarshal(body, &items) != nil || list.Limit != limit || list.Offset != 0 || len(list.Computers) > limit || list.Total < len(list.Computers) {
		return nil, errComputerResponse
	}
	for _, item := range items.Computers {
		if validateComputerResponseSchema(item, "PersistentComputerStatus") != nil {
			return nil, errComputerResponse
		}
	}
	return &list, nil
}

// ComputerSnapshot is handwritten because persistent computers are an internal
// preview kept off the public OpenAPI surface. A snapshot names one durable
// save of a stopped persistent computer; ExpiresAt is nil when the save's host
// recorded no end for it.
type ComputerSnapshot struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	ComputerID uuid.UUID  `json:"computer_id"`
	SavedAt    time.Time  `json:"saved_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

type ComputerSnapshotListResponse struct {
	Snapshots []ComputerSnapshot `json:"snapshots"`
	Limit     int                `json:"limit"`
	Offset    int                `json:"offset"`
	Total     int                `json:"total"`
}

var computerSnapshotReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidComputerSnapshotReference reports whether value can name a snapshot:
// its ID, or a name of 1 to 64 letters, digits, '.', '_' or '-' that starts
// with a letter or digit.
func ValidComputerSnapshotReference(value string) bool {
	return computerSnapshotReferencePattern.MatchString(value)
}

// SaveComputerSnapshot names the stopped persistent computer's current durable
// save. It copies no bytes, so the snapshot is ready when this returns.
func (c *Client) SaveComputerSnapshot(ctx context.Context, computerID uuid.UUID, name string) (*ComputerSnapshot, error) {
	if _, err := uuid.Parse(name); err == nil || !ValidComputerSnapshotReference(name) {
		return nil, errors.New("snapshot name must be 1-64 letters, digits, '.', '_' or '-', start with a letter or digit, and not be a UUID")
	}
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/snapshots", computerID), map[string]string{"name": name}, computerID.String(), DefaultTimeout)
	snapshot, err := readComputerSnapshot(response, err)
	if err != nil {
		return nil, err
	}
	if snapshot.ComputerID != computerID || snapshot.Name != name {
		return nil, errComputerResponse
	}
	return snapshot, nil
}

// ListComputerSnapshots reads the organization's newest live snapshots.
func (c *Client) ListComputerSnapshots(ctx context.Context, limit int) (*ComputerSnapshotListResponse, error) {
	if limit < 1 || limit > ComputerListMaxLimit {
		return nil, fmt.Errorf("snapshot list limit must be from 1 to %d", ComputerListMaxLimit)
	}
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/snapshots?limit=%d", limit))
	body, err := readComputerBody(response, err)
	if err != nil {
		return nil, err
	}
	var list ComputerSnapshotListResponse
	var items struct {
		Snapshots []json.RawMessage `json:"snapshots"`
	}
	if response.StatusCode != http.StatusOK || decodeComputerJSON(body, &list, "snapshots", "total", "limit", "offset") != nil || json.Unmarshal(body, &items) != nil || list.Limit != limit || list.Offset != 0 || len(list.Snapshots) > limit || list.Total < len(list.Snapshots) {
		return nil, errComputerResponse
	}
	for _, item := range items.Snapshots {
		var snapshot ComputerSnapshot
		if decodeComputerSnapshot(item, &snapshot) != nil {
			return nil, errComputerResponse
		}
	}
	return &list, nil
}

// GetComputerSnapshot reads one live snapshot by its ID or name.
func (c *Client) GetComputerSnapshot(ctx context.Context, reference string) (*ComputerSnapshot, error) {
	if !ValidComputerSnapshotReference(reference) {
		return nil, errors.New("computer snapshot requires a snapshot ID or name")
	}
	response, err := c.doComputerRead(ctx, fmt.Sprintf("/api/v1/computer-runs/snapshots/%s", reference))
	snapshot, err := readComputerSnapshot(response, err)
	if err != nil {
		return nil, err
	}
	if snapshot.ID.String() != strings.ToLower(reference) && snapshot.Name != reference {
		return nil, errComputerResponse
	}
	return snapshot, nil
}

// DeleteComputerSnapshot deletes one snapshot by its ID or name. Computers
// already created from it keep their own disks.
func (c *Client) DeleteComputerSnapshot(ctx context.Context, reference string) error {
	if !ValidComputerSnapshotReference(reference) {
		return errors.New("computer snapshot requires a snapshot ID or name")
	}
	response, err := c.doComputerRequestOnce(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/computer-runs/snapshots/%s", reference), nil, reference, DefaultTimeout)
	if err == nil && response.StatusCode == http.StatusNoContent {
		response.Body.Close()
		return nil
	}
	if _, err = readComputerBody(response, err); err != nil {
		return err
	}
	return errComputerResponse
}

func readComputerSnapshot(response *http.Response, requestErr error) (*ComputerSnapshot, error) {
	body, err := readComputerBody(response, requestErr)
	if err != nil {
		return nil, err
	}
	var snapshot ComputerSnapshot
	if response.StatusCode != http.StatusOK || decodeComputerSnapshot(body, &snapshot) != nil {
		return nil, errComputerResponse
	}
	return &snapshot, nil
}

func decodeComputerSnapshot(body []byte, snapshot *ComputerSnapshot) error {
	if err := decodeComputerJSON(body, snapshot, "id", "name", "computer_id", "saved_at"); err != nil {
		return err
	}
	if snapshot.ID == uuid.Nil || snapshot.ComputerID == uuid.Nil || !ValidComputerSnapshotReference(snapshot.Name) {
		return errComputerResponse
	}
	return nil
}

func readPersistentComputerStatus(response *http.Response, requestErr error, id uuid.UUID) (*PersistentComputerStatus, error) {
	body, err := readComputerBody(response, requestErr)
	if err != nil {
		return nil, err
	}
	var status PersistentComputerStatus
	if response.StatusCode != http.StatusOK || validateComputerResponseSchema(body, "PersistentComputerStatus") != nil || decodeComputerJSON(body, &status) != nil || status.ComputerId != id {
		return nil, errComputerResponse
	}
	return &status, nil
}

func (c *Client) SubmitComputerOperation(ctx context.Context, request ComputerCommandRequest) (*ComputerCommandResponse, error) {
	if err := ValidateComputerCommand(request); err != nil {
		return nil, err
	}
	wireRequest, err := encodeComputerOperationRequest(request)
	if err != nil {
		return nil, err
	}
	wait := computerOperationWait
	if command, _ := request.Command.ValueByDiscriminator(); command != nil {
		if exec, ok := command.(ComputerExec); ok && exec.TimeoutMs != nil {
			wait = max(wait, time.Duration(*exec.TimeoutMs)*time.Millisecond+computerOperationWaitMargin)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	operationID := ComputerOperationID(request.ComputerSessionId, request.RequestId)
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/operations", request.ComputerSessionId), wireRequest, request.RequestId.String(), computerOperationWait, map[string]string{"Prefer": "respond-async"})
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

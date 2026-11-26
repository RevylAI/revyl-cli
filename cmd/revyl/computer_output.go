package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/api"
)

func readComputerInput(reader io.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("computer input requires a positive byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return nil, errors.New("computer input could not be read")
	}
	if len(data) > maxBytes {
		return nil, errors.New("computer input exceeds the byte limit")
	}
	return data, nil
}

func writeComputerOutput(writer io.Writer, data []byte, maxBytes int) error {
	if maxBytes <= 0 || len(data) > maxBytes {
		return errors.New("computer output exceeds the byte limit")
	}
	written, err := writer.Write(data)
	if err != nil || written != len(data) {
		return errors.New("computer output could not be written")
	}
	return nil
}

func writeComputerJSON(writer io.Writer, value any, maxBytes int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return errors.New("computer response could not be encoded as JSON")
	}
	return writeComputerOutput(writer, append(data, '\n'), maxBytes)
}

func computerTime(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05 UTC")
}

var computerFailureReasons = map[string]string{
	"no_host":      "No Mac was free to start this computer",
	"device_limit": "All of your organization's device slots were in use, so this computer didn't start",
}

const computerLimitMessage = "Every device slot a computer can use is busy with other computers, phone sessions or builds. Stop one, then try again."
const computerMacosWithProMessage = "macOS computers come with Pro. Add a card to unlock them; your free minutes still cover macOS.\nAdd one with: revyl billing add-card"
const computerMacosWithProOfferMessage = "macOS computers come with Pro, and your free minutes still cover macOS."
const computerLinuxOnRequestMessage = "Linux computers are available on request: contact support@revyl.ai."
const computerTurnOnMessage = "Contact support@revyl.ai to turn computers on for this organization."

// computerNotEnabledMessage explains a computer_not_enabled refusal. It offers
// a card only when the backend says one unlocks Mac computers, so a backend
// that does not say so gets the support step. When the backend names the
// upgrade step, its link is the refusal's hint, adding a card for an owner or
// admin and asking one for a member.
func computerNotEnabledMessage(osName string, refusal *api.APIError) string {
	if osName == "linux" {
		return computerLinuxOnRequestMessage
	}
	if !refusal.UnlocksWithCard {
		return computerTurnOnMessage
	}
	if refusal.BillingURL != "" {
		return computerMacosWithProOfferMessage
	}
	return computerMacosWithProMessage
}

const computerLongLimitMessage = "Your organization already has 2 computers running longer than 2 hours. Ask for 2 hours or less, or stop one of them."

const computerExtendUnsupportedMessage = "This computer's host cannot extend it; it still stops at its current expiry. Computers started after the host updates can be extended."

const computerDisplayUnsupportedMessage = "No computer host can apply a display size yet. Omit --display to start at the default 1280x800."

func computerInferenceText(readiness string) string {
	switch readiness {
	case "ready":
		return "Host model ready"
	case "unavailable":
		return "Host model not answering; validation and extract are refused"
	default:
		return "Host model not checked until the computer is active"
	}
}

func computerStateText(state, release string) string {
	if (state == "stopped" || state == "failed" || state == "owner_lost") && release != "confirmed" {
		return state + ", cleanup " + release
	}
	return state
}

// computerPrintable escapes control and other non-printing runes, so text a
// guest command chose cannot drive the reader's terminal.
func computerPrintable(cell string) string {
	var printable strings.Builder
	for _, r := range cell {
		if unicode.IsPrint(r) {
			printable.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRuneToGraphic(r)
		printable.WriteString(quoted[1 : len(quoted)-1])
	}
	return printable.String()
}

func writeComputerTable(writer io.Writer, rows [][]string) error {
	var rendered bytes.Buffer
	table := tabwriter.NewWriter(&rendered, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = computerPrintable(cell)
		}
		fmt.Fprintln(table, strings.Join(cells, "\t"))
	}
	if err := table.Flush(); err != nil {
		return errors.New("computer output could not be written")
	}
	for _, line := range strings.SplitAfter(rendered.String(), "\n") {
		if line == "" {
			continue
		}
		if _, err := io.WriteString(writer, strings.TrimRight(line, " \n")+"\n"); err != nil {
			return errors.New("computer output could not be written")
		}
	}
	return nil
}

func writeComputerList(writer io.Writer, list *api.ComputerRunListResponse) error {
	if len(list.Computers) == 0 {
		_, err := fmt.Fprintln(writer, "No computers yet. Start one with: revyl computer create --wait")
		return err
	}
	if err := writeComputerRows(writer, list.Computers); err != nil {
		return err
	}
	if list.Total > len(list.Computers) {
		_, err := fmt.Fprintf(writer, "Newest %d of %d computers: %s; use --limit (up to %d) for more.\n", len(list.Computers), list.Total, computerStateCounts(list.Computers), api.ComputerListMaxLimit)
		return err
	}
	_, err := fmt.Fprintf(writer, "%d computers: %s\n", len(list.Computers), computerStateCounts(list.Computers))
	return err
}

func writeComputerRows(writer io.Writer, computers []api.ComputerRunSummary) error {
	rows := [][]string{{"COMPUTER", "OS", "STATE", "CREATED"}}
	for _, computer := range computers {
		rows = append(rows, []string{computer.ComputerSessionId.String(), string(computer.Os), computerStateText(string(computer.State), string(computer.Release)), computerTime(computer.CreatedAt)})
	}
	return writeComputerTable(writer, rows)
}

func computerStateCounts(computers []api.ComputerRunSummary) string {
	order := []string{"active", "starting"}
	counts := map[string]int{}
	for _, computer := range computers {
		state := string(computer.State)
		if counts[state] == 0 && state != "active" && state != "starting" {
			order = append(order, state)
		}
		counts[state]++
	}
	var parts []string
	for _, state := range order {
		if counts[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state], state))
		}
	}
	return strings.Join(parts, ", ")
}

func computerRunning(computers []api.ComputerRunSummary) []api.ComputerRunSummary {
	var running []api.ComputerRunSummary
	for _, computer := range computers {
		if computer.State == "active" || computer.State == "starting" {
			running = append(running, computer)
		}
	}
	return running
}

// writeComputerOverview is what a bare revyl computer prints: the running
// computers among the newest page, with counts for the whole page.
func writeComputerOverview(writer io.Writer, list *api.ComputerRunListResponse) error {
	running := computerRunning(list.Computers)
	if len(running) == 0 {
		message := "No running computers."
		if list.Total > len(list.Computers) {
			message = fmt.Sprintf("No running computers among the newest %d; older ones may still run.", len(list.Computers))
		}
		_, err := fmt.Fprintln(writer, message)
		if err != nil || len(list.Computers) == 0 {
			return err
		}
	} else if err := writeComputerRows(writer, running); err != nil {
		return err
	}
	more := "revyl computer list shows each"
	if list.Total > len(list.Computers) {
		more = fmt.Sprintf("revyl computer list --limit %d shows more", api.ComputerListMaxLimit)
	}
	_, err := fmt.Fprintf(writer, "Newest %d of %d computers: %s; %s.\n", len(list.Computers), list.Total, computerStateCounts(list.Computers), more)
	return err
}

// persistentComputerSaveWhere says where a stopped computer's save is: on the
// Mac that saved it only, or also copied off it.
func persistentComputerSaveWhere(computer api.PersistentComputerStatus) string {
	if computer.Durable {
		return "saved off host"
	}
	return "saved on host"
}

func persistentComputerListState(computer api.PersistentComputerStatus) string {
	if computer.State != api.PersistentComputerStatusStateStopped {
		return string(computer.State)
	}
	return fmt.Sprintf("%s (%s)", computer.State, persistentComputerSaveWhere(computer))
}

func persistentComputerStateText(computer api.PersistentComputerStatus) string {
	if computer.SavedAt == nil {
		return string(computer.State)
	}
	if computer.State != api.PersistentComputerStatusStateStopped {
		return fmt.Sprintf("%s (saved %s)", computer.State, computerTime(*computer.SavedAt))
	}
	return fmt.Sprintf("%s (%s %s)", computer.State, persistentComputerSaveWhere(computer), computerTime(*computer.SavedAt))
}

func writePersistentComputerStatus(writer io.Writer, computer *api.PersistentComputerStatus) error {
	line := fmt.Sprintf("computer_id=%s state=%s run_session_id=%s", computer.ComputerId, computer.State, computer.RunSessionId)
	if computer.SavedAt != nil {
		line += fmt.Sprintf(" saved_at=%s durable=%t", computer.SavedAt.UTC().Format(time.RFC3339), computer.Durable)
	}
	if computer.SavedDiskExpiresAt != nil {
		line += " saved_disk_expires_at=" + computer.SavedDiskExpiresAt.UTC().Format(time.RFC3339)
	}
	_, err := fmt.Fprintln(writer, line)
	return err
}

func writePersistentComputerList(writer io.Writer, list *api.PersistentComputerListResponse) error {
	if len(list.Computers) == 0 {
		_, err := fmt.Fprintln(writer, "No persistent computers yet. Start one with: revyl computer create --os macos --persistent")
		return err
	}
	rows := [][]string{{"ID", "STATE", "SAVED", "SAVE EXPIRES", "CREATED"}}
	for _, computer := range list.Computers {
		saved, expires := "-", "-"
		if computer.SavedAt != nil {
			saved = computerTime(*computer.SavedAt)
		}
		if computer.SavedDiskExpiresAt != nil {
			expires = computerTime(*computer.SavedDiskExpiresAt)
		}
		rows = append(rows, []string{computer.ComputerId.String(), persistentComputerListState(computer), saved, expires, computerTime(computer.CreatedAt)})
	}
	if err := writeComputerTable(writer, rows); err != nil {
		return err
	}
	if list.Total > len(list.Computers) {
		_, err := fmt.Fprintf(writer, "Showing the newest %d of %d persistent computers; use --limit (up to %d) for more.\n", len(list.Computers), list.Total, api.ComputerListMaxLimit)
		return err
	}
	return nil
}

func writeComputerSnapshot(writer io.Writer, snapshot *api.ComputerSnapshot) error {
	line := fmt.Sprintf("snapshot_id=%s name=%s computer_id=%s saved_at=%s", snapshot.ID, snapshot.Name, snapshot.ComputerID, snapshot.SavedAt.UTC().Format(time.RFC3339))
	if snapshot.ExpiresAt != nil {
		line += " expires_at=" + snapshot.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if snapshot.SavedDiskExpiresAt != nil {
		line += " saved_disk_expires_at=" + snapshot.SavedDiskExpiresAt.UTC().Format(time.RFC3339)
	}
	_, err := fmt.Fprintln(writer, line)
	return err
}

func writeComputerSnapshotList(writer io.Writer, list *api.ComputerSnapshotListResponse) error {
	if len(list.Snapshots) == 0 {
		_, err := fmt.Fprintln(writer, "No snapshots yet. Save one with: revyl computer snapshot save C1 --name NAME")
		return err
	}
	rows := [][]string{{"ID", "NAME", "COMPUTER", "SAVED", "EXPIRES"}}
	for _, snapshot := range list.Snapshots {
		expires := "-"
		if snapshot.SavedDiskExpiresAt != nil {
			expires = computerTime(*snapshot.SavedDiskExpiresAt)
		} else if snapshot.ExpiresAt != nil {
			expires = computerTime(*snapshot.ExpiresAt)
		}
		rows = append(rows, []string{snapshot.ID.String(), snapshot.Name, snapshot.ComputerID.String(), computerTime(snapshot.SavedAt), expires})
	}
	if err := writeComputerTable(writer, rows); err != nil {
		return err
	}
	if list.Total > len(list.Snapshots) {
		_, err := fmt.Fprintf(writer, "Showing the newest %d of %d snapshots; use --limit (up to %d) for more.\n", len(list.Snapshots), list.Total, api.ComputerListMaxLimit)
		return err
	}
	return nil
}

func writeComputerHosting(writer io.Writer, hosting *api.ComputerHostingCapability) error {
	if len(hosting.Platforms) == 0 {
		_, err := fmt.Fprintln(writer, "This computer can't host app sessions right now; it may still be starting.")
		return err
	}
	platforms := make([]string, 0, len(hosting.Platforms))
	for _, platform := range hosting.Platforms {
		name := string(platform)
		if hosting.MaxChildrenByPlatform != nil {
			if limit, ok := (*hosting.MaxChildrenByPlatform)[name]; ok {
				name = fmt.Sprintf("%s (%d at a time)", name, limit)
			}
		}
		platforms = append(platforms, name)
	}
	_, err := fmt.Fprintf(writer, "Platforms: %s\nMax app sessions: %d\n", strings.Join(platforms, ", "), hosting.MaxChildren)
	return err
}

func writeComputerSessions(writer io.Writer, list *api.ComputerRelatedSessionListResponse) error {
	if len(list.Sessions) == 0 {
		_, err := fmt.Fprintln(writer, "No app sessions in this computer. Start one with: revyl device start --computer ID")
		return err
	}
	rows := [][]string{{"SESSION", "PLATFORM", "STATUS", "APP", "CREATED"}}
	for _, session := range list.Sessions {
		app := "-"
		if session.AppName != nil {
			app = *session.AppName
		}
		rows = append(rows, []string{session.Id.String(), string(session.Platform), string(session.Status), app, computerTime(session.CreatedAt)})
	}
	if err := writeComputerTable(writer, rows); err != nil {
		return err
	}
	if list.Total > len(list.Sessions) {
		_, err := fmt.Fprintf(writer, "Showing the newest %d of %d sessions; use --limit (up to %d) for more.\n", len(list.Sessions), list.Total, api.ComputerListMaxLimit)
		return err
	}
	return nil
}

func writeComputerDetail(writer io.Writer, detail *api.ComputerReportDetail, status api.ComputerReportStatus, full bool) error {
	rows := [][]string{
		{"Computer", detail.ComputerSessionId.String()},
		{"OS", string(detail.Os)},
		{"State", computerStateText(status.State, status.Release)},
	}
	if detail.Persistent != nil {
		rows = append(rows, []string{"Persistent", persistentComputerStateText(*detail.Persistent)})
		if detail.Persistent.SavedDiskExpiresAt != nil {
			rows = append(rows, []string{"Save expires", computerTime(*detail.Persistent.SavedDiskExpiresAt)})
		}
	}
	if detail.Inference != nil {
		rows = append(rows, []string{"Inference", computerInferenceText(string(*detail.Inference))})
	}
	if detail.FailureReason != nil {
		reason, ok := computerFailureReasons[*detail.FailureReason]
		if !ok {
			reason = *detail.FailureReason
		}
		rows = append(rows, []string{"Reason", reason})
	}
	if status.State == "stopped" && detail.ExecutionStartedAt == nil {
		rows = append(rows, []string{"Reason", "Stopped before it started"})
	}
	rows = append(rows, []string{"Created", computerTime(detail.CreatedAt)})
	if detail.ExecutionStartedAt != nil {
		rows = append(rows, []string{"Started", computerTime(*detail.ExecutionStartedAt)})
	}
	if detail.ExecutionEndedAt != nil {
		rows = append(rows, []string{"Ended", computerTime(*detail.ExecutionEndedAt)})
	} else {
		rows = append(rows, []string{"Expires", computerTime(detail.ExpiresAt)})
	}
	if detail.ReleasedAt != nil {
		rows = append(rows, []string{"Released", computerTime(*detail.ReleasedAt)})
	}
	if status.Evidence != "recorded" {
		rows = append(rows, []string{"Evidence", status.Evidence})
	}
	if err := writeComputerTable(writer, rows); err != nil {
		return err
	}
	if detail.Steps == nil || len(*detail.Steps) == 0 {
		_, err := fmt.Fprintln(writer, "\nNo steps yet.")
		return err
	}
	steps := *detail.Steps
	shown := steps
	if !full && len(steps) > computerDetailStepLimit {
		shown = steps[len(steps)-computerDetailStepLimit:]
	}
	summary := fmt.Sprintf("\nSteps (%d: %s)", len(steps), computerStepCounts(steps))
	if len(shown) < len(steps) {
		summary += fmt.Sprintf(", the last %d shown; --full shows all", len(shown))
	}
	fmt.Fprintln(writer, summary)
	rows = [][]string{{"#", "KIND", "RESULT", "STARTED", "TOOK", "COMMAND"}}
	for _, step := range shown {
		took := "-"
		if step.CompletedAt != nil {
			took = step.CompletedAt.Sub(step.StartedAt).Round(100 * time.Millisecond).String()
		}
		command := ""
		if step.CommandDisplay != nil {
			command = *step.CommandDisplay
		}
		if runes := []rune(command); !full && len(runes) > computerDetailCommandRunes {
			command = string(runes[:computerDetailCommandRunes-1]) + "…"
		}
		rows = append(rows, []string{fmt.Sprint(step.ExecutionOrder), strings.ReplaceAll(string(step.Kind), "_", " "), computerStepResult(step), step.StartedAt.UTC().Format("15:04:05"), took, command})
	}
	return writeComputerTable(writer, rows)
}

const (
	computerDetailStepLimit    = 20
	computerDetailCommandRunes = 80
)

func computerStepCounts(steps []api.ComputerOperationDetail) string {
	var order []string
	counts := map[string]int{}
	for _, step := range steps {
		outcome := "running"
		if step.Outcome != nil && *step.Outcome != "started" {
			outcome = strings.ReplaceAll(string(*step.Outcome), "_", " ")
		}
		if counts[outcome] == 0 {
			order = append(order, outcome)
		}
		counts[outcome]++
	}
	parts := make([]string, len(order))
	for i, outcome := range order {
		parts[i] = fmt.Sprintf("%d %s", counts[outcome], outcome)
	}
	return strings.Join(parts, ", ")
}

func writeComputerPerformance(writer io.Writer, page *api.ComputerPerformancePage) error {
	if err := writeComputerTable(writer, [][]string{
		{"Computer", page.ComputerSessionId.String()},
		{"State", string(page.State)},
		{"Source", string(page.Source)},
		{"Samples", fmt.Sprint(page.Summary.SampleCount)},
		{"Duration", fmt.Sprintf("%.1fs", page.Summary.DurationS)},
	}); err != nil {
		return err
	}
	if page.Summary.SampleCount == 0 {
		if _, err := fmt.Fprintln(writer, computerPerformanceEmptyMessage(page.State)); err != nil {
			return err
		}
	}
	rows := [][]string{{"MEASURE", "LATEST", "MIN", "AVG", "MAX"}}
	for _, metric := range []struct {
		name  string
		stats *api.ComputerMetricSummary
	}{
		{"CPU %", page.Summary.CpuPercent},
		{"Memory %", page.Summary.MemoryUsedPercent},
		{"Disk read KB/s", page.Summary.DiskReadKbPerS},
		{"Disk write KB/s", page.Summary.DiskWriteKbPerS},
		{"Network in KB/s", page.Summary.NetworkReceivedKbPerS},
		{"Network out KB/s", page.Summary.NetworkSentKbPerS},
		{"Desktop FPS", page.Summary.Fps},
	} {
		row := []string{metric.name, "—", "—", "—", "—"}
		if metric.stats != nil {
			row = []string{metric.name, fmt.Sprintf("%.1f", metric.stats.Latest), fmt.Sprintf("%.1f", metric.stats.Minimum), fmt.Sprintf("%.1f", metric.stats.Average), fmt.Sprintf("%.1f", metric.stats.Maximum)}
		}
		rows = append(rows, row)
	}
	return writeComputerTable(writer, rows)
}

func writeComputerPerformanceSample(writer io.Writer, sample api.ComputerPerformanceSample) error {
	fps := "—"
	if sample.Fps != nil {
		fps = fmt.Sprintf("%.1f", *sample.Fps)
	}
	_, err := fmt.Fprintf(writer, "%s CPU %.1f%% Memory %.1f%% Disk read/write %.1f/%.1f KB/s Network in/out %.1f/%.1f KB/s Desktop FPS %s\n",
		time.UnixMicro(int64(sample.Timestamp*1e6)).UTC().Format("2006-01-02T15:04:05.000Z"),
		sample.Cpu.AppPercent, sample.MemorySystem.UsedPercent, sample.Disk.ReadKbPerS, sample.Disk.WriteKbPerS,
		sample.Network.ReceivedKbPerS, sample.Network.SentKbPerS, fps)
	return err
}

func computerStepResult(step api.ComputerOperationDetail) string {
	if step.Outcome == nil || *step.Outcome == "started" {
		return "running"
	}
	if step.Summary != nil {
		if kind, err := step.Summary.Discriminator(); err == nil && kind == "exec" {
			if summary, err := step.Summary.AsComputerExecSummary(); err == nil {
				return fmt.Sprintf("exit %d", summary.ExitCode)
			}
		}
	}
	switch *step.Outcome {
	case "failed":
		if step.ErrorCode != nil {
			return "failed: " + string(*step.ErrorCode)
		}
	case "timed_out":
		return "timed out"
	}
	return string(*step.Outcome)
}

type computerExportResult struct {
	ComputerSessionID string `json:"computer_session_id"`
	Format            string `json:"format"`
	Path              string `json:"path"`
	SizeBytes         int64  `json:"size_bytes"`
}

func saveComputerHarbor(ctx context.Context, client *api.Client, id uuid.UUID, path string) (*computerExportResult, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".revyl-computer-export-*.zip")
	if err != nil {
		return nil, errors.New("computer export destination could not be created; check the directory and permissions")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := client.ExportComputerHarbor(ctx, id, file); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.New("computer export file could not be inspected")
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("%w; retry the download", api.ErrComputerExportCutOff)
	}
	trajectory, err := archive.Open("revyl-computers/computer-" + id.String() + "/agent/trajectory.json")
	if err != nil {
		return nil, errors.New("computer export is missing the expected Harbor trajectory; retry the download")
	}
	trajectory.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("computer export could not be saved; check disk space and retry")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return nil, errors.New("computer export could not replace the destination; check permissions and retry")
	}
	return &computerExportResult{ComputerSessionID: id.String(), Format: "harbor", Path: path, SizeBytes: info.Size()}, nil
}

func writeComputerExport(writer io.Writer, result *computerExportResult) error {
	_, err := fmt.Fprintf(writer, "Saved %s (%d bytes)\nView: unzip %s -d jobs && harbor view jobs\n", computerPrintable(result.Path), result.SizeBytes, computerPrintable(quoteCLIRecoveryArgument(result.Path)))
	return err
}

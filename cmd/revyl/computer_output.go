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

const computerLimitMessage = "Every device slot a computer can use is busy. Stop a session, then try again."
const computerNotEnabledMessage = "Computers aren't enabled for this organization. Contact Revyl to turn them on."

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
		_, err := fmt.Fprintln(writer, "No computers yet. Start one with: revyl computer create --os macos")
		return err
	}
	rows := [][]string{{"COMPUTER", "OS", "STATE", "CREATED"}}
	for _, computer := range list.Computers {
		rows = append(rows, []string{computer.ComputerSessionId.String(), string(computer.Os), computerStateText(string(computer.State), string(computer.Release)), computerTime(computer.CreatedAt)})
	}
	if err := writeComputerTable(writer, rows); err != nil {
		return err
	}
	if list.Total > len(list.Computers) {
		_, err := fmt.Fprintf(writer, "Showing the newest %d of %d computers; use --limit (up to %d) for more.\n", len(list.Computers), list.Total, api.ComputerListMaxLimit)
		return err
	}
	return nil
}

func writeComputerDetail(writer io.Writer, detail *api.ComputerReportDetail, status api.ComputerReportStatus) error {
	rows := [][]string{
		{"Computer", detail.ComputerSessionId.String()},
		{"OS", string(detail.Os)},
		{"State", computerStateText(status.State, status.Release)},
	}
	if detail.FailureReason != nil {
		reason, ok := computerFailureReasons[*detail.FailureReason]
		if !ok {
			reason = *detail.FailureReason
		}
		rows = append(rows, []string{"Reason", reason})
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
	fmt.Fprintln(writer, "\nSteps")
	rows = [][]string{{"#", "KIND", "RESULT", "STARTED", "TOOK", "COMMAND"}}
	for _, step := range *detail.Steps {
		took := "-"
		if step.CompletedAt != nil {
			took = step.CompletedAt.Sub(step.StartedAt).Round(100 * time.Millisecond).String()
		}
		command := ""
		if step.CommandDisplay != nil {
			command = *step.CommandDisplay
		}
		rows = append(rows, []string{fmt.Sprint(step.ExecutionOrder), strings.ReplaceAll(string(step.Kind), "_", " "), computerStepResult(step), step.StartedAt.UTC().Format("15:04:05"), took, command})
	}
	return writeComputerTable(writer, rows)
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

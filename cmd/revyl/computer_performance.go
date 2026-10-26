package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func validateComputerReportFlags(cmd *cobra.Command) error {
	format, _ := cmd.Flags().GetString("format")
	if format != "json" && format != "atif" && format != "performance" {
		return errors.New("report format must be json, atif or performance")
	}
	if format != "performance" {
		for _, flag := range []string{"watch", "interval", "timeout"} {
			if cmd.Flags().Changed(flag) {
				return errors.New("--watch, --interval and --timeout require --format performance")
			}
		}
		return nil
	}
	watch, _ := cmd.Flags().GetBool("watch")
	if !watch && (cmd.Flags().Changed("interval") || cmd.Flags().Changed("timeout")) {
		return errors.New("--interval and --timeout require --watch")
	}
	interval, _ := cmd.Flags().GetDuration("interval")
	if interval < 2*time.Second || interval > time.Minute {
		return errors.New("--interval must be between 2s and 60s")
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout < time.Second || timeout > 2*time.Hour {
		return errors.New("--timeout must be between 1s and 2h")
	}
	return nil
}

func readComputerPerformance(cmd *cobra.Command, client *api.Client, id uuid.UUID, watch bool, interval, timeout time.Duration) (*computerPerformanceResult, error) {
	result := &computerPerformanceResult{Watch: watch}
	ctx := cmd.Context()
	if watch {
		interruptCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(interruptCtx, timeout)
		defer cancel()
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	cursor := 0
	var noSamplesDeadline time.Time
	var previousState, previousSource string
	for {
		if ctx.Err() != nil {
			if !watch {
				return result, ctx.Err()
			}
			result.EndReason = "cancelled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				result.EndReason = "timeout"
			}
			return result, nil
		}
		if !noSamplesDeadline.IsZero() && !time.Now().Before(noSamplesDeadline) {
			result.EndReason = "ended"
			fmt.Fprintln(cmd.ErrOrStderr(), "No performance was saved for this computer after the 10s grace period.")
			return result, nil
		}
		requestCtx := ctx
		cancelRequest := func() {}
		if !noSamplesDeadline.IsZero() {
			requestCtx, cancelRequest = context.WithDeadline(ctx, noSamplesDeadline)
		}
		page, err := client.GetComputerPerformanceReport(requestCtx, id, cursor)
		graceExpired := requestCtx.Err() != nil && ctx.Err() == nil && !noSamplesDeadline.IsZero()
		cancelRequest()
		if err != nil {
			if watch && (ctx.Err() != nil || graceExpired) {
				continue
			}
			return result, err
		}
		result.Page = page
		if !watch {
			return result, nil
		}
		if jsonOutput {
			if err := writeComputerJSON(cmd.OutOrStdout(), page, api.ComputerMaxResponseJSON); err != nil {
				return result, err
			}
		} else {
			state, source := string(page.State), string(page.Source)
			if state != previousState || source != previousSource {
				fmt.Fprintf(cmd.ErrOrStderr(), "Computer %s: state=%s source=%s\n", id, state, source)
				if page.Summary.SampleCount == 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), computerPerformanceEmptyMessage(page.State))
				}
			}
			previousState, previousSource = state, source
			if page.Items != nil {
				for _, sample := range *page.Items {
					if err := writeComputerPerformanceSample(cmd.OutOrStdout(), sample); err != nil {
						return result, err
					}
				}
			}
		}
		cursor, _ = strconv.Atoi(page.NextCursor)
		if !computerPerformanceEnded(page.State) || page.Source != "none" {
			noSamplesDeadline = time.Time{}
		}
		if computerPerformanceEnded(page.State) {
			if page.Source == "saved" {
				result.EndReason = "ended"
				return result, nil
			}
			if page.Source == "none" {
				if noSamplesDeadline.IsZero() {
					noSamplesDeadline = time.Now().Add(10 * time.Second)
				}
			}
		}
		delay := interval
		if !noSamplesDeadline.IsZero() && time.Until(noSamplesDeadline) < delay {
			delay = max(0, time.Until(noSamplesDeadline))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func computerPerformanceEnded(state api.ComputerPerformanceReportState) bool {
	return state == "stopped" || state == "failed" || state == "owner_lost"
}

func computerPerformanceEmptyMessage(state api.ComputerPerformanceReportState) string {
	if computerPerformanceEnded(state) {
		return "No performance was saved for this computer."
	}
	return "Waiting for the first performance sample."
}

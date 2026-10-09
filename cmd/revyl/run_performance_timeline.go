package main

import (
	"context"
	"sort"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/runinspect"
)

type performancePoint struct {
	TimeSeconds float64  `json:"time_seconds"`
	CPUPercent  *float64 `json:"cpu_percent"`
	MemoryMiB   *float64 `json:"memory_mib"`
}
type performanceCapture struct {
	TimeSeconds float64 `json:"time_seconds"`
	Label       string  `json:"label"`
	Status      string  `json:"status"`
	ImageURL    string  `json:"image_url"`
}
type performanceEvidence struct {
	Points          []performancePoint   `json:"points"`
	Captures        []performanceCapture `json:"captures"`
	TotalSamples    int                  `json:"total_samples"`
	TimeReference   string               `json:"time_reference"`
	AlignmentNotice string               `json:"alignment_notice,omitempty"`
}

func performanceTimeline(ctx context.Context, client *api.Client, id, kind string, capture *runinspect.PerfCapture) (performanceEvidence, error) {
	result := performanceEvidence{Points: []performancePoint{}, Captures: []performanceCapture{}, TotalSamples: len(capture.Samples), TimeReference: "video"}
	if len(capture.Samples) == 0 {
		result.TimeReference = "capture"
		return result, nil
	}
	for _, sample := range capture.Samples {
		if sample.VideoRelativeS == nil {
			result.TimeReference = "capture"
			break
		}
	}
	selected := map[int]bool{}
	for i := 0; i < 300 && i < len(capture.Samples); i++ {
		index := i
		if len(capture.Samples) > 300 {
			index = i * (len(capture.Samples) - 1) / 299
		}
		selected[index] = true
	}
	cpuPeak, memoryPeak := -1, -1
	for i, sample := range capture.Samples {
		if sample.CPU != nil && (cpuPeak < 0 || sample.CPU.AppPercent > capture.Samples[cpuPeak].CPU.AppPercent) {
			cpuPeak = i
		}
		if sample.MemoryApp != nil && (memoryPeak < 0 || sample.MemoryApp.RSSKB > capture.Samples[memoryPeak].MemoryApp.RSSKB) {
			memoryPeak = i
		}
	}
	if cpuPeak >= 0 {
		selected[cpuPeak] = true
	}
	if memoryPeak >= 0 {
		selected[memoryPeak] = true
	}
	for index := range selected {
		sample := capture.Samples[index]
		point := performancePoint{TimeSeconds: sample.WallTimeS - capture.Samples[0].WallTimeS}
		if result.TimeReference == "video" {
			point.TimeSeconds = *sample.VideoRelativeS
		}
		if sample.CPU != nil {
			value := sample.CPU.AppPercent
			point.CPUPercent = &value
		}
		if sample.MemoryApp != nil {
			value := float64(sample.MemoryApp.RSSKB) / 1024
			point.MemoryMiB = &value
		}
		result.Points = append(result.Points, point)
	}
	sort.SliceStable(result.Points, func(i, j int) bool { return result.Points[i].TimeSeconds < result.Points[j].TimeSeconds })
	if result.TimeReference != "video" {
		return result, nil
	}
	var report *api.CLIReportContextEnvelope
	var err error
	if kind == "session" {
		report, err = client.GetReportBySession(ctx, id, true, true, false)
	} else {
		report, err = client.GetReportContextByExecution(ctx, id, true, true, false)
	}
	if err != nil {
		return result, err
	}
	if report.Report == nil || report.Report.Steps == nil {
		return result, nil
	}
	if report.Report.StartedAt != nil && report.Report.CompletedAt != nil {
		reportSeconds := report.Report.CompletedAt.Sub(*report.Report.StartedAt).Seconds()
		if reportSeconds > 0 && result.Points[len(result.Points)-1].TimeSeconds > reportSeconds+30 {
			result.TimeReference = "capture"
			result.AlignmentNotice = "Recorded metric timestamps extend beyond this report's lifetime. Metrics use time from capture start; screenshots use their separate recording clock. Do not attribute a spike to a screen."
			for i := range result.Points {
				result.Points[i].TimeSeconds -= *capture.Samples[0].VideoRelativeS
			}
		}
	}
	for _, step := range *report.Report.Steps {
		if len(result.Captures) >= 16 {
			break
		}
		if step.Actions == nil {
			continue
		}
		for _, action := range *step.Actions {
			shot := action.ScreenshotBeforeCleanUrl
			if shot == nil {
				shot = action.ScreenshotBeforeUrl
			}
			if shot == nil || action.VideoTimestampStart == nil {
				continue
			}
			result.Captures = append(result.Captures, performanceCapture{TimeSeconds: float64(*action.VideoTimestampStart), Label: truncate(stringValue(step.StepDescription), 500), Status: stringValue(step.EffectiveStatus), ImageURL: *shot})
			break
		}
	}
	sort.SliceStable(result.Captures, func(i, j int) bool { return result.Captures[i].TimeSeconds < result.Captures[j].TimeSeconds })
	return result, nil
}

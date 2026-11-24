package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	buildFailureExcerptMaxMatches   = 10
	buildFailureExcerptContextLines = 2
	buildFailureExcerptMaxLineRunes = 400
	buildFailureExcerptMaxLogPages  = 40
	buildFailureExcerptGapMarker    = "..."
)

var buildFailureLinePattern = regexp.MustCompile(`(?i)\berror:|\*\* (?:BUILD|ARCHIVE|TEST) FAILED \*\*|^FAILURE:|^BUILD FAILED|^e: |The following build commands failed:`)

var buildLogControlPrefixes = []string{"step:", "source:", "fingerprint:"}

// buildFailureExcerpt collects the first error lines of a build log, each with
// a few following lines of context, in log order.
type buildFailureExcerpt struct {
	lines        []string
	matches      int
	contextLeft  int
	lineIndex    int
	lastAddedIdx int
}

func newBuildFailureExcerpt() *buildFailureExcerpt {
	return &buildFailureExcerpt{lastAddedIdx: -1}
}

func (e *buildFailureExcerpt) add(message string) {
	message = strings.TrimRight(message, "\r\n")
	if strings.TrimSpace(message) == "" {
		return
	}
	for _, prefix := range buildLogControlPrefixes {
		if strings.HasPrefix(message, prefix) {
			return
		}
	}
	index := e.lineIndex
	e.lineIndex++

	matched := e.matches < buildFailureExcerptMaxMatches && buildFailureLinePattern.MatchString(strings.TrimSpace(message))
	switch {
	case matched:
		e.matches++
		e.contextLeft = buildFailureExcerptContextLines
	case e.contextLeft > 0:
		e.contextLeft--
	default:
		return
	}
	if e.lastAddedIdx >= 0 && index != e.lastAddedIdx+1 {
		e.lines = append(e.lines, buildFailureExcerptGapMarker)
	}
	e.lines = append(e.lines, truncateBuildLogLine(message))
	e.lastAddedIdx = index
}

func (e *buildFailureExcerpt) complete() bool {
	return e.matches >= buildFailureExcerptMaxMatches && e.contextLeft == 0
}

func extractBuildFailureExcerpt(messages []string) []string {
	excerpt := newBuildFailureExcerpt()
	for _, message := range messages {
		excerpt.add(message)
		if excerpt.complete() {
			break
		}
	}
	return excerpt.lines
}

func truncateBuildLogLine(line string) string {
	runes := []rune(line)
	if len(runes) <= buildFailureExcerptMaxLineRunes {
		return line
	}
	return string(runes[:buildFailureExcerptMaxLineRunes]) + "…"
}

// fetchRemoteBuildFailureExcerpt reads a build log from the start, a bounded
// number of pages, and returns its first error lines. When the bounded scan
// finds none before the log ends, it falls back to the error lines of the
// latest page. It returns nil when the log has none or cannot be read, since
// the excerpt is best-effort.
func fetchRemoteBuildFailureExcerpt(ctx context.Context, client *api.Client, jobID string) []string {
	excerpt := newBuildFailureExcerpt()
	cursor := "0-0"
	page := 0
	for ; page < buildFailureExcerptMaxLogPages && !excerpt.complete(); page++ {
		logs, err := client.GetRemoteBuildLogs(ctx, jobID, cursor)
		if err != nil || logs == nil || logs.Events == nil || len(*logs.Events) == 0 {
			break
		}
		for _, event := range *logs.Events {
			excerpt.add(event.Message)
			if excerpt.complete() {
				break
			}
		}
		if logs.NextCursor == nil || *logs.NextCursor == "" || *logs.NextCursor == cursor {
			break
		}
		cursor = *logs.NextCursor
	}
	if len(excerpt.lines) > 0 || page < buildFailureExcerptMaxLogPages {
		return excerpt.lines
	}
	var latest []string
	for _, event := range fetchRemoteBuildLogEvents(ctx, client, jobID) {
		latest = append(latest, event.Message)
	}
	return extractBuildFailureExcerpt(latest)
}

func printBuildFailureExcerpt(lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\n--- Build errors ---")
	for _, line := range lines {
		if line != buildFailureExcerptGapMarker && buildFailureLinePattern.MatchString(strings.TrimSpace(line)) {
			fmt.Fprintln(os.Stderr, ui.ErrorStyle.Render(line))
			continue
		}
		fmt.Fprintln(os.Stderr, line)
	}
}

func remoteBuildStatusFailed(status *api.RemoteBuildStatusResponse) bool {
	return status != nil && (status.Status == "failed" || status.Status == "timeout")
}

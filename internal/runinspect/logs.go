// Device-log parsing + filtering for the run-inspector.
//
// We capture two different formats:
//   - iOS: `log show --predicate ...` output. Header lines describe the
//     predicate, then a column header (Timestamp / Thread / Type /
//     Activity / PID / TTL), then a blank line, then data rows. Level is
//     in the Type column ("Info", "Debug", "Default", "Error", "Fault").
//   - Android: `adb logcat -v threadtime --uid=<uid>` output. Each line
//     is "MM-DD HH:MM:SS.mmm PID TID L TAG: message" where L is a single
//     letter level (V/D/I/W/E/F).
//
// The grep/level/tail filters work on parsed-line structs; the original
// text is preserved verbatim for display so the user sees the same
// content they'd see in raw logcat / `log show` output.

package runinspect

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

// LogLine is one parsed device-log entry. Level is V/D/I/W/E/F or empty.
type LogLine struct {
	Raw        string
	Level      string
	LineNumber int `json:"line_number"`
}

// LogFilter narrows a parsed log stream. Zero value matches everything.
type LogFilter struct {
	Grep   *regexp.Regexp
	Levels map[string]bool
}

// ParseLogText splits a device-logs blob into lines and extracts a
// best-effort level for each. Non-data lines (iOS header preamble,
// blank rows) are returned with Level == "" so callers can still
// display them when no --level filter is set.
func ParseLogText(raw []byte) ([]LogLine, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var out []LogLine
	seenIOSHeader := false
	for scanner.Scan() {
		line := scanner.Text()
		level := ""
		if isIOSColumnHeader(line) {
			seenIOSHeader = true
		} else if seenIOSHeader || isLikelyIOSDataLine(line) {
			level = extractIOSLevel(line)
		}
		if level == "" {
			// Fall back to Android logcat threadtime parsing.
			level = extractAndroidLevel(line)
		}
		out = append(out, LogLine{Raw: line, Level: level, LineNumber: len(out) + 1})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("device log parsing stopped before all lines were read: %w", err)
	}
	return out, nil
}

// Apply returns the subset of lines that match the filter, in order.
func (f LogFilter) Apply(lines []LogLine) []LogLine {
	if f.Grep == nil && len(f.Levels) == 0 {
		return lines
	}
	out := make([]LogLine, 0, len(lines))
	for _, l := range lines {
		if len(f.Levels) > 0 {
			if l.Level == "" || !f.Levels[l.Level] {
				continue
			}
		}
		if f.Grep != nil && !f.Grep.MatchString(l.Raw) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// TailN returns the last n lines, or the whole slice when n <= 0 or
// n > len(lines).
func TailN(lines []LogLine, n int) []LogLine {
	if n <= 0 || n >= len(lines) {
		return lines
	}
	return lines[len(lines)-n:]
}

// NormaliseLevels maps user-typed level tokens like "warn,error" or
// "warning" or "E" to the single-letter normalised set used by
// LogFilter. Unknown tokens are dropped silently — callers can warn
// from the CLI layer when they want feedback on bad input.
func NormaliseLevels(tokens []string) map[string]bool {
	out := make(map[string]bool)
	for _, raw := range tokens {
		for _, part := range strings.Split(raw, ",") {
			s := strings.ToUpper(strings.TrimSpace(part))
			if s == "" {
				continue
			}
			switch s {
			case "V", "VERBOSE", "TRACE":
				out["V"] = true
			case "D", "DEBUG":
				out["D"] = true
			case "I", "INFO", "DEFAULT":
				out["I"] = true
			case "W", "WARN", "WARNING":
				out["W"] = true
			case "E", "ERR", "ERROR":
				out["E"] = true
			case "F", "FAULT", "FATAL":
				out["F"] = true
			}
		}
	}
	return out
}

// --- internal parsing helpers --------------------------------------------

// iosLevelRE matches the Type column of `log show` output. The column
// is positional but indentation varies, so we anchor on word matches at
// the level position rather than fixed offsets. Required preceding
// whitespace prevents accidental matches inside message bodies like
// "Info plist loaded".
var iosLevelRE = regexp.MustCompile(`^\S+\s+\S+\s+\S+\s+(Info|Debug|Default|Error|Fault|Notice)\b`)

// iosTimestampRE confirms a line starts with an iOS `log show` timestamp,
// so we don't try to parse the column header or footer as data.
var iosTimestampRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+`)

// androidThreadtimeRE matches an `-v threadtime` line and captures the
// single-letter level. Example: "05-26 19:04:38.606  60855 60855 I tag: msg".
var androidThreadtimeRE = regexp.MustCompile(`^\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+\s+\d+\s+\d+\s+([VDIWEF])\s+`)

func isIOSColumnHeader(line string) bool {
	trim := strings.TrimSpace(line)
	return strings.HasPrefix(trim, "Timestamp") && strings.Contains(trim, "Thread") && strings.Contains(trim, "Type")
}

func isLikelyIOSDataLine(line string) bool {
	return iosTimestampRE.MatchString(line)
}

func extractIOSLevel(line string) string {
	if !iosTimestampRE.MatchString(line) {
		return ""
	}
	m := iosLevelRE.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	switch m[1] {
	case "Info", "Default", "Notice":
		return "I"
	case "Debug":
		return "D"
	case "Error":
		return "E"
	case "Fault":
		return "F"
	}
	return ""
}

func extractAndroidLevel(line string) string {
	m := androidThreadtimeRE.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// LogContext preserves source line numbers and merges overlapping context windows.
func LogContext(lines, matches []LogLine, contextLines int) []LogLine {
	if contextLines == 0 {
		return matches
	}
	result := []LogLine{}
	end := 0
	for _, match := range matches {
		start := max(end, match.LineNumber-1-contextLines)
		next := min(len(lines), match.LineNumber+contextLines)
		if next > start {
			result = append(result, lines[start:next]...)
			end = next
		}
	}
	return result
}

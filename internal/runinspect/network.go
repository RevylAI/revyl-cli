package runinspect

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// NetworkRequest mirrors one entry in the captured `requests` array.
type NetworkRequest struct {
	URL                   string            `json:"url"`
	Method                string            `json:"method"`
	StatusCode            int               `json:"status_code"`
	StartTimeS            float64           `json:"start_time_s"`
	VideoRelativeS        float64           `json:"video_relative_s,omitempty"`
	DurationMs            float64           `json:"duration_ms"`
	RequestBodySize       int64             `json:"request_body_size"`
	ResponseBodySize      int64             `json:"response_body_size"`
	Error                 *string           `json:"error,omitempty"`
	RequestHeaders        map[string]string `json:"request_headers,omitempty"`
	ResponseHeaders       map[string]string `json:"response_headers,omitempty"`
	RequestBodyPreview    *string           `json:"request_body_preview,omitempty"`
	ResponseBodyPreview   *string           `json:"response_body_preview,omitempty"`
	RequestBodyTruncated  json.RawMessage   `json:"request_body_truncated,omitempty"`
	ResponseBodyTruncated json.RawMessage   `json:"response_body_truncated,omitempty"`
	IsAuth                bool              `json:"is_auth,omitempty"`
}

// NetworkSummary mirrors the captured `summary` object.
type NetworkSummary struct {
	TotalRequests      int     `json:"total_requests"`
	FailedRequests     int     `json:"failed_requests"`
	TotalDurationMs    float64 `json:"total_duration_ms"`
	TotalResponseBytes int64   `json:"total_response_bytes"`
}

// NetworkCapture is the parsed top-level document.
type NetworkCapture struct {
	Requests []NetworkRequest `json:"requests"`
	Summary  NetworkSummary   `json:"summary"`
}

// ParseNetwork decodes network-capture bytes, auto-detecting the format:
// the finalized object is a single aggregated JSON document (top-level
// "requests" array + "summary"); the in-progress live object is
// per-request JSONL. Detection is structural (isAggregated), so the
// caller need not — and must not — rely on the report's partial flag to
// choose a parser. Both paths yield the same NetworkCapture.
func ParseNetwork(data []byte) (*NetworkCapture, error) {
	if isAggregated(data, "requests") {
		return ParseNetworkCapture(data)
	}
	return ParseLiveNetwork(data)
}

// ParseNetworkCapture decodes the raw JSON blob. Returns an empty
// capture (no error) for whitespace-only input so the CLI can still
// pretty-print "0 requests" rather than crash.
func ParseNetworkCapture(data []byte) (*NetworkCapture, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return &NetworkCapture{}, nil
	}
	var out NetworkCapture
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse network capture: %w", err)
	}
	return &out, nil
}

// NetworkFilter matches all requests when unset. HostGlobs accept case-insensitive
// substrings or wildcard patterns; Grep tests URLs and body previews.
type NetworkFilter struct {
	HostGlobs    []string
	Statuses     []StatusMatcher
	FailedOnly   bool
	Grep         *regexp.Regexp
	SinceSeconds *float64
	UntilSeconds *float64
}

// StatusMatcher matches a single int (Exact) or a 100-block (Bucket 1..5).
type StatusMatcher struct {
	Exact  int
	Bucket int
}

// ParseStatuses turns user input like "4xx,500" into matchers.
// Unknown tokens are ignored — CLI layer can warn.
func ParseStatuses(tokens []string) []StatusMatcher {
	var out []StatusMatcher
	for _, raw := range tokens {
		for _, part := range strings.Split(raw, ",") {
			s := strings.ToLower(strings.TrimSpace(part))
			if s == "" {
				continue
			}
			if len(s) == 3 && (s[1:] == "xx") {
				switch s[0] {
				case '1', '2', '3', '4', '5':
					out = append(out, StatusMatcher{Bucket: int(s[0] - '0')})
				}
				continue
			}
			var n int
			if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
				out = append(out, StatusMatcher{Exact: n})
			}
		}
	}
	return out
}

func (m StatusMatcher) Matches(code int) bool {
	if m.Exact > 0 {
		return code == m.Exact
	}
	if m.Bucket > 0 {
		return code/100 == m.Bucket
	}
	return false
}

// Apply returns the subset of requests that match.
func (f NetworkFilter) Apply(reqs []NetworkRequest) []NetworkRequest {
	out := make([]NetworkRequest, 0, len(reqs))
	for _, r := range reqs {
		if !f.matches(r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (f NetworkFilter) matches(r NetworkRequest) bool {
	if f.FailedOnly {
		if !(r.Error != nil || r.StatusCode >= 400) {
			return false
		}
	}
	if f.SinceSeconds != nil && r.StartTimeS < *f.SinceSeconds {
		return false
	}
	if f.UntilSeconds != nil && r.StartTimeS > *f.UntilSeconds {
		return false
	}
	if len(f.HostGlobs) > 0 {
		host := strings.ToLower(extractHost(r.URL))
		matched := false
		for _, g := range f.HostGlobs {
			if matchHost(host, g) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(f.Statuses) > 0 {
		ok := false
		for _, m := range f.Statuses {
			if m.Matches(r.StatusCode) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.Grep != nil {
		hay := r.URL
		if r.RequestBodyPreview != nil {
			hay += "\n" + *r.RequestBodyPreview
		}
		if r.ResponseBodyPreview != nil {
			hay += "\n" + *r.ResponseBodyPreview
		}
		if !f.Grep.MatchString(hay) {
			return false
		}
	}
	return true
}

// HostRollup is the default human display: one row per host with
// counts and timing.
type HostRollup struct {
	Host           string
	Count          int
	FailedCount    int
	BytesIn        int64
	TotalDurationS float64
}

// RollupByHost groups requests by URL host and returns a slice sorted
// by descending count, ties broken alphabetically.
func RollupByHost(reqs []NetworkRequest) []HostRollup {
	by := make(map[string]*HostRollup)
	for _, r := range reqs {
		host := extractHost(r.URL)
		if host == "" {
			host = "(unknown)"
		}
		row := by[host]
		if row == nil {
			row = &HostRollup{Host: host}
			by[host] = row
		}
		row.Count++
		if r.Error != nil || r.StatusCode >= 400 {
			row.FailedCount++
		}
		row.BytesIn += r.ResponseBodySize
		row.TotalDurationS += r.DurationMs / 1000.0
	}
	out := make([]HostRollup, 0, len(by))
	for _, row := range by {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Host < out[j].Host
	})
	return out
}

// extractHost returns the lowercased host portion of `url`, or "" if
// the URL is missing a scheme. We don't import net/url because some
// captured URLs have malformed schemes ("about:blank", etc) that
// url.Parse handles inconsistently; a strict scheme prefix check is
// closer to user intent.
func extractHost(rawurl string) string {
	idx := strings.Index(rawurl, "://")
	if idx < 0 {
		return ""
	}
	rest := rawurl[idx+3:]
	slash := strings.IndexByte(rest, '/')
	if slash >= 0 {
		rest = rest[:slash]
	}
	// Strip any port.
	if colon := strings.LastIndexByte(rest, ':'); colon > 0 {
		// Don't strip if the colon is inside an IPv6 literal — heuristic:
		// IPv6 hosts in URLs are wrapped in brackets, so unwrapped colons
		// are always port separators.
		if !strings.Contains(rest, "[") {
			rest = rest[:colon]
		}
	}
	return strings.ToLower(rest)
}

// matchHost compares `host` against `glob`. Globs are case-insensitive
// path.Match patterns evaluated on the host string. As a convenience,
// a glob with no wildcards is treated as a substring match — most users
// will type `--host segment` expecting "anything containing segment",
// not "exact match on the literal string segment".
func matchHost(host, glob string) bool {
	g := strings.ToLower(strings.TrimSpace(glob))
	if g == "" {
		return false
	}
	if !strings.ContainsAny(g, "*?[") {
		return strings.Contains(host, g)
	}
	ok, err := path.Match(g, host)
	if err != nil {
		return false
	}
	return ok
}

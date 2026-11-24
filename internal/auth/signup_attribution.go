package auth

import (
	"net/url"
	"regexp"
	"strings"
)

// Commands that open a browser sign-in. They name the CLI entry point on an
// account created from that sign-in, so the set stays small and fixed.
const (
	SignupCommandAuthLogin = "auth_login"
	SignupCommandInit      = "init"
)

// Sign-in flows, sent as utm_medium.
const (
	SignupFlowBrowserLogin   = "browser_login"
	SignupFlowDeviceApproval = "device_approval"
)

const unreleasedCLIVersion = "dev"

var releasedCLIVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// SignupAttribution identifies the CLI command and release that opened a
// browser sign-in. The app stores it as the first touch of an account created
// from that page, so a CLI signup is credited to the CLI.
type SignupAttribution struct {
	// Command is one of the SignupCommand constants.
	Command string

	// Version is the running CLI version; anything but a release version is
	// sent as "dev".
	Version string
}

// addSignupAttribution fills missing or empty first-touch query values.
func addSignupAttribution(query url.Values, flow string, attribution SignupAttribution) {
	version := unreleasedCLIVersion
	if releasedCLIVersionPattern.MatchString(attribution.Version) {
		version = strings.TrimPrefix(attribution.Version, "v")
	}
	for _, param := range [][2]string{
		{"utm_source", "cli"},
		{"utm_medium", flow},
		{"utm_campaign", attribution.Command},
		{"utm_content", version},
	} {
		if param[1] != "" && query.Get(param[0]) == "" {
			query.Set(param[0], param[1])
		}
	}
}

// WithSignupAttribution returns the approval URL with the CLI's first-touch
// params added, or the URL unchanged when it does not parse.
//
// Parameters:
//   - rawURL: The approval URL the backend returned.
//   - flow: One of the SignupFlow constants.
//   - attribution: The command and version that opened the sign-in.
//
// Returns:
//   - string: The URL to open and print.
func WithSignupAttribution(rawURL, flow string, attribution SignupAttribution) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	query := parsed.Query()
	addSignupAttribution(query, flow, attribution)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

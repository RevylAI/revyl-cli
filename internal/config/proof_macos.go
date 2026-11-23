package config

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	maxMacProofTargets           = 10
	minMacWebReadyTimeoutSeconds = 10
	maxMacWebReadyTimeoutSeconds = 900
)

var (
	macTargetNamePattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	macAppBundleIDPattern     = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
	macTargetCommandPattern   = regexp.MustCompile(`^[A-Za-z0-9_.+@][A-Za-z0-9_.+@/-]*$`)
	macTargetPreviewForbidden = " \t\r\n\\\x00#"
)

// IsPreview reports whether the target opens the web target's preview.
func (t AuthoredMacTarget) IsPreview() bool {
	return t.Preview != nil
}

func (t AuthoredMacTarget) kindCount() int {
	count := 0
	for _, set := range []bool{t.Web != nil, t.App != nil, t.Command != nil, t.Preview != nil} {
		if set {
			count++
		}
	}
	return count
}

// validateMacProof checks pr_review.proof_of_changes.macos against the session
// profiles and the review build its targets depend on, in the order the
// backend contract reports them: app_id, targets, then the session profile.
func (c AuthoredConfig) validateMacProof() error {
	if c.PRReview.ProofOfChanges == nil || c.PRReview.ProofOfChanges.Macos == nil {
		return nil
	}
	macos := c.PRReview.ProofOfChanges.Macos
	macosPath := []string{"pr_review", "proof_of_changes", "macos"}
	keyPath := func(keys ...string) []string {
		return append(append([]string{}, macosPath...), keys...)
	}
	if _, err := uuid.Parse(macos.AppID); err != nil {
		return authoredContractError(keyPath("app_id"), "pr_review.proof_of_changes.macos.app_id must be a UUID")
	}
	if len(macos.Targets) > maxMacProofTargets {
		return authoredContractError(keyPath("targets"), fmt.Sprintf("pr_review.proof_of_changes.macos.targets must list at most %d targets", maxMacProofTargets))
	}
	seenNames := make(map[string]struct{}, len(macos.Targets))
	everyTargetIsPreview := len(macos.Targets) > 0
	for index, target := range macos.Targets {
		indexKey := strconv.Itoa(index)
		if !macTargetNamePattern.MatchString(target.Name) {
			return authoredContractError(keyPath("targets", indexKey, "name"), fmt.Sprintf("pr_review.proof_of_changes.macos.targets[%d].name must be 1-40 lowercase letters, digits and dashes, starting with a letter or digit", index))
		}
		label := "pr_review.proof_of_changes.macos.targets[" + target.Name + "]"
		if _, repeated := seenNames[target.Name]; repeated {
			return authoredContractError(keyPath("targets", indexKey, "name"), label+".name repeats an earlier target's name")
		}
		seenNames[target.Name] = struct{}{}
		if target.kindCount() != 1 {
			return authoredContractError(keyPath("targets", indexKey), label+" must set exactly one of web, app, command, or preview")
		}
		everyTargetIsPreview = everyTargetIsPreview && target.IsPreview()
		if target.Web != nil && !validLoopbackURL(*target.Web) {
			return authoredContractError(keyPath("targets", indexKey, "web"), label+".web must be an http(s) loopback URL such as http://localhost:3000")
		}
		if timeout := target.ReadyTimeoutSeconds; timeout != nil {
			timeoutPath := keyPath("targets", indexKey, "ready_timeout_seconds")
			if target.Web == nil {
				return authoredContractError(timeoutPath, label+".ready_timeout_seconds applies only to web targets")
			}
			if *timeout < minMacWebReadyTimeoutSeconds || *timeout > maxMacWebReadyTimeoutSeconds {
				return authoredContractError(timeoutPath, fmt.Sprintf("%s.ready_timeout_seconds must be from %d to %d", label, minMacWebReadyTimeoutSeconds, maxMacWebReadyTimeoutSeconds))
			}
		}
		if target.App != nil {
			if err := validateMacApp(*target.App, label+".app", keyPath("targets", indexKey, "app")); err != nil {
				return err
			}
		}
		if target.Command != nil && !validMacTargetCommand(*target.Command) {
			return authoredContractError(keyPath("targets", indexKey, "command"), label+".command must be one program name or project-relative path, with no spaces or shell metacharacters")
		}
		if preview := target.Preview; preview != nil && (!strings.HasPrefix(*preview, "/") || strings.HasPrefix(*preview, "//") || strings.ContainsAny(*preview, macTargetPreviewForbidden)) {
			return authoredContractError(keyPath("targets", indexKey, "preview"), label+".preview must be a path that starts with /, such as /pricing")
		}
	}
	for index, target := range macos.Targets {
		if target.IsPreview() && !c.reviewBuildHasWebTarget() {
			return authoredContractError(keyPath("targets", strconv.Itoa(index), "preview"), "pr_review.proof_of_changes.macos.targets["+target.Name+"].preview needs a web target in the pr_review.build profile, or pr_review.build.app_ids.web")
		}
	}
	return c.validateMacProofSessionProfile(*macos, keyPath("session_profile"), everyTargetIsPreview)
}

func (c AuthoredConfig) validateMacProofSessionProfile(macos AuthoredProofMacos, profilePath []string, everyTargetIsPreview bool) error {
	if everyTargetIsPreview {
		return nil
	}
	profileName := ""
	if macos.SessionProfile != nil {
		profileName = *macos.SessionProfile
	} else if c.Session != nil && c.Session.DefaultProfile != nil {
		profileName = *c.Session.DefaultProfile
	}
	if macos.SessionProfile == nil && (profileName == "" || profileName == NoSessionProfile) {
		if !macProofNeedsCheckout(macos) {
			return nil
		}
		return authoredContractError(profilePath, "pr_review.proof_of_changes.macos.session_profile is required with a web, app or command target, because session.default_profile is unset or none")
	}
	var profile AuthoredSessionProfile
	found := false
	if c.Session != nil {
		profile, found = c.Session.Profiles[profileName]
	}
	if !found {
		return authoredContractError(profilePath, "pr_review.proof_of_changes.macos.session_profile must name a profile in session.profiles")
	}
	if macProofNeedsCheckout(macos) && !profile.RequiresRepositoryCheckout {
		return authoredContractError(profilePath, "pr_review.proof_of_changes.macos.session_profile must set requires_repository_checkout: true, because web, app and command targets run in the checkout")
	}
	return nil
}

// macProofNeedsCheckout reports whether a web, app or command target runs in
// the pull request's checkout.
func macProofNeedsCheckout(macos AuthoredProofMacos) bool {
	for _, target := range macos.Targets {
		if !target.IsPreview() {
			return true
		}
	}
	return false
}

func validateMacApp(app AuthoredMacApp, label string, appPath []string) error {
	keyPath := func(key string) []string {
		return append(append([]string{}, appPath...), key)
	}
	if (app.BundleID == nil) == (app.Path == nil) {
		return authoredContractError(appPath, label+" must set exactly one of bundle_id or path")
	}
	if app.BundleID != nil {
		if !macAppBundleIDPattern.MatchString(*app.BundleID) || len(*app.BundleID) > 255 {
			return authoredContractError(keyPath("bundle_id"), label+".bundle_id must be a bundle ID such as com.example.App")
		}
		if app.Build != nil {
			return authoredContractError(keyPath("build"), label+".build needs app.path: it builds the .app that path names")
		}
		return nil
	}
	appBundlePath := *app.Path
	if appBundlePath == "" || strings.TrimSpace(appBundlePath) != appBundlePath || strings.HasPrefix(appBundlePath, "/") || strings.HasPrefix(appBundlePath, "~") ||
		strings.ContainsAny(appBundlePath, "\\\x00") || path.Clean(appBundlePath) != strings.TrimSuffix(appBundlePath, "/") || appBundlePath == ".." || strings.HasPrefix(appBundlePath, "../") {
		return authoredContractError(keyPath("path"), label+".path must stay relative to the project root")
	}
	if !strings.HasSuffix(strings.TrimSuffix(appBundlePath, "/"), ".app") {
		return authoredContractError(keyPath("path"), label+".path must end in .app")
	}
	if app.Build != nil && (*app.Build == "" || strings.TrimSpace(*app.Build) != *app.Build || strings.ContainsRune(*app.Build, 0)) {
		return authoredContractError(keyPath("build"), label+".build must be a non-empty, trimmed command")
	}
	return nil
}

// validMacTargetCommand accepts one token: a name on PATH or a path relative
// to the project root, with no shell metacharacters or parent segments.
func validMacTargetCommand(command string) bool {
	if !macTargetCommandPattern.MatchString(command) || strings.HasSuffix(command, "/") {
		return false
	}
	for _, segment := range strings.Split(command, "/") {
		if segment == "" || segment == ".." {
			return false
		}
	}
	return true
}

// validLoopbackURL reports whether value is an http or https URL on
// localhost, 127.0.0.1 or [::1], without credentials.
func validLoopbackURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Opaque != "" {
		return false
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return false
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		return err == nil && number >= 1 && number <= 65535
	}
	return !strings.HasSuffix(parsed.Host, ":")
}

// HasMacProof reports whether the review sets a Mac proof, the only proof a
// review without a build runs.
func (review AuthoredPRReview) HasMacProof() bool {
	return review.ProofOfChanges != nil && review.ProofOfChanges.Macos != nil
}

func (c AuthoredConfig) reviewBuildHasWebTarget() bool {
	build := c.PRReview.Build
	if build == nil {
		return false
	}
	if build.Kind == "ci_upload_to_revyl" {
		return build.AppIDs != nil && build.AppIDs.Web != nil
	}
	return c.Build != nil && build.Profile != nil && c.Build.Profiles[*build.Profile].Web != nil
}

func cloneAuthoredProofMacos(macos *AuthoredProofMacos) *AuthoredProofMacos {
	if macos == nil {
		return nil
	}
	targets := make([]AuthoredMacTarget, 0, len(macos.Targets))
	for _, target := range macos.Targets {
		clone := AuthoredMacTarget{Name: target.Name, Web: cloneStringPointer(target.Web), Command: cloneStringPointer(target.Command), Preview: cloneStringPointer(target.Preview)}
		if target.ReadyTimeoutSeconds != nil {
			timeout := *target.ReadyTimeoutSeconds
			clone.ReadyTimeoutSeconds = &timeout
		}
		if target.App != nil {
			clone.App = &AuthoredMacApp{BundleID: cloneStringPointer(target.App.BundleID), Path: cloneStringPointer(target.App.Path), Build: cloneStringPointer(target.App.Build)}
		}
		targets = append(targets, clone)
	}
	return &AuthoredProofMacos{AppID: macos.AppID, SessionProfile: cloneStringPointer(macos.SessionProfile), Targets: targets}
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

const (
	buildDownloadDefaultTimeout        = 30 * time.Minute
	buildDownloadResponseHeaderTimeout = time.Minute
)

var buildDownloadCmd = &cobra.Command{
	Use:   "download <build-version-id>",
	Short: "Download a build version's artifact",
	Long: `Download the artifact of one uploaded or cloud-built build version.

The file is saved under the artifact's own file name in the current directory,
or at --out (a file path, or an existing directory to save into). An existing
file is kept unless --force is passed. The file is written to a temporary name
and moved into place only once it is complete. When the build has a recorded
SHA-256 checksum, the download is verified against it and discarded on a
mismatch.

The download link is a short-lived credential and is never printed unless you
pass --print-url. Find build version IDs with "revyl build list --app <id>".`,
	Example: `  revyl build download <build-version-id>
  revyl build download <build-version-id> --out ./artifacts/
  revyl build download <build-version-id> --out app.apk --force --json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runBuildDownload,
}

type buildDownloadOutput struct {
	BuildVersionID string `json:"build_version_id"`
	Version        string `json:"version,omitempty"`
	Path           string `json:"path"`
	Bytes          int64  `json:"bytes"`
	SHA256         string `json:"sha256"`
	SHA256Verified bool   `json:"sha256_verified"`
	DownloadURL    string `json:"download_url,omitempty"`
}

func init() {
	addBuildDownloadFlags(buildDownloadCmd)
	buildCmd.AddCommand(buildDownloadCmd)
}

func addBuildDownloadFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("out", "o", "", "File path or existing directory to save the artifact to (default: the artifact's file name in the current directory)")
	cmd.Flags().Bool("force", false, "Overwrite an existing file at the destination")
	cmd.Flags().Bool("print-url", false, "Also print the short-lived signed download URL (treat it as a credential)")
	cmd.Flags().Duration("timeout", buildDownloadDefaultTimeout, "Maximum time for the whole download")
	cmd.Flags().Bool("json", false, "Output the result as JSON")
}

func runBuildDownload(cmd *cobra.Command, args []string) error {
	versionID := strings.TrimSpace(args[0])
	if versionID == "" {
		return fmt.Errorf("build version ID is required")
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}
	outFlag, _ := cmd.Flags().GetString("out")
	force, _ := cmd.Flags().GetBool("force")
	printURL, _ := cmd.Flags().GetBool("print-url")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	if jsonOutput {
		ui.SetQuietMode(true)
		defer ui.SetQuietMode(false)
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	detail, err := client.GetBuildVersionDetail(cmd.Context(), versionID, true)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("build version %s not found; run 'revyl build list --app <id>' to see build version IDs", versionID)
		}
		return fmt.Errorf("failed to get a download link for build version %s: %w", versionID, err)
	}

	result, err := downloadBuildArtifact(cmd, versionID, detail, outFlag, force, timeout)
	if err != nil {
		return err
	}
	result.BuildVersionID = versionID
	result.Version = detail.Version

	if printURL {
		ui.PrintWarning("The download URL is a temporary credential: anyone holding it can download this build until it expires. Do not share, commit, or log it.")
		result.DownloadURL = detail.DownloadURL
	}
	if jsonOutput {
		return encodeJSON(result)
	}
	ui.PrintSuccess("Downloaded build version %s to %s (%s)", versionID, result.Path, formatBytes(result.Bytes))
	if result.SHA256Verified {
		ui.PrintKeyValue("SHA-256:", result.SHA256+" (verified)")
	} else {
		ui.PrintKeyValue("SHA-256:", result.SHA256+" (not verified: no checksum is recorded for this build)")
	}
	if printURL {
		fmt.Fprintln(os.Stdout, result.DownloadURL)
	}
	return nil
}

func downloadBuildArtifact(cmd *cobra.Command, versionID string, detail *api.BuildVersionDetail, outFlag string, force bool, timeout time.Duration) (buildDownloadOutput, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = buildDownloadResponseHeaderTimeout
	httpClient := &http.Client{Timeout: timeout, Transport: transport}

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, detail.DownloadURL, nil)
	if err != nil {
		return buildDownloadOutput{}, fmt.Errorf("the download link for build version %s is invalid", versionID)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return buildDownloadOutput{}, fmt.Errorf("failed to download build version %s: %w", versionID, withoutRequestURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return buildDownloadOutput{}, fmt.Errorf("failed to download build version %s: artifact storage answered HTTP %d; rerun the command to request a fresh download link", versionID, resp.StatusCode)
	}

	destination, err := resolveBuildDownloadPath(outFlag, buildArtifactFileName(resp, versionID))
	if err != nil {
		return buildDownloadOutput{}, err
	}
	if _, statErr := os.Stat(destination); statErr == nil && !force {
		return buildDownloadOutput{}, fmt.Errorf("%s already exists; pass --force to overwrite it or --out to choose another path", destination)
	}

	partial, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".*.part")
	if err != nil {
		return buildDownloadOutput{}, fmt.Errorf("failed to create a file next to %s: %w", destination, err)
	}
	partialPath := partial.Name()
	committed := false
	defer func() {
		if !committed {
			_ = partial.Close()
			_ = os.Remove(partialPath)
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(partial, hasher), resp.Body)
	if err != nil {
		return buildDownloadOutput{}, fmt.Errorf("failed to download build version %s: %w", versionID, withoutRequestURL(err))
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return buildDownloadOutput{}, fmt.Errorf("failed to download build version %s: received %d of %d bytes; rerun the command", versionID, written, resp.ContentLength)
	}
	if err := partial.Close(); err != nil {
		return buildDownloadOutput{}, fmt.Errorf("failed to write %s: %w", destination, err)
	}

	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	expectedSHA256 := recordedArtifactSHA256(detail.Metadata)
	if expectedSHA256 != "" && expectedSHA256 != actualSHA256 {
		return buildDownloadOutput{}, fmt.Errorf("downloaded artifact of build version %s does not match its recorded SHA-256 (expected %s, got %s); the file was discarded, rerun the command", versionID, expectedSHA256, actualSHA256)
	}
	if force {
		err = os.Rename(partialPath, destination)
	} else if err = os.Link(partialPath, destination); err == nil {
		_ = os.Remove(partialPath)
	} else if errors.Is(err, fs.ErrExist) {
		return buildDownloadOutput{}, fmt.Errorf("%s already exists; pass --force to overwrite it or --out to choose another path", destination)
	} else if _, statErr := os.Lstat(destination); errors.Is(statErr, fs.ErrNotExist) {
		err = os.Rename(partialPath, destination)
	}
	if err != nil {
		return buildDownloadOutput{}, fmt.Errorf("failed to save %s: %w", destination, err)
	}
	committed = true

	return buildDownloadOutput{
		Path:           destination,
		Bytes:          written,
		SHA256:         actualSHA256,
		SHA256Verified: expectedSHA256 != "",
	}, nil
}

func resolveBuildDownloadPath(outFlag, artifactName string) (string, error) {
	outFlag = strings.TrimSpace(outFlag)
	if outFlag == "" {
		return artifactName, nil
	}
	if info, err := os.Stat(outFlag); err == nil && info.IsDir() {
		return filepath.Join(outFlag, artifactName), nil
	}
	if strings.HasSuffix(outFlag, "/") || strings.HasSuffix(outFlag, string(os.PathSeparator)) {
		return "", fmt.Errorf("directory %s does not exist; create it first or pass a file path to --out", outFlag)
	}
	return outFlag, nil
}

func buildArtifactFileName(resp *http.Response, versionID string) string {
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		if name := sanitizeArtifactFileName(params["filename"]); name != "" {
			return name
		}
	}
	if resp.Request != nil && resp.Request.URL != nil {
		if name := sanitizeArtifactFileName(path.Base(resp.Request.URL.Path)); name != "" {
			return name
		}
	}
	if name := sanitizeArtifactFileName("build-" + versionID); name != "" {
		return name
	}
	return "build-artifact"
}

func sanitizeArtifactFileName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	return strings.Trim(name, " .")
}

func recordedArtifactSHA256(metadata map[string]interface{}) string {
	value, ok := metadata["artifact_sha256"].(string)
	if !ok {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	if len(normalized) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return ""
	}
	return normalized
}

func withoutRequestURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

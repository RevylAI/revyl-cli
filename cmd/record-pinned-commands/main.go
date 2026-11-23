// Command record-pinned-commands makes internal/pinnedskills/commands/ hold the
// command tree of exactly the CLI releases the plugins pin, read from
// plugins/revyl/runtime-manifest.json and cursor-plugin/runtime-manifest.json.
// For each pinned release without a usable record it downloads the published
// binary for this machine, verifies it against the release's checksums.txt,
// and records the tree from the binary's `schema --format json` output. It
// deletes the records of releases no plugin pins and leaves the other records
// unchanged. With --check it only reports records that are missing, empty,
// malformed or unpinned; it does not download releases to compare content.
//
// Usage: record-pinned-commands [--check] [--root <revyl-cli dir>]
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/revyl/cli/internal/pinnedskills"
)

const (
	releaseDownloadURL    = "https://github.com/RevylAI/revyl-cli/releases/download"
	downloadTimeout       = 5 * time.Minute
	schemaCommandTimeout  = 2 * time.Minute
	maxDownloadBytes      = 512 << 20
	commandsDirectoryPath = "internal/pinnedskills/commands"
)

var (
	releaseTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	runtimeManifests  = []string{"plugins/revyl/runtime-manifest.json", "cursor-plugin/runtime-manifest.json"}
)

func main() {
	check := flag.Bool("check", false, "report missing or stale records without writing")
	root := flag.String("root", ".", "revyl-cli directory")
	flag.Parse()
	tags, err := pinnedReleaseTags(*root)
	if err == nil {
		commandsDir := filepath.Join(*root, commandsDirectoryPath)
		if *check {
			err = checkCommandTrees(commandsDir, tags)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			err = recordCommandTrees(ctx, &http.Client{Timeout: downloadTimeout}, releaseDownloadURL, commandsDir, tags)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "record pinned commands: %v\n", err)
		os.Exit(1)
	}
}

func pinnedReleaseTags(root string) ([]string, error) {
	cliPackage, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer cliPackage.Close()
	var tags []string
	for _, manifest := range runtimeManifests {
		content, err := cliPackage.ReadFile(manifest)
		if err != nil {
			return nil, err
		}
		var pin struct {
			ReleaseTag string `json:"release_tag"`
		}
		if err := json.Unmarshal(content, &pin); err != nil {
			return nil, fmt.Errorf("read %s: %w", manifest, err)
		}
		if !releaseTagPattern.MatchString(pin.ReleaseTag) {
			return nil, fmt.Errorf("%s pins %q, which is not a release tag such as v0.2.34", manifest, pin.ReleaseTag)
		}
		if !slices.Contains(tags, pin.ReleaseTag) {
			tags = append(tags, pin.ReleaseTag)
		}
	}
	return tags, nil
}

func recordedTags(commands *os.Root) ([]string, error) {
	entries, err := fs.ReadDir(commands.FS(), ".")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, entry := range entries {
		if tag, ok := strings.CutSuffix(entry.Name(), ".txt"); ok && !entry.IsDir() {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

// parseRecord reads the record of tag and fails unless it is a usable
// command tree.
func parseRecord(commands *os.Root, tag string) error {
	data, err := commands.ReadFile(tag + ".txt")
	if err != nil {
		return err
	}
	_, err = pinnedskills.ParseCommandTree(tag, data)
	return err
}

func checkCommandTrees(commandsDir string, tags []string) error {
	commands, err := os.OpenRoot(commandsDir)
	if err != nil {
		return err
	}
	defer commands.Close()
	recorded, err := recordedTags(commands)
	if err != nil {
		return err
	}
	var problems []string
	for _, tag := range tags {
		if !slices.Contains(recorded, tag) {
			problems = append(problems, "no record for pinned release "+tag)
		} else if err := parseRecord(commands, tag); err != nil {
			problems = append(problems, fmt.Sprintf("an unusable record for pinned release %s (%v)", tag, err))
		}
	}
	for _, tag := range recorded {
		if !slices.Contains(tags, tag) {
			problems = append(problems, "a record for "+tag+", which no plugin pins")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s has %s; run make -C revyl-cli record-pinned-commands", commandsDirectoryPath, strings.Join(problems, " and "))
	}
	return nil
}

func recordCommandTrees(ctx context.Context, client *http.Client, baseURL, commandsDir string, tags []string) error {
	commands, err := os.OpenRoot(commandsDir)
	if err != nil {
		return err
	}
	defer commands.Close()
	for _, tag := range tags {
		if !releaseTagPattern.MatchString(tag) {
			return fmt.Errorf("%q is not a release tag such as v0.2.34", tag)
		}
		if err := parseRecord(commands, tag); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			fmt.Printf("re-recording %s/%s.txt: %v\n", commandsDirectoryPath, tag, err)
		}
		text, err := releaseCommandTree(ctx, client, baseURL, tag)
		if err != nil {
			return fmt.Errorf("record %s: %w", tag, err)
		}
		if err := commands.WriteFile(tag+".txt", text, 0o644); err != nil {
			return err
		}
		fmt.Printf("recorded %s/%s.txt\n", commandsDirectoryPath, tag)
	}
	recorded, err := recordedTags(commands)
	if err != nil {
		return err
	}
	for _, tag := range recorded {
		if !slices.Contains(tags, tag) {
			if err := commands.Remove(tag + ".txt"); err != nil {
				return err
			}
			fmt.Printf("removed %s/%s.txt\n", commandsDirectoryPath, tag)
		}
	}
	return nil
}

func releaseCommandTree(ctx context.Context, client *http.Client, baseURL, tag string) ([]byte, error) {
	asset := fmt.Sprintf("revyl-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	checksums, err := download(ctx, client, baseURL+"/"+tag+"/checksums.txt")
	if err != nil {
		return nil, err
	}
	want, err := assetChecksum(checksums, asset)
	if err != nil {
		return nil, err
	}
	binary, err := download(ctx, client, baseURL+"/"+tag+"/"+asset)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(binary); hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("%s does not match its checksums.txt entry", asset)
	}
	home, err := os.MkdirTemp("", "revyl-pinned-runtime-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	executable := filepath.Join(home, asset)
	// #nosec G306 -- the verified release binary needs its execute bit; only its owner can read or run it, in a private temporary directory.
	if err := os.WriteFile(executable, binary, 0o700); err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, schemaCommandTimeout)
	defer cancel()
	// #nosec G204 -- the executable is the release binary verified against checksums.txt above, run with fixed arguments and no shell.
	command := exec.CommandContext(commandCtx, executable, "schema", "--format", "json")
	// An empty home keeps the binary away from this machine's Revyl sign-in and config.
	command.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "REVYL_API_KEY=")
	command.Stderr = os.Stderr
	schema, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%s schema --format json: %w", asset, err)
	}
	return pinnedskills.CommandTreeText(schema)
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxDownloadBytes {
		return nil, fmt.Errorf("GET %s: the response is larger than %d bytes", url, maxDownloadBytes)
	}
	return content, nil
}

func assetChecksum(checksums []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", asset)
}

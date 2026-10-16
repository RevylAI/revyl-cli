package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
)

const maxWebBundleBytes int64 = 200 * 1024 * 1024
const maxWebBundleEntries = 20000

var webBuildCommitSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func webBuildSourceCommitSHA(ctx context.Context, worktreeRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	command.Dir = worktreeRoot
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("Web builds require a source commit; commit the project before building: %w", err)
	}
	commitSHA := strings.TrimSpace(string(output))
	if !webBuildCommitSHA.MatchString(commitSHA) {
		return "", fmt.Errorf("Web build source revision must be a full 40- or 64-character lowercase Git commit SHA")
	}
	return commitSHA, nil
}

func uploadLocalWebBuild(cmd *cobra.Command, invocation projectBuildInvocation, outputPath string, duration time.Duration, client *api.Client, pin bool) (*api.UploadBuildResponse, error) {
	relativeOutput, err := filepath.Rel(invocation.ProjectRoot, outputPath)
	if err != nil {
		return nil, err
	}
	archive, web, err := packWebBuildOutput(cmd.Context(), invocation.ProjectRoot, relativeOutput)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archive)
	web.Run = managedWebRun(invocation.Recipe.Run)
	if web.Run.Start == nil {
		index, err := os.Stat(filepath.Join(outputPath, "index.html"))
		if err != nil || !index.Mode().IsRegular() {
			return nil, fmt.Errorf("static web output must contain index.html; set run.start for a server-rendered app")
		}
	}
	version := buildVersion
	if version == "" {
		version = build.GenerateVersionStringForWorkDir(invocation.ProjectRoot)
	}
	metadata := build.CollectMetadataWithContext(cmd.Context(), invocation.ProjectRoot, config.CommandSummary(invocation.Recipe.BuildCommands), "web", duration)
	if _, exists := metadata["commit_sha"]; !exists {
		if git, ok := metadata["git"].(map[string]interface{}); ok {
			if commit, ok := git["commit"].(string); ok && commit != "" {
				metadata["commit_sha"] = commit
			}
		}
		if _, exists := metadata["commit_sha"]; !exists {
			if ci, ok := build.CIContextFromContext(cmd.Context()); ok && ci.CommitSHA != "" {
				metadata["commit_sha"] = ci.CommitSHA
			}
		}
	}
	result, err := client.UploadManagedWebBuild(cmd.Context(), &api.UploadBuildRequest{
		BranchID: invocation.BranchID, BranchName: invocation.BranchName,
		AppID: invocation.AppID, Version: version, FilePath: archive, Metadata: metadata, Pin: pin,
	}, web)
	if err != nil {
		return nil, fmt.Errorf("upload Web bundle: %w", err)
	}
	return result, nil
}

func managedWebRun(run *config.AuthoredWebRun) api.ManagedWebRun {
	value := config.DefaultWebRun()
	if run != nil {
		value = *config.CloneWebRun(run)
	}
	return api.ManagedWebRun{Start: value.Start, Port: value.Port, Healthcheck: value.Healthcheck, StartTimeoutSeconds: value.StartTimeoutSeconds, EnvKeys: value.EnvKeys, SecretNames: value.SecretNames}
}

func packWebBuildOutput(ctx context.Context, projectRoot, output string) (string, api.ManagedWebBuildMetadata, error) {
	return packWebBuildOutputWithLimits(ctx, projectRoot, output, maxWebBundleBytes, maxWebBundleEntries)
}

func packWebBuildOutputWithLimits(ctx context.Context, projectRoot, output string, maxBytes int64, maxEntries int) (archivePath string, metadata api.ManagedWebBuildMetadata, returnErr error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output = filepath.ToSlash(output)
	if output == "" || filepath.IsAbs(output) || strings.ContainsAny(output, `\*?[]:`) || filepath.Clean(output) == "." {
		return "", metadata, fmt.Errorf("web output_path must name one dedicated relative output directory")
	}
	for _, component := range strings.Split(output, "/") {
		if component == ".." || webBundleSensitivePath(component) {
			return "", metadata, fmt.Errorf("web output_path contains an unsafe path component")
		}
	}
	absoluteRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return "", metadata, err
	}
	outputPath := filepath.Join(absoluteRoot, filepath.FromSlash(output))
	realOutput, err := filepath.EvalSymlinks(outputPath)
	if err != nil {
		return "", metadata, fmt.Errorf("resolve web output directory: %w", err)
	}
	if realOutput != outputPath {
		return "", metadata, fmt.Errorf("web output_path must not contain symlinks")
	}
	root, err := os.OpenRoot(outputPath)
	if err != nil {
		return "", metadata, fmt.Errorf("open web output directory: %w", err)
	}
	defer root.Close()
	archive, err := os.CreateTemp("", "revyl-web-*.tar.zst")
	if err != nil {
		return "", metadata, err
	}
	archivePath = archive.Name()
	defer func() {
		archive.Close()
		if returnErr != nil {
			os.Remove(archive.Name())
		}
	}()
	hash := sha256.New()
	compressed := &webBundleLimitedWriter{writer: io.MultiWriter(archive, hash), remaining: maxBytes}
	encoder, err := zstd.NewWriter(compressed, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(64*1024))
	if err != nil {
		return "", metadata, err
	}
	defer encoder.Close()
	tarWriter := tar.NewWriter(encoder)
	defer tarWriter.Close()
	var totalBytes int64
	entries, files := 0, 0
	err = filepath.WalkDir(outputPath, func(fullPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if fullPath == outputPath {
			return nil
		}
		name, err := filepath.Rel(outputPath, fullPath)
		if err != nil {
			return err
		}
		if len(filepath.ToSlash(name)) > 1024 || strings.ContainsAny(filepath.ToSlash(name), "\\\x00\r\n:") {
			return fmt.Errorf("web output archive paths must not exceed 1024 bytes")
		}
		if webBundleSensitivePath(entry.Name()) {
			return fmt.Errorf("web output contains forbidden source or secret file %q", filepath.ToSlash(name))
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("web output must not contain symlinks or special files: %q", filepath.ToSlash(name))
		}
		entries++
		if entries > maxEntries || info.Size() > maxBytes-totalBytes && !info.IsDir() {
			return fmt.Errorf("web output exceeds the archive size or entry limit")
		}
		header := &tar.Header{Name: filepath.ToSlash(name), Mode: int64(info.Mode().Perm()), ModTime: time.Unix(0, 0), Typeflag: tar.TypeDir}
		if info.IsDir() {
			return tarWriter.WriteHeader(header)
		}
		files++
		totalBytes += info.Size()
		header.Typeflag, header.Size = tar.TypeReg, info.Size()
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		openedInfo, err := file.Stat()
		if err != nil || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
			return fmt.Errorf("web output changed while packing %q", filepath.ToSlash(name))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		_, err = io.CopyN(tarWriter, file, info.Size())
		return err
	})
	if err != nil {
		return "", metadata, fmt.Errorf("pack web output: %w", err)
	}
	if files == 0 {
		return "", metadata, fmt.Errorf("web output directory is empty")
	}
	if err := tarWriter.Close(); err != nil {
		return "", metadata, err
	}
	if err := encoder.Close(); err != nil {
		return "", metadata, err
	}
	if err := archive.Close(); err != nil {
		return "", metadata, err
	}
	metadata = api.ManagedWebBuildMetadata{Artifact: "tarball", SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: maxBytes - compressed.remaining}
	return archivePath, metadata, nil
}

func webBundleSensitivePath(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case ".git", ".gitconfig", ".git-credentials", ".hg", ".svn", ".revyl", ".env", ".dev.vars", ".envrc", ".npmrc", ".netrc", ".pypirc", ".aws", ".ssh", ".gnupg":
		return true
	default:
		return strings.HasPrefix(name, ".env.") || strings.HasPrefix(name, ".dev.vars.")
	}
}

type webBundleLimitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *webBundleLimitedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, fmt.Errorf("compressed web bundle exceeds the archive size limit")
	}
	written, err := w.writer.Write(data)
	w.remaining -= int64(written)
	return written, err
}

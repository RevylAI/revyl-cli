package api

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/google/uuid"
)

type ManagedWebRun struct {
	Start               *string  `json:"start,omitempty"`
	Port                int      `json:"port"`
	Healthcheck         string   `json:"healthcheck"`
	StartTimeoutSeconds int      `json:"start_timeout_seconds"`
	EnvKeys             []string `json:"env_keys"`
	SecretNames         []string `json:"secret_names"`
}

type ManagedWebBuildMetadata struct {
	Artifact  string        `json:"artifact"`
	Run       ManagedWebRun `json:"run"`
	SHA256    string        `json:"sha256"`
	SizeBytes int64         `json:"size_bytes"`
}

func generatedWebRun(run ManagedWebRun) *WebBuildRun {
	return &WebBuildRun{
		Start: run.Start, Port: &run.Port, Healthcheck: &run.Healthcheck,
		StartTimeoutSeconds: &run.StartTimeoutSeconds,
		EnvKeys:             &run.EnvKeys, SecretNames: &run.SecretNames,
	}
}

type managedWebUploadURLResponse struct {
	VersionID   string `json:"version_id"`
	Version     string `json:"version"`
	UploadURL   string `json:"upload_url"`
	ContentType string `json:"content_type"`
}

func (c *Client) UploadManagedWebBuild(ctx context.Context, request *UploadBuildRequest, web ManagedWebBuildMetadata) (*UploadBuildResponse, error) {
	if request == nil {
		return nil, fmt.Errorf("managed Web upload request is required")
	}
	fileInfo, err := os.Stat(request.FilePath)
	if err != nil {
		return nil, err
	}
	if fileInfo.Size() != web.SizeBytes {
		return nil, fmt.Errorf("web bundle changed before upload")
	}
	if err := validateGitBranchName(request.GitBranchName); err != nil {
		return nil, err
	}
	if err := validateBranchSelector(request.BranchID, request.BranchName); err != nil {
		return nil, err
	}
	query := url.Values{"version": {request.Version}, "file_name": {"bundle.tar.zst"}, "source": {"cli_upload"}}
	if request.BranchID != "" {
		query.Set("branch_id", request.BranchID)
	}
	if request.BranchName != "" {
		query.Set("branch_name", request.BranchName)
	}
	if request.GitBranchName != "" {
		query.Set("git_branch_name", request.GitBranchName)
	}
	path := fmt.Sprintf("/api/v1/apps/%s/builds/upload-url?%s", url.PathEscape(request.AppID), query.Encode())
	response, err := c.doRequestOnce(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	var upload managedWebUploadURLResponse
	if err := parseResponse(response, &upload); err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(upload.VersionID)
	if err != nil {
		return nil, fmt.Errorf("invalid web upload version id: %w", err)
	}
	if err := c.uploadFileWithRetry(ctx, upload.UploadURL, upload.ContentType, request.FilePath, web.SizeBytes); err != nil {
		c.bestEffortDeleteBuildVersion(upload.VersionID)
		return nil, err
	}
	complete := BuildUploadCompleteRequest{
		VersionId: versionID, Metadata: &request.Metadata,
		Web: &WebBuildMetadata{Artifact: &web.Artifact, Run: generatedWebRun(web.Run), Sha256: web.SHA256, SizeBytes: int(web.SizeBytes)},
	}
	if request.Pin {
		complete.Pin = &request.Pin
	}
	finalizationClient := *c.httpClient
	finalizationClient.Timeout = BuildFinalizationTimeout
	response, err = c.doRequestOnceWithClient(ctx, "POST", fmt.Sprintf("/api/v1/apps/builds/%s/complete-upload", upload.VersionID), &complete, &finalizationClient)
	if err != nil {
		return nil, err
	}
	var result UploadBuildResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	if result.VersionID == "" {
		result.VersionID = upload.VersionID
	}
	if result.Version == "" {
		result.Version = upload.Version
	}
	return &result, nil
}

func (c *Client) TriggerManagedWebBuild(ctx context.Context, request *RemoteBuildRequest, run ManagedWebRun, commitSHA string, timeoutSeconds *int) (*RemoteBuildTriggerResponse, error) {
	if request == nil || request.Recipe == nil {
		return nil, fmt.Errorf("managed Web build recipe is required")
	}
	payload := *request
	recipe := *request.Recipe
	recipe.Run = generatedWebRun(run)
	payload.Recipe = &recipe
	source, err := request.Source.ValueByDiscriminator()
	if err != nil {
		return nil, err
	}
	switch value := source.(type) {
	case RemoteBuildArchiveSource:
		value.CommitSha = &commitSHA
		err = payload.Source.FromRemoteBuildArchiveSource(value)
	case RemoteBuildGitSource:
		value.CommitSha = &commitSHA
		err = payload.Source.FromRemoteBuildGitSource(value)
	default:
		return nil, fmt.Errorf("managed Web build source is unsupported")
	}
	if err != nil {
		return nil, err
	}
	if timeoutSeconds != nil {
		payload.TimeoutSeconds = timeoutSeconds
	}
	response, err := c.doRequest(ctx, "POST", "/api/v1/apps/remote", &payload)
	if err != nil {
		return nil, err
	}
	var result RemoteBuildTriggerResponse
	if err := parseResponse(response, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

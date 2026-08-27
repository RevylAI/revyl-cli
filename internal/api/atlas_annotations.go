package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) ListAtlasAnnotationFeedback(
	ctx context.Context,
	appID string,
	observationID string,
	status string,
	cursor string,
	limit int,
) (*AtlasAnnotationFeedbackResponse, error) {
	values := url.Values{}
	values.Set("app_id", appID)
	if observationID != "" {
		values.Set("observation_id", observationID)
	}
	if status != "" {
		values.Set("status", status)
	}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/v1/atlas/v2/annotations/feedback?" + values.Encode()
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationFeedbackResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetAtlasAnnotationThread(
	ctx context.Context,
	appID string,
	threadID string,
) (*AtlasAnnotationThreadResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-threads/%s",
		url.PathEscape(appID),
		url.PathEscape(threadID),
	)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationThreadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetAtlasAnnotationComment(
	ctx context.Context,
	appID string,
	commentID string,
) (*AtlasAnnotationComment, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-comments/%s",
		url.PathEscape(appID),
		url.PathEscape(commentID),
	)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationComment
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListAtlasObservationAnnotationThreads(
	ctx context.Context,
	appID string,
	observationID string,
) (*AtlasAnnotationObservationThreadsResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/observations/%s/annotation-threads",
		url.PathEscape(appID),
		url.PathEscape(observationID),
	)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationObservationThreadsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) PreviewAtlasAnnotationAnchor(
	ctx context.Context,
	appID string,
	observationID string,
	req *AtlasAnnotationAnchorPreviewRequest,
) (*AtlasAnnotationAnchorPreviewResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/observations/%s/annotation-anchor-preview",
		url.PathEscape(appID),
		url.PathEscape(observationID),
	)
	resp, err := c.doRequest(ctx, http.MethodPost, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationAnchorPreviewResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListAtlasAnnotationMembers(
	ctx context.Context,
	query string,
	limit int,
) (*OrganizationMembersResponse, error) {
	values := url.Values{}
	if strings.TrimSpace(query) != "" {
		values.Set("q", strings.TrimSpace(query))
	}
	values.Set("limit", strconv.Itoa(limit))
	path := "/api/v1/entity/orgs/members?" + values.Encode()
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var result OrganizationMembersResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CreateGroundedAtlasAnnotationThread(
	ctx context.Context,
	appID string,
	observationID string,
	req *AtlasGroundedAnnotationThreadCreateRequest,
) (*AtlasGroundedAnnotationThreadCreateResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/observations/%s/grounded-annotation-threads",
		url.PathEscape(appID),
		url.PathEscape(observationID),
	)
	resp, err := c.doRequest(ctx, http.MethodPost, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasGroundedAnnotationThreadCreateResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeclareAtlasAnnotationAttachmentUpload(
	ctx context.Context,
	appID string,
	req *AtlasAttachmentUploadRequest,
) (*AtlasAttachmentUploadResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-attachments/uploads",
		url.PathEscape(appID),
	)
	resp, err := c.doRequest(ctx, http.MethodPost, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasAttachmentUploadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CompleteAtlasAnnotationAttachmentUpload(
	ctx context.Context,
	appID string,
	attachmentID string,
) (*AtlasAttachment, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-attachments/%s/complete",
		url.PathEscape(appID),
		url.PathEscape(attachmentID),
	)
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasAttachment
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) AddAtlasAnnotationReply(
	ctx context.Context,
	appID string,
	threadID string,
	req *AtlasAnnotationReplyRequest,
) (*AtlasAnnotationCommentResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-threads/%s/replies",
		url.PathEscape(appID),
		url.PathEscape(threadID),
	)
	resp, err := c.doRequest(ctx, http.MethodPost, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationCommentResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ChangeAtlasAnnotationStatus(
	ctx context.Context,
	appID string,
	threadID string,
	action string,
	req *AtlasAnnotationStatusChangeRequest,
) (*AtlasAnnotationThreadResponse, error) {
	var path string
	switch action {
	case "resolve":
		path = fmt.Sprintf("/api/v1/atlas/v2/apps/%s/annotation-threads/%s/resolve", url.PathEscape(appID), url.PathEscape(threadID))
	case "dismiss":
		path = fmt.Sprintf("/api/v1/atlas/v2/apps/%s/annotation-threads/%s/dismiss", url.PathEscape(appID), url.PathEscape(threadID))
	case "reopen":
		path = fmt.Sprintf("/api/v1/atlas/v2/apps/%s/annotation-threads/%s/reopen", url.PathEscape(appID), url.PathEscape(threadID))
	default:
		return nil, fmt.Errorf("unsupported annotation status action %q", action)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationThreadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) MoveAtlasAnnotationThread(
	ctx context.Context,
	appID string,
	threadID string,
	req *AtlasAnnotationAnchorMoveRequest,
) (*AtlasAnnotationThreadResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-threads/%s/anchor",
		url.PathEscape(appID),
		url.PathEscape(threadID),
	)
	resp, err := c.doRequest(ctx, http.MethodPatch, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationThreadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) EditAtlasAnnotationComment(
	ctx context.Context,
	appID string,
	commentID string,
	req *AtlasAnnotationCommentEditRequest,
) (*AtlasAnnotationComment, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-comments/%s",
		url.PathEscape(appID),
		url.PathEscape(commentID),
	)
	resp, err := c.doRequest(ctx, http.MethodPatch, path, req)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationComment
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteAtlasAnnotationComment(
	ctx context.Context,
	appID string,
	commentID string,
) (*AtlasAnnotationComment, error) {
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/annotation-comments/%s",
		url.PathEscape(appID),
		url.PathEscape(commentID),
	)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasAnnotationComment
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

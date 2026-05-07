package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
)

// setCIHeaders attaches X-CI-* headers when the CLI is invoked from a GitHub
// Actions runner. Detection is gated on GITHUB_ACTIONS=true; outside CI this
// function is a no-op so manual CLI usage stays unchanged.
//
// The PR URL and number are read from the event payload at GITHUB_EVENT_PATH
// (only present on pull_request events). Missing values are omitted rather
// than sent as empty headers.
func setCIHeaders(req *http.Request) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return
	}

	server := os.Getenv("GITHUB_SERVER_URL")
	repo := os.Getenv("GITHUB_REPOSITORY")
	actor := os.Getenv("GITHUB_ACTOR")
	sha := os.Getenv("GITHUB_SHA")
	branch := os.Getenv("GITHUB_REF_NAME")
	runID := os.Getenv("GITHUB_RUN_ID")

	setIf(req, "X-CI-System", "github-actions")
	setIf(req, "X-CI-Commit-SHA", sha)
	setIf(req, "X-CI-Branch", branch)
	setIf(req, "X-CI-Run-ID", runID)
	setIf(req, "X-CI-Repository", repo)
	setIf(req, "X-CI-Actor", actor)

	if server != "" && actor != "" {
		setIf(req, "X-CI-Actor-URL", server+"/"+actor)
	}
	if server != "" && repo != "" && runID != "" {
		setIf(req, "X-CI-Run-URL", server+"/"+repo+"/actions/runs/"+runID)
	}

	prURL, prNum := readPullRequestFromEvent(os.Getenv("GITHUB_EVENT_PATH"))
	setIf(req, "X-CI-PR-URL", prURL)
	if prNum > 0 {
		setIf(req, "X-CI-PR-Number", strconv.Itoa(prNum))
	}
}

func setIf(req *http.Request, key, value string) {
	if value != "" {
		req.Header.Set(key, value)
	}
}

// readPullRequestFromEvent parses the GitHub event payload JSON for the
// triggering pull request URL and number. Returns zero values for non-PR
// events (push, schedule, workflow_dispatch) or unreadable payloads.
func readPullRequestFromEvent(eventPath string) (string, int) {
	if eventPath == "" {
		return "", 0
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		return "", 0
	}
	var payload struct {
		PullRequest *struct {
			HTMLURL string `json:"html_url"`
			Number  int    `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", 0
	}
	if payload.PullRequest == nil {
		return "", 0
	}
	return payload.PullRequest.HTMLURL, payload.PullRequest.Number
}

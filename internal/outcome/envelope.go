package outcome

// Envelope is the shared semantic result metadata adapted by CLI and MCP.
type Envelope struct {
	OperationStatus string `json:"operation_status"`
	OutcomeCode     string `json:"outcome_code,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Retryable       bool   `json:"retryable"`
	SessionID       string `json:"session_id,omitempty"`
	SessionIndex    *int   `json:"session_index,omitempty"`
	BuildJobID      string `json:"build_job_id,omitempty"`
	ViewerURL       string `json:"viewer_url,omitempty"`
	ReportURL       string `json:"report_url,omitempty"`
}

// Completed returns a successful semantic envelope.
func Completed() Envelope {
	return Envelope{OperationStatus: "completed"}
}

// Failed returns a semantic failure envelope.
func Failed(code, reason string, retryable bool) Envelope {
	return Envelope{
		OperationStatus: "failed",
		OutcomeCode:     code,
		Reason:          reason,
		Retryable:       retryable,
	}
}

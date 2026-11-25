package api

type DeviceLaunchRequest struct {
	BundleID        string            `json:"bundle_id,omitempty"`
	LaunchEnvVars   map[string]string `json:"launch_env_vars,omitempty"`
	LaunchArguments []string          `json:"launch_arguments,omitempty"`
}

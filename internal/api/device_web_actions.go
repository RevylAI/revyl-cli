package api

type DeviceKeyRequest struct {
	Key       string   `json:"key"`
	Modifiers []string `json:"modifiers,omitempty"`
}

type DeviceScrollRequest struct {
	X           *int   `json:"x,omitempty"`
	Y           *int   `json:"y,omitempty"`
	DX          int    `json:"dx"`
	DY          int    `json:"dy"`
	Target      string `json:"target,omitempty"`
	ScreenToken string `json:"screen_token,omitempty"`
}

type DeviceSelectRequest struct {
	X           *int   `json:"x,omitempty"`
	Y           *int   `json:"y,omitempty"`
	OptionLabel string `json:"option_label"`
	ScreenToken string `json:"screen_token,omitempty"`
}

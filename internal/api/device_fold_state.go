package api

import "fmt"

type DeviceSetFoldStateRequest struct {
	FoldState DeviceFoldState `json:"fold_state"`
}

func ParseDeviceFoldState(value string) (DeviceFoldState, error) {
	switch DeviceFoldState(value) {
	case DeviceFoldStateFolded, DeviceFoldStateUnfolded:
		return DeviceFoldState(value), nil
	default:
		return "", fmt.Errorf("fold state must be 'folded' or 'unfolded'")
	}
}

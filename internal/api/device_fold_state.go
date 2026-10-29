package api

import "fmt"

type DeviceFoldState string

const (
	DeviceFolded   DeviceFoldState = "folded"
	DeviceUnfolded DeviceFoldState = "unfolded"
)

type DeviceSetFoldStateRequest struct {
	FoldState DeviceFoldState `json:"fold_state"`
}

func ParseDeviceFoldState(value string) (DeviceFoldState, error) {
	switch DeviceFoldState(value) {
	case DeviceFolded, DeviceUnfolded:
		return DeviceFoldState(value), nil
	default:
		return "", fmt.Errorf("fold state must be 'folded' or 'unfolded'")
	}
}

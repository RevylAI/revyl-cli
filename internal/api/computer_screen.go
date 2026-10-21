package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type ComputerScreenPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type ComputerScreenAction struct {
	Action      string   `json:"action"`
	X           *int     `json:"x,omitempty"`
	Y           *int     `json:"y,omitempty"`
	StartX      *int     `json:"start_x,omitempty"`
	StartY      *int     `json:"start_y,omitempty"`
	EndX        *int     `json:"end_x,omitempty"`
	EndY        *int     `json:"end_y,omitempty"`
	Button      string   `json:"button,omitempty"`
	Count       *int     `json:"count,omitempty"`
	Modifiers   []string `json:"modifiers,omitempty"`
	Direction   string   `json:"direction,omitempty"`
	Amount      *int     `json:"amount,omitempty"`
	Text        string   `json:"text,omitempty"`
	Keys        string   `json:"keys,omitempty"`
	HoldSeconds float64  `json:"hold_seconds,omitempty"`
}

type ComputerScreenActionRequest struct {
	Action     ComputerScreenAction `json:"action"`
	Screenshot bool                 `json:"screenshot"`
	Via        string               `json:"via"`
}

type ComputerScreenshot struct {
	Width      int                  `json:"width"`
	Height     int                  `json:"height"`
	ObservedAt time.Time            `json:"observed_at"`
	PNG        string               `json:"png"`
	Cursor     *ComputerScreenPoint `json:"cursor"`
}

func (s ComputerScreenshot) DecodePNG() ([]byte, error) {
	if s.PNG == "" {
		return nil, errors.New("computer screenshot has no PNG")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(s.PNG)
	if err != nil || base64.StdEncoding.EncodeToString(data) != s.PNG {
		return nil, errors.New("computer screenshot PNG is invalid")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != s.Width || config.Height != s.Height {
		return nil, errors.New("computer screenshot PNG is invalid")
	}
	return data, nil
}

type ComputerScreenActionResult struct {
	Screenshot *ComputerScreenshot  `json:"screenshot"`
	Cursor     *ComputerScreenPoint `json:"cursor"`
}

var computerScreenButtons = map[string]struct{}{"left": {}, "right": {}, "middle": {}}
var computerScreenModifiers = map[string]struct{}{"shift": {}, "ctrl": {}, "alt": {}, "cmd": {}}
var computerScreenDirections = map[string]struct{}{"up": {}, "down": {}, "left": {}, "right": {}}

func screenPoint(value *int) bool { return value != nil && *value >= 0 && *value <= 4095 }

func validateScreenPoint(x, y *int) bool { return screenPoint(x) && screenPoint(y) }

func validateScreenButton(button string) bool {
	if button == "" {
		return true
	}
	_, ok := computerScreenButtons[button]
	return ok
}

func validateScreenModifiers(modifiers []string) bool {
	seen := make(map[string]struct{}, len(modifiers))
	for _, modifier := range modifiers {
		if _, ok := computerScreenModifiers[modifier]; !ok {
			return false
		}
		if _, ok := seen[modifier]; ok {
			return false
		}
		seen[modifier] = struct{}{}
	}
	return true
}

func ValidateComputerScreenAction(action ComputerScreenAction) error {
	validPoint := validateScreenPoint(action.X, action.Y)
	switch action.Action {
	case "click":
		if !validPoint || !validateScreenButton(action.Button) || !validateScreenModifiers(action.Modifiers) || action.Count != nil && (*action.Count < 1 || *action.Count > 3) {
			return errors.New("computer screen action is invalid")
		}
	case "move":
		if !validPoint {
			return errors.New("computer screen action is invalid")
		}
	case "drag":
		if !validateScreenPoint(action.StartX, action.StartY) || !validateScreenPoint(action.EndX, action.EndY) {
			return errors.New("computer screen action is invalid")
		}
	case "mouse_down", "mouse_up":
		if (action.X == nil) != (action.Y == nil) || action.X != nil && !validPoint || !validateScreenButton(action.Button) {
			return errors.New("computer screen action is invalid")
		}
	case "scroll":
		if !validPoint || !validateScreenModifiers(action.Modifiers) || !validateScreenDirections(action.Direction) || action.Amount != nil && (*action.Amount < 1 || *action.Amount > 20) {
			return errors.New("computer screen action is invalid")
		}
	case "type":
		if utf8.RuneCountInString(action.Text) < 1 || utf8.RuneCountInString(action.Text) > 500 {
			return errors.New("computer screen action is invalid")
		}
	case "key":
		if utf8.RuneCountInString(action.Keys) < 1 || utf8.RuneCountInString(action.Keys) > 200 || math.IsNaN(action.HoldSeconds) || math.IsInf(action.HoldSeconds, 0) || action.HoldSeconds < 0 || action.HoldSeconds > 5 {
			return errors.New("computer screen action is invalid")
		}
	default:
		return errors.New("computer screen action is invalid")
	}
	return nil
}

func validateScreenDirections(direction string) bool {
	_, ok := computerScreenDirections[direction]
	return ok
}

func (c *Client) PerformComputerScreenAction(ctx context.Context, id uuid.UUID, request ComputerScreenActionRequest) (*ComputerScreenActionResult, error) {
	if id == uuid.Nil || request.Via != "cli" && request.Via != "mcp" && request.Via != "api" {
		return nil, errors.New("computer screen request is invalid")
	}
	if err := ValidateComputerScreenAction(request.Action); err != nil {
		return nil, err
	}
	response, err := c.doComputerMutationOnce(ctx, fmt.Sprintf("/api/v1/computer-runs/%s/screen/actions", id), request, id.String(), DefaultTimeout)
	body, err := readComputerScreenBody(response, err)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("computer screen response is invalid")
	}
	var result ComputerScreenActionResult
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields["screenshot"] == nil || fields["cursor"] == nil || json.Unmarshal(body, &result) != nil || !validComputerScreenActionResult(result) || request.Screenshot != (result.Screenshot != nil) {
		return nil, errors.New("computer screen response is invalid")
	}
	return &result, nil
}

func (c *Client) GetComputerScreenshot(ctx context.Context, id uuid.UUID, region []int, via string) (*ComputerScreenshot, error) {
	if id == uuid.Nil || len(region) != 0 && len(region) != 4 || via != "cli" && via != "mcp" && via != "api" {
		return nil, errors.New("computer screenshot request is invalid")
	}
	path := fmt.Sprintf("/api/v1/computer-runs/%s/screen/screenshot", id)
	if len(region) == 4 {
		for _, coordinate := range region {
			if coordinate < 0 || coordinate > 4095 {
				return nil, errors.New("computer screenshot request is invalid")
			}
		}
		if region[0] >= region[2] || region[1] >= region[3] {
			return nil, errors.New("computer screenshot region must have positive width and height")
		}
		values := make([]string, 4)
		for i, coordinate := range region {
			values[i] = strconv.Itoa(coordinate)
		}
		path += "?region=" + url.QueryEscape(strings.Join(values, ","))
		path += "&via=" + url.QueryEscape(via)
	} else {
		path += "?via=" + url.QueryEscape(via)
	}
	response, err := c.doComputerRead(ctx, path)
	body, err := readComputerScreenBody(response, err)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("computer screenshot response is invalid")
	}
	var screenshot ComputerScreenshot
	var fields map[string]json.RawMessage
	if decodeComputerJSON(body, &screenshot, "width", "height", "observed_at", "png") != nil || json.Unmarshal(body, &fields) != nil || fields["cursor"] == nil || !validComputerScreenshot(screenshot) {
		return nil, errors.New("computer screenshot response is invalid")
	}
	return &screenshot, nil
}

func validComputerScreenshot(screenshot ComputerScreenshot) bool {
	_, offset := screenshot.ObservedAt.Zone()
	if screenshot.Width < 1 || screenshot.Width > 1280 || screenshot.Height < 1 || screenshot.Height > 800 || screenshot.ObservedAt.IsZero() || offset != 0 || !validComputerScreenCursor(screenshot.Cursor) {
		return false
	}
	_, err := screenshot.DecodePNG()
	return err == nil
}

func validComputerScreenActionResult(result ComputerScreenActionResult) bool {
	if result.Screenshot != nil && !validComputerScreenshot(*result.Screenshot) {
		return false
	}
	return validComputerScreenCursor(result.Cursor)
}

func validComputerScreenCursor(cursor *ComputerScreenPoint) bool {
	return cursor == nil || cursor.X >= 0 && cursor.X <= 4095 && cursor.Y >= 0 && cursor.Y <= 4095
}

const ComputerScreenMaxResponseJSON = 8 * 1024 * 1024

func readComputerScreenBody(response *http.Response, requestErr error) ([]byte, error) {
	if requestErr != nil || response.StatusCode != http.StatusOK {
		return readComputerBody(response, requestErr)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, ComputerScreenMaxResponseJSON+1))
	if err != nil || len(body) > ComputerScreenMaxResponseJSON {
		return nil, errComputerResponse
	}
	return body, nil
}

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func TestRequireCoordsOnScreenRefusesOnlyAnOffScreenPoint(t *testing.T) {
	for _, tc := range []struct {
		name          string
		platform      string
		width, height int
		healthBody    string
		x, y          int
		wantErr       string
		wantUnchecked bool
		wantRequests  int32
	}{
		{name: "inside the recorded size", platform: "ios", width: 402, height: 874, x: 200, y: 650},
		{name: "outside an iOS screen", platform: "ios", width: 402, height: 874, healthBody: `{"device_connected":true,"screen_width":402,"screen_height":874}`, x: 600, y: 1950, wantErr: "(600, 1950) is outside the 402x874 iOS screen; iOS coordinates are points from the top-left corner", wantRequests: 1},
		{name: "negative", platform: "ios", width: 402, height: 874, healthBody: `{"device_connected":true,"screen_width":402,"screen_height":874}`, x: -1, y: 10, wantErr: "(-1, 10) is outside the 402x874 iOS screen", wantRequests: 1},
		{name: "outside an Android screen", platform: "android", width: 1080, height: 2400, healthBody: `{"device_connected":true,"screen_width":1080,"screen_height":2400}`, x: 1500, y: 100, wantErr: "(1500, 100) is outside the 1080x2400 Android screen; Android coordinates are pixels from the top-left corner", wantRequests: 1},
		{name: "inside the worker's rotated size", platform: "ios", width: 402, height: 874, healthBody: `{"device_connected":true,"screen_width":874,"screen_height":402}`, x: 800, y: 100, wantRequests: 1},
		{name: "no size recorded, the worker's size refuses", platform: "ios", healthBody: `{"device_connected":true,"screen_width":402,"screen_height":874}`, x: 600, y: 1950, wantErr: "(600, 1950) is outside the 402x874 iOS screen", wantRequests: 1},
		{name: "no size recorded, inside the worker's size", platform: "ios", healthBody: `{"device_connected":true,"screen_width":402,"screen_height":874}`, x: 401, y: 873, wantRequests: 1},
		{name: "no size anywhere", platform: "ios", healthBody: `{"device_connected":true}`, x: 600, y: 1950, wantUnchecked: true, wantRequests: 1},
		{name: "no size and no worker answer", platform: "ios", x: 600, y: 1950, wantUnchecked: true, wantRequests: 2},
		{name: "worker omits its size", platform: "ios", width: 402, height: 874, healthBody: `{"device_connected":true}`, x: 600, y: 1950, wantErr: "(600, 1950) is outside the 402x874 iOS screen", wantRequests: 1},
		{name: "web is checked by its own runtime", platform: "web", width: 1280, height: 800, x: 5000, y: 5000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !strings.HasSuffix(r.URL.Path, "/health") || tc.healthBody == "" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.healthBody))
			}))
			t.Cleanup(server.Close)
			mgr := NewDeviceSessionManager(api.NewClientWithBaseURL("test-key", server.URL), "")
			session := &DeviceSession{
				Index:         UnattachedSessionIndex,
				WorkflowRunID: surfaceTestWorkflowRunID,
				Platform:      tc.platform,
				ScreenWidth:   tc.width,
				ScreenHeight:  tc.height,
			}

			unchecked, err := mgr.RequireCoordsOnScreen(context.Background(), session, tc.x, tc.y)

			if tc.wantErr == "" && err != nil {
				t.Fatalf("RequireCoordsOnScreen() = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("RequireCoordsOnScreen() = %v, want %q", err, tc.wantErr)
			}
			if unchecked != tc.wantUnchecked {
				t.Fatalf("RequireCoordsOnScreen() unchecked = %v, want %v", unchecked, tc.wantUnchecked)
			}
			if got := requests.Load(); got != tc.wantRequests {
				t.Fatalf("worker requests = %d, want %d", got, tc.wantRequests)
			}
		})
	}
}

func TestResolveCoordsRefusesAPointOffTheRecordedScreen(t *testing.T) {
	mgr := &DeviceSessionManager{
		sessions:    map[int]*DeviceSession{0: {Index: 0, SessionID: "test", Platform: "ios", ScreenWidth: 402, ScreenHeight: 874, IdleTimeout: 5 * time.Minute, StartedAt: time.Now(), LastActivity: time.Now()}},
		idleTimers:  make(map[int]*time.Timer),
		activeIndex: 0,
		nextIndex:   1,
	}
	srv := &Server{sessionMgr: mgr}
	x, y := 600, 1950

	_, err := srv.resolveCoords(context.Background(), "", &x, &y, nil, "")

	if err == nil || !strings.Contains(err.Error(), "(600, 1950) is outside the 402x874 iOS screen; iOS coordinates are points") {
		t.Fatalf("resolveCoords() error = %v, want the off-screen refusal", err)
	}
}

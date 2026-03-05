package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestStartRunInterruptHandler_FirstInterruptRequestsCancelWithTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 2)
	state := newRunInterruptState()
	state.SetTaskID("task-123")

	type cancelCall struct {
		taskID      string
		hasDeadline bool
		timeout     time.Duration
	}
	cancelCalls := make(chan cancelCall, 1)
	forceExitCalls := make(chan int, 1)

	stop := startRunInterruptHandler(ctx, cancel, sigChan, state, runInterruptOptions{
		nounLower: "test",
		nounTitle: "Test",
		requestCancel: func(cancelCtx context.Context, taskID string) error {
			deadline, ok := cancelCtx.Deadline()
			remaining := time.Duration(0)
			if ok {
				remaining = time.Until(deadline)
			}
			cancelCalls <- cancelCall{
				taskID:      taskID,
				hasDeadline: ok,
				timeout:     remaining,
			}
			return nil
		},
		exitFunc: func(code int) {
			forceExitCalls <- code
		},
	})
	defer stop()

	sigChan <- os.Interrupt

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for context cancellation")
	}

	var call cancelCall
	select {
	case call = <-cancelCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancel callback")
	}

	if !state.Cancelled() {
		t.Fatal("expected interrupt state to be marked cancelled")
	}
	if call.taskID != "task-123" {
		t.Fatalf("expected cancel task ID task-123, got %q", call.taskID)
	}
	if !call.hasDeadline {
		t.Fatal("expected cancel context to have a deadline")
	}
	if call.timeout <= 0 || call.timeout > runCancelRequestTimeout+2*time.Second {
		t.Fatalf("unexpected cancel timeout duration: %v", call.timeout)
	}

	select {
	case code := <-forceExitCalls:
		t.Fatalf("unexpected force-exit call with code %d", code)
	default:
	}
}

func TestStartRunInterruptHandler_SecondInterruptForcesExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 2)
	state := newRunInterruptState()
	state.SetTaskID("")

	forceExitCalls := make(chan int, 1)
	cancelCalls := make(chan struct{}, 1)

	stop := startRunInterruptHandler(ctx, cancel, sigChan, state, runInterruptOptions{
		nounLower: "workflow",
		nounTitle: "Workflow",
		requestCancel: func(context.Context, string) error {
			cancelCalls <- struct{}{}
			return nil
		},
		exitFunc: func(code int) {
			forceExitCalls <- code
		},
	})
	defer stop()

	sigChan <- os.Interrupt
	sigChan <- syscall.SIGTERM

	select {
	case code := <-forceExitCalls:
		if code != runForceExitCode {
			t.Fatalf("expected force-exit code %d, got %d", runForceExitCode, code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for force-exit callback")
	}

	if !state.Cancelled() {
		t.Fatal("expected interrupt state to be marked cancelled")
	}

	select {
	case <-cancelCalls:
		t.Fatal("did not expect remote cancel request without task ID")
	default:
	}
}

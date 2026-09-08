package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/revyl/cli/internal/testutil"
)

func TestWorkerWebSocketProxy(t *testing.T) {
	testutil.TestWebSocketProxy(t, func(ctx context.Context, target string) error {
		client := NewWorkerWSClient("fixture-run")
		defer client.Close()
		for _, connect := range []func(context.Context, string) error{client.Connect, client.Reconnect} {
			if err := connect(ctx, target); err != nil {
				return err
			}
			select {
			case <-client.Messages():
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
}

func TestWorkerWebSocketDirectFlow(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		var execution StepExecutionMessage
		if err := conn.ReadJSON(&execution); err != nil {
			t.Error(err)
			return
		}
		if execution.EventType != "STEP_EXECUTION" || execution.Action != "EXECUTE" || execution.StepDetails.TestId != "fixture-test" || len(execution.StepDetails.Steps) != 1 || execution.StepDetails.Steps[0].ID != "fixture-step" {
			t.Error("direct connection changed the step execution payload")
		}
		if err := conn.WriteJSON(WorkerMessage{EventType: "LOG", Type: "fixture-message"}); err != nil {
			t.Error(err)
			return
		}
		if err := conn.WriteJSON(WorkerMessage{Type: "ping", ID: "fixture-ping"}); err != nil {
			t.Error(err)
			return
		}
		var pong WorkerMessage
		if err := conn.ReadJSON(&pong); err != nil {
			t.Error(err)
		} else if pong.Type != "pong" || pong.ID != "fixture-ping" {
			t.Error("worker ping did not receive the matching pong")
		}
	})
	testutil.TestDirectWebSocket(t, handler, func(ctx context.Context, target string) error {
		client := NewWorkerWSClient("fixture-run")
		defer client.Close()
		for _, connect := range []func(context.Context, string) error{client.Connect, client.Reconnect} {
			if err := connect(ctx, target); err != nil {
				return err
			}
			if err := client.SendStepExecution(ctx, StepDefinition{ID: "fixture-step"}, "fixture-test", false); err != nil {
				return err
			}
			receivedMessage := false
		messages:
			for {
				select {
				case message, open := <-client.Messages():
					if !open {
						break messages
					}
					if message.Type == "fixture-message" {
						receivedMessage = true
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if !receivedMessage {
				return fmt.Errorf("direct worker connection lost the response message")
			}
		}
		return nil
	})
}

package build

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestCommandOutputReaderTimesOutOnlyAfterProcessExit(t *testing.T) {
	for _, exitBeforeRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "already-reading", true: "next-read"}[exitBeforeRead], func(t *testing.T) {
			pipe, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer pipe.Close()
			defer writer.Close()
			reader := &commandOutputReader{pipe: pipe, idleTimeout: 30 * time.Millisecond}
			if exitBeforeRead {
				reader.markProcessExited()
			}
			result := make(chan error, 1)
			go func() {
				_, err := reader.Read(make([]byte, 1))
				result <- err
			}()
			if !exitBeforeRead {
				select {
				case err := <-result:
					t.Fatalf("read ended before process exit: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
				reader.markProcessExited()
			}
			select {
			case err := <-result:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("read error = %v, want pipe deadline", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("orphaned pipe read did not time out")
			}
		})
	}
}

func TestCommandOutputReaderExcludesTimeBetweenReads(t *testing.T) {
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	defer writer.Close()
	reader := &commandOutputReader{pipe: pipe, idleTimeout: 30 * time.Millisecond}
	reader.markProcessExited()
	for _, value := range []byte{'a', 'b', 'c'} {
		time.Sleep(100 * time.Millisecond)
		if _, err := writer.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 1)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			t.Fatalf("time outside Read consumed the idle budget: %v", err)
		}
		if buffer[0] != value {
			t.Fatalf("read %q, want %q", buffer[0], value)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read error = %v, want EOF", err)
	}
}

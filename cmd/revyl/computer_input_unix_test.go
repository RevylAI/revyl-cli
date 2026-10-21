//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestComputerLocalInputRejectsFIFOAndSymlinksWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "input")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if file, err := openComputerInput(fifo); err == nil {
		file.Close()
		t.Fatal("FIFO accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO open blocked")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	if file, err := openComputerInputFile(link); err == nil {
		file.Close()
		t.Fatal("no-follow open followed a symlink")
	}
	file, err := openComputerInputFile(fifo)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}

func TestComputerLocalInputRejectsConcurrentSymlinkSwaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(path, []byte("allowed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("forbidden"), 0600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var done sync.WaitGroup
	done.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			swap := filepath.Join(dir, "swap")
			_ = os.Symlink(other, swap)
			_ = os.Rename(swap, path)
			_ = os.WriteFile(swap, []byte("allowed"), 0600)
			_ = os.Rename(swap, path)
		}
	})
	defer func() { close(stop); done.Wait() }()
	for range 100 {
		file, err := openComputerInput(path)
		if err != nil {
			continue
		}
		data, readErr := readComputerInput(file, 64)
		file.Close()
		if readErr == nil && string(data) == "forbidden" {
			t.Fatal("symlink race read an unintended file")
		}
	}
}

func TestComputerStalledStdinHonorsDeadlineAndClosesReader(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := readComputerInputContext(ctx, reader, 64); err == nil {
		t.Fatal("stalled stdin succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("stdin exceeded its deadline")
	}
	if _, err := reader.Stat(); err == nil {
		t.Fatal("canceled reader remained open")
	}
}

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func openComputerInput(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("computer input must be a regular local file or stdin")
	}
	file, err := openComputerInputFile(path)
	if err != nil {
		return nil, errors.New("computer input file could not be opened")
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		file.Close()
		return nil, errors.New("computer input file changed or is not regular")
	}
	return file, nil
}

func readComputerLocalInput(cmd *cobra.Command, path string, limit int, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	if path == "-" {
		return readComputerInputContext(ctx, cmd.InOrStdin(), limit)
	}
	file, err := openComputerInputContext(ctx, path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readComputerInputContext(ctx, file, limit)
}

func openComputerInputContext(ctx context.Context, path string) (*os.File, error) {
	type result struct {
		file *os.File
		err  error
	}
	results := make(chan result)
	go func() {
		file, err := openComputerInput(path)
		select {
		case results <- result{file, err}:
		case <-ctx.Done():
			if file != nil {
				file.Close()
			}
		}
	}()
	select {
	case value := <-results:
		return value.file, value.err
	case <-ctx.Done():
		return nil, errors.New("computer input acquisition was canceled or timed out")
	}
}

func readComputerInputContext(ctx context.Context, reader io.Reader, limit int) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, errors.New("computer input read was canceled or timed out")
	}
	type result struct {
		data []byte
		err  error
	}
	results := make(chan result, 1)
	go func() { data, err := readComputerInput(reader, limit); results <- result{data, err} }()
	select {
	case value := <-results:
		return value.data, value.err
	case <-ctx.Done():
		if closer, ok := reader.(io.Closer); ok {
			closer.Close()
		}
		return nil, errors.New("computer input read was canceled or timed out")
	}
}

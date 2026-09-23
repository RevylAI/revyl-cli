package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

type commandBodyOptions struct {
	body     string
	bodyFile string
}

type annotationBodyOptions = commandBodyOptions

func addCommandBodyFlags(command *cobra.Command, options *commandBodyOptions, bodyUsage string) {
	command.Flags().StringVar(&options.body, "body", "", bodyUsage)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "Read body from a file, or - for stdin")
}

func addAnnotationBodyFlags(command *cobra.Command, options *annotationBodyOptions) {
	addCommandBodyFlags(command, options, "Plain-text comment body")
}

func readAnnotationBody(command *cobra.Command, options annotationBodyOptions) (string, error) {
	return readCommandBody(command, options, "annotation body", 64<<10, 0)
}

func readCommandBody(
	command *cobra.Command,
	options commandBodyOptions,
	kind string,
	maxBytes int,
	maxRunes int,
) (string, error) {
	providedBody := command.Flags().Changed("body")
	providedFile := command.Flags().Changed("body-file")
	if providedBody == providedFile {
		return "", fmt.Errorf("exactly one of --body or --body-file is required")
	}
	if providedBody {
		body := strings.TrimSpace(options.body)
		if body == "" {
			return "", fmt.Errorf("%s cannot be empty", kind)
		}
		if err := enforceCommandBodyLimit(body, kind, maxBytes, maxRunes); err != nil {
			return "", err
		}
		return body, nil
	}
	var reader io.Reader
	var file *os.File
	if options.bodyFile == "-" {
		reader = command.InOrStdin()
	} else {
		var err error
		file, err = os.Open(options.bodyFile)
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader = file
	}
	limit := maxBytes
	if maxRunes > 0 {
		// UTF-8 worst case is 4 bytes per rune, plus one extra byte so we
		// can distinguish "exactly at the cap" from "over the cap".
		limit = maxRunes*4 + 1
		if maxBytes > 0 && maxBytes < limit {
			limit = maxBytes
		}
	} else if limit <= 0 {
		limit = 64 << 10
	}
	contents, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(contents) > maxBytes {
		return "", fmt.Errorf("%s exceeds %s", kind, formatCommandBodyLimit(maxBytes, false))
	}
	body := strings.TrimSpace(string(contents))
	if body == "" {
		return "", fmt.Errorf("%s cannot be empty", kind)
	}
	if err := enforceCommandBodyLimit(body, kind, 0, maxRunes); err != nil {
		return "", err
	}
	return body, nil
}

func enforceCommandBodyLimit(body, kind string, maxBytes, maxRunes int) error {
	if maxBytes > 0 && len(body) > maxBytes {
		return fmt.Errorf("%s exceeds %s", kind, formatCommandBodyLimit(maxBytes, false))
	}
	if maxRunes > 0 && utf8.RuneCountInString(body) > maxRunes {
		return fmt.Errorf("%s exceeds %s", kind, formatCommandBodyLimit(maxRunes, true))
	}
	return nil
}

func formatCommandBodyLimit(limit int, runes bool) string {
	if runes {
		return fmt.Sprintf("%d characters", limit)
	}
	if limit == 64<<10 {
		return "64 KiB"
	}
	return fmt.Sprintf("%d bytes", limit)
}

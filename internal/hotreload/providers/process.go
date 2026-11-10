package providers

import "fmt"

func processExitError(prefix string, err error) error {
	if err == nil {
		return fmt.Errorf("%s", prefix)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func processExitDetail(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	return fmt.Sprintf("%s: %v", prefix, err)
}

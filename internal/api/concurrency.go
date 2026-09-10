package api

import "strings"

const ConcurrencyUpgradeHint = "Upgrade your plan for more concurrency: revyl auth billing"

func withConcurrencyUpgradeHint(message string) string {
	if !isConcurrencyLimitMessage(message) || strings.Contains(message, "revyl auth billing") {
		return message
	}
	return message + "\n" + ConcurrencyUpgradeHint
}

func isConcurrencyLimitMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "concurrency limit reached") ||
		strings.Contains(lower, "concurrency limit exceeded") ||
		strings.Contains(lower, "concurrency is fully utilized") ||
		strings.Contains(lower, "not enough concurrency")
}

func isConcurrencyLimitCode(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "CONCURRENCY_LIMIT", "CONCURRENCY_LIMIT_EXCEEDED", "CONCURRENCY_LIMIT_REACHED":
		return true
	default:
		return false
	}
}

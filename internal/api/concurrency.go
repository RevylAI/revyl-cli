package api

import "strings"

const ConcurrencyUpgradeHint = "Upgrade your plan for more concurrency: revyl auth billing"

func concurrencyUpgradeHint(billingURL string) string {
	if billingURL != "" {
		return "Upgrade to Pro to run more devices at once (" + AddCardCharge + "): " + billingURL
	}
	return ConcurrencyUpgradeHint
}

func withConcurrencyUpgradeHint(message, billingURL string) string {
	if !isConcurrencyLimitMessage(message) && billingURL == "" {
		return message
	}
	if strings.Contains(message, "revyl auth billing") || (billingURL != "" && strings.Contains(message, billingURL)) {
		return message
	}
	return message + "\n" + concurrencyUpgradeHint(billingURL)
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

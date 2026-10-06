package analytics

import (
	"errors"
	"strings"
	"testing"
)

func TestFailureTailRedactsLocalTestAliasList(t *testing.T) {
	rec := testRecorder()
	run := testCommandRun(rec)

	run.ObserveOutput(
		"error",
		"test 'checkout' not found. Available tests: [login-flow checkout-guest refund smoke]\n\nHint: Run 'revyl test remote' to see all available tests.",
	)
	run.Complete(errors.New("test not found"))

	tail, ok := lastEvent(t, rec).Properties["output_tail"].([]map[string]interface{})
	if !ok || len(tail) != 1 {
		t.Fatalf("output_tail = %#v, want one entry", lastEvent(t, rec).Properties["output_tail"])
	}
	line, _ := tail[0]["message"].(string)
	for _, alias := range []string{"login-flow", "checkout-guest", "refund", "smoke"} {
		if strings.Contains(line, alias) {
			t.Fatalf("output_tail leaked test alias %q in %q", alias, line)
		}
	}
	for _, kept := range []string{"not found. Available tests: <redacted>", "Hint: Run 'revyl test remote'"} {
		if !strings.Contains(line, kept) {
			t.Fatalf("output_tail = %q, want it to keep %q", line, kept)
		}
	}
}

func TestSanitizeStringRedactsAtlasAreaList(t *testing.T) {
	sanitized := sanitizeString(`Atlas product area "Rewards" was not found; available areas: Checkout, Onboarding`)

	if strings.Contains(sanitized, "Checkout") || strings.Contains(sanitized, "Onboarding") {
		t.Fatalf("sanitized value leaked area names: %q", sanitized)
	}
	if !strings.HasSuffix(sanitized, "available areas: <redacted>") {
		t.Fatalf("sanitized value = %q, want the list replaced by a redaction marker", sanitized)
	}
}

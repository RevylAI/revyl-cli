package analytics

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSanitizeStringRedactsRevylCredentials(t *testing.T) {
	for _, prefix := range []string{"rk_", "rst_staging_", "rst_preview-example_", "rst_preview_example_", "rrt_staging_", "rrt_preview-example_", "rrt_preview_example_"} {
		t.Run(prefix, func(t *testing.T) {
			credential := prefix + strings.Repeat("a", 43)
			for _, diagnostic := range []string{
				credential,
				"revyl device start --api-key " + credential,
				"launch failed for " + credential + " on device",
				"credential=" + credential,
				"token=" + credential,
			} {
				sanitized := sanitizeString(diagnostic)
				if strings.Contains(sanitized, credential) || !strings.Contains(sanitized, "<redacted") {
					t.Fatalf("credential survived sanitization: %q", sanitized)
				}
			}
		})
	}
	if sanitized := sanitizeString("first_run took 3s"); sanitized != "first_run took 3s" {
		t.Fatalf("unrelated text was altered: %q", sanitized)
	}
}

func TestSanitizeStringPreservesNonTokenPrefixes(t *testing.T) {
	for _, diagnostic := range []string{
		"/rst_template.rst",
		"/rrt_template.rst",
		"rst_staging_example",
		"rrt_preview_example",
		"rst_staging_" + strings.Repeat("a", 42),
		"rrt_staging_" + strings.Repeat("a", 44),
		"rst_Staging_" + strings.Repeat("a", 43),
		"rrt_" + strings.Repeat("a", 33) + "_" + strings.Repeat("a", 43),
	} {
		if sanitized := sanitizeString(diagnostic); sanitized != diagnostic {
			t.Fatalf("non-token diagnostic was altered: %q", sanitized)
		}
	}
}

func TestSanitizeStringRedactsIssuedTokenBase64URLCharacters(t *testing.T) {
	for _, prefix := range []string{"rst_0_", "rrt_" + strings.Repeat("a", 32) + "_"} {
		for _, randomPart := range []string{
			strings.Repeat("a", 42) + "-",
			strings.Repeat("a", 42) + "_",
			"-_" + strings.Repeat("a", 41),
		} {
			credential := prefix + randomPart
			for _, suffix := range []string{"", ".", ", next"} {
				if sanitized := sanitizeString(credential + suffix); sanitized != "<redacted-api-key>"+suffix {
					t.Fatalf("issued token redaction = %q, want redaction with punctuation preserved", sanitized)
				}
			}
		}
	}
}

func TestFailureDiagnosticsRedactRuntimeTokens(t *testing.T) {
	for _, prefix := range []string{"rst_staging_", "rrt_staging_"} {
		for _, passedAsArgument := range []bool{false, true} {
			credential := prefix + strings.Repeat("a", 43)
			rec := testRecorder()
			var args []string
			if passedAsArgument {
				args = []string{credential}
			}
			run := rec.StartCommand(&cobra.Command{Use: "run"}, args)
			run.ObserveOutput("error", "credential="+credential)
			run.Complete(errors.New("launch failed for " + credential))
			event := lastEvent(t, rec)
			message, _ := event.Properties["error_message"].(string)
			tail, ok := event.Properties["output_tail"].([]map[string]interface{})
			if !ok || len(tail) != 1 {
				t.Fatalf("output_tail = %#v, want one entry", event.Properties["output_tail"])
			}
			line, _ := tail[0]["message"].(string)
			for _, sanitized := range []string{message, line} {
				if strings.Contains(sanitized, credential) || !strings.Contains(sanitized, "<") {
					t.Fatalf("runtime token survived failure diagnostics: %q", sanitized)
				}
			}
		}
	}
}

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

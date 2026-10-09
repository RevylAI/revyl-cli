package backendheaders

import (
	"net/http"
	"testing"
)

func TestSetRuntimeTraceContext(t *testing.T) {
	const valid = "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01"
	for _, value := range []string{"", valid, "invalid", "00-00000000000000000000000000000000-1234567890abcdef-01"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("REVYL_TRACEPARENT", value)
			request, err := http.NewRequest(http.MethodGet, "https://backend.example.com/api/v1/apps", nil)
			if err != nil {
				t.Fatal(err)
			}
			SetRuntimeTraceContext(request)
			want := ""
			if value == valid {
				want = valid
			}
			if got := request.Header.Get("traceparent"); got != want {
				t.Fatalf("traceparent = %q, want %q", got, want)
			}
			if request.Header.Get("baggage") != "" {
				t.Fatal("unexpected baggage")
			}
		})
	}
}

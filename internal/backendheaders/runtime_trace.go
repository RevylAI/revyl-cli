package backendheaders

import (
	"context"
	"net/http"
	"os"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// SetRuntimeTraceContext carries an explicitly supplied parent to backend requests.
func SetRuntimeTraceContext(request *http.Request) {
	parent := os.Getenv("REVYL_TRACEPARENT")
	if parent == "" {
		return
	}
	propagator := propagation.TraceContext{}
	ctx := propagator.Extract(context.Background(), propagation.MapCarrier{"traceparent": parent})
	if trace.SpanContextFromContext(ctx).IsValid() {
		propagator.Inject(ctx, propagation.HeaderCarrier(request.Header))
	}
}

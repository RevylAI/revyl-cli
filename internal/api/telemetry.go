package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

const (
	cliTraceIngestPath       = "/api/v1/telemetry/cli-traces"
	cliTraceExportTimeout    = 500 * time.Millisecond
	cliTraceSpanName         = "CLI: revyl device start"
	cliTraceparentHeader     = "X-Revyl-Traceparent"
	cliTraceHandoffHeader    = "X-Revyl-Trace-Handoff"
	traceRequestIDHeader     = "X-Request-ID"
	otlpProtobufContentType  = "application/x-protobuf"
	revylCLIInstrumentation  = "revyl-cli"
	revylCLICommandAttribute = "revyl.device.start"
)

type cliTraceHandoff struct {
	Traceparent  string
	HandoffToken string
	TraceID      string
	RequestID    string
}

type cliTraceIngestResponse struct {
	HandoffToken string `json:"handoff_token"`
	TraceID      string `json:"trace_id"`
	RequestID    string `json:"request_id"`
}

type cliTraceExporter struct {
	client    *Client
	requestID string
	result    *cliTraceHandoff
	err       error
}

func newTraceRequestID() string {
	return uuid.NewString()
}

func (c *Client) effectiveVersion() string {
	if v := strings.TrimSpace(c.version); v != "" {
		return v
	}
	return "dev"
}

func (c *Client) exportStartDeviceTrace(ctx context.Context, requestID string) (*cliTraceHandoff, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("missing API key")
	}
	if strings.TrimSpace(requestID) == "" {
		return nil, fmt.Errorf("missing request ID")
	}

	exporter := &cliTraceExporter{client: c, requestID: requestID}
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			attribute.String("service.name", "revyl-cli"),
			attribute.String("service.version", c.effectiveVersion()),
		),
	)
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSyncer(exporter),
	)
	tracer := provider.Tracer(revylCLIInstrumentation)
	_, span := tracer.Start(
		ctx,
		cliTraceSpanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("cli.version", c.effectiveVersion()),
			attribute.String("os.type", runtime.GOOS),
			attribute.String("os.arch", runtime.GOARCH),
			attribute.String("revyl.client", "cli"),
			attribute.String("revyl.command", revylCLICommandAttribute),
			attribute.String("revyl.request_id", requestID),
		),
	)
	traceparent := traceparentFromSpanContext(span.SpanContext())
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, cliTraceExportTimeout)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)

	if exporter.err != nil {
		return nil, exporter.err
	}
	if exporter.result == nil || exporter.result.HandoffToken == "" {
		return nil, fmt.Errorf("CLI trace handoff missing token")
	}
	if exporter.result.Traceparent == "" {
		exporter.result.Traceparent = traceparent
	}
	if exporter.result.RequestID == "" {
		exporter.result.RequestID = requestID
	}
	return exporter.result, nil
}

func (e *cliTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) != 1 {
		e.err = fmt.Errorf("expected one CLI span, got %d", len(spans))
		return e.err
	}
	payload, traceparent, err := exportRequestForSpan(spans[0])
	if err != nil {
		e.err = err
		return err
	}

	result, err := e.client.postCLITrace(ctx, e.requestID, payload)
	if err != nil {
		e.err = err
		return err
	}
	result.Traceparent = traceparent
	e.result = result
	return nil
}

func (e *cliTraceExporter) Shutdown(context.Context) error {
	return nil
}

func (c *Client) postCLITrace(ctx context.Context, requestID string, payload []byte) (*cliTraceHandoff, error) {
	exportCtx, cancel := context.WithTimeout(ctx, cliTraceExportTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		exportCtx,
		http.MethodPost,
		c.baseURL+cliTraceIngestPath,
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", otlpProtobufContentType)
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	req.Header.Set(traceRequestIDHeader, requestID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("CLI trace ingest returned status %d", resp.StatusCode)
	}

	var body cliTraceIngestResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.HandoffToken == "" || body.TraceID == "" || body.RequestID != requestID {
		return nil, fmt.Errorf("CLI trace ingest returned invalid handoff")
	}
	return &cliTraceHandoff{
		HandoffToken: body.HandoffToken,
		TraceID:      body.TraceID,
		RequestID:    body.RequestID,
	}, nil
}

func exportRequestForSpan(span sdktrace.ReadOnlySpan) ([]byte, string, error) {
	if span.Name() != cliTraceSpanName {
		return nil, "", fmt.Errorf("unexpected CLI span name %q", span.Name())
	}
	sc := span.SpanContext()
	traceparent := traceparentFromSpanContext(sc)
	if traceparent == "" {
		return nil, "", fmt.Errorf("invalid CLI span context")
	}

	// TracesData and OTLP ExportTraceServiceRequest share the same resource_spans
	// wire field. Using TracesData keeps the CLI dependency set smaller while the
	// backend can still parse and re-forward the payload as OTLP.
	req := &tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: attributesToOTLP(span.Resource().Attributes()),
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{
							Name:    span.InstrumentationScope().Name,
							Version: span.InstrumentationScope().Version,
						},
						Spans: []*tracepb.Span{spanToOTLP(span)},
					},
				},
			},
		},
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, traceparent, nil
}

func spanToOTLP(span sdktrace.ReadOnlySpan) *tracepb.Span {
	sc := span.SpanContext()
	traceID := sc.TraceID()
	spanID := sc.SpanID()
	out := &tracepb.Span{
		TraceId:           append([]byte(nil), traceID[:]...),
		SpanId:            append([]byte(nil), spanID[:]...),
		Name:              span.Name(),
		Kind:              spanKindToOTLP(span.SpanKind()),
		StartTimeUnixNano: uint64(span.StartTime().UnixNano()),
		EndTimeUnixNano:   uint64(span.EndTime().UnixNano()),
		Attributes:        attributesToOTLP(span.Attributes()),
		Status:            statusToOTLP(span.Status()),
	}
	if parent := span.Parent(); parent.IsValid() {
		parentID := parent.SpanID()
		out.ParentSpanId = append([]byte(nil), parentID[:]...)
	}
	return out
}

func attributesToOTLP(attrs []attribute.KeyValue) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		value, ok := attributeValueToOTLP(attr.Value)
		if !ok {
			continue
		}
		out = append(out, &commonpb.KeyValue{
			Key:   string(attr.Key),
			Value: value,
		})
	}
	return out
}

func attributeValueToOTLP(value attribute.Value) (*commonpb.AnyValue, bool) {
	switch value.Type() {
	case attribute.STRING:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value.AsString()}}, true
	case attribute.BOOL:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value.AsBool()}}, true
	case attribute.INT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value.AsInt64()}}, true
	case attribute.FLOAT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value.AsFloat64()}}, true
	default:
		return nil, false
	}
}

func spanKindToOTLP(kind trace.SpanKind) tracepb.Span_SpanKind {
	switch kind {
	case trace.SpanKindInternal:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case trace.SpanKindServer:
		return tracepb.Span_SPAN_KIND_SERVER
	case trace.SpanKindClient:
		return tracepb.Span_SPAN_KIND_CLIENT
	case trace.SpanKindProducer:
		return tracepb.Span_SPAN_KIND_PRODUCER
	case trace.SpanKindConsumer:
		return tracepb.Span_SPAN_KIND_CONSUMER
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

func statusToOTLP(status sdktrace.Status) *tracepb.Status {
	out := &tracepb.Status{Message: status.Description}
	switch status.Code {
	case codes.Ok:
		out.Code = tracepb.Status_STATUS_CODE_OK
	case codes.Error:
		out.Code = tracepb.Status_STATUS_CODE_ERROR
	default:
		out.Code = tracepb.Status_STATUS_CODE_UNSET
	}
	return out
}

func traceparentFromSpanContext(sc trace.SpanContext) string {
	if !sc.IsValid() {
		return ""
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(
		trace.ContextWithSpanContext(context.Background(), sc),
		carrier,
	)
	return carrier.Get("traceparent")
}

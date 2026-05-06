package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	cliTraceIngestPath      = "/api/v1/telemetry/cli-traces"
	cliSpanIngestPath       = "/api/v1/telemetry/cli-spans"
	cliTraceExportTimeout   = 500 * time.Millisecond
	cliTraceSpanName        = "CLI: revyl device start"
	cliDevTraceSpanName     = "CLI: revyl dev"
	cliLocalMetroSpanName   = "CLI: hotreload.local_metro_request"
	cliTraceparentHeader    = "X-Revyl-Traceparent"
	cliTraceHandoffHeader   = "X-Revyl-Trace-Handoff"
	traceRequestIDHeader    = "X-Request-ID"
	otlpProtobufContentType = "application/x-protobuf"
	revylCLIInstrumentation = "revyl-cli"
)

const (
	revylCLICommandDeviceStart = "revyl.device.start"
	revylCLICommandDev         = "revyl.dev"
	revylCLICommandLocalMetro  = "revyl.hotreload.local_metro_request"
)

type traceHandoffContextKey struct{}

// TraceHandoff carries a trusted CLI trace context returned by the backend.
type TraceHandoff struct {
	Traceparent  string
	HandoffToken string
	TraceID      string
	RequestID    string
}

// Headers returns the private Revyl trace handoff headers for backend requests.
func (h *TraceHandoff) Headers() map[string]string {
	if h == nil || strings.TrimSpace(h.Traceparent) == "" || strings.TrimSpace(h.HandoffToken) == "" {
		return nil
	}
	requestID := strings.TrimSpace(h.RequestID)
	if requestID == "" {
		requestID = newTraceRequestID()
	}
	return map[string]string{
		traceRequestIDHeader:  requestID,
		cliTraceparentHeader:  strings.TrimSpace(h.Traceparent),
		cliTraceHandoffHeader: strings.TrimSpace(h.HandoffToken),
	}
}

// WithTraceHandoff attaches a best-effort CLI trace handoff to a request context.
func WithTraceHandoff(ctx context.Context, handoff *TraceHandoff) context.Context {
	if ctx == nil || handoff == nil {
		return ctx
	}
	return context.WithValue(ctx, traceHandoffContextKey{}, handoff)
}

// TraceHandoffFromContext returns a CLI trace handoff attached to ctx.
func TraceHandoffFromContext(ctx context.Context) (*TraceHandoff, bool) {
	if ctx == nil {
		return nil, false
	}
	handoff, ok := ctx.Value(traceHandoffContextKey{}).(*TraceHandoff)
	if !ok || handoff == nil {
		return nil, false
	}
	return handoff, true
}

type cliTraceIngestResponse struct {
	HandoffToken string `json:"handoff_token"`
	TraceID      string `json:"trace_id"`
	RequestID    string `json:"request_id"`
}

type cliTraceExporter struct {
	client    *Client
	requestID string
	result    *TraceHandoff
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

// ExportDevTraceHandoff creates a best-effort root trace for a revyl dev loop.
func (c *Client) ExportDevTraceHandoff(ctx context.Context) (*TraceHandoff, error) {
	return c.exportRootCLITrace(ctx, newTraceRequestID(), cliDevTraceSpanName, revylCLICommandDev)
}

func (c *Client) exportStartDeviceTrace(ctx context.Context, requestID string) (*TraceHandoff, error) {
	return c.exportRootCLITrace(ctx, requestID, cliTraceSpanName, revylCLICommandDeviceStart)
}

func (c *Client) exportRootCLITrace(ctx context.Context, requestID string, spanName string, command string) (*TraceHandoff, error) {
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
		spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("cli.version", c.effectiveVersion()),
			attribute.String("os.type", runtime.GOOS),
			attribute.String("os.arch", runtime.GOARCH),
			attribute.String("revyl.client", "cli"),
			attribute.String("revyl.command", command),
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

func (c *Client) postCLITrace(ctx context.Context, requestID string, payload []byte) (*TraceHandoff, error) {
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
	return &TraceHandoff{
		HandoffToken: body.HandoffToken,
		TraceID:      body.TraceID,
		RequestID:    body.RequestID,
	}, nil
}

func (c *Client) postCLISpan(ctx context.Context, requestID string, payload []byte) error {
	exportCtx, cancel := context.WithTimeout(ctx, cliTraceExportTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		exportCtx,
		http.MethodPost,
		c.baseURL+cliSpanIngestPath,
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", otlpProtobufContentType)
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	req.Header.Set(traceRequestIDHeader, requestID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("CLI span ingest returned status %d", resp.StatusCode)
	}
	return nil
}

// HotReloadLocalMetroSpanInput records the local Metro side of a relayed request.
type HotReloadLocalMetroSpanInput struct {
	ParentTraceparent string
	Method            string
	Path              string
	RequestClass      string
	Platform          string
	StatusCode        int
	StartedAt         time.Time
	EndedAt           time.Time
	TTFB              time.Duration
	FirstBodyByte     time.Duration
	Error             string
}

// ExportHotReloadLocalMetroSpan exports a best-effort child span for a local Metro request.
func (c *Client) ExportHotReloadLocalMetroSpan(ctx context.Context, input HotReloadLocalMetroSpanInput) error {
	if strings.TrimSpace(c.apiKey) == "" {
		return fmt.Errorf("missing API key")
	}
	traceID, parentSpanID, ok := parseTraceparentIDs(input.ParentTraceparent)
	if !ok {
		return fmt.Errorf("invalid parent traceparent")
	}
	spanID, err := randomTraceSpanID()
	if err != nil {
		return err
	}
	requestID := newTraceRequestID()
	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	endedAt := input.EndedAt
	if endedAt.IsZero() || endedAt.Before(startedAt) {
		endedAt = startedAt
	}

	attrs := []attribute.KeyValue{
		attribute.String("cli.version", c.effectiveVersion()),
		attribute.String("os.type", runtime.GOOS),
		attribute.String("os.arch", runtime.GOARCH),
		attribute.String("revyl.client", "cli"),
		attribute.String("revyl.command", revylCLICommandLocalMetro),
		attribute.String("revyl.request_id", requestID),
		attribute.String("http.method", strings.ToUpper(strings.TrimSpace(input.Method))),
		attribute.String("hotreload.request_class", strings.TrimSpace(input.RequestClass)),
		attribute.String("hotreload.platform", strings.TrimSpace(input.Platform)),
		attribute.String("hotreload.path", sanitizeSpanPath(input.Path)),
	}
	if input.StatusCode > 0 {
		attrs = append(attrs, attribute.Int("http.status_code", input.StatusCode))
	}
	if input.TTFB > 0 {
		attrs = append(attrs, attribute.Int64("hotreload.ttfb_ms", input.TTFB.Milliseconds()))
	}
	if input.FirstBodyByte > 0 {
		attrs = append(attrs, attribute.Int64("hotreload.first_body_byte_ms", input.FirstBodyByte.Milliseconds()))
	}
	errMessage := strings.TrimSpace(input.Error)
	if errMessage != "" {
		attrs = append(attrs, attribute.String("error.message", truncateAttribute(errMessage, 200)))
	}

	status := &tracepb.Status{Code: tracepb.Status_STATUS_CODE_UNSET}
	if errMessage != "" || input.StatusCode >= 500 {
		status.Code = tracepb.Status_STATUS_CODE_ERROR
		status.Message = truncateAttribute(errMessage, 200)
	} else if input.StatusCode > 0 && input.StatusCode < 400 {
		status.Code = tracepb.Status_STATUS_CODE_OK
	}

	payload, err := marshalSingleSpan(traceID, spanID, parentSpanID, cliLocalMetroSpanName, tracepb.Span_SPAN_KIND_CLIENT, startedAt, endedAt, attrs, status, c.effectiveVersion())
	if err != nil {
		return err
	}
	return c.postCLISpan(ctx, requestID, payload)
}

func exportRequestForSpan(span sdktrace.ReadOnlySpan) ([]byte, string, error) {
	if !isAllowedCLIRootSpanName(span.Name()) {
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

func isAllowedCLIRootSpanName(name string) bool {
	switch name {
	case cliTraceSpanName, cliDevTraceSpanName:
		return true
	default:
		return false
	}
}

func marshalSingleSpan(
	traceID []byte,
	spanID []byte,
	parentSpanID []byte,
	name string,
	kind tracepb.Span_SpanKind,
	startedAt time.Time,
	endedAt time.Time,
	attrs []attribute.KeyValue,
	status *tracepb.Status,
	version string,
) ([]byte, error) {
	span := &tracepb.Span{
		TraceId:           append([]byte(nil), traceID...),
		SpanId:            append([]byte(nil), spanID...),
		ParentSpanId:      append([]byte(nil), parentSpanID...),
		Name:              name,
		Kind:              kind,
		StartTimeUnixNano: uint64(startedAt.UnixNano()),
		EndTimeUnixNano:   uint64(endedAt.UnixNano()),
		Attributes:        attributesToOTLP(attrs),
		Status:            status,
	}
	req := &tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: attributesToOTLP([]attribute.KeyValue{
						attribute.String("service.name", "revyl-cli"),
						attribute.String("service.version", version),
					}),
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Scope: &commonpb.InstrumentationScope{Name: revylCLIInstrumentation},
						Spans: []*tracepb.Span{span},
					},
				},
			},
		},
	}
	return proto.Marshal(req)
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

func parseTraceparentIDs(value string) ([]byte, []byte, bool) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(value)), "-")
	if len(parts) != 4 || parts[0] == "ff" || len(parts[1]) != 32 || len(parts[2]) != 16 {
		return nil, nil, false
	}
	traceID, err := hex.DecodeString(parts[1])
	if err != nil || len(traceID) != 16 || allZeroBytes(traceID) {
		return nil, nil, false
	}
	spanID, err := hex.DecodeString(parts[2])
	if err != nil || len(spanID) != 8 || allZeroBytes(spanID) {
		return nil, nil, false
	}
	return traceID, spanID, true
}

func randomTraceSpanID() ([]byte, error) {
	for {
		out := make([]byte, 8)
		if _, err := rand.Read(out); err != nil {
			return nil, err
		}
		if !allZeroBytes(out) {
			return out, nil
		}
	}
}

func allZeroBytes(raw []byte) bool {
	for _, b := range raw {
		if b != 0 {
			return false
		}
	}
	return true
}

func sanitizeSpanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if len(path) > 200 {
		return path[:200]
	}
	return path
}

func truncateAttribute(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

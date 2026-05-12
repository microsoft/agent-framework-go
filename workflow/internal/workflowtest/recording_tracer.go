// Copyright (c) Microsoft. All rights reserved.

package workflowtest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow/observability"
)

// RecordingTracer records workflow observability spans for tests.
type RecordingTracer struct {
	mu    sync.Mutex
	spans []*RecordingSpan
}

type recordingSpanContextKey struct{}

// NewRecordingTracer creates an empty workflow telemetry recorder.
func NewRecordingTracer() *RecordingTracer {
	return &RecordingTracer{}
}

func (tracer *RecordingTracer) Start(ctx context.Context, name string, options observability.SpanOptions) (context.Context, observability.Span) {
	span := &RecordingSpan{name: name, attrs: make(map[string]any), options: options}
	tracer.mu.Lock()
	tracer.spans = append(tracer.spans, span)
	tracer.mu.Unlock()
	return context.WithValue(ctx, recordingSpanContextKey{}, map[string]string{"recording-span": name}), span
}

func (*RecordingTracer) ExtractTraceContext(ctx context.Context) map[string]string {
	traceContext, ok := ctx.Value(recordingSpanContextKey{}).(map[string]string)
	if !ok || len(traceContext) == 0 {
		return nil
	}
	traceContextCopy := make(map[string]string, len(traceContext))
	for key, value := range traceContext {
		traceContextCopy[key] = value
	}
	return traceContextCopy
}

// Spans returns a snapshot of spans started by this tracer.
func (tracer *RecordingTracer) Spans() []*RecordingSpan {
	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	return append([]*RecordingSpan(nil), tracer.spans...)
}

// LastSpan returns the most recently started span.
func (tracer *RecordingTracer) LastSpan(t testing.TB) *RecordingSpan {
	t.Helper()
	spans := tracer.Spans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}
	return spans[len(spans)-1]
}

// CountSpansWithPrefix returns the number of spans whose name starts with prefix.
func CountSpansWithPrefix(spans []*RecordingSpan, prefix string) int {
	var count int
	for _, span := range spans {
		if strings.HasPrefix(span.Name(), prefix) {
			count++
		}
	}
	return count
}

// FindSpanWithPrefix returns the first span whose name starts with prefix.
func FindSpanWithPrefix(t testing.TB, spans []*RecordingSpan, prefix string) *RecordingSpan {
	t.Helper()
	for _, span := range spans {
		if strings.HasPrefix(span.Name(), prefix) {
			return span
		}
	}
	t.Fatalf("no span with prefix %q; spans: %v", prefix, SpanNames(spans))
	return nil
}

// SpanNames returns span names for assertion failures.
func SpanNames(spans []*RecordingSpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	return names
}

// RecordingSpan records workflow span operations for tests.
type RecordingSpan struct {
	mu      sync.Mutex
	name    string
	attrs   map[string]any
	events  []RecordingEvent
	ended   bool
	options observability.SpanOptions
	errors  []error
	status  string
}

// RecordingEvent records a span event.
type RecordingEvent struct {
	Name       string
	Attributes []observability.Attribute
}

func (span *RecordingSpan) End() {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.ended = true
}

func (span *RecordingSpan) AddEvent(name string, attrs ...observability.Attribute) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.events = append(span.events, RecordingEvent{Name: name, Attributes: append([]observability.Attribute(nil), attrs...)})
}

func (span *RecordingSpan) SetAttributes(attrs ...observability.Attribute) {
	span.mu.Lock()
	defer span.mu.Unlock()
	for _, attr := range attrs {
		span.attrs[attr.Key] = attr.Value
	}
}

func (span *RecordingSpan) RecordError(err error) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.errors = append(span.errors, err)
}

func (span *RecordingSpan) SetError(message string) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.status = message
}

// Name returns the span name.
func (span *RecordingSpan) Name() string {
	span.mu.Lock()
	defer span.mu.Unlock()
	return span.name
}

// Attribute returns a recorded attribute value.
func (span *RecordingSpan) Attribute(key string) (any, bool) {
	span.mu.Lock()
	defer span.mu.Unlock()
	value, ok := span.attrs[key]
	return value, ok
}

// Events returns a snapshot of recorded events.
func (span *RecordingSpan) Events() []RecordingEvent {
	span.mu.Lock()
	defer span.mu.Unlock()
	return append([]RecordingEvent(nil), span.events...)
}

// Ended reports whether End was called.
func (span *RecordingSpan) Ended() bool {
	span.mu.Lock()
	defer span.mu.Unlock()
	return span.ended
}

// Options returns the span start options.
func (span *RecordingSpan) Options() observability.SpanOptions {
	span.mu.Lock()
	defer span.mu.Unlock()
	return span.options
}

// RequireEvent fails the test if the span does not include the named event.
func (span *RecordingSpan) RequireEvent(t testing.TB, name string) {
	t.Helper()
	for _, event := range span.Events() {
		if event.Name == name {
			return
		}
	}
	t.Fatalf("span %q missing event %q", span.Name(), name)
}

// RequireAttribute fails the test if the span does not include the attribute.
func (span *RecordingSpan) RequireAttribute(t testing.TB, key string) any {
	t.Helper()
	value, ok := span.Attribute(key)
	if !ok {
		t.Fatalf("span %q missing attribute %q", span.Name(), key)
	}
	return value
}

// RequireAttributeValue fails the test if the span attribute is missing or not equal to want.
func (span *RecordingSpan) RequireAttributeValue(t testing.TB, key string, want any) {
	t.Helper()
	got := span.RequireAttribute(t, key)
	if got != want {
		t.Fatalf("span %q attribute %q = %v, want %v", span.Name(), key, got, want)
	}
}

// RequireOptionalStringAttribute fails if presence or string content does not match expectations.
func (span *RecordingSpan) RequireOptionalStringAttribute(t testing.TB, key string, wantPresent bool, contains string) {
	t.Helper()
	value, ok := span.Attribute(key)
	if !wantPresent {
		if ok {
			t.Fatalf("span %q attribute %q = %v, want omitted", span.Name(), key, value)
		}
		return
	}
	if !ok {
		t.Fatalf("span %q missing attribute %q", span.Name(), key)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("span %q attribute %q = %T, want string", span.Name(), key, value)
	}
	if !strings.Contains(text, contains) {
		t.Fatalf("span %q attribute %q = %q, want to contain %q", span.Name(), key, text, contains)
	}
}

// RequireOmittedAttribute fails if the span includes the attribute.
func (span *RecordingSpan) RequireOmittedAttribute(t testing.TB, key string) {
	t.Helper()
	if value, ok := span.Attribute(key); ok {
		t.Fatalf("span %q attribute %q = %v, want omitted", span.Name(), key, value)
	}
}

// RequireEnded fails the test if End was not called.
func (span *RecordingSpan) RequireEnded(t testing.TB) {
	t.Helper()
	if !span.Ended() {
		t.Fatalf("span %q was not ended", span.Name())
	}
}

var (
	_ observability.Tracer = (*RecordingTracer)(nil)
	_ observability.Span   = (*RecordingSpan)(nil)
)

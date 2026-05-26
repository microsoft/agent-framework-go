// Copyright (c) Microsoft. All rights reserved.

package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	workflowobservability "github.com/microsoft/agent-framework-go/workflow/observability"
)

const (
	ActivityWorkflowBuild    = "workflow.build"
	ActivityWorkflowSession  = "workflow.session"
	ActivityWorkflowInvoke   = "workflow_invoke"
	ActivityMessageSend      = "message.send"
	ActivityExecutorProcess  = "executor.process"
	ActivityEdgeGroupProcess = "edge_group.process"

	EventBuildStarted             = "build.started"
	EventBuildValidationCompleted = "build.validation_completed"
	EventBuildCompleted           = "build.completed"
	EventBuildError               = "build.error"
	EventSessionStarted           = "session.started"
	EventSessionCompleted         = "session.completed"
	EventSessionError             = "session.error"
	EventWorkflowStarted          = "workflow.started"
	EventWorkflowCompleted        = "workflow.completed"
	EventWorkflowError            = "workflow.error"

	TagWorkflowID              = "workflow.id"
	TagWorkflowName            = "workflow.name"
	TagWorkflowDescription     = "workflow.description"
	TagWorkflowDefinition      = "workflow.definition"
	TagBuildErrorMessage       = "build.error.message"
	TagBuildErrorType          = "build.error.type"
	TagErrorType               = "error.type"
	TagErrorMessage            = "error.message"
	TagSessionID               = "session.id"
	TagExecutorID              = "executor.id"
	TagImplementationID        = "executor.implementation.id"
	TagExecutorInput           = "executor.input"
	TagExecutorOutput          = "executor.output"
	TagMessageType             = "message.type"
	TagMessageContent          = "message.content"
	TagEdgeGroupType           = "edge_group.type"
	TagMessageSourceID         = "message.source_id"
	TagMessageTargetID         = "message.target_id"
	TagEdgeGroupDelivered      = "edge_group.delivered"
	TagEdgeGroupDeliveryStatus = "edge_group.delivery_status"
)

type DeliveryStatus string

const (
	DeliveryStatusDelivered             DeliveryStatus = "delivered"
	DeliveryStatusDroppedTypeMismatch   DeliveryStatus = "dropped type mismatch"
	DeliveryStatusDroppedTargetMismatch DeliveryStatus = "dropped target mismatch"
	DeliveryStatusDroppedConditionFalse DeliveryStatus = "dropped condition false"
	DeliveryStatusException             DeliveryStatus = "exception"
	DeliveryStatusBuffered              DeliveryStatus = "buffered"
)

type WorkflowMetadata struct {
	ID          string
	Name        string
	Description string
	SessionID   string
}

type EdgeGroupMetadata struct {
	Type     string
	SourceID string
	TargetID string
}

type Context struct {
	enabled bool
	options Options
	tracer  workflowobservability.Tracer
}

var disabled = &Context{}

func Disabled() *Context {
	return disabled
}

type Options struct {
	Tracer workflowobservability.Tracer

	// EnableSensitiveData includes serialized message inputs, outputs, and
	// message contents in span attributes. It is disabled by default.
	EnableSensitiveData bool

	DisableWorkflowBuild    bool
	DisableWorkflowRun      bool
	DisableExecutorProcess  bool
	DisableEdgeGroupProcess bool
	DisableMessageSend      bool
}

func New(options Options) *Context {
	return &Context{
		enabled: options.Tracer != nil,
		options: options,
		tracer:  options.Tracer,
	}
}

func (c *Context) IsEnabled() bool {
	return c != nil && c.enabled
}

func (c *Context) optionsOrZero() Options {
	if c == nil {
		return Options{}
	}
	return c.options
}

type Activity struct {
	span workflowobservability.Span
}

func (s *Activity) End() {
	if s != nil && s.span != nil {
		s.span.End()
	}
}

func (s *Activity) AddEvent(name string, attrs ...workflowobservability.Attribute) {
	if s != nil && s.span != nil {
		s.span.AddEvent(name, attrs...)
	}
}

func (s *Activity) SetAttributes(attrs ...workflowobservability.Attribute) {
	if s != nil && s.span != nil {
		s.span.SetAttributes(attrs...)
	}
}

func (s *Activity) CaptureError(err error) {
	if s == nil || s.span == nil || err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetAttributes(
		workflowobservability.StringAttribute(TagErrorType, reflect.TypeOf(err).String()),
		workflowobservability.StringAttribute(TagErrorMessage, err.Error()),
	)
	s.span.SetError(err.Error())
}

func (s *Activity) AddErrorEvent(name string, err error) {
	if err == nil {
		s.AddEvent(name)
		return
	}
	s.AddEvent(name, ErrorAttributes(err)...)
}

func (s *Activity) SetDeliveryStatus(status DeliveryStatus) {
	if s == nil || s.span == nil {
		return
	}
	s.span.SetAttributes(
		workflowobservability.BoolAttribute(TagEdgeGroupDelivered, status == DeliveryStatusDelivered),
		workflowobservability.StringAttribute(TagEdgeGroupDeliveryStatus, string(status)),
	)
}

func (c *Context) start(ctx context.Context, name string, options workflowobservability.SpanOptions) (context.Context, *Activity) {
	if !c.IsEnabled() {
		return ctx, nil
	}
	ctx, span := c.tracer.Start(ctx, name, options)
	return ctx, &Activity{span: span}
}

func (c *Context) StartWorkflowBuild(ctx context.Context) (context.Context, *Activity) {
	if c.optionsOrZero().DisableWorkflowBuild {
		return ctx, nil
	}
	return c.start(ctx, ActivityWorkflowBuild, workflowobservability.SpanOptions{})
}

func (c *Context) StartWorkflowSession(ctx context.Context, metadata WorkflowMetadata) (context.Context, *Activity) {
	if c.optionsOrZero().DisableWorkflowRun {
		return ctx, nil
	}
	ctx, span := c.start(ctx, ActivityWorkflowSession, workflowobservability.SpanOptions{})
	setWorkflowAttributes(span, metadata)
	span.AddEvent(EventSessionStarted)
	return ctx, span
}

func (c *Context) StartWorkflowRun(ctx context.Context, metadata WorkflowMetadata) (context.Context, *Activity) {
	if c.optionsOrZero().DisableWorkflowRun {
		return ctx, nil
	}
	ctx, span := c.start(ctx, ActivityWorkflowInvoke, workflowobservability.SpanOptions{})
	setWorkflowAttributes(span, metadata)
	return ctx, span
}

func (c *Context) StartExecutorProcess(ctx context.Context, executorID, implementationID, messageType string, message any, traceContext map[string]string) (context.Context, *Activity) {
	if c.optionsOrZero().DisableExecutorProcess {
		return ctx, nil
	}
	ctx, span := c.start(ctx, ActivityExecutorProcess+" "+executorID, workflowobservability.SpanOptions{SourceTraceContext: traceContext})
	span.SetAttributes(
		workflowobservability.StringAttribute(TagExecutorID, executorID),
		workflowobservability.StringAttribute(TagImplementationID, implementationID),
		workflowobservability.StringAttribute(TagMessageType, messageType),
	)
	if c.optionsOrZero().EnableSensitiveData {
		span.SetAttributes(SerializedAttribute(TagExecutorInput, message))
	}
	return ctx, span
}

func (c *Context) SetExecutorOutput(span *Activity, output any) {
	if c.optionsOrZero().EnableSensitiveData {
		span.SetAttributes(SerializedAttribute(TagExecutorOutput, output))
	}
}

func (c *Context) StartEdgeGroupProcess(ctx context.Context, metadata EdgeGroupMetadata) (context.Context, *Activity) {
	if c.optionsOrZero().DisableEdgeGroupProcess {
		return ctx, nil
	}
	ctx, span := c.start(ctx, ActivityEdgeGroupProcess, workflowobservability.SpanOptions{})
	attrs := make([]workflowobservability.Attribute, 0, 3)
	if metadata.Type != "" {
		attrs = append(attrs, workflowobservability.StringAttribute(TagEdgeGroupType, metadata.Type))
	}
	if metadata.SourceID != "" {
		attrs = append(attrs, workflowobservability.StringAttribute(TagMessageSourceID, metadata.SourceID))
	}
	if metadata.TargetID != "" {
		attrs = append(attrs, workflowobservability.StringAttribute(TagMessageTargetID, metadata.TargetID))
	}
	span.SetAttributes(attrs...)
	return ctx, span
}

func (c *Context) StartMessageSend(ctx context.Context, sourceID, targetID string, message any) (context.Context, *Activity) {
	if c.optionsOrZero().DisableMessageSend {
		return ctx, nil
	}
	ctx, span := c.start(ctx, ActivityMessageSend, workflowobservability.SpanOptions{Kind: workflowobservability.SpanKindProducer})
	span.SetAttributes(workflowobservability.StringAttribute(TagMessageSourceID, sourceID))
	if targetID != "" {
		span.SetAttributes(workflowobservability.StringAttribute(TagMessageTargetID, targetID))
	}
	if c.optionsOrZero().EnableSensitiveData {
		span.SetAttributes(SerializedAttribute(TagMessageContent, message))
	}
	return ctx, span
}

func (c *Context) ExtractTraceContext(ctx context.Context) map[string]string {
	if !c.IsEnabled() {
		return nil
	}
	return c.tracer.ExtractTraceContext(ctx)
}

func SetBuildWorkflowAttributes(span *Activity, metadata WorkflowMetadata, definition any) {
	setWorkflowAttributes(span, metadata)
	span.SetAttributes(SerializedAttribute(TagWorkflowDefinition, definition))
}

func BuildErrorAttributes(err error) []workflowobservability.Attribute {
	if err == nil {
		return nil
	}
	return []workflowobservability.Attribute{
		workflowobservability.StringAttribute(TagBuildErrorMessage, err.Error()),
		workflowobservability.StringAttribute(TagBuildErrorType, reflect.TypeOf(err).String()),
	}
}

func ErrorAttributes(err error) []workflowobservability.Attribute {
	if err == nil {
		return nil
	}
	return []workflowobservability.Attribute{
		workflowobservability.StringAttribute(TagErrorType, reflect.TypeOf(err).String()),
		workflowobservability.StringAttribute(TagErrorMessage, err.Error()),
	}
}

func SerializedAttribute(key string, value any) workflowobservability.Attribute {
	return workflowobservability.StringAttribute(key, serialize(value))
}

func setWorkflowAttributes(span *Activity, metadata WorkflowMetadata) {
	attrs := []workflowobservability.Attribute{workflowobservability.StringAttribute(TagWorkflowID, metadata.ID)}
	if metadata.Name != "" {
		attrs = append(attrs, workflowobservability.StringAttribute(TagWorkflowName, metadata.Name))
	}
	if metadata.Description != "" {
		attrs = append(attrs, workflowobservability.StringAttribute(TagWorkflowDescription, metadata.Description))
	}
	if metadata.SessionID != "" {
		attrs = append(attrs, workflowobservability.StringAttribute(TagSessionID, metadata.SessionID))
	}
	span.SetAttributes(attrs...)
}

func serialize(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("[Unserializable: %T]", value)
	}
	return string(data)
}

type contextKey struct{}

func ContextWithTelemetry(ctx context.Context, telemetry *Context) context.Context {
	if telemetry == nil {
		telemetry = Disabled()
	}
	return context.WithValue(ctx, contextKey{}, telemetry)
}

func FromContext(ctx context.Context) *Context {
	if ctx == nil {
		return Disabled()
	}
	if telemetry, ok := ctx.Value(contextKey{}).(*Context); ok && telemetry != nil {
		return telemetry
	}
	return Disabled()
}

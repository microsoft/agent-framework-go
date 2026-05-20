// Copyright (c) Microsoft. All rights reserved.

package messageworkflow

import (
	"iter"
	"reflect"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

// ForwardingOptions configures [ConfigureForwarding].
type ForwardingOptions struct {
	// StringMessageRole, when set, enables string input and forwards each string
	// as a [message.Message] with this role.
	StringMessageRole message.Role
}

// ConfigureForwarding extends spec with forwarding behavior for messages and
// turn tokens. The configured spec accepts
// [*message.Message], []*message.Message, iter.Seq[*message.Message], and
// [workflow.TurnToken]. If options.StringMessageRole is set, it also accepts
// string and forwards it as a single text message with that role.
func ConfigureForwarding(spec *workflow.ExecutorSpec, options *ForwardingOptions) {
	if spec == nil {
		panic("messageworkflow: spec is required")
	}

	var stringMessageRole message.Role
	if options != nil {
		stringMessageRole = options.StringMessageRole
	}

	forwardingSpec := workflow.ExecutorSpec{
		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
		SendTypes: []reflect.Type{
			reflect.TypeFor[*message.Message](),
			reflect.TypeFor[[]*message.Message](),
			reflect.TypeFor[workflow.TurnToken](),
		},
		Reset: func() error { return nil },
		ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
			if stringMessageRole != "" {
				rb = rb.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, ctx.SendMessage("", &message.Message{
						Role:     stringMessageRole,
						Contents: []message.Content{&message.TextContent{Text: msg.(string)}},
					})
				})
			}
			return rb.
				AddHandlerRaw(reflect.TypeFor[*message.Message](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, ctx.SendMessage("", msg.(*message.Message))
				}).
				AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, ctx.SendMessage("", msg.([]*message.Message))
				}).
				AddHandlerRaw(reflect.TypeFor[iter.Seq[*message.Message]](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					messages := make([]*message.Message, 0)
					for msg := range msg.(iter.Seq[*message.Message]) {
						messages = append(messages, msg)
					}
					return struct{}{}, ctx.SendMessage("", messages)
				}).
				AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, ctx.SendMessage("", msg.(workflow.TurnToken))
				}), nil
		},
	}
	spec.Extend(forwardingSpec)
}

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

// ConfigureForwarding extends executor with forwarding behavior for messages and
// turn tokens. The configured executor accepts
// [*message.Message], []*message.Message, iter.Seq[*message.Message], and
// [workflow.TurnToken]. If options.StringMessageRole is set, it also accepts
// string and forwards it as a single text message with that role.
func ConfigureForwarding(executor *workflow.Executor, options *ForwardingOptions) {
	if executor == nil {
		panic("messageworkflow: executor is required")
	}

	var stringMessageRole message.Role
	if options != nil {
		stringMessageRole = options.StringMessageRole
	}

	forwardingExecutor := workflow.Executor{
		ResetFunc: func() error { return nil },
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(
				reflect.TypeFor[*message.Message](),
				reflect.TypeFor[[]*message.Message](),
				reflect.TypeFor[workflow.TurnToken](),
			)
			if stringMessageRole != "" {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, forwardStringMessage(stringMessageRole))
			}
			rb.RouteBuilder.
				AddHandlerRaw(reflect.TypeFor[*message.Message](), nil, forwardMessage).
				AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, forwardMessages).
				AddHandlerRaw(reflect.TypeFor[iter.Seq[*message.Message]](), nil, forwardMessageSeq).
				AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, forwardTurnToken)
			return rb, nil
		},
	}
	executor.Extend(&forwardingExecutor)
}

func forwardStringMessage(role message.Role) func(*workflow.Context, any) (any, error) {
	return func(ctx *workflow.Context, msg any) (any, error) {
		return struct{}{}, ctx.SendMessage("", &message.Message{
			Role:     role,
			Contents: []message.Content{&message.TextContent{Text: msg.(string)}},
		})
	}
}

func forwardMessage(ctx *workflow.Context, msg any) (any, error) {
	return struct{}{}, ctx.SendMessage("", msg.(*message.Message))
}

func forwardMessages(ctx *workflow.Context, msg any) (any, error) {
	return struct{}{}, ctx.SendMessage("", msg.([]*message.Message))
}

func forwardMessageSeq(ctx *workflow.Context, msg any) (any, error) {
	return struct{}{}, ctx.SendMessage("", messageSeqToSlice(msg.(iter.Seq[*message.Message])))
}

func forwardTurnToken(ctx *workflow.Context, msg any) (any, error) {
	return struct{}{}, ctx.SendMessage("", msg.(workflow.TurnToken))
}

func messageSeqToSlice(seq iter.Seq[*message.Message]) []*message.Message {
	messages := make([]*message.Message, 0)
	for msg := range seq {
		messages = append(messages, msg)
	}
	return messages
}

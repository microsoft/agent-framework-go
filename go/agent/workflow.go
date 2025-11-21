// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/workflowext"
)

type messagesExecutorOptions struct {
	StringMessageRole string
}

type messagesExecutor struct {
	workflowext.StatefulExecutor[[]*message.Message]

	Options         messagesExecutorOptions
	TakeTurnHandler func(ctx context.Context, wctx workflow.Context, emitEvents bool, messages []*message.Message) error

	bld workflow.RouteBuilder
}

func (e *messagesExecutor) Router() (*workflow.MessageRouter, error) {
	if route, err, ok := e.bld.Cached(); ok {
		return route, err
	}
	if e.Options.StringMessageRole == "" {
		workflowext.RouteBuilderAddHandler1(&e.bld, false, func(ctx context.Context, wctx workflow.Context, msg workflow.Value) error {
			return e.addMessage(ctx, wctx, &message.Message{Role: message.Role(e.Options.StringMessageRole)})
		})
	}
	workflowext.RouteBuilderAddHandler1(&e.bld, false, e.addMessage)
	workflowext.RouteBuilderAddHandler1(&e.bld, false, e.addMessages)
	workflowext.RouteBuilderAddHandler1(&e.bld, false, e.addMessageIter)
	workflowext.RouteBuilderAddHandler1(&e.bld, false, e.takeTurn)
	return e.bld.Build()
}

func (e *messagesExecutor) addMessage(ctx context.Context, wctx workflow.Context, msg *message.Message) error {
	return e.InvokeWithState(ctx, wctx, false, func(ctx context.Context, wctx workflow.Context, state []*message.Message) ([]*message.Message, error) {
		return append(state, msg), nil
	})
}

func (e *messagesExecutor) addMessages(ctx context.Context, wctx workflow.Context, msgs []*message.Message) error {
	return e.InvokeWithState(ctx, wctx, false, func(ctx context.Context, wctx workflow.Context, state []*message.Message) ([]*message.Message, error) {
		return append(state, msgs...), nil
	})
}

func (e *messagesExecutor) addMessageIter(ctx context.Context, wctx workflow.Context, msgs iter.Seq[*message.Message]) error {
	return e.InvokeWithState(ctx, wctx, false, func(ctx context.Context, wctx workflow.Context, state []*message.Message) ([]*message.Message, error) {
		for msg := range msgs {
			state = append(state, msg)
		}
		return state, nil
	})
}

func (e *messagesExecutor) takeTurn(ctx context.Context, wctx workflow.Context, token workflow.TurnToken) error {
	return e.InvokeWithState(ctx, wctx, false, func(ctx context.Context, wctx workflow.Context, state []*message.Message) ([]*message.Message, error) {
		if err := e.TakeTurnHandler(ctx, wctx, token.EmitEvents, state); err != nil {
			return nil, err
		}
		if err := wctx.SendMessage(ctx, token, ""); err != nil {
			return nil, err
		}
		// Reset the state to empty list.
		return nil, nil
	})
}

type runUpdateEvent struct {
	ExecutorID string
	Update     *RunResponseUpdate
}

func (e runUpdateEvent) Data() any {
	return e.Update
}

type hostExecutor struct {
	messagesExecutor

	emitEvents bool
	agent      Agent
	thread     memory.Thread
}

func NewWorkflowExecutor(agent Agent, emitEvents bool) workflow.Executor {
	e := &hostExecutor{
		agent:            agent,
		emitEvents:       emitEvents,
		messagesExecutor: messagesExecutor{},
	}
	e.TakeTurnHandler = e.takeTurnHandler
	return e
}

func (e *hostExecutor) ID() string {
	return e.agent.ID()
}

func (e *hostExecutor) ensureThread() memory.Thread {
	if e.thread == nil {
		e.thread = e.agent.NewThread()
	}
	return e.thread
}

func (e *hostExecutor) takeTurnHandler(ctx context.Context, wctx workflow.Context, emitEvents bool, messages []*message.Message) error {
	if !emitEvents {
		response, err := e.agent.Run(&RunContext{Context: ctx, Thread: e.ensureThread()}, messages...)
		if err != nil {
			return err
		}
		return wctx.SendMessage(ctx, response.Messages, "")
	}
	var updates []*RunResponseUpdate
	for update, err := range e.agent.RunStream(&RunContext{Context: ctx, Thread: e.ensureThread()}, messages...) {
		if err != nil {
			return err
		}
		if err := wctx.AddEvent(ctx, runUpdateEvent{e.ID(), update}); err != nil {
			return err
		}
		updates = append(updates, update)
	}
	msgs := make([]*message.Message, 0, len(updates))
	for _, update := range updates {
		msgs = append(msgs, &message.Message{Role: update.Role, Contents: update.Contents})
	}
	return wctx.SendMessage(ctx, msgs, "")
}

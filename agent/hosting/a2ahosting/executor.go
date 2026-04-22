// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"context"
	"fmt"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

const continuationTokenMetadataKey = "__a2a__continuationToken"

type ExecutorConfig struct {
	Agent                    *agent.Agent
	AllowBackgroundResponses bool
	// AllowBackgroundResponsesWhen is a callback that determines on a per-message basis whether background responses should be allowed.
	// If both AllowBackgroundResponses and AllowBackgroundResponsesWhen are set, the callback takes precedence.
	AllowBackgroundResponsesWhen func(context.Context, *a2asrv.ExecutorContext) (bool, error)
}

type executor struct {
	cfg ExecutorConfig
}

func NewExecutor(cfg ExecutorConfig) a2asrv.AgentExecutor {
	return &executor{cfg: cfg}
}

func (e *executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx == nil || execCtx.Message == nil {
			yield(nil, fmt.Errorf("request message is required"))
			return
		}
		if len(execCtx.Message.ReferenceTasks) > 0 {
			yield(nil, fmt.Errorf("referenceTaskIds are not supported"))
			return
		}

		allowBackground, err := e.shouldRunInBackground(ctx, execCtx)
		if err != nil {
			yield(nil, err)
			return
		}

		messagesIn, err := e.buildMessages(execCtx)
		if err != nil {
			yield(nil, err)
			return
		}

		session, err := e.cfg.Agent.CreateSession(ctx, agentopt.ServiceID(execCtx.ContextID))
		if err != nil {
			yield(nil, err)
			return
		}

		runOptions := []agentopt.Option{
			agentopt.Session(session),
			agentopt.AllowBackgroundResponses(allowBackground),
		}

		resp, runErr := e.cfg.Agent.Run(ctx, messagesIn, runOptions...).Collect()
		if runErr != nil {
			if execCtx.StoredTask != nil {
				statusMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(runErr.Error()))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, statusMsg), nil)
				return
			}
			yield(nil, runErr)
			return
		}

		if execCtx.StoredTask == nil && resp.ContinuationToken == "" {
			msg, err := responseToMessage(execCtx, resp)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(msg, nil)
			return
		}

		if execCtx.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
				return
			}
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateSubmitted, nil), nil) {
				return
			}
		}

		if resp.ContinuationToken != "" {
			var progressMessage *a2a.Message
			if len(resp.Messages) > 0 {
				progressMessage, err = responseToMessage(execCtx, resp)
				if err != nil {
					yield(nil, err)
					return
				}
			}

			working := a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, progressMessage)
			if working.Metadata == nil {
				working.Metadata = map[string]any{}
			}
			working.Metadata[continuationTokenMetadataKey] = resp.ContinuationToken
			yield(working, nil)
			return
		}

		artifact, err := responseToArtifactEvent(execCtx, resp)
		if err != nil {
			yield(nil, err)
			return
		}
		if !yield(artifact, nil) {
			return
		}

		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
	}
}

func (e *executor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx == nil || execCtx.StoredTask == nil {
			yield(nil, a2a.ErrTaskNotFound)
			return
		}
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

func (e *executor) buildMessages(execCtx *a2asrv.ExecutorContext) ([]*message.Message, error) {
	messages := make([]*message.Message, 0, 1)

	if execCtx != nil && execCtx.StoredTask != nil && len(execCtx.StoredTask.History) > 0 {
		for _, m := range execCtx.StoredTask.History {
			msg, err := toAgentMessage(m)
			if err != nil {
				return nil, err
			}
			if msg != nil {
				messages = append(messages, msg)
			}
		}
	}

	incoming, err := toAgentMessage(execCtx.Message)
	if err != nil {
		return nil, err
	}
	if incoming != nil {
		messages = append(messages, incoming)
	}

	return messages, nil
}

func (e *executor) shouldRunInBackground(ctx context.Context, decisionContext *a2asrv.ExecutorContext) (bool, error) {
	if e.cfg.AllowBackgroundResponsesWhen != nil {
		return e.cfg.AllowBackgroundResponsesWhen(ctx, decisionContext)
	}
	return e.cfg.AllowBackgroundResponses, nil
}

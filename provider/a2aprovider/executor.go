// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"errors"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const continuationTokenMetadataKey = "__a2a__continuationToken"

// ExecutorConfig defines the configuration for [NewExecutor].
type ExecutorConfig struct {
	// Whether the executor should allow background responses from the agent.
	AllowBackgroundResponses bool

	// AllowBackgroundResponsesWhen is a callback that determines on a per-message basis whether background responses should be allowed.
	// If both AllowBackgroundResponses and AllowBackgroundResponsesWhen are set, the callback takes precedence.
	AllowBackgroundResponsesWhen func(context.Context, *a2asrv.ExecutorContext) (bool, error)
}

type executor struct {
	agent *agent.Agent
	cfg   ExecutorConfig
}

// NewExecutor creates a new [a2asrv.AgentExecutor] using the provided configuration.
//
// Use the returned executor with [a2asrv.NewHandler], then wrap that request
// handler with [a2asrv.NewJSONRPCHandler] or [a2asrv.NewRESTHandler] for the
// HTTP binding you want to expose.
func NewExecutor(hostedAgent *agent.Agent, cfg ExecutorConfig) a2asrv.AgentExecutor {
	if hostedAgent == nil {
		panic("agent is required")
	}
	return &executor{agent: hostedAgent, cfg: cfg}
}

func (e *executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx == nil || execCtx.Message == nil {
			yield(nil, errors.New("request message is required"))
			return
		}
		if len(execCtx.Message.ReferenceTasks) > 0 {
			// An agent does not support resuming from arbitrary prior tasks.
			// Return an error explicitly so the client gets a clear error rather than a response
			// that silently ignores the referenced task context.
			yield(nil, errors.New("referenceTaskIds is not supported, an agent cannot resume from arbitrary prior task context"))
			return
		}

		if execCtx.StoredTask != nil {
			if err := e.executeTaskUpdate(ctx, execCtx, yield); err != nil {
				yield(nil, err)
			}
			return
		}

		if e.isStreamingRequest(ctx) {
			if err := e.executeNewMessageStreaming(ctx, execCtx, yield); err != nil {
				yield(nil, err)
			}
			return
		}

		if err := e.executeNewMessage(ctx, execCtx, yield); err != nil {
			yield(nil, err)
		}
	}
}

func (e *executor) executeNewMessage(ctx context.Context, execCtx *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) error {
	messagesIn, err := buildNewMessageInputs(execCtx.Message)
	if err != nil {
		return err
	}

	resp, err := e.runResponse(ctx, execCtx, messagesIn)
	if err != nil {
		return err
	}

	if resp.ContinuationToken == "" {
		msg, err := responseToMessage(execCtx, resp)
		if err != nil {
			return err
		}
		yield(msg, nil)
		return nil
	}

	if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
		return nil
	}
	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateSubmitted, nil), nil) {
		return nil
	}

	return yieldWorkingStatusFromResponse(execCtx, resp, yield)
}

func (e *executor) executeTaskUpdate(ctx context.Context, execCtx *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) error {
	messagesIn, err := buildTaskUpdateInputs(execCtx)
	if err != nil {
		return err
	}

	resp, runErr := e.runResponse(ctx, execCtx, messagesIn)
	if runErr != nil {
		statusMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(runErr.Error()))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, statusMsg), nil)
		return nil
	}

	if resp.ContinuationToken != "" {
		return yieldWorkingStatusFromResponse(execCtx, resp, yield)
	}

	artifact, err := responseToArtifactEvent(execCtx, resp)
	if err != nil {
		return err
	}
	if !yield(artifact, nil) {
		return nil
	}

	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
	return nil
}

func (e *executor) executeNewMessageStreaming(ctx context.Context, execCtx *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) error {
	messagesIn, err := buildNewMessageInputs(execCtx.Message)
	if err != nil {
		return err
	}

	runOptions, err := e.newRunOptions(ctx, execCtx, true)
	if err != nil {
		return err
	}

	if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
		return nil
	}
	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateSubmitted, nil), nil) {
		return nil
	}

	var artifactID a2a.ArtifactID
	var yieldedWorking bool
	for update, runErr := range e.agent.Run(ctx, messagesIn, runOptions...) {
		if runErr != nil {
			statusMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(runErr.Error()))
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, statusMsg), nil) {
				return nil
			}
			return nil
		}
		if update == nil {
			continue
		}
		if update.ContinuationToken != "" {
			working, err := responseUpdateToWorkingStatusEvent(execCtx, update)
			if err != nil {
				return err
			}
			if !yield(working, nil) {
				return nil
			}
			return nil
		}

		artifact, nextArtifactID, err := responseUpdateToArtifactEvent(execCtx, artifactID, update)
		if err != nil {
			return err
		}
		if artifact == nil {
			continue
		}
		if !yieldedWorking {
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
				return nil
			}
			yieldedWorking = true
		}
		artifactID = nextArtifactID
		if !yield(artifact, nil) {
			return nil
		}
	}

	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) {
		return nil
	}
	return nil
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

func buildNewMessageInputs(in *a2a.Message) ([]*message.Message, error) {
	incoming, err := toAgentMessage(in)
	if err != nil {
		return nil, err
	}
	if incoming == nil {
		return nil, nil
	}
	return []*message.Message{incoming}, nil
}

func buildTaskUpdateInputs(execCtx *a2asrv.ExecutorContext) ([]*message.Message, error) {
	messages := make([]*message.Message, 0, 1)
	if len(execCtx.StoredTask.History) == 0 {
		return messages, nil
	}

	for _, m := range execCtx.StoredTask.History {
		if execCtx.Message != nil && m != nil && m.ID == execCtx.Message.ID {
			continue
		}
		msg, err := toAgentMessage(m)
		if err != nil {
			return nil, err
		}
		if msg != nil {
			messages = append(messages, msg)
		}
	}

	return messages, nil
}

func yieldWorkingStatusFromResponse(execCtx *a2asrv.ExecutorContext, resp *agent.Response, yield func(a2a.Event, error) bool) error {
	var progressMessage *a2a.Message
	var err error
	if len(resp.Messages) > 0 {
		progressMessage, err = responseToMessage(execCtx, resp)
		if err != nil {
			return err
		}
	}

	working := a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, progressMessage)
	if working.Metadata == nil {
		working.Metadata = map[string]any{}
	}
	working.Metadata[continuationTokenMetadataKey] = resp.ContinuationToken
	yield(working, nil)
	return nil
}

func (e *executor) runResponse(ctx context.Context, execCtx *a2asrv.ExecutorContext, messagesIn []*message.Message) (*agent.Response, error) {
	runOptions, err := e.newRunOptions(ctx, execCtx, false)
	if err != nil {
		return nil, err
	}
	return e.agent.Run(ctx, messagesIn, runOptions...).Collect()
}

func (e *executor) newRunOptions(ctx context.Context, execCtx *a2asrv.ExecutorContext, stream bool) ([]agent.Option, error) {
	allowBackground, err := e.shouldRunInBackground(ctx, execCtx)
	if err != nil {
		return nil, err
	}

	session, err := e.agent.CreateSession(ctx, agent.WithServiceID(execCtx.ContextID))
	if err != nil {
		return nil, err
	}

	runOptions := []agent.Option{
		agent.WithSession(session),
		agent.AllowBackgroundResponses(allowBackground),
	}
	if stream {
		runOptions = append(runOptions, agent.Stream(true))
	}
	return runOptions, nil
}

func (e *executor) isStreamingRequest(ctx context.Context) bool {
	callCtx, ok := a2asrv.CallContextFrom(ctx)
	return ok && callCtx.Method() == "SendStreamingMessage"
}

func (e *executor) shouldRunInBackground(ctx context.Context, decisionContext *a2asrv.ExecutorContext) (bool, error) {
	if e.cfg.AllowBackgroundResponsesWhen != nil {
		return e.cfg.AllowBackgroundResponsesWhen(ctx, decisionContext)
	}
	return e.cfg.AllowBackgroundResponses, nil
}

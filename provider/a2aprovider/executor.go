// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"errors"
	"iter"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const (
	continuationTokenMetadataKey   = "__a2a__continuationToken"
	backgroundResponsePollInterval = time.Second
	unexpectedFailureMessage       = "The agent encountered an unexpected error and could not complete the request."
)

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

	resp, err := e.runResponse(ctx, execCtx, messagesIn, "")
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

	if ok, err := yieldWorkingStatusFromResponse(execCtx, resp, yield); err != nil || !ok {
		return err
	}
	return e.pollBackgroundResponse(ctx, execCtx, resp.ContinuationToken, yield)
}

func (e *executor) executeTaskUpdate(ctx context.Context, execCtx *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool) error {
	messagesIn, continuationToken, err := buildTaskUpdateInputs(execCtx)
	if err != nil {
		return err
	}

	resp, runErr := e.runResponse(ctx, execCtx, messagesIn, continuationToken)
	if runErr != nil {
		if isCancellationError(runErr) {
			return runErr
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, nil), nil) {
			return nil
		}
		return runErr
	}

	if resp.ContinuationToken != "" {
		if ok, err := yieldWorkingStatusFromResponse(execCtx, resp, yield); err != nil || !ok {
			return err
		}
		return e.pollBackgroundResponse(ctx, execCtx, resp.ContinuationToken, yield)
	}

	return yieldCompletedResponse(execCtx, resp, yield)
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
	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
		return nil
	}

	artifactWriter := newArtifactStreamWriter(execCtx)
	yieldedWorking := true
	for update, runErr := range e.agent.Run(ctx, messagesIn, runOptions...) {
		if runErr != nil {
			artifact, err := artifactWriter.Complete()
			if err != nil {
				return err
			}
			if artifact != nil {
				if !yieldedWorking {
					if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
						return nil
					}
				}
				if !yield(artifact, nil) {
					return nil
				}
			}

			state := a2a.TaskStateFailed
			var statusMessage *a2a.Message
			if isCallerCancellation(ctx, runErr) {
				state = a2a.TaskStateCanceled
			} else {
				statusMessage = unexpectedFailureStatusMessage()
			}
			if !yield(a2a.NewStatusUpdateEvent(execCtx, state, statusMessage), nil) {
				return nil
			}
			// a2a-go cancels its event queue when an executor error follows a
			// terminal update, dropping artifacts already queued above.
			return nil
		}
		if update == nil {
			continue
		}
		if update.ContinuationToken != "" {
			artifact, err := artifactWriter.Complete()
			if err != nil {
				return err
			}

			working, err := responseUpdateToWorkingStatusEvent(execCtx, update)
			if err != nil {
				return err
			}
			if !yield(working, nil) {
				return nil
			}

			if artifact != nil {
				if !yield(artifact, nil) {
					return nil
				}
			}
			return e.pollBackgroundResponse(ctx, execCtx, update.ContinuationToken, yield)
		}

		artifacts, writeErr := artifactWriter.Write(update)

		for _, artifact := range artifacts {
			if artifact == nil {
				continue
			}
			if !yieldedWorking {
				if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
					return nil
				}
				yieldedWorking = true
			}
			if !yield(artifact, nil) {
				return nil
			}
		}

		if writeErr != nil {
			return writeErr
		}
	}

	artifact, err := artifactWriter.Complete()
	if err != nil {
		return err
	}
	if artifact != nil {
		if !yieldedWorking {
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
				return nil
			}
		}
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

func buildTaskUpdateInputs(execCtx *a2asrv.ExecutorContext) ([]*message.Message, string, error) {
	if value, ok := execCtx.StoredTask.Metadata[continuationTokenMetadataKey]; ok {
		token, ok := value.(string)
		if !ok || token == "" {
			return nil, "", errors.New("stored A2A continuation token is invalid")
		}
		return nil, token, nil
	}
	messages := make([]*message.Message, 0, 1)
	if len(execCtx.StoredTask.History) == 0 {
		return messages, "", nil
	}

	for _, m := range execCtx.StoredTask.History {
		if execCtx.Message != nil && m != nil && m.ID == execCtx.Message.ID {
			continue
		}
		msg, err := toAgentMessage(m)
		if err != nil {
			return nil, "", err
		}
		if msg != nil {
			messages = append(messages, msg)
		}
	}

	return messages, "", nil
}

func yieldWorkingStatusFromResponse(execCtx *a2asrv.ExecutorContext, resp *agent.Response, yield func(a2a.Event, error) bool) (bool, error) {
	var progressMessage *a2a.Message
	var err error
	if len(resp.Messages) > 0 {
		progressMessage, err = responseToMessage(execCtx, resp)
		if err != nil {
			return false, err
		}
	}

	working := a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, progressMessage)
	if working.Metadata == nil {
		working.Metadata = map[string]any{}
	}
	working.Metadata[continuationTokenMetadataKey] = resp.ContinuationToken
	return yield(working, nil), nil
}

func (e *executor) pollBackgroundResponse(ctx context.Context, execCtx *a2asrv.ExecutorContext, continuationToken string, yield func(a2a.Event, error) bool) error {
	for continuationToken != "" {
		resp, runErr := e.runResponse(ctx, execCtx, nil, continuationToken)
		if runErr != nil {
			if isCancellationError(runErr) {
				if isCallerCancellation(ctx, runErr) {
					if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil) {
						return nil
					}
					return nil
				}
				return runErr
			}
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, unexpectedFailureStatusMessage()), nil) {
				return nil
			}
			return nil
		}
		if resp.ContinuationToken == "" {
			return yieldCompletedResponse(execCtx, resp, yield)
		}
		if ok, err := yieldWorkingStatusFromResponse(execCtx, resp, yield); err != nil || !ok {
			return err
		}
		continuationToken = resp.ContinuationToken

		select {
		case <-ctx.Done():
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil) {
				return nil
			}
			return nil
		case <-time.After(backgroundResponsePollInterval):
		}
	}
	return nil
}

func isCancellationError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func isCallerCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil && isCancellationError(err)
}

func unexpectedFailureStatusMessage() *a2a.Message {
	return a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(unexpectedFailureMessage))
}

func yieldCompletedResponse(execCtx *a2asrv.ExecutorContext, resp *agent.Response, yield func(a2a.Event, error) bool) error {
	artifact, err := responseToArtifactEvent(execCtx, resp)
	if err != nil {
		return err
	}
	if !yield(artifact, nil) {
		return nil
	}
	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) {
		return nil
	}
	return nil
}

func (e *executor) runResponse(ctx context.Context, execCtx *a2asrv.ExecutorContext, messagesIn []*message.Message, continuationToken string) (*agent.Response, error) {
	runOptions, err := e.newRunOptions(ctx, execCtx, false)
	if err != nil {
		return nil, err
	}
	if continuationToken != "" {
		runOptions = append(runOptions, agent.WithContinuationToken(continuationToken))
	}
	return e.agent.Run(ctx, messagesIn, runOptions...).Collect()
}

func (e *executor) newRunOptions(ctx context.Context, execCtx *a2asrv.ExecutorContext, stream bool) ([]agent.Option, error) {
	allowBackground, err := e.shouldRunInBackground(ctx, execCtx)
	if err != nil {
		return nil, err
	}

	session, err := e.agent.CreateSession(ctx)
	if err != nil {
		return nil, err
	}

	runOptions := []agent.Option{
		agent.WithSession(session),
		agent.AllowBackgroundResponses(allowBackground),
	}
	if execCtx.Metadata != nil {
		runOptions = append(runOptions, WithMetadata(execCtx.Metadata))
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

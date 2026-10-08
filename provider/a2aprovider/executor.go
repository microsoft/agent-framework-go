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
	// SessionStore persists sessions by A2A context ID. Nil creates a private
	// in-memory store for this executor. A supplied store must be scoped to the
	// hosted agent and trusted caller identity before looking up context IDs.
	SessionStore SessionStore

	// AgentRunMode selects the shape of new responses. The zero value aggregates
	// streaming agent updates into one message on both send endpoints, regardless
	// of immediate-response configuration. Use [ReturnTaskWhen] for dynamic selection.
	// Existing task continuations are unchanged.
	AgentRunMode AgentRunMode
}

type executor struct {
	agent *agent.Agent
	cfg   ExecutorConfig
}

// NewExecutor creates a new [a2asrv.AgentExecutor] using the provided configuration.
//
// For native request handling, use [NewHandler], then wrap it with
// [a2asrv.NewJSONRPCHandler] or [a2asrv.NewRESTHandler]. Using [a2asrv.NewHandler]
// directly does not forward incoming send configuration or suppress executor
// errors after terminal status updates.
func NewExecutor(hostedAgent *agent.Agent, cfg ExecutorConfig) a2asrv.AgentExecutor {
	if hostedAgent == nil {
		panic("agent is required")
	}
	if cfg.SessionStore == nil {
		cfg.SessionStore = NewInMemorySessionStore()
	}
	return &executor{agent: hostedAgent, cfg: cfg}
}

type executionState struct {
	session *agent.Session
	err     error
}

type executionStateKey struct{}

func (e *executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx == nil {
			yield(nil, errors.New("executor context is required"))
			return
		}
		if execCtx.Message != nil && len(execCtx.Message.ReferenceTasks) > 0 {
			// An agent does not support resuming from arbitrary prior tasks.
			// Return an error explicitly so the client gets a clear error rather than a response
			// that silently ignores the referenced task context.
			yield(nil, errors.New("referenceTaskIds is not supported, an agent cannot resume from arbitrary prior task context"))
			return
		}
		if execCtx.ContextID == "" {
			owned := *execCtx
			owned.ContextID = a2a.NewContextID()
			if execCtx.Message != nil && execCtx.Message.ContextID != "" {
				owned.ContextID = execCtx.Message.ContextID
			}
			execCtx = &owned
		}
		session, err := e.cfg.SessionStore.Get(ctx, execCtx.ContextID)
		if err != nil {
			yield(nil, err)
			return
		}
		if session == nil {
			session, err = e.agent.CreateSession(ctx)
			if err != nil {
				yield(nil, err)
				return
			}
		}
		var returnTask bool
		if execCtx.StoredTask == nil {
			returnTask, err = e.cfg.AgentRunMode.shouldReturnTask(ctx, execCtx)
			if err != nil {
				yield(nil, err)
				return
			}
		}
		// Persist only after a new request's mode has been selected successfully.
		// Continuations still save their session after processing, including failures.
		state := &executionState{session: session}
		ctx = context.WithValue(ctx, executionStateKey{}, state)
		stopped := false
		saved := false
		saveSession := func() error {
			saved = true
			return e.cfg.SessionStore.Save(context.WithoutCancel(ctx), execCtx.ContextID, session)
		}
		forward := func(event a2a.Event, err error) bool {
			if err != nil {
				state.err = err
			}
			if stopped {
				return false
			}
			if err == nil && !saved {
				var taskState a2a.TaskState
				switch event := event.(type) {
				case *a2a.Task:
					taskState = event.Status.State
				case *a2a.TaskStatusUpdateEvent:
					taskState = event.Status.State
				}
				if taskState == a2a.TaskStateCompleted {
					if saveErr := saveSession(); saveErr != nil {
						event, err = nil, saveErr
						state.err = saveErr
					}
				}
			}
			stopped = !yield(event, err)
			return !stopped
		}
		defer func() {
			if saved {
				return
			}
			if saveErr := saveSession(); saveErr != nil && state.err == nil && !stopped {
				forward(nil, saveErr)
			}
		}()

		if execCtx.StoredTask != nil {
			if err := e.executeTaskUpdate(ctx, execCtx, forward); err != nil {
				forward(nil, err)
			}
			return
		}
		if err := e.executeResponseMode(ctx, execCtx, returnTask, forward); err != nil {
			forward(nil, err)
		}
	}
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
	if in == nil || in.Parts == nil {
		return nil, nil
	}
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
					return runErr
				}
				return runErr
			}
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, unexpectedFailureStatusMessage()), nil) {
				return nil
			}
			return runErr
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
			return ctx.Err()
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
	state, ok := ctx.Value(executionStateKey{}).(*executionState)
	if !ok {
		session, err := e.agent.CreateSession(ctx)
		if err != nil {
			return nil, err
		}
		state = &executionState{session: session}
	}

	runOptions := []agent.Option{agent.WithSession(state.session)}
	if configuration, ok := ctx.Value(configurationKey{}).(*a2a.SendMessageConfig); ok {
		runOptions = append(runOptions, WithConfiguration(configuration))
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

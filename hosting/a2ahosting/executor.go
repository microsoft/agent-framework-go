// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"context"
	"fmt"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"
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
	AllowBackgroundResponsesWhen func(context.Context, *a2asrv.RequestContext) (bool, error)

	Options []a2asrv.RequestHandlerOption
}

type executor struct {
	cfg ExecutorConfig
}

func NewExecutor(cfg ExecutorConfig) a2asrv.AgentExecutor {
	return &executor{cfg: cfg}
}

func (e *executor) Execute(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	if reqCtx == nil || reqCtx.Message == nil {
		return fmt.Errorf("request message is required")
	}
	if len(reqCtx.Message.ReferenceTasks) > 0 {
		return fmt.Errorf("referenceTaskIds are not supported")
	}

	allowBackground, err := e.shouldRunInBackground(ctx, reqCtx)
	if err != nil {
		return err
	}

	messagesIn, err := e.buildMessages(reqCtx)
	if err != nil {
		return err
	}

	session, err := e.cfg.Agent.CreateSession(ctx, agentopt.ServiceID(reqCtx.ContextID))
	if err != nil {
		return err
	}

	runOptions := []agentopt.Option{
		agentopt.Session(session),
		agentopt.AllowBackgroundResponses(allowBackground),
	}

	resp, runErr := e.cfg.Agent.Run(ctx, messagesIn, runOptions...).Collect()
	if runErr != nil {
		if reqCtx.StoredTask != nil {
			statusErr := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateFailed, nil)
			statusErr.Final = true
			if err := queue.Write(ctx, statusErr); err != nil {
				return fmt.Errorf("failed to write failed status: %w", err)
			}
			return nil
		}
		return runErr
	}

	if reqCtx.StoredTask == nil && resp.ContinuationToken == "" {
		msg, err := responseToMessage(reqCtx, resp)
		if err != nil {
			return err
		}
		return queue.Write(ctx, msg)
	}

	if reqCtx.StoredTask == nil {
		submitted := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateSubmitted, nil)
		if err := queue.Write(ctx, submitted); err != nil {
			return fmt.Errorf("failed to write submitted status: %w", err)
		}
	}

	if resp.ContinuationToken != "" {
		var progressMessage *a2a.Message
		if len(resp.Messages) > 0 {
			progressMessage, err = responseToMessage(reqCtx, resp)
			if err != nil {
				return err
			}
		}

		working := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateWorking, progressMessage)
		working.Metadata = map[string]any{continuationTokenMetadataKey: resp.ContinuationToken}
		if err := queue.Write(ctx, working); err != nil {
			return fmt.Errorf("failed to write working status: %w", err)
		}
		return nil
	}

	artifact, err := responseToArtifactEvent(reqCtx, resp)
	if err != nil {
		return err
	}
	if err := queue.Write(ctx, artifact); err != nil {
		return fmt.Errorf("failed to write artifact event: %w", err)
	}

	completed := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCompleted, nil)
	completed.Final = true
	if err := queue.Write(ctx, completed); err != nil {
		return fmt.Errorf("failed to write completed status: %w", err)
	}

	return nil
}

func (e *executor) Cancel(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	if reqCtx == nil || reqCtx.StoredTask == nil {
		return a2a.ErrTaskNotFound
	}
	if reqCtx.StoredTask.Metadata != nil {
		delete(reqCtx.StoredTask.Metadata, continuationTokenMetadataKey)
	}

	event := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCanceled, nil)
	event.Final = true
	if err := queue.Write(ctx, event); err != nil {
		return fmt.Errorf("failed to write canceled status: %w", err)
	}
	return nil
}

func (e *executor) buildMessages(reqCtx *a2asrv.RequestContext) ([]*message.Message, error) {
	messages := make([]*message.Message, 0, 1)

	if reqCtx != nil && reqCtx.StoredTask != nil && len(reqCtx.StoredTask.History) > 0 {
		for _, m := range reqCtx.StoredTask.History {
			msg, err := toAgentMessage(m)
			if err != nil {
				return nil, err
			}
			if msg != nil {
				messages = append(messages, msg)
			}
		}
	}

	incoming, err := toAgentMessage(reqCtx.Message)
	if err != nil {
		return nil, err
	}
	if incoming != nil {
		messages = append(messages, incoming)
	}

	return messages, nil
}

func (e *executor) shouldRunInBackground(ctx context.Context, decisionContext *a2asrv.RequestContext) (bool, error) {
	if e.cfg.AllowBackgroundResponsesWhen != nil {
		return e.cfg.AllowBackgroundResponsesWhen(ctx, decisionContext)
	}
	return e.cfg.AllowBackgroundResponses, nil
}

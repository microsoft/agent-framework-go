// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
)

// AgentRunMode selects the response shape for a new hosted-agent request.
// The zero value returns a message. Modes can be copied; copying a dynamic mode
// retains its callback. Existing task continuations do not evaluate the mode.
type AgentRunMode struct {
	returnTask bool
	decide     func(context.Context, *a2asrv.ExecutorContext) (bool, error)
}

// ReturnMessage returns a mode that collects all streaming agent updates into
// one message, even for streaming or immediate-response requests.
func ReturnMessage() AgentRunMode {
	return AgentRunMode{}
}

// ReturnTask returns a mode that produces a completed task for non-streaming
// requests unless an immediate response is requested. Streaming and immediate
// requests receive submitted, working, artifact, and terminal updates.
// Continuation tokens do not interrupt the stream.
func ReturnTask() AgentRunMode {
	return AgentRunMode{returnTask: true}
}

// ReturnTaskWhen returns a mode whose callback selects a task (true) or message
// (false) for each new request. The callback receives the caller's context and a
// read-only executor context. It must support concurrent calls. Callback errors
// are returned without invoking the agent. Existing tasks do not invoke it.
// ReturnTaskWhen panics if decide is nil.
func ReturnTaskWhen(decide func(context.Context, *a2asrv.ExecutorContext) (bool, error)) AgentRunMode {
	if decide == nil {
		panic("a2aprovider: return-task callback is required")
	}
	return AgentRunMode{decide: decide}
}

// String returns "message", "task", or "dynamic".
func (m AgentRunMode) String() string {
	if m.decide != nil {
		return "dynamic"
	}
	if m.returnTask {
		return "task"
	}
	return "message"
}

func (m AgentRunMode) shouldReturnTask(ctx context.Context, execCtx *a2asrv.ExecutorContext) (bool, error) {
	if m.decide != nil {
		return m.decide(ctx, execCtx)
	}
	return m.returnTask, nil
}

func (e *executor) executeResponseMode(ctx context.Context, execCtx *a2asrv.ExecutorContext, returnTask bool, yield func(a2a.Event, error) bool) error {
	messagesIn, err := buildNewMessageInputs(execCtx.Message)
	if err != nil {
		return err
	}
	runOptions, err := e.newRunOptions(ctx, execCtx, true)
	if err != nil {
		return err
	}

	configuration, _ := agent.GetOption(runOptions, WithConfiguration)
	updates := e.agent.Run(ctx, messagesIn, runOptions...)
	if returnTask && (e.isStreamingRequest(ctx) || configuration != nil && configuration.ReturnImmediately) {
		return streamResponseModeTask(ctx, execCtx, updates, yield)
	}
	if returnTask {
		return aggregateResponseModeTask(ctx, execCtx, updates, yield)
	}

	response, err := updates.Collect()
	if err != nil {
		return err
	}
	parts, err := messagesToParts(response.Messages)
	if err != nil {
		return err
	}
	out := a2a.NewMessage(a2a.MessageRoleAgent, parts...)
	out.ContextID = execCtx.ContextID
	if response.ID != "" {
		out.ID = response.ID
	}
	out.Metadata = cloneMetadata(response.AdditionalProperties)
	yield(out, nil)
	return nil
}

func aggregateResponseModeTask(ctx context.Context, execCtx *a2asrv.ExecutorContext, updates agent.ResponseStream, yield func(a2a.Event, error) bool) error {
	response, err := updates.Collect()
	if err != nil {
		return err
	}
	// Do not publish a submitted task: native handlers return on that event.
	// Only create the task after the complete response has been collected.
	task := a2a.NewSubmittedTask(execCtx, execCtx.Message)
	parts, runErr := messagesToParts(response.Messages)
	if runErr == nil && len(parts) > 0 {
		task.Artifacts = []*a2a.Artifact{{
			ID:       a2a.NewArtifactID(),
			Parts:    parts,
			Metadata: cloneMetadata(response.AdditionalProperties),
		}}
	}
	task.Status = responseModeTerminalStatus(ctx, task, runErr).Status
	yield(task, nil)
	return runErr
}

func streamResponseModeTask(ctx context.Context, execCtx *a2asrv.ExecutorContext, updates agent.ResponseStream, yield func(a2a.Event, error) bool) error {
	task := a2a.NewSubmittedTask(execCtx, execCtx.Message)
	// Direct executors can be supplied an empty task ID. Use the submitted
	// task's generated IDs consistently without modifying the caller's context.
	owned := *execCtx
	owned.TaskID, owned.ContextID = task.ID, task.ContextID
	execCtx = &owned
	if !yield(task, nil) {
		return nil
	}
	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
		return nil
	}

	writer := newArtifactStreamWriter(execCtx)
	var runErr error
	for update, err := range updates {
		if err != nil {
			runErr = err
			break
		}
		artifacts, err := writer.Write(update)
		for _, artifact := range artifacts {
			if !yield(artifact, nil) {
				return nil
			}
		}
		if err != nil {
			runErr = err
			break
		}
	}
	artifact, err := writer.Complete()
	if err != nil {
		if runErr == nil {
			runErr = err
		}
	} else if artifact != nil && !yield(artifact, nil) {
		return nil
	}

	if !yield(responseModeTerminalStatus(ctx, execCtx, runErr), nil) {
		return nil
	}
	// Preserve the original error for direct execution. The native handler
	// boundary must not forward an executor error after this terminal event:
	// a2a-go cancels its queue and discards already-emitted artifacts in that case.
	return runErr
}

func responseModeTerminalStatus(ctx context.Context, info a2a.TaskInfoProvider, runErr error) *a2a.TaskStatusUpdateEvent {
	state := a2a.TaskStateCompleted
	var statusMessage *a2a.Message
	if runErr != nil {
		if isCallerCancellation(ctx, runErr) {
			state = a2a.TaskStateCanceled
		} else {
			state = a2a.TaskStateFailed
			statusMessage = unexpectedFailureStatusMessage()
			statusMessage.TaskID = info.TaskInfo().TaskID
			statusMessage.ContextID = info.TaskInfo().ContextID
		}
	}
	return a2a.NewStatusUpdateEvent(info, state, statusMessage)
}

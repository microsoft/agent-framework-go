// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"context"

	"github.com/a2aproject/a2a-go/a2a"
)

const (
	runModeMessage = "message"
	runModeTask    = "task"
	runModeDynamic = "dynamic"
)

type A2ARunDecisionContext struct {
	MessageSendParams *a2a.MessageSendParams
}

type AgentRunMode struct {
	value           string
	runInBackground func(context.Context, A2ARunDecisionContext) (bool, error)
}

func DisallowBackground() AgentRunMode {
	return AgentRunMode{value: runModeMessage}
}

func AllowBackgroundIfSupported() AgentRunMode {
	return AgentRunMode{value: runModeTask}
}

func AllowBackgroundWhen(runInBackground func(context.Context, A2ARunDecisionContext) (bool, error)) AgentRunMode {
	if runInBackground == nil {
		panic("runInBackground cannot be nil")
	}
	return AgentRunMode{value: runModeDynamic, runInBackground: runInBackground}
}

func (m AgentRunMode) shouldRunInBackground(ctx context.Context, decisionContext A2ARunDecisionContext) (bool, error) {
	switch m.value {
	case "", runModeMessage:
		return false, nil
	case runModeTask:
		return true, nil
	case runModeDynamic:
		if m.runInBackground == nil {
			return false, nil
		}
		return m.runInBackground(ctx, decisionContext)
	default:
		return false, nil
	}
}

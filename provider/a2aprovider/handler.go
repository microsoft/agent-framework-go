// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"iter"
	"slices"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
)

type (
	configurationKey struct{}
	configurationOpt struct{ value *a2a.SendMessageConfig }
)

func (o configurationOpt) MAFValue() any { return o.value }

// WithConfiguration supplies a read-only snapshot of the incoming A2A send
// configuration to a hosted-agent run. It does not enable background responses.
func WithConfiguration(configuration *a2a.SendMessageConfig) agent.Option {
	if configuration == nil {
		return configurationOpt{}
	}
	cloned := *configuration
	cloned.AcceptedOutputModes = slices.Clone(configuration.AcceptedOutputModes)
	if configuration.HistoryLength != nil {
		cloned.HistoryLength = new(*configuration.HistoryLength)
	}
	if configuration.PushConfig != nil {
		push := *configuration.PushConfig
		if push.Auth != nil {
			auth := *push.Auth
			push.Auth = &auth
		}
		cloned.PushConfig = &push
	}
	return configurationOpt{value: &cloned}
}

// NewHandler creates a native A2A request handler for the hosted agent. It
// forwards incoming send configuration and preserves terminal task events on
// failures. Native options configure task storage, authentication and transport
// processing; the executor's session store remains independently configured.
func NewHandler(hostedAgent *agent.Agent, cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) a2asrv.RequestHandler {
	defaults := []a2asrv.RequestHandlerOption{a2asrv.WithCallInterceptors(&configurationInterceptor{})}
	return a2asrv.NewHandler(&terminalExecutor{inner: NewExecutor(hostedAgent, cfg)}, append(defaults, options...)...)
}

type configurationInterceptor struct{}

func (*configurationInterceptor) After(context.Context, *a2asrv.CallContext, *a2asrv.Response) error {
	return nil
}

func (*configurationInterceptor) Before(ctx context.Context, _ *a2asrv.CallContext, request *a2asrv.Request) (context.Context, any, error) {
	telemetry.MarkUsed(telemetry.FeatureHostingA2A)
	if request != nil {
		if send, ok := request.Payload.(*a2a.SendMessageRequest); ok && send.Config != nil {
			configuration, _ := agent.GetOption([]agent.Option{WithConfiguration(send.Config)}, WithConfiguration)
			ctx = context.WithValue(ctx, configurationKey{}, configuration)
		}
	}
	return ctx, nil, nil
}

type terminalExecutor struct{ inner a2asrv.AgentExecutor }

func (e *terminalExecutor) Execute(ctx context.Context, request *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		terminal := false
		for event, err := range e.inner.Execute(ctx, request) {
			if err != nil && terminal {
				return
			}
			var state a2a.TaskState
			switch event := event.(type) {
			case *a2a.TaskStatusUpdateEvent:
				state = event.Status.State
			case *a2a.Task:
				state = event.Status.State
			}
			terminal = state == a2a.TaskStateCompleted || state == a2a.TaskStateFailed || state == a2a.TaskStateCanceled
			if !yield(event, err) {
				return
			}
		}
	}
}

func (e *terminalExecutor) Cancel(ctx context.Context, request *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return e.inner.Cancel(ctx, request)
}

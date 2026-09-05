// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"maps"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
)

// RequestConfigurationPropertyKey is the agent additional-properties key under
// which inbound A2A message/send configuration is forwarded to hosted agents.
const RequestConfigurationPropertyKey = "a2a.configuration"

// NewHandler creates an A2A request handler for a hosted agent and forwards
// inbound A2A request configuration to the hosted agent's run options.
func NewHandler(hostedAgent *agent.Agent, cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) a2asrv.RequestHandler {
	options = append([]a2asrv.RequestHandlerOption{WithRequestConfigForwarding()}, options...)
	return a2asrv.NewHandler(NewExecutor(hostedAgent, cfg), options...)
}

// WithRequestConfigForwarding returns an A2A request-handler option that
// forwards the inbound [a2a.SendMessageRequest.Config] to hosted agents through
// [agent.WithAdditionalProperties] under [RequestConfigurationPropertyKey].
func WithRequestConfigForwarding() a2asrv.RequestHandlerOption {
	return a2asrv.WithCallInterceptors(requestConfigForwardingInterceptor{})
}

type requestConfigForwardingInterceptor struct {
	a2asrv.PassthroughCallInterceptor
}

func (requestConfigForwardingInterceptor) Before(ctx context.Context, _ *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
	sendReq, ok := req.Payload.(*a2a.SendMessageRequest)
	if !ok || sendReq == nil || sendReq.Config == nil {
		return ctx, nil, nil
	}

	cloned := *sendReq
	cloned.Metadata = maps.Clone(sendReq.Metadata)
	if cloned.Metadata == nil {
		cloned.Metadata = map[string]any{}
	}
	cloned.Metadata[RequestConfigurationPropertyKey] = cloned.Config
	req.Payload = &cloned
	return ctx, nil, nil
}

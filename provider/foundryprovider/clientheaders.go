// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"net/http"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/openai/openai-go/v3/option"
)

const (
	clientHeaderPrefix            = "x-client-"
	HostedAgentUserIdentityHeader = "x-ms-user-identity"
)

type requestHeadersContextKey struct{}

type clientHeadersOpt map[string]string

func (o clientHeadersOpt) Value() any { return map[string]string(o) }

type hostedAgentUserIdentityOpt string

func (o hostedAgentUserIdentityOpt) Value() any { return string(o) }

// WithClientHeader adds a single x-client-* header to a Foundry agent run.
func WithClientHeader(name string, value string) agent.Option {
	validateClientHeader(name, value)
	return clientHeadersOpt{name: value}
}

// WithClientHeaders adds multiple x-client-* headers to a Foundry agent run.
func WithClientHeaders(headers map[string]string) agent.Option {
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		validateClientHeader(name, value)
		cloned[name] = value
	}
	return clientHeadersOpt(cloned)
}

// WithHostedAgentUserIdentity adds a per-call hosted-agent user identity header to a Foundry agent run.
func WithHostedAgentUserIdentity(userIdentity string) agent.Option {
	validateHostedAgentUserIdentity(userIdentity)
	return hostedAgentUserIdentityOpt(userIdentity)
}

type requestHeadersMiddleware struct{}

func (requestHeadersMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	headers := collectRequestHeaders(options)
	if len(headers) != 0 {
		ctx = context.WithValue(ctx, requestHeadersContextKey{}, headers)
	}
	return next(ctx, messages, options...)
}

func requestHeadersRequestOption() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if headers, ok := req.Context().Value(requestHeadersContextKey{}).(map[string]string); ok {
			for name, value := range headers {
				req.Header.Set(name, value)
			}
		}
		return next(req)
	})
}

func collectRequestHeaders(options []agent.Option) map[string]string {
	var headers map[string]string
	for _, opt := range options {
		switch opt := opt.(type) {
		case clientHeadersOpt:
			if headers == nil {
				headers = make(map[string]string, len(opt)+1)
			}
			maps.Copy(headers, opt)
		case hostedAgentUserIdentityOpt:
			if headers == nil {
				headers = make(map[string]string, 1)
			}
			headers[HostedAgentUserIdentityHeader] = string(opt)
		}
	}
	return headers
}

func validateClientHeader(name string, value string) {
	if strings.TrimSpace(name) == "" {
		panic("client header name is required")
	}
	if value == "" {
		panic("client header value is required")
	}
	if !strings.HasPrefix(strings.ToLower(name), clientHeaderPrefix) {
		panic(fmt.Sprintf("client header %q must start with %q", name, clientHeaderPrefix))
	}
}

func validateHostedAgentUserIdentity(userIdentity string) {
	if strings.TrimSpace(userIdentity) == "" {
		panic("hosted agent user identity is required")
	}
}

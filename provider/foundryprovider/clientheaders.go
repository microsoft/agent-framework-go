// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"context"
	"fmt"
	"iter"
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

type clientHeadersContextKey struct{}

type hostedAgentUserIdentityContextKey struct{}

type clientHeadersOpt map[string]string

func (o clientHeadersOpt) MAFValue() any { return map[string]string(o) }

type hostedAgentUserIdentityOpt string

func (o hostedAgentUserIdentityOpt) MAFValue() any { return string(o) }

// WithClientHeader adds a single x-client-* header to a Foundry agent run.
func WithClientHeader(name string, value string) agent.Option {
	validateClientHeader(name, value)
	return clientHeadersOpt{strings.ToLower(name): value}
}

// WithClientHeaders adds multiple x-client-* headers to a Foundry agent run.
func WithClientHeaders(headers map[string]string) agent.Option {
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		validateClientHeader(name, value)
		normalizedName := strings.ToLower(name)
		if _, exists := cloned[normalizedName]; exists {
			panic(fmt.Sprintf("duplicate client header %q", name))
		}
		cloned[normalizedName] = value
	}
	return clientHeadersOpt(cloned)
}

// WithHostedAgentUserIdentity adds a per-call hosted-agent user identity header to a Foundry agent run.
func WithHostedAgentUserIdentity(userIdentity string) agent.Option {
	validateHostedAgentUserIdentity(userIdentity)
	return hostedAgentUserIdentityOpt(userIdentity)
}

type clientHeadersMiddleware struct{}

func (clientHeadersMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	headers := collectClientHeaders(options)
	if len(headers) != 0 {
		ctx = context.WithValue(ctx, clientHeadersContextKey{}, headers)
	}
	return next(ctx, messages, options...)
}

func clientHeadersRequestOption() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if headers, ok := req.Context().Value(clientHeadersContextKey{}).(map[string]string); ok {
			for name, value := range headers {
				req.Header.Set(name, value)
			}
		}
		return next(req)
	})
}

func collectClientHeaders(options []agent.Option) map[string]string {
	var headers map[string]string
	for _, opt := range options {
		clientHeaders, ok := opt.(clientHeadersOpt)
		if !ok {
			continue
		}
		if headers == nil {
			headers = make(map[string]string, len(clientHeaders))
		}
		for name, value := range clientHeaders {
			headers[strings.ToLower(name)] = value
		}
	}
	return headers
}

// hostedAgentUserIdentityMiddleware records the per-call hosted-agent user identity in
// the context, always setting it (including to the empty string) so that nested agent
// calls do not inherit the outer run's identity through the shared context. It is kept
// separate from client headers so their existing inheritance behavior is unchanged.
type hostedAgentUserIdentityMiddleware struct{}

func (hostedAgentUserIdentityMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	ctx = context.WithValue(ctx, hostedAgentUserIdentityContextKey{}, collectHostedAgentUserIdentity(options))
	return next(ctx, messages, options...)
}

func hostedAgentUserIdentityRequestOption() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if identity, ok := req.Context().Value(hostedAgentUserIdentityContextKey{}).(string); ok && identity != "" {
			req.Header.Set(HostedAgentUserIdentityHeader, identity)
		}
		return next(req)
	})
}

func collectHostedAgentUserIdentity(options []agent.Option) string {
	identity := ""
	for _, opt := range options {
		if id, ok := opt.(hostedAgentUserIdentityOpt); ok {
			identity = string(id)
		}
	}
	return identity
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

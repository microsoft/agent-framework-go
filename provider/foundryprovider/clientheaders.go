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

const clientHeaderPrefix = "x-client-"

type clientHeadersContextKey struct{}

type clientHeadersOpt map[string]string

func (o clientHeadersOpt) Value() any { return map[string]string(o) }

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
		maps.Copy(headers, clientHeaders)
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

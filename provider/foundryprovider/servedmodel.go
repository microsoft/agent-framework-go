// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"context"
	"iter"
	"net/http"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/openai/openai-go/v3/option"
)

const (
	servedModelHeader             = "x-ms-served-model"
	servedModelAdditionalProperty = "ServedModel"
)

type servedModelContextKey struct{}

type servedModelBox struct {
	value        string
	metadataSent bool
}

type servedModelMiddleware struct{}

func (servedModelMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	box := &servedModelBox{}
	ctx = context.WithValue(ctx, servedModelContextKey{}, box)
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		for update, err := range next(ctx, messages, options...) {
			if update != nil && box.value != "" {
				if update.MessageID == "" {
					if update.AdditionalProperties == nil {
						update.AdditionalProperties = make(map[string]any, 1)
					}
					update.AdditionalProperties[servedModelAdditionalProperty] = box.value
					box.metadataSent = true
				} else if !box.metadataSent {
					// The served model belongs to the response, not this message.
					box.metadataSent = true
					if !yield(&agent.ResponseUpdate{
						ResponseID:           update.ResponseID,
						ConversationID:       update.ConversationID,
						CreatedAt:            update.CreatedAt,
						AdditionalProperties: map[string]any{servedModelAdditionalProperty: box.value},
					}, nil) {
						return
					}
				}
			}
			if !yield(update, err) {
				return
			}
		}
	}
}

func servedModelRequestOption() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil || resp == nil {
			return resp, err
		}
		servedModel := strings.TrimSpace(resp.Header.Get(servedModelHeader))
		if box, ok := req.Context().Value(servedModelContextKey{}).(*servedModelBox); ok {
			box.value = servedModel
			box.metadataSent = false
		}
		return resp, nil
	})
}

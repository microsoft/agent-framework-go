// Copyright (c) Microsoft. All rights reserved.

// Package exp provides experimental features for the agent framework,
// mostly related to implementing custom agents.
package exp

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

type RawRunAgent interface {
	agent.Agent

	RawRun(ctx context.Context, options *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error)
}

type RawStreamAgent interface {
	agent.Agent

	RawRunStream(ctx context.Context, options *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error]
}

func Run(ctx context.Context, ag RawRunAgent, t agent.Thread, options *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	var err error
	if options != nil {
		if err := initTools(ctx, options.Tools); err != nil {
			return nil, err
		}
		extraTools, err := loadTools(ctx, options.Tools)
		if err != nil {
			return nil, err
		}
		options.Tools = append(options.Tools, extraTools...)
	}

	// Prepare messages with system instructions
	threadMessages, err := prepareMessages(ctx, t, messages)
	if err != nil {
		return nil, err
	}
	startLength := len(threadMessages)
	for {
		// Call the chat client
		response, err := ag.RawRun(ctx, options, threadMessages...)
		if err != nil {
			return nil, err
		}
		message := response.Messages[0]
		threadMessages = append(threadMessages, message)
		toolResult := runToolCalls(ctx, options, message.Contents...)
		if len(toolResult) > 0 {
			// Add a single Message to the response with the results
			threadMessages = append(threadMessages, agent.NewMessage(agent.RoleTool, toolResult...))
			continue
		}
		if t != nil {
			if err := t.Add(ctx, threadMessages[startLength:]...); err != nil {
				return nil, err
			}
		}
		return &agent.RunResponse{
			Messages:   threadMessages[startLength:],
			ResponseID: response.ResponseID,
			AgentID:    ag.ID(),
		}, nil
	}
}

func RunStream(ctx context.Context, ag RawStreamAgent, t agent.Thread, options *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	err := initTools(ctx, options.Tools)
	var threadMessages []*agent.Message
	if err == nil {
		threadMessages, err = prepareMessages(ctx, t, messages)
	}
	startLength := len(threadMessages)
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		if err != nil {
			if !yield(nil, err) {
				return
			}
		}
		for {
			var contents []agent.Content
			for update, err := range ag.RawRunStream(ctx, options, threadMessages...) {
				if err != nil {
					if !yield(nil, err) {
						return
					}
				}
				contents = append(contents, update.Contents...)
				if !yield(update, nil) {
					return
				}
			}
			if !slices.ContainsFunc(contents, func(content agent.Content) bool {
				_, ok := content.(*agent.FunctionCallContent)
				return ok
			}) {
				// No tool calls
				return
			}
			threadMessages = append(threadMessages, agent.NewMessage(agent.RoleAssistant, contents...))
			toolResult := runToolCalls(ctx, options, contents...)
			if len(toolResult) > 0 {
				// Add a single Message to the response with the results
				if !yield(&agent.RunResponseUpdate{
					Contents: toolResult,
					Role:     agent.RoleAssistant,
					AgentID:  ag.ID(),
				}, nil) {
					return
				}
				threadMessages = append(threadMessages, agent.NewMessage(agent.RoleTool, toolResult...))
				continue
			}
			// No more tool calls to process
			if t != nil {
				if err := t.Add(ctx, threadMessages[startLength:]...); err != nil {
					if !yield(nil, err) {
						return
					}
				}
			}
			return
		}
	}
}

func prepareMessages(ctx context.Context, t agent.Thread, messages []*agent.Message) ([]*agent.Message, error) {
	if t != nil {
		for msg, err := range t.All(ctx) {
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

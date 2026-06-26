// Copyright (c) Microsoft. All rights reserved.

// Package azmemory provides Azure AI Foundry memory integration for agents.
package azmemory

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azaiprojects"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

const (
	defaultMemoryContextPrompt = "## Memories\nConsider the following memories when answering user questions:"
	defaultMaxMemories         = 5
	defaultSourceID            = "azmemory"
)

// Config configures a Foundry memory provider.
type Config struct {
	// Logger receives provider diagnostics.
	Logger *slog.Logger

	// ContextPrompt prefixes retrieved memories injected into the run. The default
	// prompt is "## Memories\nConsider the following memories when answering user questions:".
	ContextPrompt string

	// MaxMemories limits the number of memories returned by search. The default is 5.
	MaxMemories int32

	// UpdateDelay controls Foundry memory extraction delay in seconds. The default is 0,
	// which submits memory updates immediately.
	UpdateDelay int32
}

// Provider backs an [agent.ContextProvider] with Foundry memory stores.
type Provider struct {
	client          *azaiprojects.MemoryStoresClient
	memoryStoreName string
	scopeFunc       func(*agent.Session) string
	config          Config
	provider        *agent.ContextProvider
}

// NewProvider creates a Foundry memory provider backed by an Azure AI Projects memory store.
//
// The returned provider is attached to an agent with:
//
//	agent.Config{ContextProviders: []*agent.ContextProvider{provider.ContextProvider()}}
//
// The client must be a project-scoped azaiprojects memory stores client, and memoryStoreName
// must name an existing Foundry memory store. The scope callback is invoked for each run and
// must return the Foundry memory scope that partitions stored memories, such as a user or
// tenant identifier. A blank scope is treated as a configuration error and panics when used.
func NewProvider(client *azaiprojects.MemoryStoresClient, memoryStoreName string, scope func(*agent.Session) string, config Config) *Provider {
	if client == nil {
		panic("memory stores client is required")
	}
	if strings.TrimSpace(memoryStoreName) == "" {
		panic("memory store name is required")
	}
	if scope == nil {
		panic("memory scope is required")
	}
	if config.ContextPrompt == "" {
		config.ContextPrompt = defaultMemoryContextPrompt
	}
	if config.MaxMemories == 0 {
		config.MaxMemories = defaultMaxMemories
	}
	p := &Provider{
		client:          client,
		memoryStoreName: memoryStoreName,
		scopeFunc:       scope,
		config:          config,
	}
	p.provider = &agent.ContextProvider{
		SourceID:           defaultSourceID,
		StoreRequestFilter: messagefilter.ExternalOnly,
		Provide:            p.provide,
		Store:              p.store,
	}
	return p
}

// ContextProvider returns the Agent Framework context provider.
func (p *Provider) ContextProvider() *agent.ContextProvider {
	if p == nil {
		return nil
	}
	return p.provider
}

func (p *Provider) provide(ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	items := memoryItems(messages)
	if len(items) == 0 {
		return messages, options, nil
	}
	session, _ := agent.GetOption(options, agent.WithSession)
	scope := p.scope(session)
	searchOptions := &azaiprojects.MemoryStoresClientSearchMemoriesOptions{
		Items: items,
	}
	if p.config.MaxMemories > 0 {
		searchOptions.Options = &azaiprojects.MemorySearchResultOptions{MaxMemories: &p.config.MaxMemories}
	}
	result, err := p.client.SearchMemories(ctx, p.memoryStoreName, scope, searchOptions)
	if err != nil {
		p.log(ctx, slog.LevelError, "azmemory: failed to search memories", "memory_store", p.memoryStoreName, "error", err)
		return messages, options, nil
	}
	memories := memoryContents(result.Memories)
	p.log(ctx, slog.LevelInfo, "azmemory: retrieved memories", "memory_store", p.memoryStoreName, "count", len(memories))
	if len(memories) == 0 {
		return messages, options, nil
	}
	contextMessage := message.NewText(p.config.ContextPrompt + "\n" + strings.Join(memories, "\n"))
	out := slices.Clone(messages)
	out = append(out, contextMessage)
	return out, options, nil
}

func (p *Provider) store(ctx context.Context, requestMessages, responseMessages []*message.Message, options ...agent.Option) error {
	items := memoryItems(requestMessages)
	items = append(items, memoryItems(responseMessages)...)
	if len(items) == 0 {
		return nil
	}
	session, _ := agent.GetOption(options, agent.WithSession)
	scope := p.scope(session)
	updateOptions := &azaiprojects.MemoryStoresClientBeginUpdateMemoriesOptions{
		Items:       items,
		UpdateDelay: &p.config.UpdateDelay,
	}
	if _, err := p.client.BeginUpdateMemories(ctx, p.memoryStoreName, scope, updateOptions); err != nil {
		p.log(ctx, slog.LevelError, "azmemory: failed to update memories", "memory_store", p.memoryStoreName, "count", len(items), "error", err)
		return nil
	}
	p.log(ctx, slog.LevelInfo, "azmemory: submitted memory update", "memory_store", p.memoryStoreName, "count", len(items))
	return nil
}

func (p *Provider) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if p.config.Logger == nil || !p.config.Logger.Enabled(ctx, level) {
		return
	}
	p.config.Logger.Log(ctx, level, msg, args...)
}

func (p *Provider) scope(session *agent.Session) string {
	if scope := strings.TrimSpace(p.scopeFunc(session)); scope != "" {
		return scope
	}
	panic("memory scope must not be empty")
}

func memoryItems(messages []*message.Message) []map[string]any {
	items := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		text := strings.TrimSpace(msg.String())
		if text == "" {
			continue
		}
		switch msg.Role {
		case message.RoleUser, message.RoleAssistant, message.RoleSystem:
			items = append(items, map[string]any{
				"type": "message",
				"role": string(msg.Role),
				"content": []map[string]any{{
					"type": "input_text",
					"text": text,
				}},
			})
		}
	}
	return items
}

func memoryContents(memories []azaiprojects.MemorySearchItem) []string {
	contents := make([]string, 0, len(memories))
	for _, memory := range memories {
		if memory.MemoryItem == nil {
			continue
		}
		item := memory.MemoryItem.GetMemoryItem()
		if item == nil || item.Content == nil {
			continue
		}
		content := strings.TrimSpace(*item.Content)
		if content != "" {
			contents = append(contents, content)
		}
	}
	return contents
}

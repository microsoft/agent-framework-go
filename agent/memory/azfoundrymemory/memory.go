// Copyright (c) Microsoft. All rights reserved.

// Package azfoundrymemory provides Azure AI Foundry memory integration for agents.
package azfoundrymemory

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/azaiprojects"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

const (
	defaultMemoryContextPrompt = "## Memories\nConsider the following memories when answering user questions:"
	defaultMaxMemories         = 5
	defaultSourceID            = "azfoundrymemory"
)

// Config configures a Foundry memory provider.
type Config struct {
	// ClientOptions configures the underlying Azure AI Projects client.
	ClientOptions azcore.ClientOptions

	// Logger receives provider diagnostics.
	Logger *slog.Logger

	// ContextPrompt prefixes retrieved memories injected into the run. The default
	// prompt is "## Memories\nConsider the following memories when answering user questions:".
	ContextPrompt string

	// MaxMemories limits the number of memories returned by search. The default is 5.
	MaxMemories int32

	// SearchInputFilter filters messages used to search for relevant memories. The
	// default is [messagefilter.ExternalOnly].
	SearchInputFilter messagefilter.Filter

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
// The endpoint must be a project-scoped Azure AI Foundry endpoint, and memoryStoreName must
// name an existing Foundry memory store. The scope callback is invoked for each run and must
// return the Foundry memory scope that partitions stored memories, such as a user or tenant
// identifier. A blank scope is treated as a configuration error and panics when used.
func NewProvider(endpoint string, credential azcore.TokenCredential, memoryStoreName string, scope func(*agent.Session) string, config Config) *Provider {
	if strings.TrimSpace(endpoint) == "" {
		panic("endpoint is required")
	}
	if credential == nil {
		panic("credential is required")
	}
	client, err := azaiprojects.NewClient(endpoint, credential, &azaiprojects.ClientOptions{ClientOptions: config.ClientOptions})
	if err != nil {
		panic("failed to create Azure AI Projects client: " + err.Error())
	}
	return newProvider(client.NewMemoryStoresClient(), memoryStoreName, scope, config)
}

func newProvider(client *azaiprojects.MemoryStoresClient, memoryStoreName string, scope func(*agent.Session) string, config Config) *Provider {
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
	if config.SearchInputFilter == nil {
		config.SearchInputFilter = messagefilter.ExternalOnly
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
	searchMessages, err := p.config.SearchInputFilter(ctx, slices.Clone(messages))
	if err != nil {
		return nil, nil, err
	}
	items := searchMemoryItems(searchMessages)
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
		p.log(ctx, slog.LevelError, "azfoundrymemory: failed to search memories", "memory_store", p.memoryStoreName, "error", err)
		return messages, options, nil
	}
	memories := memoryContents(result.Memories)
	p.log(ctx, slog.LevelInfo, "azfoundrymemory: retrieved memories", "memory_store", p.memoryStoreName, "count", len(memories))
	if len(memories) == 0 {
		return messages, options, nil
	}
	contextMessage := message.NewText(p.config.ContextPrompt + "\n" + strings.Join(memories, "\n"))
	out := slices.Clone(messages)
	out = append(out, contextMessage)
	return out, options, nil
}

func (p *Provider) store(ctx context.Context, requestMessages, responseMessages []*message.Message, options ...agent.Option) error {
	items := updateMemoryItems(requestMessages)
	items = append(items, updateMemoryItems(responseMessages)...)
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
		p.log(ctx, slog.LevelError, "azfoundrymemory: failed to update memories", "memory_store", p.memoryStoreName, "count", len(items), "error", err)
		return nil
	}
	p.log(ctx, slog.LevelInfo, "azfoundrymemory: submitted memory update", "memory_store", p.memoryStoreName, "count", len(items))
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

func searchMemoryItems(messages []*message.Message) []map[string]any {
	items := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		text := strings.TrimSpace(msg.String())
		if text == "" {
			continue
		}
		items = append(items, toResponseItem(msg.Role, text))
	}
	return items
}

func updateMemoryItems(messages []*message.Message) []map[string]any {
	items := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		text := strings.TrimSpace(msg.String())
		if text == "" {
			continue
		}
		if isAllowedRole(msg.Role) {
			items = append(items, toResponseItem(msg.Role, text))
		}
	}
	return items
}

func toResponseItem(role message.Role, text string) map[string]any {
	switch role {
	case message.RoleAssistant, message.RoleSystem:
	default:
		role = message.RoleUser
	}
	return map[string]any{
		"type": "message",
		"role": string(role),
		"content": []map[string]any{{
			"type": "input_text",
			"text": text,
		}},
	}
}

func isAllowedRole(role message.Role) bool {
	return role == message.RoleUser || role == message.RoleAssistant || role == message.RoleSystem
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

// Copyright (c) Microsoft. All rights reserved.

// Package foundryprovider provides Microsoft Foundry provider integrations for agents.
package foundryprovider

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	defaultSourceID            = "foundrymemory"
)

// MemoryProviderConfig configures a Foundry memory provider.
type MemoryProviderConfig struct {
	// ClientOptions configures the underlying Microsoft Foundry Projects client.
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

// MemoryProvider backs an [agent.ContextProvider] with Foundry memory stores.
type MemoryProvider struct {
	client          *azaiprojects.MemoryStoresClient
	memoryStoreName string
	scopeFunc       func(*agent.Session) string
	config          MemoryProviderConfig
	providerConfig  agent.ContextProviderConfig
	provider        agent.ContextProvider
}

// NewMemoryProvider creates a Foundry memory provider backed by a Microsoft Foundry Projects memory store.
//
// The returned provider is attached to an agent with:
//
//	agent.Config{ContextProviders: []agent.ContextProvider{provider}}
//
// The endpoint must be a project-scoped Microsoft Foundry endpoint, and memoryStoreName must
// name an existing Foundry memory store. The scope callback is invoked for each run and must
// return the Foundry memory scope that partitions stored memories, such as a user or tenant
// identifier. A blank scope is treated as a configuration error and panics when used.
func NewMemoryProvider(endpoint string, credential azcore.TokenCredential, memoryStoreName string, scope func(*agent.Session) string, config MemoryProviderConfig) *MemoryProvider {
	if strings.TrimSpace(endpoint) == "" {
		panic("endpoint is required")
	}
	if credential == nil {
		panic("credential is required")
	}
	client, err := azaiprojects.NewClient(endpoint, credential, &azaiprojects.ClientOptions{ClientOptions: config.ClientOptions})
	if err != nil {
		panic("failed to create Microsoft Foundry Projects client: " + err.Error())
	}
	return newMemoryProvider(client.NewMemoryStoresClient(), memoryStoreName, scope, config)
}

func newMemoryProvider(client *azaiprojects.MemoryStoresClient, memoryStoreName string, scope func(*agent.Session) string, config MemoryProviderConfig) *MemoryProvider {
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
	providerConfig := agent.ContextProviderConfig{
		ProvideInputMessageFilter:      config.SearchInputFilter,
		SourceID:                       defaultSourceID,
		StoreInputRequestMessageFilter: messagefilter.ExternalOnly,
	}
	p := &MemoryProvider{
		client:          client,
		memoryStoreName: memoryStoreName,
		scopeFunc:       scope,
		config:          config,
		providerConfig:  providerConfig,
	}
	p.providerConfig.Provide = p.provide
	p.providerConfig.Store = p.store
	p.provider = agent.NewContextProvider(p.providerConfig)
	return p
}

// EnsureMemoryStoreCreated provisions the configured Foundry memory store when it does not
// already exist. It first retrieves the store; if the store is present the call is a no-op.
// When the store is missing (HTTP 404) it creates a default memory store backed by the given
// chat and embedding model deployments, applying description when non-nil. The helper is
// idempotent: if another process creates the store between the retrieval and the create, the
// resulting HTTP 409 conflict is treated as success. Any other retrieval or create error is
// returned unchanged.
func (p *MemoryProvider) EnsureMemoryStoreCreated(ctx context.Context, chatModel, embeddingModel string, description *string) error {
	if _, err := p.client.GetMemoryStore(ctx, p.memoryStoreName, nil); err != nil {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
			return err
		}
		def := &azaiprojects.MemoryStoreDefaultDefinition{
			ChatModel:      &chatModel,
			EmbeddingModel: &embeddingModel,
		}
		var options *azaiprojects.MemoryStoresClientCreateMemoryStoreOptions
		if description != nil {
			options = &azaiprojects.MemoryStoresClientCreateMemoryStoreOptions{Description: description}
		}
		if _, err = p.client.CreateMemoryStore(ctx, p.memoryStoreName, def, options); err != nil {
			// Tolerate a concurrent create: if another process created the store after the
			// GET returned 404, CreateMemoryStore reports a conflict. Treat it as a no-op.
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
				return nil
			}
			return err
		}
		return nil
	}
	return nil
}

func (p *MemoryProvider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.provider.Invoking(ctx, invoking)
}

func (p *MemoryProvider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.provider.Invoked(ctx, invoked)
}

func (p *MemoryProvider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	items := searchMemoryItems(invoking.Messages)
	if len(items) == 0 {
		return nil, nil, nil
	}
	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	scope := p.scope(session)
	searchOptions := &azaiprojects.MemoryStoresClientSearchMemoriesOptions{
		Items: items,
	}
	if p.config.MaxMemories > 0 {
		searchOptions.Options = &azaiprojects.MemorySearchResultOptions{MaxMemories: &p.config.MaxMemories}
	}
	result, err := p.client.SearchMemories(ctx, p.memoryStoreName, scope, searchOptions)
	if err != nil {
		p.log(ctx, slog.LevelError, "foundrymemory: failed to search memories", "memory_store", p.memoryStoreName, "error", err)
		return nil, nil, nil
	}
	memories := memoryContents(result.Memories)
	p.log(ctx, slog.LevelInfo, "foundrymemory: retrieved memories", "memory_store", p.memoryStoreName, "count", len(memories))
	if len(memories) == 0 {
		return nil, nil, nil
	}
	contextMessage := message.NewText(p.config.ContextPrompt + "\n" + strings.Join(memories, "\n"))
	return []*message.Message{contextMessage}, nil, nil
}

func (p *MemoryProvider) store(ctx context.Context, invoked agent.InvokedContext) error {
	items := updateMemoryItems(invoked.RequestMessages)
	items = append(items, updateMemoryItems(invoked.ResponseMessages)...)
	if len(items) == 0 {
		return nil
	}
	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	scope := p.scope(session)
	updateOptions := &azaiprojects.MemoryStoresClientBeginUpdateMemoriesOptions{
		Items:       items,
		UpdateDelay: &p.config.UpdateDelay,
	}
	if _, err := p.client.BeginUpdateMemories(ctx, p.memoryStoreName, scope, updateOptions); err != nil {
		p.log(ctx, slog.LevelError, "foundrymemory: failed to update memories", "memory_store", p.memoryStoreName, "count", len(items), "error", err)
		return nil
	}
	p.log(ctx, slog.LevelInfo, "foundrymemory: submitted memory update", "memory_store", p.memoryStoreName, "count", len(items))
	return nil
}

func (p *MemoryProvider) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if p.config.Logger == nil || !p.config.Logger.Enabled(ctx, level) {
		return
	}
	p.config.Logger.Log(ctx, level, msg, args...)
}

func (p *MemoryProvider) scope(session *agent.Session) string {
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

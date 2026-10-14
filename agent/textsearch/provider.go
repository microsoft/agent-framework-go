// Copyright (c) Microsoft. All rights reserved.

// Package textsearch provides retrieval-augmented context from text searches.
package textsearch

import (
	"context"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	defaultSourceID                = "TextSearchProvider"
	defaultFunctionToolName        = "Search"
	defaultFunctionToolDescription = "Allows searching for additional information to help answer the user question."
	defaultContextPrompt           = "## Additional Context\nConsider the following information from source documents when responding to the user:"
	defaultCitationsPrompt         = "Include citations to the source document with document name and link if document name and link is available."
)

// SearchBehavior controls when a text search is executed.
type SearchBehavior int

const (
	// BeforeInvoke searches before each agent run and injects results as a user message.
	BeforeInvoke SearchBehavior = iota

	// OnDemandFunctionCalling exposes a search tool for the model to call when needed.
	OnDemandFunctionCalling
)

// Result contains a retrieved text chunk and its optional source information.
type Result struct {
	SourceName string
	SourceLink string
	Text       string

	// RawRepresentation retains the underlying search result for a custom formatter.
	RawRepresentation any
}

// Config configures the provider created by [NewContextProvider].
type Config struct {
	// SearchTime defaults to [BeforeInvoke].
	SearchTime SearchBehavior

	// FunctionToolName names the on-demand search tool. Nil defaults to "Search".
	FunctionToolName *string

	// FunctionToolDescription describes the on-demand search tool. Nil uses
	// "Allows searching for additional information to help answer the user question.".
	FunctionToolDescription *string

	// ContextPrompt prefixes search results. Nil uses a default additional-context prompt.
	// An explicit empty string suppresses the prompt text.
	ContextPrompt *string

	// CitationsPrompt follows search results. Nil uses a default citation instruction.
	// An explicit empty string suppresses the instruction text.
	CitationsPrompt *string

	// ContextFormatter replaces the default formatting and ignores both prompts.
	// Automatic searches do not call the formatter for an empty result list;
	// on-demand searches do. Formatting errors are ignored during automatic
	// searches, logged when Logger is configured, and returned from the on-demand tool.
	ContextFormatter func([]Result) (string, error)

	// RecentMessageMemoryLimit is the maximum number of individual messages
	// retained in the session and prepended to automatic search queries.
	// Zero disables memory. Negative values are invalid.
	RecentMessageMemoryLimit int

	// RecentMessageRolesIncluded selects message roles retained in memory.
	// Nil defaults to user messages; an empty, non-nil slice retains no messages.
	// Roles are compared case-insensitively. The slice is retained, not copied;
	// do not modify it while the provider is in use. This does not filter the
	// current request's search input.
	RecentMessageRolesIncluded []message.Role

	// StateKey identifies provider state in the session. Nil defaults to
	// "TextSearchProvider". Explicit empty or whitespace-only keys are invalid.
	// Use distinct keys for providers that need independent memory.
	StateKey *string

	// SearchInputMessageFilter filters automatic search input.
	// Nil defaults to [messagefilter.ExternalOnly].
	SearchInputMessageFilter messagefilter.Filter

	// StorageInputRequestMessageFilter filters request messages before storing memory.
	// Nil defaults to [messagefilter.ExternalOnly].
	StorageInputRequestMessageFilter messagefilter.Filter

	// StorageInputResponseMessageFilter filters response messages before storing memory.
	// Nil passes all response messages through.
	StorageInputResponseMessageFilter messagefilter.Filter

	// Logger receives provider diagnostics. When nil, no diagnostics are logged.
	Logger *slog.Logger

	// LogSensitiveData includes raw queries and formatted results in debug logs.
	// The default is false. When true, Redactor is ignored.
	LogSensitiveData bool

	// Redactor transforms queries and formatted results in debug logs.
	// When nil and LogSensitiveData is false, they are replaced with "<redacted>".
	Redactor func(string) string
}

// State is the provider's per-session state. The provider stores a *State under
// the key returned by [Provider.StateKeys]. Synchronize external edits with runs,
// and use [agent.Session.Set] to persist changes to restored state.
type State struct {
	// RecentMessagesText contains retained message texts in chronological order.
	// Nil means memory has not been populated; a non-nil empty slice is stored
	// as an empty JSON array.
	RecentMessagesText []string `json:"recentMessagesText,omitzero"`
}

// Provider supplies text search context and maintains optional recent-message
// memory in an [agent.Session]. Create it with [NewContextProvider].
type Provider struct {
	searchFunc      func(context.Context, string) ([]Result, error)
	config          Config
	stateKey        string
	contextPrompt   string
	citationsPrompt string
	searchTool      tool.FuncTool
	contextProvider agent.ContextProvider
	mu              sync.Mutex
}

// NewContextProvider creates a text search context provider.
//
// Attach it using [agent.Config.ContextProviders]. In automatic mode, filtered
// current request texts are joined with newlines, following any retained memory,
// and passed to search. Non-empty results are formatted and appended as a
// source-attributed user message. Search and formatting failures leave the input
// unchanged, including cancellation and deadline errors returned by search.
// Failures are logged only when Logger is configured. Message-filter and
// session-state errors are returned.
//
// In on-demand mode, the provider adds a function tool accepting a userQuestion
// string. The tool searches only that query, without recent-message memory, and
// returns formatted text. Tool errors are returned to the tool-calling machinery.
// Use automatic tool invocation middleware when the provider does not execute tools.
//
// Memory is stored after successful runs, in request-then-response order. Without
// a session, searches still work but no memory is retained. Automatic searches
// initialize session state even when memory retention is disabled.
//
// Search inputs and retrieved documents are untrusted. The search implementation
// must enforce appropriate access controls, and retrieved content may require
// sanitization to prevent indirect prompt injection.
//
// It panics if search is nil, RecentMessageMemoryLimit is negative, or StateKey
// is explicitly empty or whitespace-only.
func NewContextProvider(search func(context.Context, string) ([]Result, error), config Config) *Provider {
	if search == nil {
		panic("search is required")
	}
	if config.RecentMessageMemoryLimit < 0 {
		panic("RecentMessageMemoryLimit must not be negative")
	}
	stateKey := defaultSourceID
	if config.StateKey != nil {
		stateKey = *config.StateKey
	}
	if strings.TrimSpace(stateKey) == "" {
		panic("StateKey must not be blank")
	}
	if config.RecentMessageRolesIncluded == nil {
		config.RecentMessageRolesIncluded = []message.Role{message.RoleUser}
	}
	p := &Provider{
		searchFunc:      search,
		config:          config,
		stateKey:        stateKey,
		contextPrompt:   defaultContextPrompt,
		citationsPrompt: defaultCitationsPrompt,
	}
	if config.ContextPrompt != nil {
		p.contextPrompt = *config.ContextPrompt
	}
	if config.CitationsPrompt != nil {
		p.citationsPrompt = *config.CitationsPrompt
	}
	name := defaultFunctionToolName
	if config.FunctionToolName != nil {
		name = *config.FunctionToolName
	}
	description := defaultFunctionToolDescription
	if config.FunctionToolDescription != nil {
		description = *config.FunctionToolDescription
	}
	p.searchTool = functool.MustNew(
		functool.Config{Name: name, Description: description},
		func(ctx context.Context, args struct {
			UserQuestion string `json:"userQuestion"`
		},
		) (string, error) {
			return p.search(ctx, args.UserQuestion)
		},
	)
	p.contextProvider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID:                        defaultSourceID,
		ProvideInputMessageFilter:       config.SearchInputMessageFilter,
		StoreInputRequestMessageFilter:  config.StorageInputRequestMessageFilter,
		StoreInputResponseMessageFilter: config.StorageInputResponseMessageFilter,
		Provide:                         p.provide,
		Store:                           p.store,
	})
	return p
}

// StateKeys returns the session key used by this provider. The returned slice
// belongs to the caller.
func (p *Provider) StateKeys() []string {
	return []string{p.stateKey}
}

// Invoking returns the original messages and options with search context or the
// on-demand search tool added.
func (p *Provider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.contextProvider.Invoking(ctx, invoking)
}

// Invoked retains eligible request and response texts after successful runs.
func (p *Provider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.contextProvider.Invoked(ctx, invoked)
}

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	telemetry.MarkUsed(telemetry.FeatureTextSearchProvider)
	if p.config.SearchTime != BeforeInvoke {
		return nil, []agent.Option{agent.WithTool(p.searchTool)}, nil
	}

	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	p.mu.Lock()
	state, err := p.loadState(session)
	p.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	var parts []string
	// Do not reuse retained text when memory is disabled, even if a previous
	// configuration populated the session's state.
	if p.config.RecentMessageMemoryLimit > 0 {
		parts = slices.Clone(state.RecentMessagesText)
	}
	for _, msg := range invoking.Messages {
		if msg != nil {
			if text := msg.String(); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
	}
	var sb strings.Builder
	for _, text := range parts {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(text)
	}
	query := sb.String()
	results, err := p.searchFunc(ctx, query)
	if err != nil {
		p.logSearchFailure(ctx, err)
		return nil, nil, nil
	}
	if p.config.Logger != nil {
		p.config.Logger.InfoContext(ctx, "text search completed", "count", len(results))
	}
	if len(results) == 0 {
		return nil, nil, nil
	}
	formatted, err := p.formatResults(results)
	if err != nil {
		p.logSearchFailure(ctx, err)
		return nil, nil, nil
	}
	p.logSearchResults(ctx, query, formatted)
	return []*message.Message{message.NewText(formatted)}, nil, nil
}

func (p *Provider) store(_ context.Context, invoked agent.InvokedContext) error {
	limit := p.config.RecentMessageMemoryLimit
	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	if limit == 0 || session == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, err := p.loadState(session)
	if err != nil {
		return err
	}
	recent := slices.Clone(state.RecentMessagesText)
	if recent == nil {
		recent = []string{}
	}
	for _, messages := range [][]*message.Message{invoked.RequestMessages, invoked.ResponseMessages} {
		for _, msg := range messages {
			if msg == nil || strings.TrimSpace(msg.String()) == "" {
				continue
			}
			for _, role := range p.config.RecentMessageRolesIncluded {
				if strings.EqualFold(string(role), string(msg.Role)) {
					recent = append(recent, msg.String())
					break
				}
			}
		}
	}
	if len(recent) > limit {
		recent = slices.Clone(recent[len(recent)-limit:])
	}
	session.Set(p.stateKey, &State{RecentMessagesText: recent})
	return nil
}

func (p *Provider) loadState(session *agent.Session) (*State, error) {
	if session == nil {
		return new(State), nil
	}
	var state *State
	if ok, err := session.Get(p.stateKey, &state); err != nil {
		return nil, err
	} else if !ok || state == nil {
		state = new(State)
		session.Set(p.stateKey, state)
	}
	return state, nil
}

func (p *Provider) search(ctx context.Context, query string) (string, error) {
	results, err := p.searchFunc(ctx, query)
	if err != nil {
		return "", err
	}
	formatted, err := p.formatResults(results)
	if err != nil {
		return "", err
	}
	if logger := p.config.Logger; logger != nil && logger.Enabled(ctx, slog.LevelInfo) {
		logger.InfoContext(ctx, "text search completed", "count", len(results))
		p.logSearchResults(ctx, query, formatted)
	}
	return formatted, nil
}

func (p *Provider) formatResults(results []Result) (string, error) {
	if p.config.ContextFormatter != nil {
		return p.config.ContextFormatter(results)
	}
	if len(results) == 0 {
		return "", nil
	}
	newline := "\n"
	if runtime.GOOS == "windows" {
		newline = "\r\n"
	}
	var sb strings.Builder
	sb.WriteString(p.contextPrompt)
	sb.WriteString(newline)
	for _, result := range results {
		if strings.TrimSpace(result.SourceName) != "" {
			sb.WriteString("SourceDocName: ")
			sb.WriteString(result.SourceName)
			sb.WriteString(newline)
		}
		if strings.TrimSpace(result.SourceLink) != "" {
			sb.WriteString("SourceDocLink: ")
			sb.WriteString(result.SourceLink)
			sb.WriteString(newline)
		}
		sb.WriteString("Contents: ")
		sb.WriteString(result.Text)
		sb.WriteString(newline)
		sb.WriteString("----")
		sb.WriteString(newline)
	}
	sb.WriteString(p.citationsPrompt)
	sb.WriteString(newline)
	sb.WriteString(newline)
	return sb.String(), nil
}

func (p *Provider) logSearchFailure(ctx context.Context, err error) {
	if logger := p.config.Logger; logger != nil {
		logger.ErrorContext(ctx, "text search failed", "error", err)
	}
}

func (p *Provider) logSearchResults(ctx context.Context, query, formatted string) {
	if logger := p.config.Logger; logger != nil && logger.Enabled(ctx, slog.LevelDebug) {
		logger.DebugContext(ctx, "text search results",
			"query", p.sanitizeLogData(query), "context", p.sanitizeLogData(formatted))
	}
}

func (p *Provider) sanitizeLogData(data string) string {
	if p.config.LogSensitiveData {
		return data
	}
	if p.config.Redactor != nil {
		return p.config.Redactor(data)
	}
	return "<redacted>"
}

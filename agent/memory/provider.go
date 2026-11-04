// Copyright (c) Microsoft. All rights reserved.

// Package memory provides vector-backed chat history memory for agents.
package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/vectordata"
	"golang.org/x/sync/semaphore"
)

const (
	defaultSourceID                = "ChatHistoryMemoryProvider"
	defaultContextPrompt           = "## Memories\nConsider the following memories when answering user questions:"
	defaultMaxResults              = 3
	defaultFunctionToolName        = "Search"
	defaultFunctionToolDescription = "Allows searching for related previous chat history to help answer the user question."
)

// SearchBehavior controls when chat history is searched.
type SearchBehavior int

const (
	// BeforeInvoke searches before each agent run and adds results as a user message.
	BeforeInvoke SearchBehavior = iota

	// OnDemandFunctionCalling exposes a search tool for the model to call when needed.
	OnDemandFunctionCalling
)

// Scope selects which application, agent, user, and session a memory belongs to.
// A nil ID leaves that dimension unrestricted when searching. An explicit empty
// string matches only an empty ID.
type Scope struct {
	ApplicationID *string `json:"applicationId,omitempty"`
	AgentID       *string `json:"agentId,omitempty"`
	UserID        *string `json:"userId,omitempty"`
	SessionID     *string `json:"sessionId,omitempty"`
}

// State holds a provider's per-session scopes. The provider stores a *State under
// the key returned by [HistoryProvider.StateKeys]. Synchronize edits with runs and tool
// calls, and use [agent.Session.Set] to persist changes to restored state.
type State struct {
	StorageScope Scope `json:"storageScope"`

	// SearchScope defaults to StorageScope when nil.
	SearchScope *Scope `json:"searchScope,omitempty"`
}

// Config configures the provider created by [NewHistoryProvider].
type Config struct {
	// SearchTime defaults to [BeforeInvoke].
	SearchTime SearchBehavior

	// FunctionToolName names the on-demand search tool. Nil defaults to "Search".
	FunctionToolName *string

	// FunctionToolDescription describes the on-demand search tool. Nil uses a
	// default description about searching related previous chat history.
	FunctionToolDescription *string

	// ContextPrompt prefixes retrieved messages. Nil uses a default memory prompt.
	// An explicit empty string suppresses the prompt text.
	ContextPrompt *string

	// MaxResults limits the number of search results. Nil defaults to 3.
	// An explicit value must be positive.
	MaxResults *int

	// StateKey identifies provider state in the session. Nil defaults to
	// "ChatHistoryMemoryProvider". Explicit empty or whitespace-only keys are
	// invalid. Use distinct keys for providers that need independent scopes.
	StateKey *string

	// SearchInputMessageFilter filters automatic search input.
	// Nil defaults to [messagefilter.ExternalOnly].
	SearchInputMessageFilter messagefilter.Filter

	// StorageInputRequestMessageFilter filters request messages before storage.
	// Nil defaults to [messagefilter.ExternalOnly].
	StorageInputRequestMessageFilter messagefilter.Filter

	// StorageInputResponseMessageFilter filters response messages before storage.
	// Nil passes all response messages through.
	StorageInputResponseMessageFilter messagefilter.Filter

	// Logger receives provider diagnostics. When nil, logging is disabled.
	Logger *slog.Logger

	// LogSensitiveData includes user IDs, queries, and retrieved text in logs.
	// The default is false. When true, Redactor is ignored.
	LogSensitiveData bool

	// Redactor transforms sensitive log data. When nil and LogSensitiveData is
	// false, nonempty sensitive values are replaced with "<redacted>".
	Redactor func(string) string
}

// HistoryProvider stores chat messages in a vector collection and retrieves related
// messages as context. Create it with [NewHistoryProvider]. Do not copy it after use.
type HistoryProvider struct {
	collection       vectordata.Collection[any, map[string]any]
	config           Config
	stateInitializer func(*agent.Session) *State
	stateKey         string
	maxResults       int
	contextPrompt    string
	toolConfig       functool.Config
	contextProvider  agent.ContextProvider
	stateMu          sync.Mutex
	initialized      atomic.Bool
	initialization   *semaphore.Weighted
	cleanup          runtime.Cleanup
}

// NewHistoryProvider creates a chat history memory provider.
//
// The store must support dynamic map records, UUID keys, and embedding generation
// from strings. The provider obtains a collection handle at construction and
// creates the collection lazily on the first search or successful run. Failed
// creation is retried. The store is borrowed.
//
// stateInitializer supplies storage and search scopes when a session has no
// readable state. It must return a non-nil state. Without a session, it is called with
// nil for each lifecycle hook and no state is retained. On-demand tools capture
// the current invocation's search scope, not a later run's scope.
//
// Attach the provider using [agent.Config.ContextProviders]. Automatic searches
// join nonblank request texts with newlines and append nonblank results as one
// source-attributed user message. On-demand searches use only the tool's
// userQuestion argument. Use automatic tool invocation middleware when the
// underlying provider does not execute tools.
//
// Successful runs store filtered request messages followed by response messages,
// including messages without text. Storage and automatic-search failures are
// logged when Logger is configured and otherwise ignored, except cancellation
// errors while the caller's context is canceled. On-demand search, message-filter,
// and session-state errors are returned.
//
// Stored messages may contain sensitive data. Configure storage access controls
// and appropriate scopes; an empty scope searches across all stored conversations.
// Retrieved content and model-generated queries are untrusted.
//
// It panics if store or stateInitializer is nil, collectionName or StateKey is
// blank, or vectorDimensions or an explicit MaxResults is not positive.
// Collection configuration errors are returned.
func NewHistoryProvider(store *vectordata.Store, collectionName string, vectorDimensions int, stateInitializer func(*agent.Session) *State, config Config) (*HistoryProvider, error) {
	if store == nil {
		panic("store is required")
	}
	if stateInitializer == nil {
		panic("stateInitializer is required")
	}
	if strings.TrimSpace(collectionName) == "" {
		panic("collectionName must not be blank")
	}
	if vectorDimensions < 1 {
		panic("vectorDimensions must be positive")
	}
	p := &HistoryProvider{
		config:           config,
		stateInitializer: stateInitializer,
		stateKey:         defaultSourceID,
		maxResults:       defaultMaxResults,
		contextPrompt:    defaultContextPrompt,
		toolConfig: functool.Config{
			Name:        defaultFunctionToolName,
			Description: defaultFunctionToolDescription,
		},
		initialization: semaphore.NewWeighted(1),
	}
	if config.StateKey != nil {
		p.stateKey = *config.StateKey
	}
	if strings.TrimSpace(p.stateKey) == "" {
		panic("StateKey must not be blank")
	}
	if config.MaxResults != nil {
		p.maxResults = *config.MaxResults
		if p.maxResults < 1 {
			panic("MaxResults must be positive")
		}
	}
	if config.ContextPrompt != nil {
		p.contextPrompt = *config.ContextPrompt
	}
	if config.FunctionToolName != nil {
		p.toolConfig.Name = *config.FunctionToolName
	}
	if config.FunctionToolDescription != nil {
		p.toolConfig.Description = *config.FunctionToolDescription
	}
	collection, err := store.DynamicCollection(collectionName,
		&vectordata.Definition{Properties: []vectordata.Property{
			&vectordata.KeyProperty{Name: "Key", Type: reflect.TypeFor[uuid.UUID]()},
			&vectordata.DataProperty{Name: "Role", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.DataProperty{Name: "MessageId", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.DataProperty{Name: "AuthorName", Type: reflect.TypeFor[string]()},
			&vectordata.DataProperty{Name: "ApplicationId", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.DataProperty{Name: "AgentId", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.DataProperty{Name: "UserId", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.DataProperty{Name: "SessionId", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.DataProperty{Name: "Content", Type: reflect.TypeFor[string](), IsFullTextIndexed: true},
			&vectordata.DataProperty{Name: "CreatedAt", Type: reflect.TypeFor[string](), IsIndexed: true},
			&vectordata.VectorProperty{Name: "ContentEmbedding", Type: reflect.TypeFor[string](), Dimensions: vectorDimensions},
		}},
	)
	if err != nil {
		return nil, err
	}
	p.collection = collection
	p.contextProvider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID:                        defaultSourceID,
		ProvideInputMessageFilter:       config.SearchInputMessageFilter,
		StoreInputRequestMessageFilter:  config.StorageInputRequestMessageFilter,
		StoreInputResponseMessageFilter: config.StorageInputResponseMessageFilter,
		Provide:                         p.provide,
		Store:                           p.store,
	})
	if closer, ok := collection.(io.Closer); ok {
		logger := config.Logger
		p.cleanup = runtime.AddCleanup(p, func(closer io.Closer) {
			if err := closer.Close(); err != nil && logger != nil {
				logger.LogAttrs(context.Background(), slog.LevelError, "chat history collection cleanup failed", slog.Any("error", err))
			}
		}, closer)
	}
	return p, nil
}

// StateKeys returns the session key used by this provider. The returned slice
// belongs to the caller.
func (p *HistoryProvider) StateKeys() []string {
	return []string{p.stateKey}
}

// Invoking adds retrieved memories or a search tool scoped to this invocation.
func (p *HistoryProvider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	defer runtime.KeepAlive(p)
	return p.contextProvider.Invoking(ctx, invoking)
}

// Invoked stores eligible request and response messages after successful runs.
func (p *HistoryProvider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	defer runtime.KeepAlive(p)
	return p.contextProvider.Invoked(ctx, invoked)
}

// Close is an optimization that releases the collection early if it implements
// [io.Closer]. A successful call cancels the [runtime.AddCleanup] fallback, which
// attempts to close the collection after the provider becomes unreachable.
// Runtime cleanup is asynchronous and is not guaranteed to run before program exit.
//
// Close never closes the borrowed store. Repeated calls follow the collection's
// Close behavior. Do not overlap Close with runs or tool calls.
func (p *HistoryProvider) Close() error {
	defer runtime.KeepAlive(p)
	if closer, ok := p.collection.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	p.cleanup.Stop()
	return nil
}

func (p *HistoryProvider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	telemetry.MarkUsed(telemetry.FeatureChatHistoryMemoryProvider)
	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	state, err := p.loadState(session)
	if err != nil {
		return nil, nil, err
	}
	scope := state.SearchScope
	if scope == nil {
		scope = &state.StorageScope
	}
	if p.config.SearchTime == OnDemandFunctionCalling {
		search, err := functool.New(p.toolConfig, func(ctx context.Context, args struct {
			UserQuestion string `json:"userQuestion"`
		},
		) (string, error) {
			return p.search(ctx, args.UserQuestion, scope)
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, []agent.Option{agent.WithTool(search)}, nil
	}
	var parts []string
	for _, msg := range invoking.Messages {
		if msg != nil {
			if text := msg.String(); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
	}
	formatted, err := p.search(ctx, strings.Join(parts, "\n"), scope)
	if err != nil {
		return nil, nil, p.handleFailure(ctx, "chat history search failed", scope, err)
	}
	if strings.TrimSpace(formatted) == "" {
		return nil, nil, nil
	}
	return []*message.Message{message.NewText(formatted)}, nil, nil
}

func (p *HistoryProvider) store(ctx context.Context, invoked agent.InvokedContext) error {
	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	state, err := p.loadState(session)
	if err != nil {
		return err
	}
	if err := p.storeMessages(ctx, invoked, &state.StorageScope); err != nil {
		return p.handleFailure(ctx, "chat history storage failed", &state.StorageScope, err)
	}
	return nil
}

func (p *HistoryProvider) storeMessages(ctx context.Context, invoked agent.InvokedContext, scope *Scope) error {
	if err := p.ensureCollection(ctx); err != nil {
		return err
	}
	records := make([]map[string]any, 0, len(invoked.RequestMessages)+len(invoked.ResponseMessages))
	for _, messages := range [][]*message.Message{invoked.RequestMessages, invoked.ResponseMessages} {
		for _, msg := range messages {
			if msg == nil {
				return errors.New("memory: cannot store a nil message")
			}
			key, err := uuid.NewRandom()
			if err != nil {
				return err
			}
			createdAt := msg.CreatedAt
			if createdAt.IsZero() {
				createdAt = time.Now().UTC()
			}
			text := msg.String()
			records = append(records, map[string]any{
				"Key":              key,
				"Role":             string(msg.Role),
				"MessageId":        msg.ID,
				"AuthorName":       msg.AuthorName,
				"ApplicationId":    nullableString(scope.ApplicationID),
				"AgentId":          nullableString(scope.AgentID),
				"UserId":           nullableString(scope.UserID),
				"SessionId":        nullableString(scope.SessionID),
				"Content":          text,
				"CreatedAt":        createdAt.Format(time.RFC3339Nano),
				"ContentEmbedding": text,
			})
		}
	}
	if len(records) == 0 {
		return nil
	}
	return p.collection.Upsert(ctx, records...)
}

func (p *HistoryProvider) search(ctx context.Context, query string, scope *Scope) (string, error) {
	defer runtime.KeepAlive(p)
	if strings.TrimSpace(query) == "" {
		return "", nil
	}
	if err := p.ensureCollection(ctx); err != nil {
		return "", err
	}
	var records []map[string]any
	for result, err := range p.collection.Search(ctx, query, p.maxResults, &vectordata.SearchOptions{Filter: scope.filter()}) {
		if err != nil {
			return "", err
		}
		records = append(records, result.Record)
	}
	if logger := p.config.Logger; logger != nil && logger.Enabled(ctx, slog.LevelInfo) {
		logger.LogAttrs(ctx, slog.LevelInfo, "chat history search completed",
			append(p.scopeLogAttrs(scope), slog.Int("count", len(records)))...)
	}
	var parts []string
	for i, record := range records {
		content, ok := record["Content"]
		if !ok {
			return "", fmt.Errorf("memory: search result %d has no Content field", i)
		}
		if content == nil {
			continue
		}
		text, ok := content.(string)
		if !ok {
			return "", fmt.Errorf("memory: search result %d has Content of type %T, want string", i, content)
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	formatted := p.contextPrompt + "\n" + strings.Join(parts, "\n")
	if logger := p.config.Logger; logger != nil && logger.Enabled(ctx, slog.LevelDebug) {
		logger.LogAttrs(ctx, slog.LevelDebug, "chat history search results",
			append(p.scopeLogAttrs(scope),
				slog.String("query", p.sanitizeLogData(query)),
				slog.String("context", p.sanitizeLogData(formatted)))...)
	}
	return formatted, nil
}

func (p *HistoryProvider) loadState(session *agent.Session) (*State, error) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	var state *State
	if ok, err := session.Get(p.stateKey, &state); err != nil {
		return nil, err
	} else if !ok || state == nil {
		state = p.stateInitializer(session)
		if state == nil {
			return nil, errors.New("memory: stateInitializer returned nil")
		}
		session.Set(p.stateKey, state)
	}
	return state, nil
}

func (p *HistoryProvider) ensureCollection(ctx context.Context) error {
	if p.initialized.Load() {
		return nil
	}
	if err := p.initialization.Acquire(ctx, 1); err != nil {
		return err
	}
	defer p.initialization.Release(1)
	if p.initialized.Load() {
		return nil
	}
	if err := p.collection.EnsureExists(ctx); err != nil {
		return err
	}
	p.initialized.Store(true)
	return nil
}

func (s *Scope) filter() *vectordata.Filter {
	var filters []vectordata.Filter
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"ApplicationId", s.ApplicationID},
		{"AgentId", s.AgentID},
		{"UserId", s.UserID},
		{"SessionId", s.SessionID},
	} {
		if field.value != nil {
			filters = append(filters, vectordata.Filter{Operator: vectordata.Equal, Property: field.name, Value: *field.value})
		}
	}
	switch len(filters) {
	case 0:
		return nil
	case 1:
		return &filters[0]
	default:
		return &vectordata.Filter{Operator: vectordata.And, Operands: filters}
	}
}

func (p *HistoryProvider) handleFailure(ctx context.Context, text string, scope *Scope, err error) error {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	if logger := p.config.Logger; logger != nil && logger.Enabled(ctx, slog.LevelError) {
		logger.LogAttrs(ctx, slog.LevelError, text, append(p.scopeLogAttrs(scope), slog.Any("error", err))...)
	}
	return nil
}

func (p *HistoryProvider) scopeLogAttrs(scope *Scope) []slog.Attr {
	userID := ""
	if scope.UserID != nil {
		userID = p.sanitizeLogData(*scope.UserID)
	}
	return []slog.Attr{
		slog.Any("application_id", nullableString(scope.ApplicationID)),
		slog.Any("agent_id", nullableString(scope.AgentID)),
		slog.Any("session_id", nullableString(scope.SessionID)),
		slog.String("user_id", userID),
	}
}

func (p *HistoryProvider) sanitizeLogData(data string) string {
	if p.config.LogSensitiveData || data == "" {
		return data
	}
	if p.config.Redactor != nil {
		return p.config.Redactor(data)
	}
	return "<redacted>"
}

func nullableString(value *string) any {
	if value != nil {
		return *value
	}
	return nil
}

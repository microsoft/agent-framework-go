// Copyright (c) Microsoft. All rights reserved.

package memory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/embedding"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/vectordata"
	"github.com/microsoft/agent-framework-go/vectordata/inmemory"
	"github.com/microsoft/agent-framework-go/vectordata/providerservices"
)

const (
	stateKey      = "ChatHistoryMemoryProvider"
	contextPrompt = "## Memories\nConsider the following memories when answering user questions:"
)

type record = map[string]any

type testStore struct {
	vectordata.StoreProvider
	collection vectordata.Collection[any, any]
	name       string
	definition *vectordata.Definition
	err        error
	closeCalls int
}

func (s *testStore) OpenCollection(name string, _, _ reflect.Type, definition *vectordata.Definition) (vectordata.Collection[any, any], error) {
	s.name, s.definition = name, definition
	return s.collection, s.err
}

func (s *testStore) Close() error {
	s.closeCalls++
	return nil
}

type testCollection struct {
	vectordata.Collection[any, any]
	ensure func(context.Context) error
	search func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error]
	upsert func(context.Context, ...record) error
	close  func() error
}

func (c *testCollection) EnsureExists(ctx context.Context) error {
	if c.ensure != nil {
		return c.ensure(ctx)
	}
	return nil
}

func (c *testCollection) Search(ctx context.Context, query any, top int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[any], error] {
	return func(yield func(vectordata.SearchResult[any], error) bool) {
		if c.search != nil {
			for result, err := range c.search(ctx, query, top, options) {
				if !yield(vectordata.SearchResult[any]{Record: result.Record, Score: result.Score}, err) {
					return
				}
			}
		}
	}
}

func (c *testCollection) Upsert(ctx context.Context, records ...any) error {
	if c.upsert != nil {
		typed := make([]record, len(records))
		for i, value := range records {
			typed[i] = value.(record)
		}
		return c.upsert(ctx, typed...)
	}
	return nil
}

func (c *testCollection) Close() error {
	if c.close != nil {
		return c.close()
	}
	return nil
}

func searchResults(records ...record) iter.Seq2[vectordata.SearchResult[record], error] {
	return func(yield func(vectordata.SearchResult[record], error) bool) {
		for _, r := range records {
			if !yield(vectordata.SearchResult[record]{Record: r}, nil) {
				return
			}
		}
	}
}

func newProvider(t *testing.T, collection vectordata.Collection[any, any], initialize func(*agent.Session) *memory.State, config memory.Config) *memory.HistoryProvider {
	t.Helper()
	if initialize == nil {
		initialize = func(*agent.Session) *memory.State { return new(memory.State) }
	}
	provider, err := memory.NewHistoryProvider(vectordata.NewStore(&testStore{collection: collection}), "history", 2, initialize, config)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func invokeProvider(provider agent.ContextProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func storeMessages(t *testing.T, provider agent.ContextProvider, session *agent.Session, request, response []*message.Message) {
	t.Helper()
	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: request, ResponseMessages: response, Options: []agent.Option{agent.WithSession(session)},
	}); err != nil {
		t.Fatal(err)
	}
}

func searchTool(t *testing.T, provider agent.ContextProvider, options ...agent.Option) tool.FuncTool {
	t.Helper()
	request := message.NewText("not an automatic query")
	messages, returned, err := invokeProvider(provider, t.Context(), []*message.Message{request}, options...)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != request {
		t.Fatalf("on-demand mode changed input messages: %v", messages)
	}
	tools := slices.Collect(agent.AllOptions(returned, agent.WithTool))
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	search, ok := tools[0].(tool.FuncTool)
	if !ok {
		t.Fatalf("search tool has type %T", tools[0])
	}
	return search
}

func TestChatHistoryMemoryProvider_ConfigValidation(t *testing.T) {
	for _, name := range []string{
		"nil store", "nil initializer", "empty collection", "blank collection",
		"zero dimensions", "negative dimensions", "zero maximum", "negative maximum",
		"empty state key", "blank state key",
	} {
		t.Run(name, func(t *testing.T) {
			store := vectordata.NewStore(&testStore{collection: &testCollection{}})
			initialize := func(*agent.Session) *memory.State { return new(memory.State) }
			collectionName, dimensions := "history", 2
			config := memory.Config{}
			switch name {
			case "nil store":
				store = nil
			case "nil initializer":
				initialize = nil
			case "empty collection":
				collectionName = ""
			case "blank collection":
				collectionName = " \t"
			case "zero dimensions":
				dimensions = 0
			case "negative dimensions":
				dimensions = -5
			case "zero maximum":
				config.MaxResults = new(0)
			case "negative maximum":
				config.MaxResults = new(-1)
			case "empty state key":
				config.StateKey = new("")
			case "blank state key":
				config.StateKey = new(" \t")
			}
			defer func() {
				if recover() == nil {
					t.Fatal("expected a configuration panic")
				}
			}()
			_, _ = memory.NewHistoryProvider(store, collectionName, dimensions, initialize, config)
		})
	}
}

func TestChatHistoryMemoryProvider_CollectionDefinition(t *testing.T) {
	store := &testStore{collection: &testCollection{ensure: func(context.Context) error {
		t.Fatal("collection was created eagerly")
		return nil
	}}}
	provider, err := memory.NewHistoryProvider(vectordata.NewStore(store), "history", 1536,
		func(*agent.Session) *memory.State { return new(memory.State) }, memory.Config{})
	if err != nil || provider == nil || store.name != "history" {
		t.Fatalf("NewHistoryProvider = (%v, %v), name = %q", provider, err, store.name)
	}
	want := &vectordata.Definition{Properties: []vectordata.Property{
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
		&vectordata.VectorProperty{Name: "ContentEmbedding", Type: reflect.TypeFor[string](), Dimensions: 1536},
	}}
	if !reflect.DeepEqual(store.definition, want) {
		t.Fatalf("collection definition = %#v, want %#v", store.definition, want)
	}
	wantErr := errors.New("unsupported collection")
	store.err = wantErr
	if provider, err := memory.NewHistoryProvider(vectordata.NewStore(store), "history", 2,
		func(*agent.Session) *memory.State { return new(memory.State) }, memory.Config{}); provider != nil || err != wantErr {
		t.Fatalf("collection failure = (%v, %v)", provider, err)
	}
}

func TestChatHistoryMemoryProvider_StateKeysAndInitialization(t *testing.T) {
	for _, key := range []*string{nil, new("custom-key")} {
		wantKey := stateKey
		if key != nil {
			wantKey = *key
		}
		t.Run(wantKey, func(t *testing.T) {
			session := agenttest.CreateSession()
			want := &memory.State{StorageScope: memory.Scope{UserID: new("user")}}
			calls := 0
			provider := newProvider(t, &testCollection{}, func(got *agent.Session) *memory.State {
				if got != session {
					t.Fatal("initializer received a different session")
				}
				calls++
				return want
			}, memory.Config{StateKey: key})
			keys := provider.StateKeys()
			if !slices.Equal(keys, []string{wantKey}) {
				t.Fatalf("StateKeys = %v", keys)
			}
			keys[0] = "changed"
			if key != nil {
				*key = "changed"
			}
			if provider.StateKeys()[0] != wantKey {
				t.Fatal("state key changed with caller-owned configuration")
			}
			for range 2 {
				if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
					t.Fatal(err)
				}
				storeMessages(t, provider, session, nil, nil)
			}
			var got *memory.State
			if ok, err := session.Get(wantKey, &got); err != nil || !ok || got != want || calls != 1 {
				t.Fatalf("state = (%v, %v, %v), initializer calls = %d", got, ok, err, calls)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_StateRestoration(t *testing.T) {
	for _, explicitSearch := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-search=%t", explicitSearch), func(t *testing.T) {
			initial := &memory.State{StorageScope: memory.Scope{
				ApplicationID: new("app"), AgentID: new("agent"), UserID: new("user"), SessionID: new(""),
			}}
			wantSearch := initial.StorageScope
			if explicitSearch {
				initial.SearchScope = &memory.Scope{UserID: new("search-user")}
				wantSearch = *initial.SearchScope
			}
			calls := 0
			collection := &testCollection{}
			provider := newProvider(t, collection, func(*agent.Session) *memory.State {
				calls++
				return initial
			}, memory.Config{})
			session := agenttest.CreateSession()
			if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			restored := new(agent.Session)
			if err := json.Unmarshal(data, restored); err != nil {
				t.Fatal(err)
			}
			var got *memory.State
			if ok, err := restored.Get(stateKey, &got); err != nil || !ok || !reflect.DeepEqual(got, initial) {
				t.Fatalf("restored state = (%#v, %v, %v)", got, ok, err)
			}
			collection.search = func(_ context.Context, _ any, _ int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
				assertScopeFilter(t, options.Filter, wantSearch)
				return searchResults()
			}
			collection.upsert = func(_ context.Context, records ...record) error {
				if len(records) != 1 || records[0]["UserId"] != "user" || records[0]["SessionId"] != "" {
					t.Fatalf("restored storage scope = %#v", records)
				}
				return nil
			}
			if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("query")}, agent.WithSession(restored)); err != nil {
				t.Fatal(err)
			}
			storeMessages(t, provider, restored, []*message.Message{message.NewText("stored")}, nil)
			if calls != 1 {
				t.Fatalf("initializer calls = %d, want no reinitialization after restore", calls)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_StateErrors(t *testing.T) {
	session := agenttest.CreateSession()
	var logs bytes.Buffer
	collection := &testCollection{ensure: func(context.Context) error {
		t.Fatal("state failure reached the collection")
		return nil
	}}
	provider := newProvider(t, collection, func(*agent.Session) *memory.State {
		return nil
	}, memory.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err == nil {
		t.Fatal("Invoking did not return a state error")
	}
	if err := provider.Invoked(t.Context(), agent.InvokedContext{Options: []agent.Option{agent.WithSession(session)}}); err == nil {
		t.Fatal("Invoked did not return a state error")
	}
	if logs.Len() != 0 {
		t.Fatal("state errors were treated as best-effort storage/search failures")
	}
}

func TestChatHistoryMemoryProvider_MissingAndNilSessionState(t *testing.T) {
	for _, name := range []string{"missing", "typed nil", "null", "other type", "unreadable restored state", "no session"} {
		t.Run(name, func(t *testing.T) {
			session := agenttest.CreateSession()
			switch name {
			case "typed nil":
				session.Set(stateKey, (*memory.State)(nil))
			case "null":
				if err := json.Unmarshal([]byte(`{"State":{"ChatHistoryMemoryProvider":null}}`), session); err != nil {
					t.Fatal(err)
				}
			case "other type":
				session.Set(stateKey, 42)
			case "unreadable restored state":
				if err := json.Unmarshal([]byte(`{"State":{"ChatHistoryMemoryProvider":{"storageScope":42}}}`), session); err != nil {
					t.Fatal(err)
				}
			case "no session":
				session = nil
			}
			calls := 0
			provider := newProvider(t, &testCollection{}, func(got *agent.Session) *memory.State {
				if got != session {
					t.Fatal("initializer session mismatch")
				}
				calls++
				return new(memory.State)
			}, memory.Config{})
			for range 2 {
				if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
					t.Fatal(err)
				}
				storeMessages(t, provider, session, nil, nil)
			}
			want := 1
			if session == nil {
				want = 4
			}
			if calls != want {
				t.Fatalf("initializations = %d, want %d", calls, want)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_StoresMessages(t *testing.T) {
	var stored []record
	ensureCalls, upsertCalls := 0, 0
	collection := &testCollection{
		ensure: func(ctx context.Context) error {
			if ctx != t.Context() {
				t.Fatal("EnsureExists context changed")
			}
			ensureCalls++
			return nil
		},
		upsert: func(ctx context.Context, records ...record) error {
			if ctx != t.Context() {
				t.Fatal("Upsert context changed")
			}
			upsertCalls++
			stored = append(stored, records...)
			return nil
		},
	}
	provider := newProvider(t, collection, func(*agent.Session) *memory.State {
		return &memory.State{StorageScope: memory.Scope{
			ApplicationID: new("app"), AgentID: new("agent"), UserID: new("user"), SessionID: new("session"),
		}}
	}, memory.Config{})
	request := message.NewText("request text")
	request.ID, request.AuthorName = "req-1", "user1"
	request.CreatedAt = time.Date(2000, 1, 1, 0, 0, 0, 123456789, time.FixedZone("offset", 3600))
	response := message.NewText("response text")
	response.Role, response.ID, response.AuthorName = message.RoleAssistant, "resp-1", "assistant"
	empty := message.New(&message.FunctionCallContent{CallID: "call", Name: "tool"})
	empty.Role = message.RoleAssistant
	before := time.Now().UTC()
	storeMessages(t, provider, agenttest.CreateSession(),
		[]*message.Message{request, message.NewText("request without metadata")},
		[]*message.Message{response, empty})
	after := time.Now().UTC()
	if len(stored) != 4 || ensureCalls != 1 || upsertCalls != 1 {
		t.Fatalf("stored = %d, ensure calls = %d, upsert calls = %d", len(stored), ensureCalls, upsertCalls)
	}
	keys := make(map[uuid.UUID]bool)
	for i, want := range []*message.Message{request, message.NewText("request without metadata"), response, empty} {
		got := stored[i]
		key, ok := got["Key"].(uuid.UUID)
		if !ok || key == uuid.Nil || keys[key] || key.Version() != 4 {
			t.Fatalf("invalid or repeated key: %v", got["Key"])
		}
		keys[key] = true
		if len(got) != 11 || got["Content"] != want.String() || got["ContentEmbedding"] != want.String() ||
			got["MessageId"] != want.ID || got["AuthorName"] != want.AuthorName || got["Role"] != string(want.Role) {
			t.Fatalf("stored message %d = %#v", i, got)
		}
		for field, value := range map[string]string{"ApplicationId": "app", "AgentId": "agent", "UserId": "user", "SessionId": "session"} {
			if got[field] != value {
				t.Fatalf("stored %s = %v, want %q", field, got[field], value)
			}
		}
		created, err := time.Parse(time.RFC3339Nano, got["CreatedAt"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if !want.CreatedAt.IsZero() {
			if got["CreatedAt"] != want.CreatedAt.Format(time.RFC3339Nano) {
				t.Fatalf("creation time = %q, want original offset and precision", got["CreatedAt"])
			}
		} else if created.Before(before) || created.After(after) {
			t.Fatalf("generated creation time %v not in [%v, %v]", created, before, after)
		}
	}
	if !response.CreatedAt.IsZero() || !empty.CreatedAt.IsZero() || request.ID != "req-1" {
		t.Fatal("storage changed input messages")
	}
	storeMessages(t, provider, nil, []*message.Message{request}, nil)
	if len(stored) != 5 || ensureCalls != 1 || upsertCalls != 2 || stored[4]["Key"] == stored[0]["Key"] {
		t.Fatal("repeated storage did not create a fresh record without reinitializing the collection")
	}
}

func TestChatHistoryMemoryProvider_EmptyStorageAndScopes(t *testing.T) {
	ensureCalls, upsertCalls := 0, 0
	provider := newProvider(t, &testCollection{
		ensure: func(context.Context) error { ensureCalls++; return nil },
		upsert: func(_ context.Context, records ...record) error {
			upsertCalls++
			if len(records) != 1 || records[0]["ApplicationId"] != nil || records[0]["AgentId"] != nil ||
				records[0]["SessionId"] != nil || records[0]["UserId"] != "" {
				t.Fatalf("nil/empty storage scopes = %#v", records)
			}
			return nil
		},
	}, func(*agent.Session) *memory.State {
		return &memory.State{StorageScope: memory.Scope{UserID: new("")}}
	}, memory.Config{})
	storeMessages(t, provider, nil, nil, nil)
	if ensureCalls != 1 || upsertCalls != 0 {
		t.Fatalf("empty store: ensure = %d, upsert = %d", ensureCalls, upsertCalls)
	}
	storeMessages(t, provider, nil, []*message.Message{message.NewText("")}, nil)
	if upsertCalls != 1 {
		t.Fatal("message without text was dropped")
	}
}

func TestChatHistoryMemoryProvider_FailedRunSkipsStorage(t *testing.T) {
	provider := newProvider(t, &testCollection{}, func(*agent.Session) *memory.State {
		t.Fatal("failed run initialized state")
		return nil
	}, memory.Config{StorageInputRequestMessageFilter: func(context.Context, []*message.Message) ([]*message.Message, error) {
		t.Fatal("failed run applied a storage filter")
		return nil, nil
	}})
	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: []*message.Message{message.NewText("request")}, Err: errors.New("run failed"),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestChatHistoryMemoryProvider_AutomaticSearch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  memory.Config
		wantTop int
		prompt  string
	}{
		{name: "defaults", wantTop: 3, prompt: contextPrompt},
		{name: "custom", config: memory.Config{MaxResults: new(2), ContextPrompt: new("Context:\n")}, wantTop: 2, prompt: "Context:\n"},
		{name: "empty prompt", config: memory.Config{ContextPrompt: new("")}, wantTop: 3},
		{name: "unknown behavior", config: memory.Config{SearchTime: memory.SearchBehavior(42)}, wantTop: 3, prompt: contextPrompt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			provider := newProvider(t, &testCollection{
				search: func(ctx context.Context, query any, top int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					calls++
					if ctx != t.Context() || query != " first \nsecond" || top != tc.wantTop || options.Filter != nil ||
						options.Skip != 0 || options.IncludeVectors || options.VectorProperty != "" {
						t.Fatalf("search = (%v, %v, %d, %#v)", ctx, query, top, options)
					}
					return searchResults(
						record{"Content": "one"}, record{"Content": nil}, record{"Content": " \t"}, record{"Content": " two "},
					)
				},
			}, nil, tc.config)
			request := []*message.Message{message.NewText(" first "), nil, message.NewText("\t "), message.NewText("second")}
			original := slices.Clone(request)
			options := []agent.Option{agent.WithInstructions("keep these instructions")}
			messages, returned, err := invokeProvider(provider, t.Context(), request, options...)
			if err != nil || calls != 1 || len(messages) != len(request)+1 {
				t.Fatalf("Invoking = (%v, %v), calls = %d", messages, err, calls)
			}
			if !slices.Equal(messages[:len(request)], original) || !slices.Equal(request, original) {
				t.Fatal("search changed the input messages")
			}
			if got, _ := agent.GetOption(returned, agent.WithInstructions); got != "keep these instructions" || len(returned) != len(options) {
				t.Fatal("search changed input options")
			}
			result := messages[len(request)]
			if result.String() != tc.prompt+"\none\n two " || result.Role != message.RoleUser ||
				result.Source != (message.Source{Type: agent.SourceTypeContextProvider, ID: stateKey}) {
				t.Fatalf("retrieved context = %#v (%q)", result, result.String())
			}
		})
	}
}

func TestChatHistoryMemoryProvider_EmptySearch(t *testing.T) {
	for _, mode := range []memory.SearchBehavior{memory.BeforeInvoke, memory.OnDemandFunctionCalling} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			provider := newProvider(t, &testCollection{ensure: func(context.Context) error {
				t.Fatal("blank query initialized the collection")
				return nil
			}}, nil, memory.Config{SearchTime: mode})
			session := agenttest.CreateSession()
			if mode == memory.BeforeInvoke {
				request := []*message.Message{nil, message.NewText(" \t\n")}
				messages, _, err := invokeProvider(provider, t.Context(), request, agent.WithSession(session))
				if err != nil || !slices.Equal(messages, request) {
					t.Fatalf("blank search = (%v, %v)", messages, err)
				}
			} else {
				result, err := searchTool(t, provider, agent.WithSession(session)).Call(t.Context(), `{"userQuestion":" \t\n"}`)
				if err != nil || result != "" {
					t.Fatalf("blank tool search = (%v, %v)", result, err)
				}
			}
			var state *memory.State
			if ok, err := session.Get(stateKey, &state); err != nil || !ok || state == nil {
				t.Fatalf("blank query did not initialize session state: (%v, %v, %v)", state, ok, err)
			}
		})
	}
	for _, records := range [][]record{nil, {{"Content": nil}, {"Content": ""}, {"Content": " \n"}}} {
		provider := newProvider(t, &testCollection{
			search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
				return searchResults(records...)
			},
		}, nil, memory.Config{})
		request := []*message.Message{message.NewText("query")}
		messages, _, err := invokeProvider(provider, t.Context(), request)
		if err != nil || !slices.Equal(messages, request) {
			t.Fatalf("empty results produced context: (%v, %v)", messages, err)
		}
	}
}

func assertScopeFilter(t *testing.T, filter *vectordata.Filter, scope memory.Scope) {
	t.Helper()
	values := map[string]*string{
		"ApplicationId": scope.ApplicationID, "AgentId": scope.AgentID, "UserId": scope.UserID, "SessionId": scope.SessionID,
	}
	matching := make(record)
	restricted := false
	for name, value := range values {
		if value != nil {
			matching[name] = *value
			restricted = true
		} else {
			matching[name] = "unrestricted"
		}
	}
	if !restricted && filter != nil || restricted && filter == nil {
		t.Fatalf("scope filter = %#v for %#v", filter, scope)
	}
	predicate, err := providerservices.CompileFilter(filter, func(name string) (providerservices.FilterProperty, error) {
		if _, ok := values[name]; !ok {
			return providerservices.FilterProperty{}, fmt.Errorf("unexpected scope property %q", name)
		}
		return providerservices.FilterProperty{
			Type: reflect.TypeFor[string](),
			Value: func(input any) (any, bool, error) {
				value, ok := input.(record)[name]
				return value, ok, nil
			},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := predicate(matching); err != nil || !ok {
		t.Fatalf("matching record rejected: (%v, %v)", ok, err)
	}
	for name, value := range values {
		original := matching[name]
		for _, mismatch := range []any{"different", nil} {
			matching[name] = mismatch
			if ok, err := predicate(matching); err != nil || ok != (value == nil) {
				t.Fatalf("%s=%v matched: (%v, %v), restricted=%t", name, mismatch, ok, err, value != nil)
			}
		}
		matching[name] = original
	}
}

func TestChatHistoryMemoryProvider_ScopeFilters(t *testing.T) {
	for mask := range 16 {
		for _, value := range []string{"scope-value", ""} {
			t.Run(fmt.Sprintf("mask=%d/value=%q", mask, value), func(t *testing.T) {
				scope := memory.Scope{}
				for i, field := range []**string{&scope.ApplicationID, &scope.AgentID, &scope.UserID, &scope.SessionID} {
					if mask&(1<<i) != 0 {
						*field = new(value)
					}
				}
				calls := 0
				provider := newProvider(t, &testCollection{
					search: func(_ context.Context, _ any, _ int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
						calls++
						assertScopeFilter(t, options.Filter, scope)
						return searchResults()
					},
				}, func(*agent.Session) *memory.State {
					return &memory.State{StorageScope: memory.Scope{UserID: new("storage")}, SearchScope: &scope}
				}, memory.Config{})
				if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("query")}); err != nil || calls != 1 {
					t.Fatalf("scope search: error = %v, calls = %d", err, calls)
				}
			})
		}
	}
}

func TestChatHistoryMemoryProvider_MessageFilters(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom=%t", custom), func(t *testing.T) {
			var query any
			var stored []record
			config := memory.Config{}
			if custom {
				config.SearchInputMessageFilter = messagefilter.PassThrough
				config.StorageInputRequestMessageFilter = messagefilter.PassThrough
				config.StorageInputResponseMessageFilter = messagefilter.None
			}
			provider := newProvider(t, &testCollection{
				search: func(_ context.Context, input any, _ int, _ *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					query = input
					return searchResults()
				},
				upsert: func(_ context.Context, records ...record) error { stored = records; return nil },
			}, nil, config)
			request := []*message.Message{
				message.NewText("external"),
				message.NewText("history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider, ID: "history"}),
				message.NewText("context").WithSource(message.Source{Type: agent.SourceTypeContextProvider, ID: "context"}),
			}
			response := []*message.Message{message.NewText("reply")}
			original := slices.Clone(request)
			if _, _, err := invokeProvider(provider, t.Context(), request); err != nil {
				t.Fatal(err)
			}
			storeMessages(t, provider, nil, request, response)
			wantQuery, wantStored := "external", []string{"external", "reply"}
			if custom {
				wantQuery, wantStored = "external\nhistory\ncontext", []string{"external", "history", "context"}
			}
			var contents []string
			for _, r := range stored {
				contents = append(contents, r["Content"].(string))
			}
			if query != wantQuery || !slices.Equal(contents, wantStored) {
				t.Fatalf("query = %v, stored = %q", query, contents)
			}
			if !slices.Equal(request, original) || len(response) != 1 || response[0] == nil {
				t.Fatal("filters mutated caller-owned slices")
			}
		})
	}
}

func TestChatHistoryMemoryProvider_FilterErrors(t *testing.T) {
	wantErr := errors.New("filter failed")
	filter := func(context.Context, []*message.Message) ([]*message.Message, error) { return nil, wantErr }
	for _, stage := range []string{"search", "storage request", "storage response"} {
		t.Run(stage, func(t *testing.T) {
			config := memory.Config{}
			switch stage {
			case "search":
				config.SearchInputMessageFilter = filter
			case "storage request":
				config.StorageInputRequestMessageFilter = filter
			case "storage response":
				config.StorageInputResponseMessageFilter = filter
			}
			provider := newProvider(t, &testCollection{}, func(*agent.Session) *memory.State {
				t.Fatal("filter failure reached state initialization")
				return nil
			}, config)
			var err error
			if stage == "search" {
				_, _, err = invokeProvider(provider, t.Context(), nil)
			} else {
				err = provider.Invoked(t.Context(), agent.InvokedContext{})
			}
			if err != wantErr {
				t.Fatalf("filter error = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_FailureAndCancellation(t *testing.T) {
	for _, operation := range []string{"initialize", "search", "upsert"} {
		for _, tc := range []struct {
			name      string
			cause     error
			cancel    bool
			propagate bool
		}{
			{name: "backend failure", cause: errors.New("backend failed")},
			{name: "provider cancellation", cause: context.Canceled},
			{name: "provider deadline", cause: context.DeadlineExceeded},
			{name: "caller cancellation", cause: context.Canceled, cancel: true, propagate: true},
			{name: "caller canceled with provider deadline", cause: context.DeadlineExceeded, cancel: true, propagate: true},
			{name: "ordinary error with canceled caller", cause: errors.New("backend failed"), cancel: true},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				wantErr := fmt.Errorf("original failure: %w", tc.cause)
				fail := func() error {
					if tc.cancel {
						cancel()
					}
					return wantErr
				}
				collection := &testCollection{}
				switch operation {
				case "initialize":
					collection.ensure = func(context.Context) error { return fail() }
				case "search":
					collection.search = func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
						return func(yield func(vectordata.SearchResult[record], error) bool) {
							yield(vectordata.SearchResult[record]{}, fail())
						}
					}
				case "upsert":
					collection.upsert = func(context.Context, ...record) error { return fail() }
				}
				var logs bytes.Buffer
				provider := newProvider(t, collection, nil, memory.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
				request := []*message.Message{message.NewText("query")}
				var err error
				if operation == "upsert" {
					err = provider.Invoked(ctx, agent.InvokedContext{RequestMessages: request})
				} else {
					var messages []*message.Message
					messages, _, err = invokeProvider(provider, ctx, request)
					if !tc.propagate && !slices.Equal(messages, request) {
						t.Fatal("failed automatic search changed request messages")
					}
				}
				if tc.propagate {
					if err != wantErr || logs.Len() != 0 {
						t.Fatalf("caller cancellation = %v, logs = %s", err, logs.String())
					}
				} else if err != nil || !strings.Contains(logs.String(), `"level":"ERROR"`) {
					t.Fatalf("best-effort failure = %v, logs = %s", err, logs.String())
				}
			})
		}
	}
}

func TestChatHistoryMemoryProvider_InvalidAndPartialResults(t *testing.T) {
	for _, name := range []string{"missing content", "wrong content type", "iterator error"} {
		for _, onDemand := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/on-demand=%t", name, onDemand), func(t *testing.T) {
				wantErr := errors.New("iterator failed")
				var stopped bool
				collection := &testCollection{
					search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
						if name == "missing content" {
							return searchResults(record{"Content": "first"}, record{})
						}
						if name == "wrong content type" {
							return searchResults(record{"Content": "first"}, record{"Content": 42})
						}
						return func(yield func(vectordata.SearchResult[record], error) bool) {
							defer func() { stopped = true }()
							if !yield(vectordata.SearchResult[record]{Record: record{"Content": "first"}}, nil) {
								return
							}
							if yield(vectordata.SearchResult[record]{}, wantErr) {
								t.Error("iteration continued after an error")
							}
						}
					},
				}
				var logs bytes.Buffer
				config := memory.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
				if onDemand {
					config.SearchTime = memory.OnDemandFunctionCalling
				}
				provider := newProvider(t, collection, nil, config)
				if onDemand {
					if _, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"query"}`); err == nil ||
						name == "iterator error" && !errors.Is(err, wantErr) {
						t.Fatalf("tool search error = %v", err)
					}
				} else {
					request := []*message.Message{message.NewText("query")}
					messages, _, err := invokeProvider(provider, t.Context(), request)
					if err != nil || !slices.Equal(messages, request) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
						t.Fatalf("failed automatic search = (%v, %v), logs = %s", messages, err, logs.String())
					}
				}
				if name == "iterator error" && !stopped {
					t.Fatal("failed iterator was not released")
				}
			})
		}
	}
}

func TestChatHistoryMemoryProvider_OnDemandTool(t *testing.T) {
	for _, tc := range []struct {
		name        *string
		description *string
		wantName    string
		wantDesc    string
	}{
		{wantName: "Search", wantDesc: "Allows searching for related previous chat history to help answer the user question."},
		{name: new("Recall"), description: new("Recall past chats"), wantName: "Recall", wantDesc: "Recall past chats"},
		{name: new(""), description: new("")},
	} {
		t.Run(tc.wantName, func(t *testing.T) {
			ensureCalls, searchCalls := 0, 0
			provider := newProvider(t, &testCollection{
				ensure: func(context.Context) error { ensureCalls++; return nil },
				search: func(ctx context.Context, query any, top int, _ *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					searchCalls++
					if ctx != t.Context() || query != "tool query" || top != 3 {
						t.Fatalf("tool search = (%v, %v, %d)", ctx, query, top)
					}
					return searchResults(record{"Content": "memory"})
				},
			}, nil, memory.Config{SearchTime: memory.OnDemandFunctionCalling, FunctionToolName: tc.name, FunctionToolDescription: tc.description})
			search := searchTool(t, provider)
			if search.Name() != tc.wantName || search.Description() != tc.wantDesc || ensureCalls != 0 || searchCalls != 0 {
				t.Fatalf("tool = (%q, %q), eager initialization/search = (%d, %d)", search.Name(), search.Description(), ensureCalls, searchCalls)
			}
			data, err := json.Marshal(search.Schema())
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Type       string
				Required   []string
				Properties map[string]struct{ Type string }
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if schema.Type != "object" || !slices.Equal(schema.Required, []string{"userQuestion"}) || schema.Properties["userQuestion"].Type != "string" {
				t.Fatalf("tool input schema = %s", data)
			}
			for _, args := range []string{`{}`, `{"userQuestion":42}`, `not json`} {
				if _, err := search.Call(t.Context(), args); err == nil {
					t.Fatalf("invalid arguments accepted: %s", args)
				}
			}
			if searchCalls != 0 || ensureCalls != 0 {
				t.Fatal("invalid tool arguments reached the collection")
			}
			result, err := search.Call(t.Context(), `{"userQuestion":"tool query"}`)
			if err != nil || result != contextPrompt+"\nmemory" || searchCalls != 1 || ensureCalls != 1 {
				t.Fatalf("tool result = (%v, %v), search = %d, ensure = %d", result, err, searchCalls, ensureCalls)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_OnDemandScopes(t *testing.T) {
	first, second := agenttest.CreateSession(), agenttest.CreateSession()
	firstState := &memory.State{StorageScope: memory.Scope{UserID: new("first")}}
	secondState := &memory.State{StorageScope: memory.Scope{UserID: new("second")}}
	states := map[*agent.Session]*memory.State{first: firstState, second: secondState}
	var wantScope memory.Scope
	provider := newProvider(t, &testCollection{
		search: func(_ context.Context, _ any, _ int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
			assertScopeFilter(t, options.Filter, wantScope)
			return searchResults()
		},
	}, func(session *agent.Session) *memory.State { return states[session] },
		memory.Config{SearchTime: memory.OnDemandFunctionCalling})
	firstTool := searchTool(t, provider, agent.WithSession(first))
	secondTool := searchTool(t, provider, agent.WithSession(second))
	for _, call := range []struct {
		search tool.FuncTool
		state  *memory.State
	}{{firstTool, firstState}, {secondTool, secondState}, {firstTool, firstState}} {
		wantScope = call.state.StorageScope
		if _, err := call.search.Call(t.Context(), `{"userQuestion":"query"}`); err != nil {
			t.Fatal(err)
		}
	}

	firstState.StorageScope.UserID = new("edited")
	wantScope = firstState.StorageScope
	if _, err := firstTool.Call(t.Context(), `{"userQuestion":"query"}`); err != nil {
		t.Fatal(err)
	}
	baseline := functool.MustNew(functool.Config{Name: "baseline"}, func(context.Context, struct{}) (string, error) { return "", nil })
	options := []agent.Option{agent.WithSession(first), agent.WithTool(baseline)}
	for range 2 {
		_, returned, err := invokeProvider(provider, t.Context(), nil, options...)
		tools := slices.Collect(agent.AllOptions(returned, agent.WithTool))
		if err != nil || len(tools) != 2 || tools[0] != baseline || tools[1].Name() != "Search" || len(options) != 2 {
			t.Fatalf("tools = %v, options = %v, error = %v", tools, options, err)
		}
	}
}

func TestChatHistoryMemoryProvider_ConfigCapturesScalarValues(t *testing.T) {
	for _, mode := range []memory.SearchBehavior{memory.BeforeInvoke, memory.OnDemandFunctionCalling} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			name, description, prompt, maximum := "Recall", "Recall memories", "Context", 2
			provider := newProvider(t, &testCollection{
				search: func(_ context.Context, _ any, top int, _ *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					if top != 2 {
						t.Fatalf("MaxResults changed to %d", top)
					}
					return searchResults(record{"Content": "memory"})
				},
			}, nil, memory.Config{
				SearchTime: mode, FunctionToolName: &name, FunctionToolDescription: &description,
				ContextPrompt: &prompt, MaxResults: &maximum,
			})
			name, description, prompt, maximum = "changed", "changed", "changed", 99
			if mode == memory.OnDemandFunctionCalling {
				search := searchTool(t, provider)
				if search.Name() != "Recall" || search.Description() != "Recall memories" {
					t.Fatal("tool metadata changed with caller-owned configuration")
				}
				result, err := search.Call(t.Context(), `{"userQuestion":"query"}`)
				if err != nil || result != "Context\nmemory" {
					t.Fatalf("tool result = (%v, %v)", result, err)
				}
			} else {
				messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("query")})
				if err != nil || len(messages) != 2 || messages[1].String() != "Context\nmemory" {
					t.Fatalf("automatic search = (%v, %v)", messages, err)
				}
			}
		})
	}
}

func TestChatHistoryMemoryProvider_OnDemandErrors(t *testing.T) {
	for _, failure := range []error{errors.New("search failed"), context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			provider := newProvider(t, &testCollection{
				ensure: func(context.Context) error { return failure },
			}, nil, memory.Config{SearchTime: memory.OnDemandFunctionCalling})
			if _, err := searchTool(t, provider).Call(t.Context(), `{"userQuestion":"query"}`); !errors.Is(err, failure) {
				t.Fatalf("tool error = %v, want %v", err, failure)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_InitializationRetries(t *testing.T) {
	for _, storageFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("storage-first=%t", storageFirst), func(t *testing.T) {
			ensureCalls, searchCalls, upsertCalls := 0, 0, 0
			var logs bytes.Buffer
			provider := newProvider(t, &testCollection{
				ensure: func(context.Context) error {
					ensureCalls++
					if ensureCalls == 1 {
						return errors.New("transient creation error")
					}
					return nil
				},
				search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					searchCalls++
					return searchResults()
				},
				upsert: func(context.Context, ...record) error { upsertCalls++; return nil },
			}, nil, memory.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			request := []*message.Message{message.NewText("request")}
			for range 3 {
				if storageFirst {
					storeMessages(t, provider, nil, request, nil)
				} else if _, _, err := invokeProvider(provider, t.Context(), request); err != nil {
					t.Fatal(err)
				}
			}
			if ensureCalls != 2 || searchCalls+upsertCalls != 2 ||
				strings.Count(logs.String(), `"level":"ERROR"`) != 1 {
				t.Fatalf("ensure=%d, search=%d, upsert=%d, logs=%s", ensureCalls, searchCalls, upsertCalls, logs.String())
			}
		})
	}
}

func TestChatHistoryMemoryProvider_ConcurrentInitialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		releaseFirst := sync.OnceFunc(func() { close(release) })
		defer releaseFirst()
		var ensureCalls, initializations atomic.Int32
		provider := newProvider(t, &testCollection{
			ensure: func(context.Context) error {
				if ensureCalls.Add(1) == 1 {
					close(started)
					<-release
				}
				return nil
			},
		}, func(*agent.Session) *memory.State {
			initializations.Add(1)
			return new(memory.State)
		}, memory.Config{})
		session := agenttest.CreateSession()
		options := []agent.Option{agent.WithSession(session)}
		request := []*message.Message{message.NewText("query")}
		results := make(chan error, 17)
		var wg sync.WaitGroup
		wg.Go(func() {
			_, _, err := invokeProvider(provider, t.Context(), request, options...)
			results <- err
		})
		<-started
		for range 8 {
			wg.Go(func() {
				_, _, err := invokeProvider(provider, t.Context(), request, options...)
				results <- err
			})
			wg.Go(func() {
				results <- provider.Invoked(t.Context(), agent.InvokedContext{RequestMessages: request, Options: options})
			})
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		canceled := make(chan error, 1)
		go func() {
			_, _, err := invokeProvider(provider, ctx, request, options...)
			canceled <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting caller cancellation = %v", err)
		}
		if ensureCalls.Load() != 1 {
			t.Fatalf("concurrent collection initializations = %d", ensureCalls.Load())
		}
		releaseFirst()
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		if ensureCalls.Load() != 1 || initializations.Load() != 1 {
			t.Fatalf("collection initializations=%d, state initializations=%d", ensureCalls.Load(), initializations.Load())
		}
	})
}

func TestChatHistoryMemoryProvider_Close(t *testing.T) {
	wantErr := errors.New("close failed")
	alreadyClosedErr := errors.New("collection already closed")
	closeCalls := 0
	collection := &testCollection{close: func() error {
		closeCalls++
		switch closeCalls {
		case 1:
			return wantErr
		case 2:
			return nil
		default:
			return alreadyClosedErr
		}
	}}
	store := &testStore{collection: collection}
	provider, err := memory.NewHistoryProvider(vectordata.NewStore(store), "history", 2,
		func(*agent.Session) *memory.State { return new(memory.State) }, memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(); err != wantErr {
		t.Fatalf("first Close = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(); err != alreadyClosedErr {
		t.Fatalf("repeated Close = %v", err)
	}
	if closeCalls != 3 || store.closeCalls != 0 {
		t.Fatalf("collection closes = %d, borrowed store closes = %d", closeCalls, store.closeCalls)
	}
	withoutCloser := struct {
		vectordata.Collection[any, any]
	}{collection}
	provider = newProvider(t, withoutCloser, nil, memory.Config{})
	if err := provider.Close(); err != nil || closeCalls != 3 {
		t.Fatalf("collection without io.Closer: error=%v, closes=%d", err, closeCalls)
	}
}

func waitForMemoryCleanup(t *testing.T, done <-chan struct{}) {
	t.Helper()
	for {
		runtime.GC()
		select {
		case <-done:
			return
		case <-t.Context().Done():
			t.Fatal("collection cleanup did not finish")
		default:
			runtime.Gosched()
		}
	}
}

func TestChatHistoryMemoryProvider_RuntimeCleanup(t *testing.T) {
	closed := make(chan struct{})
	var calls atomic.Int32
	store := &testStore{collection: &testCollection{close: func() error {
		calls.Add(1)
		close(closed)
		return nil
	}}}
	func() {
		_, err := memory.NewHistoryProvider(vectordata.NewStore(store), "history", 2,
			func(*agent.Session) *memory.State { return new(memory.State) }, memory.Config{})
		if err != nil {
			t.Fatal(err)
		}
	}()
	waitForMemoryCleanup(t, closed)
	if calls.Load() != 1 || store.closeCalls != 0 {
		t.Fatalf("collection closes=%d, borrowed store closes=%d", calls.Load(), store.closeCalls)
	}
}

func TestChatHistoryMemoryProvider_CloseCancelsRuntimeCleanup(t *testing.T) {
	released := make(chan struct{})
	var calls atomic.Int32
	func() {
		collection := &testCollection{close: func() error {
			calls.Add(1)
			return nil
		}}
		runtime.AddCleanup(collection, func(done chan struct{}) { close(done) }, released)
		provider := newProvider(t, collection, nil, memory.Config{})
		for range 2 {
			if err := provider.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}()
	waitForMemoryCleanup(t, released)
	if calls.Load() != 2 {
		t.Fatalf("two explicit closes and runtime cleanup closed the collection %d times", calls.Load())
	}
}

func TestChatHistoryMemoryProvider_FailedCloseRetainsRuntimeCleanup(t *testing.T) {
	closed := make(chan struct{})
	var calls atomic.Int32
	wantErr := errors.New("close failed")
	func() {
		provider := newProvider(t, &testCollection{close: func() error {
			if calls.Add(1) == 1 {
				return wantErr
			}
			close(closed)
			return nil
		}}, nil, memory.Config{})
		if err := provider.Close(); err != wantErr {
			t.Fatalf("Close error = %v", err)
		}
	}()
	waitForMemoryCleanup(t, closed)
	if calls.Load() != 2 {
		t.Fatalf("failed Close was not retried by runtime cleanup: %d calls", calls.Load())
	}
}

func TestChatHistoryMemoryProvider_RuntimeCleanupLogsFailure(t *testing.T) {
	for _, nilLogger := range []bool{false, true} {
		t.Run(fmt.Sprintf("nil_logger=%t", nilLogger), func(t *testing.T) {
			reader, writer := io.Pipe()
			t.Cleanup(func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
				if err := writer.Close(); err != nil {
					t.Error(err)
				}
			})
			var logs bytes.Buffer
			readDone := make(chan error, 1)
			go func() {
				_, err := logs.ReadFrom(reader)
				readDone <- err
			}()
			logger := slog.New(slog.NewJSONHandler(writer, nil))
			config := memory.Config{Logger: logger}
			if nilLogger {
				previous := slog.Default()
				slog.SetDefault(logger)
				t.Cleanup(func() { slog.SetDefault(previous) })
				config.Logger = nil
			}
			wantErr := errors.New("close failed")
			released := make(chan struct{})
			var calls atomic.Int32
			func() {
				collection := &testCollection{close: func() error {
					calls.Add(1)
					return wantErr
				}}
				runtime.AddCleanup(collection, func(done chan struct{}) { close(done) }, released)
				newProvider(t, collection, nil, config)
			}()
			waitForMemoryCleanup(t, released)
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-readDone; err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("runtime cleanup closed the collection %d times", calls.Load())
			}
			if nilLogger {
				if logs.Len() != 0 {
					t.Fatalf("nil Logger wrote to the default logger: %s", logs.String())
				}
				return
			}
			var entry struct {
				Message string `json:"msg"`
				Level   string `json:"level"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Message != "chat history collection cleanup failed" || entry.Level != "ERROR" || entry.Error != wantErr.Error() {
				t.Fatalf("cleanup diagnostic = %+v", entry)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_RuntimeCleanupWaitsForEscapedTool(t *testing.T) {
	closed := make(chan struct{})
	searches := 0
	func() {
		provider := newProvider(t, &testCollection{
			search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
				searches++
				return searchResults(record{"Content": "memory"})
			},
			close: func() error {
				close(closed)
				return nil
			},
		}, nil, memory.Config{SearchTime: memory.OnDemandFunctionCalling})
		search := searchTool(t, provider)
		for range 2 {
			runtime.GC()
			select {
			case <-closed:
				t.Fatal("runtime cleanup closed a collection retained by a search tool")
			default:
			}
			if _, err := search.Call(t.Context(), `{"userQuestion":"query"}`); err != nil {
				t.Fatal(err)
			}
		}
	}()
	waitForMemoryCleanup(t, closed)
	if searches != 2 {
		t.Fatalf("tool searched the collection %d times", searches)
	}
}

func TestChatHistoryMemoryProvider_RuntimeCleanupWaitsForOperations(t *testing.T) {
	for _, operation := range []string{"search", "store", "tool"} {
		t.Run(operation, func(t *testing.T) {
			started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			finished := make(chan error, 1)
			collection := &testCollection{
				search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					return func(yield func(vectordata.SearchResult[record], error) bool) {
						close(started)
						<-release
						yield(vectordata.SearchResult[record]{Record: record{"Content": "memory"}}, nil)
					}
				},
				upsert: func(context.Context, ...record) error {
					close(started)
					<-release
					return nil
				},
				close: func() error {
					close(closed)
					return nil
				},
			}
			func() {
				config := memory.Config{}
				if operation == "tool" {
					config.SearchTime = memory.OnDemandFunctionCalling
				}
				provider := newProvider(t, collection, nil, config)
				switch operation {
				case "store":
					go func() {
						finished <- provider.Invoked(t.Context(), agent.InvokedContext{
							RequestMessages: []*message.Message{message.NewText("stored text")},
						})
					}()
				case "search":
					go func() {
						_, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("query")})
						finished <- err
					}()
				case "tool":
					search := searchTool(t, provider)
					go func() {
						_, err := search.Call(t.Context(), `{"userQuestion":"query"}`)
						finished <- err
					}()
				}
			}()
			<-started
			runtime.GC()
			select {
			case <-closed:
				t.Error("runtime cleanup closed an active collection")
			default:
			}
			close(release)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			waitForMemoryCleanup(t, closed)
		})
	}
}

func TestChatHistoryMemoryProvider_LoggingRedaction(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		for _, custom := range []bool{false, true} {
			for _, operation := range []string{"search", "empty search", "search failure", "storage", "storage failure"} {
				t.Run(fmt.Sprintf("sensitive=%t/custom=%t/%s", sensitive, custom, operation), func(t *testing.T) {
					var logs bytes.Buffer
					config := memory.Config{
						Logger:           slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
						LogSensitiveData: sensitive,
					}
					redactions := 0
					if custom {
						config.Redactor = func(string) string { redactions++; return "[masked]" }
					}
					collection := &testCollection{
						search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
							if operation == "empty search" {
								return searchResults()
							}
							return searchResults(record{"Content": "private memory"})
						},
					}
					if strings.HasSuffix(operation, "failure") {
						collection.ensure = func(context.Context) error { return errors.New("backend failed") }
					}
					provider := newProvider(t, collection, func(*agent.Session) *memory.State {
						return &memory.State{StorageScope: memory.Scope{
							ApplicationID: new("app"), AgentID: new("agent"), SessionID: new("session"), UserID: new("private user"),
						}}
					}, config)
					request := []*message.Message{message.NewText("private query")}
					if strings.HasPrefix(operation, "storage") {
						storeMessages(t, provider, nil, request, nil)
					} else if _, _, err := invokeProvider(provider, t.Context(), request); err != nil {
						t.Fatal(err)
					}
					wantCount := 1
					switch operation {
					case "storage":
						wantCount = 0
					case "search":
						wantCount = 2
					}
					var entries []string
					if logs.Len() != 0 {
						entries = strings.Split(strings.TrimSpace(logs.String()), "\n")
					}
					if len(entries) != wantCount {
						t.Fatalf("log entries = %d, want %d: %s", len(entries), wantCount, logs.String())
					}
					redacted := "<redacted>"
					if custom {
						redacted = "[masked]"
					}
					for _, entry := range entries {
						var attributes map[string]any
						if err := json.Unmarshal([]byte(entry), &attributes); err != nil {
							t.Fatal(err)
						}
						wantUser := redacted
						if sensitive {
							wantUser = "private user"
						}
						if attributes["application_id"] != "app" || attributes["agent_id"] != "agent" ||
							attributes["session_id"] != "session" || attributes["user_id"] != wantUser {
							t.Fatalf("logged scope = %v", attributes)
						}
						if attributes["level"] == "DEBUG" {
							wantQuery, wantContext := redacted, redacted
							if sensitive {
								wantQuery, wantContext = "private query", contextPrompt+"\nprivate memory"
							}
							if attributes["query"] != wantQuery || attributes["context"] != wantContext {
								t.Fatalf("logged search data = %v", attributes)
							}
						}
						if attributes["level"] == "INFO" {
							wantResults := float64(1)
							if operation == "empty search" {
								wantResults = 0
							}
							if attributes["count"] != wantResults {
								t.Fatalf("logged result count = %v", attributes["count"])
							}
						}
					}
					if sensitive && redactions != 0 || !sensitive && custom && wantCount > 0 && redactions == 0 {
						t.Fatalf("redactor calls = %d", redactions)
					}
				})
			}
		}
	}
}

func TestChatHistoryMemoryProvider_DisabledLogging(t *testing.T) {
	for _, withLogger := range []bool{false, true} {
		t.Run(fmt.Sprintf("logger=%t", withLogger), func(t *testing.T) {
			var logs bytes.Buffer
			config := memory.Config{Redactor: func(string) string {
				t.Fatal("disabled log invoked the redactor")
				return ""
			}}
			if withLogger {
				config.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.Level(100)}))
			}
			provider := newProvider(t, &testCollection{
				search: func(context.Context, any, int, *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					return searchResults(record{"Content": "memory"})
				},
				upsert: func(context.Context, ...record) error { return errors.New("storage failed") },
			}, func(*agent.Session) *memory.State {
				return &memory.State{StorageScope: memory.Scope{UserID: new("private user")}}
			}, config)
			request := []*message.Message{message.NewText("query")}
			if _, _, err := invokeProvider(provider, t.Context(), request); err != nil {
				t.Fatal(err)
			}
			storeMessages(t, provider, nil, request, nil)
			if logs.Len() != 0 {
				t.Fatalf("disabled logging produced %s", logs.String())
			}
		})
	}
}

func TestChatHistoryMemoryProvider_NilStoredMessage(t *testing.T) {
	var logs bytes.Buffer
	provider := newProvider(t, &testCollection{
		upsert: func(context.Context, ...record) error {
			t.Fatal("invalid message batch was partially stored")
			return nil
		},
	}, nil, memory.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	storeMessages(t, provider, nil, []*message.Message{message.NewText("valid"), nil}, nil)
	if !strings.Contains(logs.String(), "cannot store a nil message") {
		t.Fatalf("invalid message was not logged: %s", logs.String())
	}
}

type textGenerator struct {
	calls [][]string
}

func (g *textGenerator) Generate(_ context.Context, inputs []string, _ *embedding.Options) (*embedding.Generated[*embedding.Of[float32]], error) {
	g.calls = append(g.calls, slices.Clone(inputs))
	result := &embedding.Generated[*embedding.Of[float32]]{}
	for _, input := range inputs {
		vector := embedding.VectorOf[float32]{1, 0}
		if input == "beta" {
			vector = embedding.VectorOf[float32]{0, 1}
		}
		result.Embeddings = append(result.Embeddings, &embedding.Of[float32]{Vector: vector})
	}
	return result, nil
}

func TestChatHistoryMemoryProvider_InMemoryStore(t *testing.T) {
	generator := new(textGenerator)
	store := inmemory.New(inmemory.Config{EmbeddingGenerator: embedding.Bind(generator)})
	provider, err := memory.NewHistoryProvider(store.Store, "history", 2,
		func(*agent.Session) *memory.State { return new(memory.State) }, memory.Config{MaxResults: new(1)})
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		user    string
		session string
		text    string
	}{{"user-a", "one", "alpha"}, {"user-a", "two", "beta"}, {"user-b", "one", "alpha"}} {
		session := agenttest.CreateSession()
		session.Set(stateKey, &memory.State{StorageScope: memory.Scope{UserID: new(seed.user), SessionID: new(seed.session)}})
		storeMessages(t, provider, session, []*message.Message{message.NewText(seed.text)}, nil)
	}
	reader := agenttest.CreateSession()
	state := &memory.State{
		StorageScope: memory.Scope{UserID: new("user-a"), SessionID: new("one")},
		SearchScope:  &memory.Scope{UserID: new("user-a")},
	}
	reader.Set(stateKey, state)
	for _, query := range []string{"alpha", "beta"} {
		messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText(query)}, agent.WithSession(reader))
		if err != nil || len(messages) != 2 || messages[1].String() != contextPrompt+"\n"+query {
			t.Fatalf("cross-session scoped search %q = (%v, %v)", query, messages, err)
		}
	}
	state.SearchScope = nil
	messages, _, err := invokeProvider(provider, t.Context(), []*message.Message{message.NewText("beta")}, agent.WithSession(reader))
	if err != nil || len(messages) != 2 || messages[1].String() != contextPrompt+"\nalpha" {
		t.Fatalf("default storage-scoped search = (%v, %v)", messages, err)
	}
	if want := [][]string{{"alpha"}, {"beta"}, {"alpha"}, {"alpha"}, {"beta"}, {"beta"}}; !reflect.DeepEqual(generator.calls, want) {
		t.Fatalf("embedding inputs = %v, want %v", generator.calls, want)
	}
}

func TestChatHistoryMemoryProvider_AgentLifecycle(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var queries []string
			var batches [][]string
			provider := newProvider(t, &testCollection{
				search: func(_ context.Context, query any, _ int, _ *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					queries = append(queries, query.(string))
					return searchResults(record{"Content": "retrieved memory"})
				},
				upsert: func(_ context.Context, records ...record) error {
					var texts []string
					for _, r := range records {
						texts = append(texts, r["Content"].(string))
					}
					batches = append(batches, texts)
					return nil
				},
			}, nil, memory.Config{})
			assertContext := func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
				last := messages[len(messages)-1]
				if last.Source.ID != stateKey || last.String() != contextPrompt+"\nretrieved memory" {
					t.Fatalf("agent did not receive memory context: %#v", last)
				}
			}
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(assertContext).
				AddText("reply ").AddText("A").NewTurn(assertContext).AddText("reply ").AddText("B").Build()}
			a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ContextProviders: []agent.ContextProvider{provider}})
			session, err := a.CreateSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, question := range []string{"A", "B"} {
				response, err := a.RunText(t.Context(), question, agent.WithSession(session), agent.Stream(stream)).Collect()
				if err != nil || response.String() != "reply "+question {
					t.Fatalf("agent response = (%v, %v)", response, err)
				}
			}
			if !slices.Equal(queries, []string{"A", "B"}) ||
				!reflect.DeepEqual(batches, [][]string{{"A", "reply A"}, {"B", "reply B"}}) {
				t.Fatalf("queries=%q, stored=%q; history or retrieved context was duplicated", queries, batches)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_AgentOnDemandToolExecution(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			searchCalls := 0
			var stored []record
			provider := newProvider(t, &testCollection{
				search: func(_ context.Context, query any, _ int, options *vectordata.SearchOptions) iter.Seq2[vectordata.SearchResult[record], error] {
					searchCalls++
					if query != "model query" {
						t.Fatalf("query = %v", query)
					}
					assertScopeFilter(t, options.Filter, memory.Scope{UserID: new("user")})
					return searchResults(record{"Content": "retrieved"})
				},
				upsert: func(_ context.Context, records ...record) error { stored = records; return nil },
			}, func(*agent.Session) *memory.State {
				return &memory.State{StorageScope: memory.Scope{UserID: new("user")}}
			}, memory.Config{SearchTime: memory.OnDemandFunctionCalling})
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().
				AddFunctionCall("call", "Search", `{"userQuestion":"model query"}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					result, ok := messages[len(messages)-1].Contents[0].(*message.FunctionResultContent)
					if !ok || result.CallID != "call" || result.Result != contextPrompt+"\nretrieved" {
						t.Fatalf("tool result = %#v", result)
					}
				}).AddText("answer").Build()}
			a := agent.New(agent.ProviderConfig{
				Run: runner.Run, Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{ContextProviders: []agent.ContextProvider{provider}})
			response, err := a.RunText(t.Context(), "question", agent.Stream(stream)).Collect()
			if err != nil || response.String() != "answer" || searchCalls != 1 {
				t.Fatalf("run = (%v, %v), search calls = %d", response, err, searchCalls)
			}
			var contents, roles []string
			for _, r := range stored {
				contents, roles = append(contents, r["Content"].(string)), append(roles, r["Role"].(string))
			}
			if !slices.Equal(contents, []string{"question", "", "", "answer"}) ||
				!slices.Equal(roles, []string{"user", "assistant", "tool", "assistant"}) {
				t.Fatalf("stored tool conversation: contents=%q, roles=%q", contents, roles)
			}
		})
	}
}

func TestChatHistoryMemoryProvider_UnfinishedRunsDoNotStore(t *testing.T) {
	for _, earlyStop := range []bool{false, true} {
		t.Run(fmt.Sprintf("early-stop=%t", earlyStop), func(t *testing.T) {
			provider := newProvider(t, &testCollection{upsert: func(context.Context, ...record) error {
				t.Fatal("unfinished run stored messages")
				return nil
			}}, nil, memory.Config{})
			released := false
			wantErr := errors.New("model failed")
			a := agent.New(agent.ProviderConfig{
				Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					return func(yield func(*agent.ResponseUpdate, error) bool) {
						defer func() { released = true }()
						if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "partial"}}}, nil) {
							return
						}
						yield(nil, wantErr)
					}
				},
			}, agent.Config{ContextProviders: []agent.ContextProvider{provider}})
			if earlyStop {
				for _, err := range a.RunText(t.Context(), "query", agent.Stream(true)) {
					if err != nil {
						t.Fatal(err)
					}
					break
				}
			} else if _, err := a.RunText(t.Context(), "query", agent.Stream(true)).Collect(); !errors.Is(err, wantErr) {
				t.Fatalf("run error = %v", err)
			}
			if !released {
				t.Fatal("underlying run was not released")
			}
		})
	}
}

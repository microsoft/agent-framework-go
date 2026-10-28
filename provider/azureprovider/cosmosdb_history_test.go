// Copyright (c) Microsoft. All rights reserved.

package azureprovider_test

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/provider/azureprovider"
)

type cosmosHistoryDocument struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversationId"`
	Timestamp      int64           `json:"timestamp"`
	MessageID      string          `json:"messageId,omitempty"`
	Role           message.Role    `json:"role"`
	Message        string          `json:"message"`
	Type           string          `json:"type"`
	TTL            json.RawMessage `json:"ttl,omitempty"`
	TenantID       *string         `json:"tenantId,omitempty"`
	UserID         *string         `json:"userId,omitempty"`
	SessionID      string          `json:"sessionId"`
}

type cosmosHistoryOperation struct {
	OperationType string                `json:"operationType"`
	ID            string                `json:"id"`
	ResourceBody  cosmosHistoryDocument `json:"resourceBody"`
}

func newCosmosHistoryClient(t *testing.T, handler http.HandlerFunc) *azcosmos.Client {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			writeCosmosHistoryJSON(t, w, map[string]any{
				"id": "mock", "enableMultipleWriteLocations": false,
				"readableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
				"writableLocations": []any{map[string]any{"name": "local", "databaseAccountEndpoint": server.URL + "/"}},
			})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	credential, err := azcosmos.NewKeyCredential(base64.StdEncoding.EncodeToString([]byte("local-test-key")))
	if err != nil {
		t.Fatal(err)
	}
	client, err := azcosmos.NewClientWithKey(server.URL, credential, &azcosmos.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: server.Client(), InsecureAllowCredentialWithHTTP: true,
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func newCosmosHistoryProvider(t *testing.T, client *azcosmos.Client, state *azureprovider.CosmosHistoryState, config azureprovider.CosmosHistoryProviderConfig) *azureprovider.CosmosHistoryProvider {
	t.Helper()
	p, err := azureprovider.NewCosmosHistoryProvider(client, "sample", "history",
		func(*agent.Session) *azureprovider.CosmosHistoryState { return state }, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func writeCosmosHistoryJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode Cosmos response: %v", err)
	}
}

func readCosmosHistoryOperations(t *testing.T, r *http.Request) []cosmosHistoryOperation {
	t.Helper()
	if r.Header.Get("x-ms-cosmos-is-batch-request") != "True" ||
		r.Header.Get("x-ms-cosmos-batch-atomic") != "True" {
		t.Errorf("not a transactional batch: %v", r.Header)
	}
	var operations []cosmosHistoryOperation
	if err := json.NewDecoder(r.Body).Decode(&operations); err != nil {
		t.Errorf("decode batch: %v", err)
	}
	return operations
}

func writeCosmosHistoryBatch(t *testing.T, w http.ResponseWriter, count int, status int32) {
	t.Helper()
	results := make([]struct {
		StatusCode int32 `json:"statusCode"`
	}, count)
	for i := range results {
		results[i].StatusCode = status
	}
	writeCosmosHistoryJSON(t, w, results)
}

func cosmosHistoryFixture(t *testing.T, text string, timestamp int64) cosmosHistoryDocument {
	t.Helper()
	msg := message.NewText(text)
	msg.ID = text
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return cosmosHistoryDocument{
		ID: text, ConversationID: "conversation", Timestamp: timestamp,
		MessageID: text, Role: message.RoleUser, Message: string(data), Type: "Message",
	}
}

type cosmosHistoryProtocol struct {
	client        *azcosmos.Client
	mu            sync.Mutex
	documents     map[string][]cosmosHistoryDocument
	continuations map[string][]any
	nextToken     int
	queries       int
}

func newCosmosHistoryProtocol(t *testing.T) *cosmosHistoryProtocol {
	t.Helper()
	protocol := &cosmosHistoryProtocol{
		documents:     make(map[string][]cosmosHistoryDocument),
		continuations: make(map[string][]any),
	}
	protocol.client = newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/docs") {
			t.Errorf("unexpected Cosmos request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		partition := r.Header.Get("x-ms-documentdb-partitionkey")
		if partition == "" {
			t.Error("missing partition key")
			http.Error(w, "missing partition key", http.StatusBadRequest)
			return
		}
		key := r.URL.Path + "\n" + partition
		protocol.mu.Lock()
		defer protocol.mu.Unlock()
		if strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
			protocol.queries++
			var query cosmosTestQuery
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var conversation string
			if err := json.Unmarshal([]byte(query.parameter("@conversationId")), &conversation); err != nil {
				t.Error(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if query.parameter("@type") != `"Message"` {
				t.Errorf("message discriminator = %s", query.parameter("@type"))
				http.Error(w, "invalid discriminator", http.StatusBadRequest)
				return
			}
			const predicate = " FROM c WHERE c.conversationId = @conversationId AND c.type = @type"
			switch query.Query {
			case "SELECT *" + predicate + " ORDER BY c.timestamp ASC",
				"SELECT *" + predicate + " ORDER BY c.timestamp DESC",
				"SELECT VALUE COUNT(1)" + predicate, "SELECT VALUE c.id" + predicate:
			default:
				t.Errorf("unexpected Cosmos query: %q", query.Query)
				http.Error(w, "unexpected query", http.StatusBadRequest)
				return
			}
			var items []any
			if token := r.Header.Get("x-ms-continuation"); token != "" {
				var ok bool
				items, ok = protocol.continuations[token]
				if !ok {
					t.Errorf("unknown continuation token %q", token)
					http.Error(w, "unknown continuation", http.StatusBadRequest)
					return
				}
				delete(protocol.continuations, token)
			} else {
				var documents []cosmosHistoryDocument
				for _, document := range protocol.documents[key] {
					if document.ConversationID == conversation && document.Type == "Message" {
						documents = append(documents, document)
					}
				}
				// Equal timestamps need not preserve insertion order.
				slices.Reverse(documents)
				slices.SortStableFunc(documents, func(a, b cosmosHistoryDocument) int {
					return cmp.Compare(a.Timestamp, b.Timestamp)
				})
				if strings.HasSuffix(query.Query, " DESC") {
					slices.Reverse(documents)
				}
				if strings.Contains(query.Query, "COUNT(1)") {
					items = []any{len(documents)}
				} else {
					for _, document := range documents {
						if strings.HasPrefix(query.Query, "SELECT VALUE c.id") {
							items = append(items, document.ID)
						} else {
							items = append(items, document)
						}
					}
				}
			}
			if hint := r.Header.Get("x-ms-max-item-count"); hint != "" {
				size, err := strconv.Atoi(hint)
				if err != nil {
					t.Error(err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if size > 0 && len(items) > size {
					protocol.nextToken++
					token := strconv.Itoa(protocol.nextToken)
					protocol.continuations[token] = slices.Clone(items[size:])
					w.Header().Set("x-ms-continuation", token)
					items = items[:size]
				}
			}
			writeCosmosHistoryJSON(t, w, map[string]any{"Documents": items})
			return
		}
		if r.Header.Get("x-ms-cosmos-is-batch-request") != "" {
			operations := readCosmosHistoryOperations(t, r)
			for _, operation := range operations {
				switch operation.OperationType {
				case "Create":
					protocol.documents[key] = append(protocol.documents[key], operation.ResourceBody)
				case "Delete":
					protocol.documents[key] = slices.DeleteFunc(protocol.documents[key], func(document cosmosHistoryDocument) bool {
						return document.ID == operation.ID
					})
				default:
					t.Errorf("unexpected batch operation %q", operation.OperationType)
					http.Error(w, "unexpected operation", http.StatusBadRequest)
					return
				}
			}
			writeCosmosHistoryBatch(t, w, len(operations), http.StatusOK)
			return
		}
		var document cosmosHistoryDocument
		if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		protocol.documents[key] = append(protocol.documents[key], document)
		w.WriteHeader(http.StatusCreated)
		writeCosmosHistoryJSON(t, w, document)
	})
	return protocol
}

func newCosmosHistoryConversation(t *testing.T, config azureprovider.CosmosHistoryProviderConfig) (*azureprovider.CosmosHistoryProvider, *agent.Session, *cosmosHistoryProtocol) {
	t.Helper()
	protocol := newCosmosHistoryProtocol(t)
	provider := newCosmosHistoryProvider(t, protocol.client,
		&azureprovider.CosmosHistoryState{ConversationID: "conversation"}, config)
	return provider, new(agent.Session), protocol
}

func storeCosmosHistoryMessages(t *testing.T, provider *azureprovider.CosmosHistoryProvider, session *agent.Session, requests, responses []*message.Message) {
	t.Helper()
	if err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: requests, ResponseMessages: responses,
		Options: []agent.Option{agent.WithSession(session)},
	}); err != nil {
		t.Fatal(err)
	}
}

func loadCosmosHistoryMessages(t *testing.T, provider *azureprovider.CosmosHistoryProvider, session *agent.Session) []*message.Message {
	t.Helper()
	messages, err := provider.Invoking(t.Context(), agent.InvokingContext{
		Options: []agent.Option{agent.WithSession(session)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

func assertCosmosHistoryMessages(t *testing.T, messages []*message.Message, texts ...string) {
	t.Helper()
	if len(messages) != len(texts) {
		t.Fatalf("messages = %v, want %v", messages, texts)
	}
	for i, text := range texts {
		if messages[i] == nil || messages[i].String() != text {
			t.Errorf("message %d = %v, want %q", i, messages[i], text)
		}
	}
}

func assertCosmosHistoryMessagesUnordered(t *testing.T, messages []*message.Message, texts ...string) {
	t.Helper()
	got := make([]string, len(messages))
	for i, msg := range messages {
		if msg == nil {
			t.Fatalf("message %d is nil", i)
		}
		got[i] = msg.String()
	}
	want := slices.Clone(texts)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v (ignoring order)", got, want)
	}
}

func TestCosmosHistoryProviderConfiguration(t *testing.T) {
	client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("construction contacted Cosmos: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	})
	initializer := func(*agent.Session) *azureprovider.CosmosHistoryState {
		return &azureprovider.CosmosHistoryState{ConversationID: "conversation"}
	}
	for _, tc := range []struct {
		name        string
		client      *azcosmos.Client
		database    string
		container   string
		initializer func(*agent.Session) *azureprovider.CosmosHistoryState
	}{
		{"nil client", nil, "sample", "history", initializer},
		{"blank database", client, " \t", "history", initializer},
		{"empty container", client, "sample", "", initializer},
		{"nil initializer", client, "sample", "history", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := azureprovider.NewCosmosHistoryProvider(tc.client, tc.database, tc.container, tc.initializer, azureprovider.CosmosHistoryProviderConfig{})
			if err == nil || p != nil {
				t.Fatalf("constructor = (%v, %v), want error", p, err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		config azureprovider.CosmosHistoryProviderConfig
	}{
		{"empty state key", azureprovider.CosmosHistoryProviderConfig{StateKey: new("")}},
		{"blank state key", azureprovider.CosmosHistoryProviderConfig{StateKey: new(" \t")}},
		{"zero page size", azureprovider.CosmosHistoryProviderConfig{MaxItemCount: new(int32(0))}},
		{"negative page size", azureprovider.CosmosHistoryProviderConfig{MaxItemCount: new(int32(-2))}},
		{"zero batch size", azureprovider.CosmosHistoryProviderConfig{MaxBatchSize: new(0)}},
		{"negative batch size", azureprovider.CosmosHistoryProviderConfig{MaxBatchSize: new(-1)}},
		{"oversized batch", azureprovider.CosmosHistoryProviderConfig{MaxBatchSize: new(101)}},
		{"negative retrieval limit", azureprovider.CosmosHistoryProviderConfig{MaxMessagesToRetrieve: new(-1)}},
		{"invalid TTL", azureprovider.CosmosHistoryProviderConfig{MessageTTLSeconds: new(int32(-2))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := azureprovider.NewCosmosHistoryProvider(client, "sample", "history", initializer, tc.config)
			if err == nil || p != nil {
				t.Fatalf("constructor = (%v, %v), want error", p, err)
			}
		})
	}
	p := newCosmosHistoryProvider(t, client, initializer(nil), azureprovider.CosmosHistoryProviderConfig{})
	if p.DatabaseID() != "sample" || p.ContainerID() != "history" ||
		!slices.Equal(p.StateKeys(), []string{"CosmosHistoryProvider"}) {
		t.Fatalf("provider identity = %q, %q, %v", p.DatabaseID(), p.ContainerID(), p.StateKeys())
	}
	keys := p.StateKeys()
	keys[0] = "changed"
	if p.StateKeys()[0] != "CosmosHistoryProvider" {
		t.Fatal("StateKeys returned a borrowed slice")
	}
}

func TestCosmosHistoryProviderPartitionRouting(t *testing.T) {
	conversationID := `conversation ' " \ value`
	for _, tc := range []struct {
		name    string
		tenant  *string
		user    *string
		factory func(azureprovider.CosmosHistoryState) azcosmos.PartitionKey
		key     []string
	}{
		{"simple", nil, nil, nil, []string{conversationID}},
		{"hierarchical", new("tenant"), new("user"), nil, []string{"tenant", "user", conversationID}},
		{"explicit empty components", new(""), new(""), nil, []string{"", "", conversationID}},
		{"partial metadata", new("tenant"), nil, nil, []string{conversationID}},
		{"user metadata", nil, new("user"), nil, []string{conversationID}},
		{
			"flat with metadata", new("tenant"), new("user"),
			func(s azureprovider.CosmosHistoryState) azcosmos.PartitionKey {
				return azcosmos.NewPartitionKeyString(s.ConversationID)
			},
			[]string{conversationID},
		},
		{
			"custom hierarchy", new("tenant"), new("user"),
			func(s azureprovider.CosmosHistoryState) azcosmos.PartitionKey {
				return azcosmos.NewPartitionKeyString(*s.UserID).AppendString(s.ConversationID)
			},
			[]string{"user", conversationID},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var partitions []string
			var stored cosmosHistoryDocument
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/dbs/sample/colls/history/docs" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				mu.Lock()
				defer mu.Unlock()
				partitions = append(partitions, r.Header.Get("x-ms-documentdb-partitionkey"))
				if strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
					var query cosmosTestQuery
					if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
						t.Error(err)
					}
					if query.parameter("@conversationId") != strconv.Quote(conversationID) ||
						query.parameter("@type") != `"Message"` ||
						!strings.Contains(query.Query, "c.type = @type") ||
						strings.Contains(query.Query, conversationID) {
						t.Errorf("query is not scoped and parameterized: %+v", query)
					}
					if r.Header.Get("x-ms-documentdb-query-enablecrosspartition") == "true" {
						t.Error("cross-partition querying enabled")
					}
					switch {
					case strings.Contains(query.Query, "COUNT(1)"):
						writeCosmosHistoryJSON(t, w, map[string]any{"Documents": []int{1}})
					case strings.Contains(query.Query, "SELECT VALUE c.id"):
						writeCosmosHistoryJSON(t, w, map[string]any{"Documents": []string{"stored"}})
					default:
						writeCosmosHistoryJSON(t, w, map[string]any{"Documents": []any{}})
					}
					return
				}
				if r.Header.Get("x-ms-cosmos-is-batch-request") != "" {
					operations := readCosmosHistoryOperations(t, r)
					if len(operations) != 1 || operations[0].OperationType != "Delete" || operations[0].ID != "stored" {
						t.Errorf("delete operations = %+v", operations)
					}
					writeCosmosHistoryBatch(t, w, len(operations), http.StatusNoContent)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
					t.Error(err)
				}
				w.WriteHeader(http.StatusCreated)
				writeCosmosHistoryJSON(t, w, stored)
			})
			state := &azureprovider.CosmosHistoryState{
				ConversationID: conversationID, TenantID: tc.tenant, UserID: tc.user,
			}
			p := newCosmosHistoryProvider(t, client, state, azureprovider.CosmosHistoryProviderConfig{
				StateKey:            new("custom-state"),
				PartitionKeyFactory: tc.factory,
			})
			if !slices.Equal(p.StateKeys(), []string{"custom-state"}) {
				t.Fatalf("state keys = %v", p.StateKeys())
			}
			session := new(agent.Session)
			if _, err := p.Messages(t.Context(), session); err != nil {
				t.Fatal(err)
			}
			if err := p.Invoked(t.Context(), agent.InvokedContext{
				RequestMessages: []*message.Message{message.NewText("hello")},
				Options:         []agent.Option{agent.WithSession(session)},
			}); err != nil {
				t.Fatal(err)
			}
			if count, err := p.MessageCount(t.Context(), session); err != nil || count != 1 {
				t.Fatalf("count = (%d, %v)", count, err)
			}
			if count, err := p.ClearMessages(t.Context(), session); err != nil || count != 1 {
				t.Fatalf("clear = (%d, %v)", count, err)
			}
			mu.Lock()
			defer mu.Unlock()
			wantKey, err := json.Marshal(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			if len(partitions) != 5 {
				t.Fatalf("partition-scoped requests = %v, want 5", partitions)
			}
			for _, key := range partitions {
				if key != string(wantKey) {
					t.Errorf("partition = %s, want %s", key, wantKey)
				}
			}
			if stored.ConversationID != conversationID || stored.SessionID != conversationID ||
				!reflect.DeepEqual(stored.TenantID, tc.tenant) || !reflect.DeepEqual(stored.UserID, tc.user) {
				t.Fatalf("routing metadata = %+v", stored)
			}
		})
	}
}

func TestCosmosHistoryProviderSingleMessageRoundTripAndTTL(t *testing.T) {
	for _, tc := range []struct {
		name string
		ttl  *int32
		want string
	}{
		{"default", nil, "86400"},
		{"container default", new(int32(0)), ""},
		{"never expires", new(int32(-1)), "-1"},
		{"custom", new(int32(60)), "60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var stored cosmosHistoryDocument
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
					writeCosmosHistoryJSON(t, w, map[string]any{"Documents": []cosmosHistoryDocument{stored}})
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
					t.Error(err)
				}
				w.WriteHeader(http.StatusCreated)
				writeCosmosHistoryJSON(t, w, stored)
			})
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{MessageTTLSeconds: tc.ttl})
			msg := message.New(
				&message.TextContent{Text: "hello"},
				&message.FunctionCallContent{CallID: "call", Name: "lookup", Arguments: `{"id":1}`},
				&message.FunctionResultContent{CallID: "call", Result: map[string]any{"ok": true}},
				&message.DataContent{Data: "aGVsbG8=", MediaType: "image/png"},
			)
			msg.ID, msg.Role, msg.AuthorName = "original-message", message.RoleAssistant, "author"
			msg.CreatedAt = time.Date(2026, 9, 1, 12, 0, 0, 123, time.UTC)
			msg.Source = message.Source{Type: "context-provider", ID: "context"}
			msg.AdditionalProperties = map[string]any{"number": float64(3), "flag": true}
			msg.RawRepresentation = "not serialized"
			before := time.Now().Unix()
			if err := p.Invoked(t.Context(), agent.InvokedContext{ResponseMessages: []*message.Message{msg}}); err != nil {
				t.Fatal(err)
			}
			got, err := p.Messages(t.Context(), nil)
			if err != nil || len(got) != 1 {
				t.Fatalf("messages = (%v, %v)", got, err)
			}
			wantJSON, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, err := json.Marshal(got[0])
			if err != nil {
				t.Fatal(err)
			}
			if string(gotJSON) != string(wantJSON) || got[0].RawRepresentation != nil {
				t.Fatalf("message round trip = %s, want %s", gotJSON, wantJSON)
			}
			mu.Lock()
			defer mu.Unlock()
			if _, err := uuid.Parse(stored.ID); err != nil || stored.ID == msg.ID ||
				stored.MessageID != msg.ID || stored.Role != msg.Role || stored.Type != "Message" ||
				stored.Timestamp < before || stored.Timestamp > time.Now().Unix() || string(stored.TTL) != tc.want {
				t.Fatalf("stored document = %+v, want ttl %q", stored, tc.want)
			}
		})
	}
}

func TestCosmosHistoryProviderRetrievalPagingAndLimits(t *testing.T) {
	oldest, middle, newest := cosmosHistoryFixture(t, "oldest", 1), cosmosHistoryFixture(t, "middle", 2), cosmosHistoryFixture(t, "newest", 3)
	extra := cosmosHistoryFixture(t, "extra", 0)
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("limited=%t", limited), func(t *testing.T) {
			var mu sync.Mutex
			var tokens []string
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				var query cosmosTestQuery
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
				}
				direction := "ASC"
				if limited {
					direction = "DESC"
				}
				if !strings.HasSuffix(query.Query, "ORDER BY c.timestamp "+direction) ||
					r.Header.Get("x-ms-max-item-count") != "2" {
					t.Errorf("query = %q, headers = %v", query.Query, r.Header)
				}
				token := r.Header.Get("x-ms-continuation")
				mu.Lock()
				tokens = append(tokens, token)
				mu.Unlock()
				var documents []cosmosHistoryDocument
				switch token {
				case "":
					w.Header().Set("x-ms-continuation", "second")
				case "second":
					first := oldest
					if limited {
						first = newest
					}
					documents = []cosmosHistoryDocument{first, {}, {Message: "null"}}
					w.Header().Set("x-ms-continuation", "third")
				case "third":
					last := newest
					if limited {
						last = oldest
						w.Header().Set("x-ms-continuation", "unused")
					}
					documents = []cosmosHistoryDocument{middle, last}
					if limited {
						documents = append(documents, extra)
					}
				default:
					t.Errorf("retrieval did not stop at limit: token %q", token)
				}
				writeCosmosHistoryJSON(t, w, map[string]any{"Documents": documents})
			})
			config := azureprovider.CosmosHistoryProviderConfig{MaxItemCount: new(int32(2))}
			if limited {
				config.MaxMessagesToRetrieve = new(3)
			}
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"}, config)
			got, err := p.Messages(t.Context(), nil)
			if err != nil || len(got) != 3 {
				t.Fatalf("messages = (%v, %v)", got, err)
			}
			for i, want := range []string{"oldest", "middle", "newest"} {
				if got[i].String() != want {
					t.Errorf("message %d = %q, want %q", i, got[i], want)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(tokens, []string{"", "second", "third"}) {
				t.Errorf("page tokens = %v", tokens)
			}
		})
	}
	t.Run("zero", func(t *testing.T) {
		client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("zero retrieval limit contacted Cosmos")
			http.Error(w, "unexpected request", http.StatusBadRequest)
		})
		p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
			azureprovider.CosmosHistoryProviderConfig{MaxMessagesToRetrieve: new(0)})
		if messages, err := p.Messages(t.Context(), nil); err != nil || messages == nil || len(messages) != 0 {
			t.Fatalf("messages = (%v, %v), want empty non-nil slice", messages, err)
		}
	})
}

func TestCosmosHistoryProviderFilters(t *testing.T) {
	for _, customStorage := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom-storage=%t", customStorage), func(t *testing.T) {
			fixtures := []cosmosHistoryDocument{cosmosHistoryFixture(t, "visible", 1), cosmosHistoryFixture(t, "hidden", 2)}
			var mu sync.Mutex
			var stored []string
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
					writeCosmosHistoryJSON(t, w, map[string]any{"Documents": fixtures})
					return
				}
				operations := readCosmosHistoryOperations(t, r)
				mu.Lock()
				for _, operation := range operations {
					var msg message.Message
					if err := json.Unmarshal([]byte(operation.ResourceBody.Message), &msg); err != nil {
						t.Error(err)
					}
					stored = append(stored, msg.String())
				}
				mu.Unlock()
				writeCosmosHistoryBatch(t, w, len(operations), http.StatusCreated)
			})
			config := azureprovider.CosmosHistoryProviderConfig{
				ProvideOutputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
					return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.String() == "hidden" }), nil
				},
			}
			if customStorage {
				config.StoreInputRequestMessageFilter = func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
					return slices.DeleteFunc(messages, func(msg *message.Message) bool {
						return msg.Source.Type != agent.SourceTypeHistoryProvider
					}), nil
				}
				config.StoreInputResponseMessageFilter = func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
					return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.String() == "hidden-response" }), nil
				}
			}
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"}, config)
			raw, err := p.Messages(t.Context(), nil)
			if err != nil || len(raw) != 2 || raw[0].Source.Type != message.SourceTypeExternal {
				t.Fatalf("raw messages = (%v, %v)", raw, err)
			}
			input := message.NewText("input")
			provided, err := p.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{input}})
			if err != nil || len(provided) != 2 || provided[0].String() != "visible" || provided[1] != input ||
				provided[0].Source != (message.Source{ID: "CosmosHistoryProvider", Type: agent.SourceTypeHistoryProvider}) {
				t.Fatalf("invocation messages = (%v, %v)", provided, err)
			}
			if input.Source.Type != message.SourceTypeExternal || raw[0].Source.Type != message.SourceTypeExternal {
				t.Fatal("invocation changed caller or raw messages")
			}
			history := message.NewText("history").WithSource(message.Source{Type: agent.SourceTypeHistoryProvider})
			contextMessage := message.NewText("context").WithSource(message.Source{Type: "context-provider"})
			requests := []*message.Message{history, contextMessage, message.NewText("user")}
			responses := []*message.Message{message.NewText("response"), message.NewText("hidden-response")}
			originalRequests, originalResponses := slices.Clone(requests), slices.Clone(responses)
			if err := p.Invoked(t.Context(), agent.InvokedContext{RequestMessages: requests, ResponseMessages: responses}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(requests, originalRequests) || !slices.Equal(responses, originalResponses) {
				t.Fatal("storage changed caller slices")
			}
			want := []string{"context", "user", "response", "hidden-response"}
			if customStorage {
				want = []string{"history", "response"}
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(stored, want) {
				t.Fatalf("stored = %v, want %v", stored, want)
			}
		})
	}
}

func TestCosmosHistoryProviderStoreBatchSizes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  *int
		count int
		want  []int
	}{
		{"default", nil, 205, []int{100, 100, 5}},
		{"custom", new(3), 8, []int{3, 3, 2}},
		{"minimum", new(1), 2, []int{1, 1}},
		{"middle", new(50), 101, []int{50, 50, 1}},
		{"maximum", new(100), 101, []int{100, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sizes []int
			var documents []cosmosHistoryDocument
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				operations := readCosmosHistoryOperations(t, r)
				mu.Lock()
				sizes = append(sizes, len(operations))
				for _, operation := range operations {
					if operation.OperationType != "Create" {
						t.Errorf("operation = %q", operation.OperationType)
					}
					documents = append(documents, operation.ResourceBody)
				}
				mu.Unlock()
				writeCosmosHistoryBatch(t, w, len(operations), http.StatusCreated)
			})
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{MaxBatchSize: tc.size})
			messages := make([]*message.Message, tc.count)
			for i := range messages {
				messages[i] = message.NewText(strconv.Itoa(i))
				messages[i].ID = strconv.Itoa(i)
			}
			if err := p.Invoked(t.Context(), agent.InvokedContext{
				RequestMessages: messages[:tc.count/2], ResponseMessages: messages[tc.count/2:],
			}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(sizes, tc.want) || len(documents) != tc.count {
				t.Fatalf("batch sizes = %v, documents = %d", sizes, len(documents))
			}
			ids := make(map[string]bool)
			for i, document := range documents {
				if ids[document.ID] || document.MessageID != strconv.Itoa(i) || document.Timestamp != documents[0].Timestamp {
					t.Errorf("document %d = %+v", i, document)
				}
				ids[document.ID] = true
			}
		})
	}
}

func TestCosmosHistoryProviderSplitsOversizedBatches(t *testing.T) {
	for _, operationFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("operation-failure=%t", operationFailure), func(t *testing.T) {
			var mu sync.Mutex
			var sizes []int
			original := make(map[string]cosmosHistoryDocument)
			committed := make(map[string]int)
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				operations := readCosmosHistoryOperations(t, r)
				mu.Lock()
				defer mu.Unlock()
				sizes = append(sizes, len(operations))
				for _, operation := range operations {
					if operation.OperationType != "Create" {
						t.Errorf("operation = %q", operation.OperationType)
					}
					document := operation.ResourceBody
					if old, ok := original[document.MessageID]; ok {
						if !reflect.DeepEqual(old, document) {
							t.Errorf("split recreated document: old %+v, new %+v", old, document)
						}
					} else {
						original[document.MessageID] = document
					}
				}
				if len(operations) > 2 {
					if operationFailure {
						w.WriteHeader(http.StatusMultiStatus)
						_, _ = io.WriteString(w, `[{"statusCode":424},{"statusCode":413},{"statusCode":424},{"statusCode":424}]`)
					} else {
						w.WriteHeader(http.StatusRequestEntityTooLarge)
						_, _ = io.WriteString(w, `{"code":"RequestEntityTooLarge","message":"batch too large"}`)
					}
					return
				}
				for _, operation := range operations {
					committed[operation.ResourceBody.ID]++
				}
				writeCosmosHistoryBatch(t, w, len(operations), http.StatusCreated)
			})
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{})
			messages := make([]*message.Message, 4)
			for i := range messages {
				messages[i] = message.NewText(strconv.Itoa(i))
				messages[i].ID = strconv.Itoa(i)
			}
			if err := p.Invoked(t.Context(), agent.InvokedContext{RequestMessages: messages}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(sizes, []int{4, 2, 2}) || len(original) != len(messages) || len(committed) != len(messages) {
				t.Fatalf("batch sizes = %v, original documents = %d, committed documents = %v", sizes, len(original), committed)
			}
			for messageID, document := range original {
				if count := committed[document.ID]; count != 1 {
					t.Errorf("message %q committed %d times, want 1", messageID, count)
				}
			}
		})
	}
}

func TestCosmosHistoryProviderOversizedSingleFallback(t *testing.T) {
	for _, rejectSingle := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject-single=%t", rejectSingle), func(t *testing.T) {
			var batches, singles atomic.Int32
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-ms-cosmos-is-batch-request") != "" {
					batches.Add(1)
					readCosmosHistoryOperations(t, r)
				} else {
					singles.Add(1)
					if !rejectSingle {
						var document cosmosHistoryDocument
						if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
							t.Error(err)
						}
						w.WriteHeader(http.StatusCreated)
						writeCosmosHistoryJSON(t, w, document)
						return
					}
				}
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = io.WriteString(w, `{"code":"RequestEntityTooLarge","message":"too large"}`)
			})
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{MaxBatchSize: new(1)})
			err := p.Invoked(t.Context(), agent.InvokedContext{
				RequestMessages: []*message.Message{message.NewText("one"), message.NewText("two")},
			})
			if rejectSingle {
				responseErr, ok := errors.AsType[*azcore.ResponseError](err)
				if !ok || responseErr.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(err.Error(), "2 MB") {
					t.Fatalf("oversized message error = %v", err)
				}
				if batches.Load() != 1 || singles.Load() != 1 {
					t.Fatalf("attempts = %d batches, %d singles", batches.Load(), singles.Load())
				}
			} else if err != nil || batches.Load() != 2 || singles.Load() != 2 {
				t.Fatalf("fallback = %v, %d batches, %d singles", err, batches.Load(), singles.Load())
			}
		})
	}
}

func TestCosmosHistoryProviderClearBatchesAndPartialProgress(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%t", fail), func(t *testing.T) {
			var mu sync.Mutex
			var sizes []int
			var deletedIDs []string
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
					if r.Header.Get("x-ms-max-item-count") != "500" {
						t.Errorf("page size = %q", r.Header.Get("x-ms-max-item-count"))
					}
					if r.Header.Get("x-ms-continuation") == "" {
						w.Header().Set("x-ms-continuation", "next")
						_, _ = io.WriteString(w, `{"Documents":[]}`)
						return
					}
					ids := make([]any, 0, 207)
					ids = append(ids, "", nil)
					for i := range 205 {
						ids = append(ids, strconv.Itoa(i))
					}
					writeCosmosHistoryJSON(t, w, map[string]any{"Documents": ids})
					return
				}
				operations := readCosmosHistoryOperations(t, r)
				mu.Lock()
				defer mu.Unlock()
				sizes = append(sizes, len(operations))
				if fail && len(sizes) == 2 {
					w.WriteHeader(http.StatusMultiStatus)
					_, _ = io.WriteString(w, `[{"statusCode":424},{"statusCode":403}]`)
					return
				}
				for _, operation := range operations {
					if operation.OperationType != "Delete" {
						t.Errorf("operation = %+v", operation)
					}
					deletedIDs = append(deletedIDs, operation.ID)
				}
				writeCosmosHistoryBatch(t, w, len(operations), http.StatusNoContent)
			})
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{
					MaxItemCount: new(int32(500)), MaxMessagesToRetrieve: new(0),
					ProvideOutputMessageFilter: func(context.Context, []*message.Message) ([]*message.Message, error) { return nil, nil },
				})
			count, err := p.ClearMessages(t.Context(), nil)
			mu.Lock()
			defer mu.Unlock()
			if fail {
				responseErr, ok := errors.AsType[*azcore.ResponseError](err)
				if !ok || responseErr.StatusCode != http.StatusForbidden || count != 100 ||
					!slices.Equal(sizes, []int{100, 100}) || len(deletedIDs) != 100 {
					t.Fatalf("partial clear = (%d, %v), sizes %v, deleted %d", count, err, sizes, len(deletedIDs))
				}
			} else if err != nil || count != 205 || !slices.Equal(sizes, []int{100, 100, 5}) || len(deletedIDs) != 205 {
				t.Fatalf("clear = (%d, %v), sizes %v, deleted %d", count, err, sizes, len(deletedIDs))
			}
		})
	}
}

func TestCosmosHistoryProviderStatePersistence(t *testing.T) {
	var mu sync.Mutex
	var partitions []string
	client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		partitions = append(partitions, r.Header.Get("x-ms-documentdb-partitionkey"))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"Documents":[]}`)
	})
	var initialized atomic.Int32
	initializer := func(*agent.Session) *azureprovider.CosmosHistoryState {
		initialized.Add(1)
		return &azureprovider.CosmosHistoryState{ConversationID: "original", TenantID: new("tenant"), UserID: new("user")}
	}
	p, err := azureprovider.NewCosmosHistoryProvider(client, "sample", "history", initializer,
		azureprovider.CosmosHistoryProviderConfig{StateKey: new("custom")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	session := new(agent.Session)
	session.Set("custom", (*azureprovider.CosmosHistoryState)(nil))
	for range 2 {
		if messages, err := p.Messages(t.Context(), session); err != nil || len(messages) != 0 {
			t.Fatalf("empty history = (%v, %v)", messages, err)
		}
	}
	if initialized.Load() != 1 {
		t.Fatalf("initializer calls = %d, want 1", initialized.Load())
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	replacement := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "wrong"},
		azureprovider.CosmosHistoryProviderConfig{StateKey: new("custom")})
	if _, err := replacement.Messages(t.Context(), &restored); err != nil {
		t.Fatal(err)
	}
	var state *azureprovider.CosmosHistoryState
	if ok, err := restored.Get("custom", &state); !ok || err != nil || state == nil || state.ConversationID != "original" {
		t.Fatalf("restored state = (%+v, %t, %v)", state, ok, err)
	}
	for range 2 {
		if _, err := p.Messages(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if initialized.Load() != 3 {
		t.Fatalf("nil-session initializer calls = %d, want total 3", initialized.Load())
	}
	other := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "other"},
		azureprovider.CosmosHistoryProviderConfig{StateKey: new("other")})
	if _, err := other.Messages(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, partition := range partitions[:5] {
		if partition != `["tenant","user","original"]` {
			t.Errorf("restored partition = %s", partition)
		}
	}
	if partitions[5] != `["other"]` || session.Count() != 2 {
		t.Fatalf("independent provider state = %v, keys %d", partitions, session.Count())
	}
}

func TestCosmosHistoryProviderConcurrentSessionInitialization(t *testing.T) {
	client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Documents":[]}`)
	})
	var initialized atomic.Int32
	p, err := azureprovider.NewCosmosHistoryProvider(client, "sample", "history",
		func(session *agent.Session) *azureprovider.CosmosHistoryState {
			initialized.Add(1)
			var id string
			if ok, err := session.Get("id", &id); !ok || err != nil {
				t.Errorf("session ID = (%q, %t, %v)", id, ok, err)
			}
			return &azureprovider.CosmosHistoryState{ConversationID: id}
		}, azureprovider.CosmosHistoryProviderConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 8 {
		session := new(agent.Session)
		session.Set("id", strconv.Itoa(i))
		for range 2 {
			wg.Go(func() {
				<-start
				if _, err := p.Messages(t.Context(), session); err != nil {
					t.Error(err)
				}
			})
		}
	}
	close(start)
	wg.Wait()
	if initialized.Load() != 8 {
		t.Fatalf("initializer calls = %d, want one per session", initialized.Load())
	}
}

func TestCosmosHistoryProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		call   func(*azureprovider.CosmosHistoryProvider) error
	}{
		{
			"query failure", `{"code":"Forbidden","message":"denied"}`, http.StatusForbidden,
			func(p *azureprovider.CosmosHistoryProvider) error {
				_, err := p.Messages(t.Context(), nil)
				return err
			},
		},
		{
			"single failure", `{"code":"Forbidden","message":"denied"}`, http.StatusForbidden,
			func(p *azureprovider.CosmosHistoryProvider) error {
				return p.Invoked(t.Context(), agent.InvokedContext{RequestMessages: []*message.Message{message.NewText("one")}})
			},
		},
		{
			"batch failure", `[{"statusCode":424},{"statusCode":400}]`, http.StatusMultiStatus,
			func(p *azureprovider.CosmosHistoryProvider) error {
				return p.Invoked(t.Context(), agent.InvokedContext{RequestMessages: []*message.Message{message.NewText("one"), message.NewText("two")}})
			},
		},
		{
			"invalid document", `{"Documents":[{"message":123}]}`, http.StatusOK,
			func(p *azureprovider.CosmosHistoryProvider) error {
				_, err := p.Messages(t.Context(), nil)
				return err
			},
		},
		{
			"invalid message", `{"Documents":[{"id":"broken","message":"not JSON"}]}`, http.StatusOK,
			func(p *azureprovider.CosmosHistoryProvider) error {
				_, err := p.Messages(t.Context(), nil)
				return err
			},
		},
		{
			"invalid count", `{"Documents":["not a count"]}`, http.StatusOK,
			func(p *azureprovider.CosmosHistoryProvider) error {
				_, err := p.MessageCount(t.Context(), nil)
				return err
			},
		},
		{
			"null count", `{"Documents":[null]}`, http.StatusOK,
			func(p *azureprovider.CosmosHistoryProvider) error {
				_, err := p.MessageCount(t.Context(), nil)
				return err
			},
		},
		{
			"invalid delete ID", `{"Documents":[123]}`, http.StatusOK,
			func(p *azureprovider.CosmosHistoryProvider) error {
				_, err := p.ClearMessages(t.Context(), nil)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{})
			err := tc.call(p)
			if err == nil {
				t.Fatal("operation succeeded, want error")
			}
			if tc.status != http.StatusOK {
				want := tc.status
				if want == http.StatusMultiStatus {
					want = http.StatusBadRequest
				}
				responseErr, ok := errors.AsType[*azcore.ResponseError](err)
				if !ok || responseErr.StatusCode != want {
					t.Fatalf("error = %v, want response status %d", err, want)
				}
			}
		})
	}
}

func TestCosmosHistoryProviderStateAndFilterErrors(t *testing.T) {
	var requests atomic.Int32
	client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("invalid state or message contacted Cosmos")
		http.Error(w, "unexpected request", http.StatusBadRequest)
	})
	for _, state := range []*azureprovider.CosmosHistoryState{nil, {}, {ConversationID: " \t"}} {
		p := newCosmosHistoryProvider(t, client, state, azureprovider.CosmosHistoryProviderConfig{})
		if _, err := p.Messages(t.Context(), nil); err == nil {
			t.Errorf("state %+v was accepted", state)
		}
	}
	p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
		azureprovider.CosmosHistoryProviderConfig{})
	session := new(agent.Session)
	if err := json.Unmarshal([]byte(`{"State":{"CosmosHistoryProvider":{"conversationId":" "}}}`), session); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Messages(t.Context(), session); err == nil {
		t.Fatal("invalid restored conversation ID was ignored")
	}
	bad := message.NewText("unserializable")
	bad.AdditionalProperties = map[string]any{"bad": make(chan int)}
	for _, messages := range [][]*message.Message{{nil}, {message.NewText("valid"), bad}} {
		if err := p.Invoked(t.Context(), agent.InvokedContext{RequestMessages: messages}); err == nil {
			t.Fatal("invalid message was accepted")
		}
	}
	filterErr := errors.New("filter failure")
	for _, responseFilter := range []bool{false, true} {
		config := azureprovider.CosmosHistoryProviderConfig{}
		filter := func(context.Context, []*message.Message) ([]*message.Message, error) { return nil, filterErr }
		if responseFilter {
			config.StoreInputResponseMessageFilter = filter
		} else {
			config.StoreInputRequestMessageFilter = filter
		}
		p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"}, config)
		if err := p.Invoked(t.Context(), agent.InvokedContext{RequestMessages: []*message.Message{message.NewText("hello")}}); !errors.Is(err, filterErr) {
			t.Fatalf("filter error = %v", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid operations made %d requests", requests.Load())
	}
}

func TestCosmosHistoryProviderCancellationCloseAndSkippedStorage(t *testing.T) {
	var requests atomic.Int32
	client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"Documents":[]}`)
	})
	p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
		azureprovider.CosmosHistoryProviderConfig{})
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, call := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := p.Messages(ctx, nil); return err },
		func(ctx context.Context) error { _, err := p.MessageCount(ctx, nil); return err },
		func(ctx context.Context) error { _, err := p.ClearMessages(ctx, nil); return err },
		func(ctx context.Context) error { return p.Invoked(ctx, agent.InvokedContext{}) },
	} {
		if err := call(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	}
	if err := p.Invoked(t.Context(), agent.InvokedContext{Err: errors.New("failed run"), RequestMessages: []*message.Message{message.NewText("not stored")}}); err != nil {
		t.Fatal(err)
	}
	if err := p.Invoked(t.Context(), agent.InvokedContext{}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range []func() error{
		func() error { _, err := p.Messages(t.Context(), nil); return err },
		func() error { _, err := p.MessageCount(t.Context(), nil); return err },
		func() error { _, err := p.ClearMessages(t.Context(), nil); return err },
		func() error { _, err := p.Invoking(t.Context(), agent.InvokingContext{}); return err },
		func() error { return p.Invoked(t.Context(), agent.InvokedContext{}) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("closed provider error = %v", err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("skipped operations made %d requests", requests.Load())
	}
	container, err := client.NewContainer("sample", "history")
	if err != nil {
		t.Fatal(err)
	}
	pager := container.NewQueryItemsPager("SELECT * FROM c", azcosmos.NewPartitionKeyString("conversation"), nil)
	if _, err := pager.NextPage(t.Context()); err != nil || requests.Load() != 1 {
		t.Fatalf("borrowed client after provider close = %v, requests %d", err, requests.Load())
	}
}

func TestCosmosHistoryProviderAgentIntegration(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, perServiceCall := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/per-service-call=%t", stream, perServiceCall), func(t *testing.T) {
				var mu sync.Mutex
				var documents []cosmosHistoryDocument
				client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if strings.Contains(r.Header.Get("Content-Type"), "application/query+json") {
						writeCosmosHistoryJSON(t, w, map[string]any{"Documents": documents})
						return
					}
					operations := readCosmosHistoryOperations(t, r)
					for _, operation := range operations {
						documents = append(documents, operation.ResourceBody)
					}
					writeCosmosHistoryBatch(t, w, len(operations), http.StatusCreated)
				})
				p := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
					azureprovider.CosmosHistoryProviderConfig{})
				calls := 0
				runErr := errors.New("model failed")
				a := agent.New(agent.ProviderConfig{
					Run: func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
						return func(yield func(*agent.ResponseUpdate, error) bool) {
							calls++
							if len(messages) != 2*(calls-1)+1 {
								t.Errorf("call %d has %d messages", calls, len(messages))
							}
							for _, msg := range messages[:len(messages)-1] {
								if msg.Source.Type != agent.SourceTypeHistoryProvider {
									t.Errorf("retained message has source %v", msg.Source)
								}
							}

							if calls == 3 {
								yield(nil, runErr)
								return
							}
							yield(&agent.ResponseUpdate{
								MessageID: fmt.Sprintf("response-%d", calls), Role: message.RoleAssistant,
								Contents: message.Contents{&message.TextContent{Text: "reply"}},
							}, nil)
						}
					},
				}, agent.Config{HistoryProvider: p, RequirePerServiceCallHistoryPersistence: perServiceCall})
				session, err := a.CreateSession(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range []string{"first", "second"} {
					response, err := a.RunText(t.Context(), text, agent.WithSession(session), agent.Stream(stream)).Collect()
					if err != nil || response.String() != "reply" {
						t.Fatalf("run = (%v, %v)", response, err)
					}
				}
				if _, err := a.RunText(t.Context(), "failed", agent.WithSession(session), agent.Stream(stream)).Collect(); !errors.Is(err, runErr) {
					t.Fatalf("run failure = %v", err)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(documents) != 4 {
					t.Fatalf("stored %d messages, want 4 without retained-history duplicates or failed-run inputs", len(documents))
				}
			})
		}
	}
}

func TestCosmosHistoryProviderDefaultStateKey(t *testing.T) {
	provider, _, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	if !slices.Equal(provider.StateKeys(), []string{"CosmosHistoryProvider"}) {
		t.Fatalf("state keys = %v", provider.StateKeys())
	}
}

func TestCosmosHistoryProviderCustomStateKey(t *testing.T) {
	provider, _, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{StateKey: new("custom-key")})
	if !slices.Equal(provider.StateKeys(), []string{"custom-key"}) {
		t.Fatalf("state keys = %v", provider.StateKeys())
	}
}

func TestCosmosHistoryProviderNilInitializer(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	provider, err := azureprovider.NewCosmosHistoryProvider(protocol.client, "sample", "history", nil, azureprovider.CosmosHistoryProviderConfig{})
	if err == nil || provider != nil {
		t.Fatalf("constructor = (%v, %v), want an error", provider, err)
	}
}

func TestCosmosHistoryProviderHierarchicalClient(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	provider := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{
		ConversationID: "session-789", TenantID: new("tenant-123"), UserID: new("user-456"),
	}, azureprovider.CosmosHistoryProviderConfig{})
	if provider.DatabaseID() != "sample" || provider.ContainerID() != "history" {
		t.Fatalf("database/container = %q/%q", provider.DatabaseID(), provider.ContainerID())
	}
}

func TestCosmosHistoryProviderConnectionStringClient(t *testing.T) {
	connectionString := "AccountEndpoint=https://cosmos.example/;AccountKey=" + base64.StdEncoding.EncodeToString([]byte("local-test-key"))
	client, err := azcosmos.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	provider := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "test-conversation"},
		azureprovider.CosmosHistoryProviderConfig{OwnsClient: true})
	if provider.DatabaseID() != "sample" || provider.ContainerID() != "history" {
		t.Fatalf("database/container = %q/%q", provider.DatabaseID(), provider.ContainerID())
	}
}

func TestCosmosHistoryProviderHierarchicalConnectionStringClient(t *testing.T) {
	connectionString := "AccountEndpoint=https://cosmos.example/;AccountKey=" + base64.StdEncoding.EncodeToString([]byte("local-test-key"))
	client, err := azcosmos.NewClientFromConnectionString(connectionString, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	provider := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{
		ConversationID: "session-789", TenantID: new("tenant-123"), UserID: new("user-456"),
	}, azureprovider.CosmosHistoryProviderConfig{OwnsClient: true})
	if provider.DatabaseID() != "sample" || provider.ContainerID() != "history" {
		t.Fatalf("database/container = %q/%q", provider.DatabaseID(), provider.ContainerID())
	}
}

func TestCosmosHistoryProviderHierarchicalTokenClient(t *testing.T) {
	client, err := azcosmos.NewClient("https://cosmos.example/", &azfake.TokenCredential{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	provider := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{
		ConversationID: "session-789", TenantID: new("tenant-123"), UserID: new("user-456"),
	}, azureprovider.CosmosHistoryProviderConfig{OwnsClient: true})
	if provider.DatabaseID() != "sample" || provider.ContainerID() != "history" {
		t.Fatalf("database/container = %q/%q", provider.DatabaseID(), provider.ContainerID())
	}
}

func TestCosmosHistoryProviderSingleMessage(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	storeCosmosHistoryMessages(t, provider, session, []*message.Message{message.NewText("Hello, world!")}, nil)
	messages := loadCosmosHistoryMessages(t, provider, session)
	assertCosmosHistoryMessages(t, messages, "Hello, world!")
	if messages[0].Role != message.RoleUser {
		t.Fatalf("role = %q, want user", messages[0].Role)
	}
}

func TestCosmosHistoryProviderOmittedTTL(t *testing.T) {
	provider, session, protocol := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{
		MessageTTLSeconds: new(int32(0)),
	})
	storeCosmosHistoryMessages(t, provider, session, []*message.Message{message.NewText("No TTL message")}, nil)
	assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, provider, session), "No TTL message")
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	count := 0
	for _, documents := range protocol.documents {
		for _, document := range documents {
			count++
			if len(document.TTL) != 0 {
				t.Errorf("ttl property = %s, want omitted", document.TTL)
			}
		}
	}
	if count != 1 {
		t.Fatalf("stored document count = %d, want 1", count)
	}
}

func TestCosmosHistoryProviderMultipleMessages(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	requests := []*message.Message{
		message.NewText("First message"), message.NewText("Second message"),
		message.NewText("Third message"), message.NewText("System context message"),
	}
	requests[1].Role = message.RoleAssistant
	requests[3].Role = message.RoleSystem
	requests[3].Source = message.Source{Type: "context-provider", ID: "TestSource"}
	response := message.NewText("Response message")
	response.Role = message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session, requests, []*message.Message{response})
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, provider, session),
		"First message", "Second message", "Third message", "System context message", "Response message")
}

func TestCosmosHistoryProviderEmptyInvocation(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, provider, session))
}

func TestCosmosHistoryProviderConversationIsolation(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	session := new(agent.Session)
	first := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{ConversationID: "first"},
		azureprovider.CosmosHistoryProviderConfig{StateKey: new("conv1")})
	second := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{ConversationID: "second"},
		azureprovider.CosmosHistoryProviderConfig{StateKey: new("conv2")})
	storeCosmosHistoryMessages(t, first, session, []*message.Message{message.NewText("Message for conversation 1")}, nil)
	storeCosmosHistoryMessages(t, second, session, []*message.Message{message.NewText("Message for conversation 2")}, nil)
	for _, tc := range []struct {
		provider *azureprovider.CosmosHistoryProvider
		text     string
	}{{first, "Message for conversation 1"}, {second, "Message for conversation 2"}} {
		messages := loadCosmosHistoryMessages(t, tc.provider, session)
		assertCosmosHistoryMessages(t, messages, tc.text)
		if messages[0].Source.Type != agent.SourceTypeHistoryProvider {
			t.Errorf("source = %v, want history provider", messages[0].Source)
		}
	}
}

func TestCosmosHistoryProviderPersistenceAcrossInstances(t *testing.T) {
	provider, session, protocol := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	texts := []string{
		"You are a helpful assistant.", "Hello!", "Hi there! How can I help you today?",
		"What's the weather like?", "I'm sorry, I don't have access to current weather data.",
	}
	roles := []message.Role{message.RoleSystem, message.RoleUser, message.RoleAssistant, message.RoleUser, message.RoleAssistant}
	messages := make([]*message.Message, len(texts))
	for i, text := range texts {
		messages[i] = message.NewText(text)
		messages[i].Role = roles[i]
	}
	storeCosmosHistoryMessages(t, provider, session, messages, nil)
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, provider, session), texts...)
	replacement := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
		azureprovider.CosmosHistoryProviderConfig{})
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, replacement, new(agent.Session)), texts...)
}

func TestCosmosHistoryProviderClose(t *testing.T) {
	provider, _, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{OwnsClient: true})
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCosmosHistoryProviderCloseIdempotent(t *testing.T) {
	provider, _, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{OwnsClient: true})
	for range 2 {
		if err := provider.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCosmosHistoryProviderHierarchicalMessageMetadata(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	provider := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{
		ConversationID: "session-789", TenantID: new("tenant-123"), UserID: new("user-456"),
	}, azureprovider.CosmosHistoryProviderConfig{})
	session := new(agent.Session)
	storeCosmosHistoryMessages(t, provider, session, []*message.Message{message.NewText("Hello from hierarchical partitioning!")}, nil)
	messages := loadCosmosHistoryMessages(t, provider, session)
	assertCosmosHistoryMessages(t, messages, "Hello from hierarchical partitioning!")
	if messages[0].Role != message.RoleUser {
		t.Errorf("role = %q, want user", messages[0].Role)
	}
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	documents := protocol.documents["/dbs/sample/colls/history/docs\n[\"tenant-123\",\"user-456\",\"session-789\"]"]
	if len(documents) != 1 {
		t.Fatalf("hierarchical document count = %d, want 1", len(documents))
	}
	document := documents[0]
	if document.ConversationID != "session-789" || document.SessionID != "session-789" ||
		document.TenantID == nil || *document.TenantID != "tenant-123" ||
		document.UserID == nil || *document.UserID != "user-456" {
		t.Fatalf("hierarchical document = %+v", document)
	}
}

func TestCosmosHistoryProviderHierarchicalMessages(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	provider := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{
		ConversationID: "session-batch", TenantID: new("tenant-batch"), UserID: new("user-batch"),
	}, azureprovider.CosmosHistoryProviderConfig{})
	session := new(agent.Session)
	messages := []*message.Message{
		message.NewText("First hierarchical message"), message.NewText("Second hierarchical message"),
		message.NewText("Third hierarchical message"),
	}
	messages[1].Role = message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session, messages, nil)
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, provider, session),
		"First hierarchical message", "Second hierarchical message", "Third hierarchical message")
}

func TestCosmosHistoryProviderUserIsolation(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	session := new(agent.Session)
	providers := make([]*azureprovider.CosmosHistoryProvider, 2)
	for i := range providers {
		user := fmt.Sprintf("user-%d", i+1)
		providers[i] = newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{
			ConversationID: "session-isolation", TenantID: new("tenant-isolation"), UserID: &user,
		}, azureprovider.CosmosHistoryProviderConfig{StateKey: &user})
		storeCosmosHistoryMessages(t, providers[i], session,
			[]*message.Message{message.NewText(fmt.Sprintf("Message from user %d", i+1))}, nil)
	}
	for i, provider := range providers {
		assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, provider, session), fmt.Sprintf("Message from user %d", i+1))
	}
}

func TestCosmosHistoryProviderSharedSessionRouting(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	session := new(agent.Session)
	first := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{
		ConversationID: "session-serialize", TenantID: new("tenant-serialize"), UserID: new("user-serialize"),
	}, azureprovider.CosmosHistoryProviderConfig{})
	storeCosmosHistoryMessages(t, first, session, []*message.Message{message.NewText("Test serialization message")}, nil)
	second := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{ConversationID: "wrong"},
		azureprovider.CosmosHistoryProviderConfig{})
	assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, second, session), "Test serialization message")
	if second.DatabaseID() != "sample" || second.ContainerID() != "history" {
		t.Fatalf("database/container = %q/%q", second.DatabaseID(), second.ContainerID())
	}
}

func TestCosmosHistoryProviderPartitionModesCoexist(t *testing.T) {
	protocol := newCosmosHistoryProtocol(t)
	session := new(agent.Session)
	for _, tc := range []struct {
		container string
		state     *azureprovider.CosmosHistoryState
		text      string
	}{
		{"simple", &azureprovider.CosmosHistoryState{ConversationID: "coexist-session"}, "Simple partitioning message"},
		{"hierarchical", &azureprovider.CosmosHistoryState{
			ConversationID: "coexist-session", TenantID: new("tenant-coexist"), UserID: new("user-coexist"),
		}, "Hierarchical partitioning message"},
	} {
		provider, err := azureprovider.NewCosmosHistoryProvider(protocol.client, "sample", tc.container,
			func(*agent.Session) *azureprovider.CosmosHistoryState { return tc.state },
			azureprovider.CosmosHistoryProviderConfig{StateKey: &tc.container})
		if err != nil {
			t.Fatal(err)
		}
		defer provider.Close()
		storeCosmosHistoryMessages(t, provider, session, []*message.Message{message.NewText(tc.text)}, nil)
	}
	for _, tc := range []struct {
		container string
		text      string
	}{{"simple", "Simple partitioning message"}, {"hierarchical", "Hierarchical partitioning message"}} {
		provider, err := azureprovider.NewCosmosHistoryProvider(protocol.client, "sample", tc.container,
			func(*agent.Session) *azureprovider.CosmosHistoryState { return nil },
			azureprovider.CosmosHistoryProviderConfig{StateKey: &tc.container})
		if err != nil {
			t.Fatal(err)
		}
		defer provider.Close()
		assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, provider, session), tc.text)
	}
}

func TestCosmosHistoryProviderMessagesAcrossPages(t *testing.T) {
	provider, session, protocol := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{MaxItemCount: new(int32(2))})
	texts := []string{"Message 1", "Message 2", "Message 3", "Message 4", "Message 5"}
	messages := make([]*message.Message, len(texts))
	for i, text := range texts {
		messages[i] = message.NewText(text)
		if i%2 == 1 {
			messages[i].Role = message.RoleAssistant
		}
	}
	storeCosmosHistoryMessages(t, provider, session, messages, nil)
	got, err := provider.Messages(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	assertCosmosHistoryMessagesUnordered(t, got, texts...)
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	if protocol.queries != 3 {
		t.Fatalf("query pages = %d, want 3", protocol.queries)
	}
}

func TestCosmosHistoryProviderEmptyMessages(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	messages, err := provider.Messages(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	assertCosmosHistoryMessages(t, messages)
}

func TestCosmosHistoryProviderRawMessages(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{
		ProvideOutputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.String() == "Hidden" }), nil
		},
	})
	hidden := message.NewText("Hidden")
	hidden.Role = message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session, []*message.Message{message.NewText("Visible"), hidden}, nil)
	raw, err := provider.Messages(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	assertCosmosHistoryMessagesUnordered(t, raw, "Visible", "Hidden")
	for _, msg := range raw {
		if msg.Source.Type != message.SourceTypeExternal {
			t.Errorf("raw source = %v, want external", msg.Source)
		}
	}
	filtered := loadCosmosHistoryMessages(t, provider, session)
	assertCosmosHistoryMessages(t, filtered, "Visible")
	if filtered[0].Source.Type != agent.SourceTypeHistoryProvider {
		t.Errorf("invocation source = %v, want history provider", filtered[0].Source)
	}
}

func TestCosmosHistoryProviderMessagesAfterClose(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Messages(t.Context(), session); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("retrieval after Close = %v, want closed error", err)
	}
}

func TestCosmosHistoryProviderMessagesCanceled(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := provider.Messages(ctx, session); !errors.Is(err, context.Canceled) {
		t.Fatalf("retrieval error = %v, want context cancellation", err)
	}
}

func TestCosmosHistoryProviderLatestMessages(t *testing.T) {
	provider, session, protocol := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	messages := make([]*message.Message, 10)
	for i := range messages {
		messages[i] = message.NewText(fmt.Sprintf("Message %d", i+1))
	}
	storeCosmosHistoryMessages(t, provider, session, messages, nil)
	// Batch writes share a timestamp; distinct service fixtures make recency unambiguous.
	protocol.mu.Lock()
	for _, documents := range protocol.documents {
		for i := range documents {
			documents[i].Timestamp = int64(i + 1)
		}
	}
	protocol.mu.Unlock()
	limited := newCosmosHistoryProvider(t, protocol.client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
		azureprovider.CosmosHistoryProviderConfig{MaxMessagesToRetrieve: new(5)})
	assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, limited, session),
		"Message 6", "Message 7", "Message 8", "Message 9", "Message 10")
}

func TestCosmosHistoryProviderUnlimitedMessages(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	messages := make([]*message.Message, 10)
	texts := make([]string, len(messages))
	for i := range messages {
		texts[i] = fmt.Sprintf("Message %d", i+1)
		messages[i] = message.NewText(texts[i])
	}
	storeCosmosHistoryMessages(t, provider, session, messages, nil)
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, provider, session), texts...)
}

func TestCosmosHistoryProviderMessageCount(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	messages := make([]*message.Message, 5)
	for i := range messages {
		messages[i] = message.NewText(fmt.Sprintf("Message %d", i+1))
	}
	storeCosmosHistoryMessages(t, provider, session, messages, nil)
	if count, err := provider.MessageCount(t.Context(), session); err != nil || count != 5 {
		t.Fatalf("message count = (%d, %v), want 5", count, err)
	}
}

func TestCosmosHistoryProviderEmptyMessageCount(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	if count, err := provider.MessageCount(t.Context(), session); err != nil || count != 0 {
		t.Fatalf("message count = (%d, %v), want 0", count, err)
	}
}

func TestCosmosHistoryProviderClearMessages(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	storeCosmosHistoryMessages(t, provider, session,
		[]*message.Message{message.NewText("Message 1"), message.NewText("Message 2"), message.NewText("Message 3")}, nil)
	if count, err := provider.MessageCount(t.Context(), session); err != nil || count != 3 {
		t.Fatalf("initial count = (%d, %v), want 3", count, err)
	}
	if deleted, err := provider.ClearMessages(t.Context(), session); err != nil || deleted != 3 {
		t.Fatalf("clear = (%d, %v), want 3", deleted, err)
	}
	if count, err := provider.MessageCount(t.Context(), session); err != nil || count != 0 {
		t.Fatalf("final count = (%d, %v), want 0", count, err)
	}
	assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, provider, session))
}

func TestCosmosHistoryProviderEmptyClear(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	if deleted, err := provider.ClearMessages(t.Context(), session); err != nil || deleted != 0 {
		t.Fatalf("clear = (%d, %v), want 0", deleted, err)
	}
}

func TestCosmosHistoryProviderDefaultStorageFilter(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{})
	history, contextMessage := message.NewText("From history"), message.NewText("From context provider")
	history.Role, contextMessage.Role = message.RoleSystem, message.RoleSystem
	history.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "HistorySource"}
	contextMessage.Source = message.Source{Type: "context-provider", ID: "ContextSource"}
	response := message.NewText("Response")
	response.Role = message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session,
		[]*message.Message{message.NewText("External message"), history, contextMessage}, []*message.Message{response})
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, provider, session),
		"External message", "From context provider", "Response")
}

func TestCosmosHistoryProviderExternalStorageFilter(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{
		StoreInputRequestMessageFilter: messagefilter.ExternalOnly,
	})
	history, contextMessage := message.NewText("From history"), message.NewText("From context provider")
	history.Role, contextMessage.Role = message.RoleSystem, message.RoleSystem
	history.Source = message.Source{Type: agent.SourceTypeHistoryProvider, ID: "HistorySource"}
	contextMessage.Source = message.Source{Type: "context-provider", ID: "ContextSource"}
	response := message.NewText("Response")
	response.Role = message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session,
		[]*message.Message{message.NewText("External message"), history, contextMessage}, []*message.Message{response})
	assertCosmosHistoryMessagesUnordered(t, loadCosmosHistoryMessages(t, provider, session), "External message", "Response")
}

func TestCosmosHistoryProviderRetrievalOutputFilter(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{
		ProvideOutputMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.Role != message.RoleUser }), nil
		},
	})
	system, assistant := message.NewText("System message"), message.NewText("Assistant response")
	system.Role, assistant.Role = message.RoleSystem, message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session,
		[]*message.Message{message.NewText("User message"), system}, []*message.Message{assistant})
	messages := loadCosmosHistoryMessages(t, provider, session)
	assertCosmosHistoryMessages(t, messages, "User message")
	if messages[0].Role != message.RoleUser {
		t.Errorf("role = %q, want user", messages[0].Role)
	}
}

func TestCosmosHistoryProviderCustomBatchPartition(t *testing.T) {
	for _, flat := range []bool{false, true} {
		t.Run(fmt.Sprintf("flat=%t", flat), func(t *testing.T) {
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				key := `["tenant"]`
				if flat {
					key = `["conversation"]`
				}
				if got := r.Header.Get("x-ms-documentdb-partitionkey"); got != key {
					t.Errorf("batch key = %s, want %s", got, key)
				}
				operations := readCosmosHistoryOperations(t, r)
				if len(operations) != 2 {
					t.Errorf("operations = %d, want 2", len(operations))
				}
				for _, operation := range operations {
					document := operation.ResourceBody
					if operation.OperationType != "Create" || document.TenantID == nil || *document.TenantID != "tenant" ||
						document.UserID == nil || *document.UserID != "user" || document.SessionID != "conversation" {
						t.Errorf("batch document = %+v", document)
					}
				}
				writeCosmosHistoryBatch(t, w, len(operations), http.StatusCreated)
			})
			provider := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{
				ConversationID: "conversation", TenantID: new("tenant"), UserID: new("user"),
			}, azureprovider.CosmosHistoryProviderConfig{
				PartitionKeyFactory: func(state azureprovider.CosmosHistoryState) azcosmos.PartitionKey {
					if flat {
						return azcosmos.NewPartitionKeyString(state.ConversationID)
					}
					return azcosmos.NewPartitionKeyString(*state.TenantID)
				},
			})
			storeCosmosHistoryMessages(t, provider, new(agent.Session),
				[]*message.Message{message.NewText("Hello"), message.NewText("World")}, nil)
		})
	}
}

func TestCosmosHistoryProviderReadPartitionFactory(t *testing.T) {
	for _, operation := range []string{"retrieve", "count", "clear"} {
		t.Run(operation, func(t *testing.T) {
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("partition factory failure contacted Cosmos")
				http.Error(w, "unexpected request", http.StatusBadRequest)
			})
			state := &azureprovider.CosmosHistoryState{ConversationID: "conversation", TenantID: new("tenant"), UserID: new("user")}
			expected := errors.New("custom partition key factory")
			var observed azureprovider.CosmosHistoryState
			provider := newCosmosHistoryProvider(t, client, state, azureprovider.CosmosHistoryProviderConfig{
				PartitionKeyFactory: func(state azureprovider.CosmosHistoryState) azcosmos.PartitionKey {
					observed = state
					panic(expected)
				},
			})
			defer func() {
				if got := recover(); got != expected {
					t.Errorf("partition factory panic = %v, want original error", got)
				}
				if !reflect.DeepEqual(observed, *state) {
					t.Errorf("partition factory state = %+v, want %+v", observed, *state)
				}
			}()
			switch operation {
			case "retrieve":
				_, _ = provider.Messages(t.Context(), nil)
			case "count":
				_, _ = provider.MessageCount(t.Context(), nil)
			case "clear":
				_, _ = provider.ClearMessages(t.Context(), nil)
			}
		})
	}
}

func TestCosmosHistoryProviderStorageFiltersConfig(t *testing.T) {
	provider, session, _ := newCosmosHistoryConversation(t, azureprovider.CosmosHistoryProviderConfig{
		StoreInputRequestMessageFilter: func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return slices.DeleteFunc(messages, func(msg *message.Message) bool { return msg.String() != "Keep" }), nil
		},
		StoreInputResponseMessageFilter: func(context.Context, []*message.Message) ([]*message.Message, error) { return nil, nil },
	})
	response := message.NewText("Discard response")
	response.Role = message.RoleAssistant
	storeCosmosHistoryMessages(t, provider, session,
		[]*message.Message{message.NewText("Keep"), message.NewText("Discard")}, []*message.Message{response})
	assertCosmosHistoryMessages(t, loadCosmosHistoryMessages(t, provider, session), "Keep")
}

func TestCosmosHistoryProviderClientOwnership(t *testing.T) {
	for _, owns := range []bool{false, true} {
		t.Run(fmt.Sprintf("owns=%t", owns), func(t *testing.T) {
			var metadataReads atomic.Int32
			client := newCosmosHistoryClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/dbs/sample/colls/history":
					metadataReads.Add(1)
					writeCosmosHistoryJSON(t, w, map[string]any{
						"id": "history", "_rid": "history-rid",
						"partitionKey": map[string]any{"paths": []string{"/conversationId"}, "kind": "Hash", "version": 2},
					})
				case r.Method == http.MethodGet && r.URL.Path == "/dbs/sample/colls/history/pkranges":
					w.Header().Set("ETag", `"ranges"`)
					if r.Header.Get("If-None-Match") != "" {
						w.WriteHeader(http.StatusNotModified)
						return
					}
					writeCosmosHistoryJSON(t, w, map[string]any{
						"PartitionKeyRanges": []any{map[string]any{"id": "0", "minInclusive": "", "maxExclusive": "FF"}},
						"_count":             1,
					})
				default:
					t.Errorf("unexpected Cosmos request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			})
			readFeedRanges := func(client *azcosmos.Client) {
				t.Helper()
				container, err := client.NewContainer("sample", "history")
				if err != nil {
					t.Fatal(err)
				}
				if ranges, err := container.ReadFeedRanges(t.Context(), nil); err != nil || len(ranges) != 1 {
					t.Fatalf("feed ranges = (%v, %v), want one range", ranges, err)
				}
			}

			// Client.Close releases shared metadata caches, but does not prevent queries.
			readFeedRanges(client)
			if reads := metadataReads.Load(); reads != 1 {
				t.Fatalf("initial metadata reads = %d, want 1", reads)
			}
			provider := newCosmosHistoryProvider(t, client, &azureprovider.CosmosHistoryState{ConversationID: "conversation"},
				azureprovider.CosmosHistoryProviderConfig{OwnsClient: owns})
			for range 2 {
				if err := provider.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := provider.Messages(t.Context(), new(agent.Session)); err == nil {
				t.Fatal("closed provider remained usable")
			}

			connectionString := "AccountEndpoint=" + client.Endpoint() + ";AccountKey=" + base64.StdEncoding.EncodeToString([]byte("local-test-key"))
			probe, err := azcosmos.NewClientFromConnectionString(connectionString, &azcosmos.ClientOptions{
				ClientOptions: azcore.ClientOptions{
					InsecureAllowCredentialWithHTTP: true,
					Retry:                           policy.RetryOptions{MaxRetries: -1},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(probe.Close)
			readFeedRanges(probe)
			wantReads := int32(1)
			if owns {
				wantReads++
			}
			if reads := metadataReads.Load(); reads != wantReads {
				t.Fatalf("metadata reads after Close = %d, want %d", reads, wantReads)
			}
		})
	}
}

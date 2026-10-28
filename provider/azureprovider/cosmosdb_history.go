// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

const (
	cosmosHistorySourceID    = "CosmosHistoryProvider"
	cosmosHistoryMessageType = "Message"
)

// CosmosHistoryState identifies a conversation in Cosmos DB. ConversationID
// must be nonblank. The default container partition path is /conversationId.
// When both TenantID and UserID are non-nil, the container must instead use
// hierarchical paths /tenantId, /userId, and /sessionId, in that order.
// Non-nil empty tenant and user IDs remain key components. Documents always
// store ConversationID as sessionId and retain supplied tenant and user metadata,
// regardless of partition strategy.
//
// The provider stores a *CosmosHistoryState in the session under its
// [CosmosHistoryProvider.StateKeys] key. Synchronize changes with operations
// and use [agent.Session.Set] to persist edits to restored state. Partition keys
// route data; they do not replace application-level authorization.
type CosmosHistoryState struct {
	ConversationID string  `json:"conversationId"`
	TenantID       *string `json:"tenantId,omitempty"`
	UserID         *string `json:"userId,omitempty"`
}

// CosmosHistoryProviderConfig configures [NewCosmosHistoryProvider].
type CosmosHistoryProviderConfig struct {
	// OwnsClient transfers responsibility for closing the supplied client to the
	// provider. The default is false.
	OwnsClient bool

	// StateKey identifies provider state in the session. Nil defaults to
	// "CosmosHistoryProvider". Empty and whitespace-only keys are invalid.
	// Use distinct keys for independent providers sharing a session.
	StateKey *string

	// MaxItemCount limits query page sizes. Nil defaults to 100. An explicit
	// value must be positive, or -1 for a service-selected page size.
	MaxItemCount *int32

	// MaxBatchSize limits operations in each transactional batch. Nil defaults
	// to 100. Explicit values must be between 1 and 100.
	MaxBatchSize *int

	// MaxMessagesToRetrieve limits retrieval to the most recent messages. Nil
	// retrieves all messages. Zero retrieves none; negative values are invalid.
	// This limit does not affect [CosmosHistoryProvider.MessageCount] or
	// [CosmosHistoryProvider.ClearMessages].
	MaxMessagesToRetrieve *int

	// MessageTTLSeconds sets the per-item expiration in seconds. Nil defaults
	// to 86400 (24 hours). Zero omits ttl, allowing the container default to
	// apply. -1 disables item expiration. Other negative values are invalid.
	// The container must have TTL enabled for expiration to take effect.
	MessageTTLSeconds *int32

	// PartitionKeyFactory overrides the default routing described by
	// [CosmosHistoryState] for retrieval, storage, count, and deletion. It
	// must return a key matching the container definition and stored document
	// values. The state and its pointed-to values are read-only.
	PartitionKeyFactory func(CosmosHistoryState) azcosmos.PartitionKey

	// ProvideOutputMessageFilter filters retrieved messages during
	// [CosmosHistoryProvider.Invoking]. Nil passes all retrieved messages
	// through. [CosmosHistoryProvider.Messages] bypasses this filter.
	ProvideOutputMessageFilter messagefilter.Filter

	// StoreInputRequestMessageFilter filters request messages before storage.
	// Nil excludes messages supplied by history providers.
	StoreInputRequestMessageFilter messagefilter.Filter

	// StoreInputResponseMessageFilter filters response messages before storage.
	// Nil stores all response messages.
	StoreInputResponseMessageFilter messagefilter.Filter
}

// CosmosHistoryProvider implements [agent.HistoryProvider] using a Cosmos DB
// NoSQL container. Create it with [NewCosmosHistoryProvider].
// Documents use the "Message" type discriminator and [message.Message] JSON
// encoding. Caller messages and slices are not modified.
//
// Messages are ordered by their stored Unix-second timestamp. Equal timestamps,
// including those in a write batch, have no guaranteed relative order or
// selection when a retrieval limit intersects a tie.
// Transactional batches are atomic individually, not across a complete write or
// clear operation. Failures are returned; earlier batches may have committed.
// Oversized write batches split and retry; individually oversized messages
// return an error preserving the SDK cause. Throttling retries follow the
// configured SDK policy.
//
// Stored messages can contain sensitive data. Configure appropriate access
// controls, encryption, network security, and retention. Retrieved messages are
// trusted as conversation history without additional validation or sanitization.
//
// A provider can be shared across sessions. Callers must synchronize edits to
// session state with operations. Do not copy a provider after use.
type CosmosHistoryProvider struct {
	client                *azcosmos.Client
	container             *azcosmos.ContainerClient
	databaseID            string
	containerID           string
	stateInitializer      func(*agent.Session) *CosmosHistoryState
	stateKey              string
	maxItemCount          int32
	maxBatchSize          int
	maxMessagesToRetrieve int
	messageTTLSeconds     int32
	partitionKeyFactory   func(CosmosHistoryState) azcosmos.PartitionKey
	ownsClient            bool
	history               agent.HistoryProvider
	stateMu               sync.Mutex
	closed                atomic.Bool
}

// NewCosmosHistoryProvider creates a provider for an existing database and
// container. Construction does not contact the service. Attach the provider
// through [agent.Config.HistoryProvider].
//
// Configure authentication, transport, and retries using [azcosmos.NewClient],
// [azcosmos.NewClientWithKey], or [azcosmos.NewClientFromConnectionString].
// The client is borrowed unless config.OwnsClient is true. Close the provider
// when finished; closing a borrowing provider does not close the client.
//
// stateInitializer is required and supplies routing state on first use of a
// session. It must return a non-nil state with a nonblank ConversationID. State
// is retained in the session and survives JSON serialization. Without a session,
// the initializer is called with nil for each operation and no state is retained.
// It must return stable routing information for operations to share history.
// Configuration values are captured at construction.
//
// For example, with a caller-owned client:
//
//	history, err := azureprovider.NewCosmosHistoryProvider(client, "agents", "history",
//		func(*agent.Session) *azureprovider.CosmosHistoryState {
//			return &azureprovider.CosmosHistoryState{ConversationID: uuid.NewString()}
//		}, azureprovider.CosmosHistoryProviderConfig{})
//	if err != nil {
//		return err
//	}
//	defer history.Close()
//	a := agent.New(providerConfig, agent.Config{HistoryProvider: history})
func NewCosmosHistoryProvider(client *azcosmos.Client, databaseID, containerID string, stateInitializer func(*agent.Session) *CosmosHistoryState, config CosmosHistoryProviderConfig) (*CosmosHistoryProvider, error) {
	if client == nil {
		return nil, errors.New("cosmos history: client is required")
	}
	if strings.TrimSpace(databaseID) == "" || strings.TrimSpace(containerID) == "" {
		return nil, errors.New("cosmos history: database and container IDs must be nonblank")
	}
	if stateInitializer == nil {
		return nil, errors.New("cosmos history: stateInitializer is required")
	}
	p := &CosmosHistoryProvider{
		client: client, databaseID: databaseID, containerID: containerID,
		stateInitializer: stateInitializer, stateKey: cosmosHistorySourceID,
		maxItemCount: 100, maxBatchSize: 100, maxMessagesToRetrieve: -1,
		messageTTLSeconds: 86400, partitionKeyFactory: config.PartitionKeyFactory,
		ownsClient: config.OwnsClient,
	}
	if config.StateKey != nil {
		p.stateKey = *config.StateKey
	}
	if strings.TrimSpace(p.stateKey) == "" {
		return nil, errors.New("cosmos history: state key must be nonblank")
	}
	if config.MaxItemCount != nil {
		p.maxItemCount = *config.MaxItemCount
	}
	if p.maxItemCount != -1 && p.maxItemCount <= 0 {
		return nil, errors.New("cosmos history: MaxItemCount must be positive or -1")
	}
	if config.MaxBatchSize != nil {
		p.maxBatchSize = *config.MaxBatchSize
	}
	if p.maxBatchSize < 1 || p.maxBatchSize > 100 {
		return nil, errors.New("cosmos history: MaxBatchSize must be between 1 and 100")
	}
	if config.MaxMessagesToRetrieve != nil {
		p.maxMessagesToRetrieve = *config.MaxMessagesToRetrieve
		if p.maxMessagesToRetrieve < 0 {
			return nil, errors.New("cosmos history: MaxMessagesToRetrieve must not be negative")
		}
	}
	if config.MessageTTLSeconds != nil {
		p.messageTTLSeconds = *config.MessageTTLSeconds
	}
	if p.messageTTLSeconds < -1 {
		return nil, errors.New("cosmos history: MessageTTLSeconds must be nonnegative or -1")
	}
	database, err := client.NewDatabase(databaseID)
	if err != nil {
		return nil, err
	}
	p.container, err = database.NewContainer(containerID)
	if err != nil {
		return nil, err
	}
	p.history = agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID:                        cosmosHistorySourceID,
		ProvideOutputMessageFilter:      config.ProvideOutputMessageFilter,
		StoreInputRequestMessageFilter:  config.StoreInputRequestMessageFilter,
		StoreInputResponseMessageFilter: config.StoreInputResponseMessageFilter,
		Provide: func(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
			session, _ := agent.GetOption(invoking.Options, agent.WithSession)
			return p.Messages(ctx, session)
		},
		Store: p.store,
	})
	return p, nil
}

// DatabaseID returns the configured database ID.
func (p *CosmosHistoryProvider) DatabaseID() string { return p.databaseID }

// ContainerID returns the configured container ID.
func (p *CosmosHistoryProvider) ContainerID() string { return p.containerID }

// StateKeys returns the session state key. The returned slice belongs to the caller.
func (p *CosmosHistoryProvider) StateKeys() []string { return []string{p.stateKey} }

// Invoking prepends source-attributed, filtered history to the input messages.
func (p *CosmosHistoryProvider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
	return p.history.Invoking(ctx, invoking)
}

// Invoked stores filtered request messages followed by response messages.
// Failed runs are not stored.
func (p *CosmosHistoryProvider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.history.Invoked(ctx, invoked)
}

// Messages retrieves stored messages in timestamp order, applying
// MaxMessagesToRetrieve but not the invocation output filter or source attribution.
func (p *CosmosHistoryProvider) Messages(ctx context.Context, session *agent.Session) ([]*message.Message, error) {
	state, err := p.loadState(ctx, session)
	if err != nil {
		return nil, err
	}
	messages := make([]*message.Message, 0)
	if p.maxMessagesToRetrieve == 0 {
		return messages, nil
	}
	direction := "ASC"
	if p.maxMessagesToRetrieve > 0 {
		direction = "DESC"
	}
	query := "SELECT * FROM c WHERE c.conversationId = @conversationId AND c.type = @type ORDER BY c.timestamp " + direction
	pager := p.container.NewQueryItemsPager(query, p.partitionKey(state), p.queryOptions(state))
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("cosmos history: retrieve messages: %w", err)
		}
		for _, item := range page.Items {
			var document cosmosHistoryDocument
			if err := json.Unmarshal(item, &document); err != nil {
				return nil, fmt.Errorf("cosmos history: decode document: %w", err)
			}
			if document.Message == "" {
				continue
			}
			var msg *message.Message
			if err := json.Unmarshal([]byte(document.Message), &msg); err != nil {
				return nil, fmt.Errorf("cosmos history: decode message in document %q: %w", document.ID, err)
			}
			if msg != nil {
				messages = append(messages, msg)
			}
			if p.maxMessagesToRetrieve > 0 && len(messages) >= p.maxMessagesToRetrieve {
				break
			}
		}
		if p.maxMessagesToRetrieve > 0 && len(messages) >= p.maxMessagesToRetrieve {
			break
		}
	}
	if p.maxMessagesToRetrieve > 0 {
		slices.Reverse(messages)
	}
	return messages, nil
}

// MessageCount counts all chat-message documents in the conversation,
// independently of retrieval limits and message filters.
func (p *CosmosHistoryProvider) MessageCount(ctx context.Context, session *agent.Session) (int, error) {
	state, err := p.loadState(ctx, session)
	if err != nil {
		return 0, err
	}
	pager := p.container.NewQueryItemsPager(
		"SELECT VALUE COUNT(1) FROM c WHERE c.conversationId = @conversationId AND c.type = @type",
		p.partitionKey(state), p.queryOptions(state))
	count := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return 0, fmt.Errorf("cosmos history: count messages: %w", err)
		}
		for _, item := range page.Items {
			var value *int
			if err := json.Unmarshal(item, &value); err != nil {
				return 0, fmt.Errorf("cosmos history: decode message count: %w", err)
			}
			if value == nil || *value < 0 {
				return 0, errors.New("cosmos history: expected a nonnegative message count")
			}
			count += *value
		}
	}
	return count, nil
}

// ClearMessages deletes all chat-message documents in the conversation,
// independently of retrieval limits and filters. It returns the number of
// successfully deleted documents, including on an error after partial progress.
// It retains the conversation's session state.
func (p *CosmosHistoryProvider) ClearMessages(ctx context.Context, session *agent.Session) (int, error) {
	state, err := p.loadState(ctx, session)
	if err != nil {
		return 0, err
	}
	key := p.partitionKey(state)
	pager := p.container.NewQueryItemsPager(
		"SELECT VALUE c.id FROM c WHERE c.conversationId = @conversationId AND c.type = @type",
		key, p.queryOptions(state))
	deleted := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return deleted, fmt.Errorf("cosmos history: list messages to delete: %w", err)
		}
		ids := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			var id string
			if err := json.Unmarshal(item, &id); err != nil {
				return deleted, fmt.Errorf("cosmos history: decode message ID: %w", err)
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		for chunk := range slices.Chunk(ids, p.maxBatchSize) {
			batch := p.container.NewTransactionalBatch(key)
			for _, id := range chunk {
				batch.DeleteItem(id, nil)
			}
			if err := p.executeBatch(ctx, batch); err != nil {
				return deleted, fmt.Errorf("cosmos history: delete messages: %w", err)
			}
			deleted += len(chunk)
		}
	}
	return deleted, nil
}

// Close prevents further storage operations and closes the client if OwnsClient
// was set. It is idempotent and does not cancel operations already in flight.
func (p *CosmosHistoryProvider) Close() error {
	if !p.closed.Swap(true) && p.ownsClient {
		p.client.Close()
	}
	return nil
}

func (p *CosmosHistoryProvider) loadState(ctx context.Context, session *agent.Session) (CosmosHistoryState, error) {
	if p.closed.Load() {
		return CosmosHistoryState{}, errors.New("cosmos history: provider is closed")
	}
	telemetry.MarkUsed(telemetry.FeatureAzureCosmos)
	if err := ctx.Err(); err != nil {
		return CosmosHistoryState{}, err
	}
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	var state *CosmosHistoryState
	ok, err := session.Get(p.stateKey, &state)
	if err != nil {
		return CosmosHistoryState{}, err
	}
	initialize := !ok || state == nil
	if initialize {
		state = p.stateInitializer(session)
		if state == nil {
			return CosmosHistoryState{}, errors.New("cosmos history: stateInitializer returned nil")
		}
	}
	if strings.TrimSpace(state.ConversationID) == "" {
		return CosmosHistoryState{}, errors.New("cosmos history: conversation ID must be nonblank")
	}
	if initialize {
		session.Set(p.stateKey, state)
	}
	return *state, nil
}

func (p *CosmosHistoryProvider) partitionKey(state CosmosHistoryState) azcosmos.PartitionKey {
	if p.partitionKeyFactory != nil {
		return p.partitionKeyFactory(state)
	}
	if state.TenantID != nil && state.UserID != nil {
		return azcosmos.NewPartitionKeyString(*state.TenantID).AppendString(*state.UserID).AppendString(state.ConversationID)
	}
	return azcosmos.NewPartitionKeyString(state.ConversationID)
}

func (p *CosmosHistoryProvider) queryOptions(state CosmosHistoryState) *azcosmos.QueryOptions {
	return &azcosmos.QueryOptions{
		PageSizeHint:              p.maxItemCount,
		EnableCrossPartitionQuery: new(false),
		QueryParameters: []azcosmos.QueryParameter{
			{Name: "@conversationId", Value: state.ConversationID},
			{Name: "@type", Value: cosmosHistoryMessageType},
		},
	}
}

func (p *CosmosHistoryProvider) store(ctx context.Context, invoked agent.InvokedContext) error {
	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	state, err := p.loadState(ctx, session)
	if err != nil {
		return err
	}
	messages := slices.Concat(invoked.RequestMessages, invoked.ResponseMessages)
	if len(messages) == 0 {
		return nil
	}
	timestamp := time.Now().Unix()
	documents := make([][]byte, len(messages))
	for i, msg := range messages {
		if msg == nil {
			return fmt.Errorf("cosmos history: message %d is nil", i)
		}
		serialized, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("cosmos history: serialize message %d: %w", i, err)
		}
		document := cosmosHistoryDocument{
			ID: uuid.NewString(), ConversationID: state.ConversationID,
			Timestamp: timestamp, MessageID: msg.ID, Role: msg.Role,
			Message: string(serialized), Type: cosmosHistoryMessageType,
			TenantID: state.TenantID, UserID: state.UserID, SessionID: state.ConversationID,
		}
		if p.messageTTLSeconds != 0 {
			document.TTL = &p.messageTTLSeconds
		}
		documents[i], err = json.Marshal(document)
		if err != nil {
			return fmt.Errorf("cosmos history: serialize document %d: %w", i, err)
		}
	}
	key := p.partitionKey(state)
	if len(documents) == 1 {
		return p.createSingle(ctx, key, documents[0])
	}
	for chunk := range slices.Chunk(documents, p.maxBatchSize) {
		if err := p.createBatch(ctx, key, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (p *CosmosHistoryProvider) createSingle(ctx context.Context, key azcosmos.PartitionKey, document []byte) error {
	_, err := p.container.CreateItem(ctx, key, document, nil)
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok && responseErr.StatusCode == http.StatusRequestEntityTooLarge {
		return fmt.Errorf("cosmos history: message exceeds the Cosmos DB maximum item size of 2 MB: %w", err)
	}
	if err != nil {
		return fmt.Errorf("cosmos history: create message: %w", err)
	}
	return nil
}

func (p *CosmosHistoryProvider) createBatch(ctx context.Context, key azcosmos.PartitionKey, documents [][]byte) error {
	batch := p.container.NewTransactionalBatch(key)
	for _, document := range documents {
		batch.CreateItem(document, nil)
	}
	err := p.executeBatch(ctx, batch)
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok && responseErr.StatusCode == http.StatusRequestEntityTooLarge {
		if len(documents) == 1 {
			return p.createSingle(ctx, key, documents[0])
		}
		midpoint := len(documents) / 2
		if err := p.createBatch(ctx, key, documents[:midpoint]); err != nil {
			return err
		}
		return p.createBatch(ctx, key, documents[midpoint:])
	}
	return err
}

func (p *CosmosHistoryProvider) executeBatch(ctx context.Context, batch azcosmos.TransactionalBatch) error {
	response, err := p.container.ExecuteTransactionalBatch(ctx, batch, nil)
	if err != nil {
		return fmt.Errorf("cosmos history: execute transactional batch: %w", err)
	}
	if response.Success {
		return nil
	}
	for i, result := range response.OperationResults {
		if result.StatusCode >= 200 && result.StatusCode < 300 || result.StatusCode == http.StatusFailedDependency {
			continue
		}
		return fmt.Errorf("cosmos history: transactional batch operation %d failed: %w", i, &azcore.ResponseError{
			StatusCode: int(result.StatusCode), ErrorCode: "TransactionalBatchFailed", RawResponse: response.RawResponse,
		})
	}
	return errors.New("cosmos history: transactional batch was not committed")
}

type cosmosHistoryDocument struct {
	ID             string       `json:"id"`
	ConversationID string       `json:"conversationId"`
	Timestamp      int64        `json:"timestamp"`
	MessageID      string       `json:"messageId,omitempty"`
	Role           message.Role `json:"role"`
	Message        string       `json:"message"`
	Type           string       `json:"type"`
	TTL            *int32       `json:"ttl,omitempty"`
	TenantID       *string      `json:"tenantId,omitempty"`
	UserID         *string      `json:"userId,omitempty"`
	SessionID      string       `json:"sessionId"`
}

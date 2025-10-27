// Copyright (c) Microsoft. All rights reserved.

package agent

import "context"

// ContextProvider provides additional context to agents.
type ContextProvider[M ~string | any] interface {
	// GetContext retrieves context based on the current messages.
	GetContext(ctx context.Context, messages ...M) ([]string, error)
}

// AggregateContextProvider combines multiple context providers.
type AggregateContextProvider[M ~string | any] struct {
	providers []ContextProvider[M]
}

// NewAggregateContextProvider creates a new AggregateContextProvider.
func NewAggregateContextProvider[M ~string | any](providers ...ContextProvider[M]) *AggregateContextProvider[M] {
	return &AggregateContextProvider[M]{
		providers: providers,
	}
}

// GetContext retrieves context from all providers.
func (a *AggregateContextProvider[M]) GetContext(ctx context.Context, messages ...M) ([]string, error) {
	var allContext []string
	for _, provider := range a.providers {
		context, err := provider.GetContext(ctx, messages...)
		if err != nil {
			return nil, err
		}
		allContext = append(allContext, context...)
	}
	return allContext, nil
}

// MessageStore persists and retrieves messages.
type MessageStore[M ~string | any] interface {
	// Save stores messages.
	Save(ctx context.Context, threadID string, messages ...M) error

	// Load retrieves messages for a thread.
	Load(ctx context.Context, threadID string) ([]M, error)

	// Delete removes messages for a thread.
	Delete(ctx context.Context, threadID string) error
}

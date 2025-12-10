// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import (
	"context"

	"github.com/microsoft/agent-framework/go/message"
)

// Thread represents a thread identified by a service-managed identifier.
type Thread struct {
	ContextID string
	TaskID    string
}

func (t *Thread) MessagesReceived(ctx context.Context, messages ...*message.Message) error {
	// The thread messages are stored in the service there is nothing to do here,
	// since invoking the service should already update the thread.
	return nil
}

// Copyright (c) Microsoft. All rights reserved.

package toolautocall

import (
	"context"
	"sync"

	"github.com/microsoft/agent-framework-go/message"
)

type injectorKey struct{}

// MessageInjector allows tool implementations to enqueue additional messages
// into the function-call loop so they are sent to the model on the next service
// call, even when the tool itself is not the last tool to be called.
//
// A MessageInjector is only available when the enclosing [Config] has
// [Config.EnableMessageInjection] set to true.  Tools retrieve it from the
// context with [MessageInjectorFromContext].
//
// This type is modelled after the .NET MessageInjectingChatClient / EnqueueMessages API.
type MessageInjector struct {
	mu      sync.Mutex
	pending []*message.Message
}

// EnqueueMessages appends non-nil msgs to the pending queue.  They will be included
// in the next provider call, after the current round of tool results.
// This method is safe to call from concurrent goroutines (e.g. when
// [Config.AllowConcurrentInvocations] is true).
func (mi *MessageInjector) EnqueueMessages(msgs ...*message.Message) {
	if len(msgs) == 0 {
		return
	}
	mi.mu.Lock()
	defer mi.mu.Unlock()
	for _, msg := range msgs {
		if msg != nil {
			mi.pending = append(mi.pending, msg)
		}
	}
}

// drain atomically removes and returns all pending messages.
func (mi *MessageInjector) drain() []*message.Message {
	mi.mu.Lock()
	defer mi.mu.Unlock()
	msgs := mi.pending
	mi.pending = nil
	return msgs
}

// MessageInjectorFromContext returns the [MessageInjector] stored in ctx, or nil if
// message injection is not enabled for the current run.
func MessageInjectorFromContext(ctx context.Context) *MessageInjector {
	v, _ := ctx.Value(injectorKey{}).(*MessageInjector)
	return v
}

func withMessageInjector(ctx context.Context, mi *MessageInjector) context.Context {
	return context.WithValue(ctx, injectorKey{}, mi)
}

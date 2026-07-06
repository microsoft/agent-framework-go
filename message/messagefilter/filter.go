// Copyright (c) Microsoft. All rights reserved.

package messagefilter

import (
	"context"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
)

// Filter represents a function that filters a slice of messages, potentially returning an error.
//
// Implementations are expected to modify/re-slice the provided slice in place
// (for example via messages[:0]) and return the filtered view.
//
// Implementations must only remove messages from the input. They must not
// add new messages that were not already present in the provided slice.
type Filter func(context.Context, []*message.Message) ([]*message.Message, error)

// And composes multiple filters and applies them in order.
//
// Nil filters are skipped. If any filter returns an error, execution stops and
// the error is returned.
func And(filters ...Filter) Filter {
	return func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		current := messages
		for _, filter := range filters {
			if filter == nil {
				continue
			}
			var err error
			current, err = filter(ctx, current)
			if err != nil {
				return nil, err
			}
		}
		return current, nil
	}
}

// Or composes filters and keeps messages that are kept by at least one filter.
//
// Nil filters are skipped. If any filter returns an error, execution stops and
// the error is returned. If no non-nil filters are provided, no messages are kept.
func Or(filters ...Filter) Filter {
	return func(ctx context.Context, messages []*message.Message) ([]*message.Message, error) {
		matched := make(map[*message.Message]struct{}, len(messages))
		scratch := make([]*message.Message, len(messages))
		for _, filter := range filters {
			if filter == nil {
				continue
			}
			copy(scratch, messages)
			filtered, err := filter(ctx, scratch)
			if err != nil {
				return nil, err
			}
			for _, msg := range filtered {
				matched[msg] = struct{}{}
			}
		}
		return slices.DeleteFunc(messages, func(msg *message.Message) bool {
			_, ok := matched[msg]
			return !ok
		}), nil
	}
}

// PassThrough returns all messages unchanged.
func PassThrough(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
	return messages, nil
}

// None returns no messages, reusing the input slice storage when possible.
func None(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
	return messages[:0], nil
}

// ExternalOnly returns only messages whose source type is external.
//
// A zero source type is considered external.
// The filter operates in place by compacting the input slice.
func ExternalOnly(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
	return slices.DeleteFunc(messages, func(msg *message.Message) bool {
		if msg == nil {
			return false
		}
		return msg.Source.Type != message.SourceTypeExternal
	}), nil
}

// Sources returns only messages whose source is in the provided allow list.
func Sources(sources ...message.Source) Filter {
	return func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
		return slices.DeleteFunc(messages, func(msg *message.Message) bool {
			if msg == nil {
				return false
			}
			return !slices.Contains(sources, msg.Source)
		}), nil
	}
}

// NotSourceTypes returns messages whose source type is not in the provided deny list.
func NotSourceTypes(sourceTypes ...message.SourceType) Filter {
	return func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
		return slices.DeleteFunc(messages, func(msg *message.Message) bool {
			if msg == nil {
				return false
			}
			return slices.Contains(sourceTypes, msg.Source.Type)
		}), nil
	}
}

// NotSources returns messages whose source is not in the provided deny list.
func NotSources(sources ...message.Source) Filter {
	return func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
		return slices.DeleteFunc(messages, func(msg *message.Message) bool {
			if msg == nil {
				return false
			}
			return slices.Contains(sources, msg.Source)
		}), nil
	}
}

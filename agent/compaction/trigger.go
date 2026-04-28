// Copyright (c) Microsoft. All rights reserved.

package compaction

// Trigger defines a predicate over message-index metrics used to start or stop compaction.
//
// Triggers are used both to decide whether a strategy should run and, when used as targets,
// to decide when incremental compaction has reduced the index enough.
type Trigger func(*MessageIndex) bool

// Always returns a trigger that always fires regardless of index state.
func Always() Trigger { return func(*MessageIndex) bool { return true } }

// Never returns a trigger that never fires regardless of index state.
func Never() Trigger { return func(*MessageIndex) bool { return false } }

// TokensBelow returns a trigger that fires when the included token count is below maxTokens.
func TokensBelow(maxTokens int) Trigger {
	return func(index *MessageIndex) bool { return index.IncludedTokenCount() < maxTokens }
}

// TokensExceed returns a trigger that fires when the included token count exceeds maxTokens.
func TokensExceed(maxTokens int) Trigger {
	return func(index *MessageIndex) bool { return index.IncludedTokenCount() > maxTokens }
}

// MessagesExceed returns a trigger that fires when the included message count exceeds maxMessages.
func MessagesExceed(maxMessages int) Trigger {
	return func(index *MessageIndex) bool { return index.IncludedMessageCount() > maxMessages }
}

// TurnsExceed returns a trigger that fires when the included user turn count exceeds maxTurns.
//
// A user turn starts with a user group and includes subsequent non-user, non-system groups until
// the next user group or end of conversation.
func TurnsExceed(maxTurns int) Trigger {
	return func(index *MessageIndex) bool { return index.IncludedTurnCount() > maxTurns }
}

// GroupsExceed returns a trigger that fires when the included group count exceeds maxGroups.
func GroupsExceed(maxGroups int) Trigger {
	return func(index *MessageIndex) bool { return index.IncludedGroupCount() > maxGroups }
}

// HasToolCalls returns a trigger that fires when the included index contains a tool-call group.
func HasToolCalls() Trigger {
	return func(index *MessageIndex) bool {
		for _, group := range index.Groups {
			if !group.IsExcluded && group.Kind == GroupKindToolCall {
				return true
			}
		}
		return false
	}
}

// All returns a compound trigger that fires only when every trigger fires.
func All(triggers ...Trigger) Trigger {
	return func(index *MessageIndex) bool {
		for _, trigger := range triggers {
			if trigger == nil || !trigger(index) {
				return false
			}
		}
		return true
	}
}

// Any returns a compound trigger that fires when at least one trigger fires.
func Any(triggers ...Trigger) Trigger {
	return func(index *MessageIndex) bool {
		for _, trigger := range triggers {
			if trigger != nil && trigger(index) {
				return true
			}
		}
		return false
	}
}

// Copyright (c) Microsoft. All rights reserved.

package chatclient_test

import (
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/message"
)

// messageText returns all concatenated text from a message's contents
func messageText(msg *message.Message) string {
	var sb strings.Builder
	for _, c := range msg.Contents {
		if tc, ok := c.(*message.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestNewMessageFromUpdates_SuccessfullyCreatesResponse(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		{
			Role:       message.RoleAssistant,
			Contents:   []message.Content{&message.TextContent{Text: "Hello"}},
			ResponseID: "someResponse",
			MessageID:  "12345",
			CreatedAt:  time.Date(1, 2, 3, 4, 5, 6, 0, time.UTC),
			ModelID:    "model123",
		},
		{
			Role:                 message.RoleAssistant,
			Contents:             []message.Content{&message.TextContent{Text: ", "}},
			AuthorName:           "Someone",
			AdditionalProperties: map[string]any{"a": "b"},
		},
		{
			Contents:             []message.Content{&message.TextContent{Text: "world!"}},
			CreatedAt:            time.Date(2, 2, 3, 4, 5, 6, 0, time.UTC),
			ConversationID:       "123",
			AdditionalProperties: map[string]any{"c": "d"},
		},
		{
			Contents: []message.Content{&message.UsageContent{Details: message.UsageDetails{InputTokenCount: 1, OutputTokenCount: 2}}},
		},
		{
			Contents: []message.Content{&message.UsageContent{Details: message.UsageDetails{InputTokenCount: 4, OutputTokenCount: 5}}},
		},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]

	// Verify usage is aggregated in the message contents
	var inputTokens, outputTokens int64
	for _, c := range msg.Contents {
		if uc, ok := c.(*message.UsageContent); ok {
			inputTokens += uc.Details.InputTokenCount
			outputTokens += uc.Details.OutputTokenCount
		}
	}

	if inputTokens != 5 {
		t.Errorf("expected input token count 5, got %d", inputTokens)
	}
	if outputTokens != 7 {
		t.Errorf("expected output token count 7, got %d", outputTokens)
	}

	if msg.ID != "12345" {
		t.Errorf("expected message ID '12345', got %q", msg.ID)
	}
	expectedCreatedAt := time.Date(2, 2, 3, 4, 5, 6, 0, time.UTC)
	if !msg.CreatedAt.Equal(expectedCreatedAt) {
		t.Errorf("expected created at %v, got %v", expectedCreatedAt, msg.CreatedAt)
	}

	if msg.Role != message.RoleAssistant {
		t.Errorf("expected role Assistant, got %v", msg.Role)
	}
	if msg.AuthorName != "Someone" {
		t.Errorf("expected author name 'Someone', got %q", msg.AuthorName)
	}
	if msg.AdditionalProperties != nil {
		t.Errorf("expected nil additional properties on message, got %v", msg.AdditionalProperties)
	}

	if msg.String() != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got %q", msg.String())
	}
}

func TestNewMessageFromUpdates_RoleOrIdOrAuthorNameChangeDictatesMessageChange(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		{Contents: []message.Content{&message.TextContent{Text: "!"}}, MessageID: "1"},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "a"}}, MessageID: "1"},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "b"}}, MessageID: "2"},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "c"}}, MessageID: "2"},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "d"}}, MessageID: "2"},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "e"}}, MessageID: "3"},
		{Role: message.RoleTool, Contents: []message.Content{&message.TextContent{Text: "f"}}, MessageID: "4"},
		{Role: message.RoleTool, Contents: []message.Content{&message.TextContent{Text: "g"}}, MessageID: "4"},
		{Role: message.RoleTool, Contents: []message.Content{&message.TextContent{Text: "h"}}, MessageID: "5"},
		{Role: message.Role("custom"), Contents: []message.Content{&message.TextContent{Text: "i"}}, MessageID: "6"},
		{Role: message.Role("custom"), Contents: []message.Content{&message.TextContent{Text: "j"}}, MessageID: "6"},
		{Role: message.Role("other"), Contents: []message.Content{&message.TextContent{Text: "k"}}, MessageID: "6"},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)

	// In Go, empty role defaults to Assistant, so going from empty->Assistant doesn't create a boundary
	// This differs from .NET where null vs explicit Assistant are treated differently
	// So we expect one fewer message than .NET (messages[0] and [1] are combined)
	if len(msgs) != 8 {
		t.Logf("Messages:")
		for i, msg := range msgs {
			t.Logf("  [%d] Role=%v, ID=%q, Text=%q", i, msg.Role, msg.ID, messageText(msg))
		}
		t.Fatalf("expected 8 messages, got %d", len(msgs))
	}

	if messageText(msgs[0]) != "!a" {
		t.Errorf("message 0: expected '!a', got %q", messageText(msgs[0]))
	}
	if messageText(msgs[1]) != "b" {
		t.Errorf("message 1: expected 'b', got %q", messageText(msgs[1]))
	}
	if messageText(msgs[2]) != "cd" {
		t.Errorf("message 2: expected 'cd', got %q", messageText(msgs[2]))
	}
	if messageText(msgs[3]) != "e" {
		t.Errorf("message 3: expected 'e', got %q", messageText(msgs[3]))
	}
	if messageText(msgs[4]) != "fg" {
		t.Errorf("message 4: expected 'fg', got %q", messageText(msgs[4]))
	}
	if messageText(msgs[5]) != "h" {
		t.Errorf("message 5: expected 'h', got %q", messageText(msgs[5]))
	}
	if messageText(msgs[6]) != "ij" {
		t.Errorf("message 6: expected 'ij', got %q", messageText(msgs[6]))
	}
	if messageText(msgs[7]) != "k" {
		t.Errorf("message 7: expected 'k', got %q", messageText(msgs[7]))
	}
}

func TestNewMessageFromUpdates_AuthorNameChangeDictatesMessageBoundary(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with AuthorName "Alice"
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Hello "}}, AuthorName: "Alice"},
		{Contents: []message.Content{&message.TextContent{Text: "from "}}, AuthorName: "Alice"},
		{Contents: []message.Content{&message.TextContent{Text: "Alice!"}}},
		// Second message - AuthorName changes to "Bob"
		{Contents: []message.Content{&message.TextContent{Text: "Hi "}}, AuthorName: "Bob"},
		{Contents: []message.Content{&message.TextContent{Text: "from "}}, AuthorName: "Bob"},
		{Contents: []message.Content{&message.TextContent{Text: "Bob!"}}},
		// Third message - AuthorName changes to "Charlie"
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Greetings "}}, AuthorName: "Charlie"},
		{Contents: []message.Content{&message.TextContent{Text: "from Charlie!"}}, AuthorName: "Charlie"},
		// Fourth message - AuthorName changes back to "Alice"
		{Contents: []message.Content{&message.TextContent{Text: "Back to Alice."}}, AuthorName: "Alice"},
		// Continue fourth message - empty AuthorName should not create new message
		{Contents: []message.Content{&message.TextContent{Text: " Still Alice."}}, AuthorName: ""},
		{Contents: []message.Content{&message.TextContent{Text: " And more."}}},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello from Alice!" {
		t.Errorf("message 0: expected 'Hello from Alice!', got %q", msgs[0].String())
	}
	if msgs[0].AuthorName != "Alice" {
		t.Errorf("message 0: expected author 'Alice', got %q", msgs[0].AuthorName)
	}
	if msgs[0].Role != message.RoleAssistant {
		t.Errorf("message 0: expected role Assistant, got %v", msgs[0].Role)
	}

	if msgs[1].String() != "Hi from Bob!" {
		t.Errorf("message 1: expected 'Hi from Bob!', got %q", msgs[1].String())
	}
	if msgs[1].AuthorName != "Bob" {
		t.Errorf("message 1: expected author 'Bob', got %q", msgs[1].AuthorName)
	}
	if msgs[1].Role != message.RoleAssistant {
		t.Errorf("message 1: expected role Assistant, got %v", msgs[1].Role)
	}

	if msgs[2].String() != "Greetings from Charlie!" {
		t.Errorf("message 2: expected 'Greetings from Charlie!', got %q", msgs[2].String())
	}
	if msgs[2].AuthorName != "Charlie" {
		t.Errorf("message 2: expected author 'Charlie', got %q", msgs[2].AuthorName)
	}
	if msgs[2].Role != message.RoleAssistant {
		t.Errorf("message 2: expected role Assistant, got %v", msgs[2].Role)
	}

	if msgs[3].String() != "Back to Alice. Still Alice. And more." {
		t.Errorf("message 3: expected 'Back to Alice. Still Alice. And more.', got %q", msgs[3].String())
	}
	if msgs[3].AuthorName != "Alice" {
		t.Errorf("message 3: expected author 'Alice', got %q", msgs[3].AuthorName)
	}
	if msgs[3].Role != message.RoleAssistant {
		t.Errorf("message 3: expected role Assistant, got %v", msgs[3].Role)
	}
}

func TestNewMessageFromUpdates_AuthorNameWithOtherBoundaries(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// Message 1: Role=Assistant, MessageId="1", AuthorName="Alice"
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "A"}}, MessageID: "1", AuthorName: "Alice"},
		{Contents: []message.Content{&message.TextContent{Text: "B"}}, MessageID: "1", AuthorName: "Alice"},
		// Message 2: AuthorName changes to "Bob", same MessageId and Role
		{Contents: []message.Content{&message.TextContent{Text: "C"}}, MessageID: "1", AuthorName: "Bob"},
		// Message 3: MessageId changes to "2", AuthorName stays "Bob"
		{Contents: []message.Content{&message.TextContent{Text: "D"}}, MessageID: "2", AuthorName: "Bob"},
		{Contents: []message.Content{&message.TextContent{Text: "E"}}, MessageID: "2", AuthorName: "Bob"},
		// Message 4: Role changes to User, AuthorName stays "Bob"
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "F"}}, MessageID: "2", AuthorName: "Bob"},
		// Message 5: All three boundaries change
		{Role: message.RoleTool, Contents: []message.Content{&message.TextContent{Text: "G"}}, MessageID: "3", AuthorName: "Charlie"},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	if msgs[0].String() != "AB" {
		t.Errorf("message 0: expected 'AB', got %q", msgs[0].String())
	}
	if msgs[0].AuthorName != "Alice" {
		t.Errorf("message 0: expected author 'Alice', got %q", msgs[0].AuthorName)
	}

	if msgs[1].String() != "C" {
		t.Errorf("message 1: expected 'C', got %q", msgs[1].String())
	}
	if msgs[1].AuthorName != "Bob" {
		t.Errorf("message 1: expected author 'Bob', got %q", msgs[1].AuthorName)
	}

	if msgs[2].String() != "DE" {
		t.Errorf("message 2: expected 'DE', got %q", msgs[2].String())
	}
	if msgs[2].AuthorName != "Bob" {
		t.Errorf("message 2: expected author 'Bob', got %q", msgs[2].AuthorName)
	}

	if msgs[3].String() != "F" {
		t.Errorf("message 3: expected 'F', got %q", msgs[3].String())
	}
	if msgs[3].AuthorName != "Bob" {
		t.Errorf("message 3: expected author 'Bob', got %q", msgs[3].AuthorName)
	}

	if msgs[4].String() != "G" {
		t.Errorf("message 4: expected 'G', got %q", msgs[4].String())
	}
	if msgs[4].AuthorName != "Charlie" {
		t.Errorf("message 4: expected author 'Charlie', got %q", msgs[4].AuthorName)
	}
}

func TestNewMessageFromUpdates_EmptyOrNullAuthorNameDoesNotCreateBoundary(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with AuthorName "Assistant"
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Hello"}}, AuthorName: "Assistant"},
		// Empty AuthorName should not create new message
		{Contents: []message.Content{&message.TextContent{Text: " world"}}, AuthorName: ""},
		// Null AuthorName should not create new message
		{Contents: []message.Content{&message.TextContent{Text: "!"}}},
		// Another empty AuthorName
		{Contents: []message.Content{&message.TextContent{Text: " How"}}, AuthorName: ""},
		{Contents: []message.Content{&message.TextContent{Text: " are"}}, AuthorName: ""},
		// Null again
		{Contents: []message.Content{&message.TextContent{Text: " you?"}}},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello world! How are you?" {
		t.Errorf("expected 'Hello world! How are you?', got %q", msgs[0].String())
	}
	if msgs[0].AuthorName != "Assistant" {
		t.Errorf("expected author 'Assistant', got %q", msgs[0].AuthorName)
	}
	if msgs[0].Role != message.RoleAssistant {
		t.Errorf("expected role Assistant, got %v", msgs[0].Role)
	}
}

func TestNewMessageFromUpdates_AuthorNameNullToNonNullDoesNotCreateBoundary(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with no AuthorName
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Hello"}}, MessageID: "1"},
		{Contents: []message.Content{&message.TextContent{Text: " there"}}, MessageID: "1"},
		// AuthorName becomes non-empty but doesn't create boundary
		{Contents: []message.Content{&message.TextContent{Text: " I'm Bob"}}, MessageID: "1", AuthorName: "Bob"},
		{Contents: []message.Content{&message.TextContent{Text: " speaking"}}, MessageID: "1", AuthorName: "Bob"},
		// Second message - AuthorName changes to "Alice" creates boundary
		{Contents: []message.Content{&message.TextContent{Text: "Now Alice"}}, MessageID: "1", AuthorName: "Alice"},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello there I'm Bob speaking" {
		t.Errorf("message 0: expected 'Hello there I'm Bob speaking', got %q", msgs[0].String())
	}
	if msgs[0].AuthorName != "Bob" {
		t.Errorf("message 0: expected author 'Bob', got %q", msgs[0].AuthorName)
	}
	if msgs[0].ID != "1" {
		t.Errorf("message 0: expected ID '1', got %q", msgs[0].ID)
	}

	if msgs[1].String() != "Now Alice" {
		t.Errorf("message 1: expected 'Now Alice', got %q", msgs[1].String())
	}
	if msgs[1].AuthorName != "Alice" {
		t.Errorf("message 1: expected author 'Alice', got %q", msgs[1].AuthorName)
	}
	if msgs[1].ID != "1" {
		t.Errorf("message 1: expected ID '1', got %q", msgs[1].ID)
	}
}

func TestNewMessageFromUpdates_MessageIdNullToNonNullDoesNotCreateBoundary(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with no MessageId
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Hello"}}},
		{Contents: []message.Content{&message.TextContent{Text: " there"}}},
		// MessageId becomes non-empty but doesn't create boundary
		{Contents: []message.Content{&message.TextContent{Text: " from"}}, MessageID: "msg1"},
		{Contents: []message.Content{&message.TextContent{Text: " AI"}}, MessageID: "msg1"},
		// Second message - MessageId changes to different value creates boundary
		{Contents: []message.Content{&message.TextContent{Text: "Next message"}}, MessageID: "msg2"},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello there from AI" {
		t.Errorf("message 0: expected 'Hello there from AI', got %q", msgs[0].String())
	}
	if msgs[0].ID != "msg1" {
		t.Errorf("message 0: expected ID 'msg1', got %q", msgs[0].ID)
	}
	if msgs[0].Role != message.RoleAssistant {
		t.Errorf("message 0: expected role Assistant, got %v", msgs[0].Role)
	}

	if msgs[1].String() != "Next message" {
		t.Errorf("message 1: expected 'Next message', got %q", msgs[1].String())
	}
	if msgs[1].ID != "msg2" {
		t.Errorf("message 1: expected ID 'msg2', got %q", msgs[1].ID)
	}
	if msgs[1].Role != message.RoleAssistant {
		t.Errorf("message 1: expected role Assistant, got %v", msgs[1].Role)
	}
}

func TestNewMessageFromUpdates_EmptyMessageIdDoesNotCreateBoundary(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with MessageId
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Hello"}}, MessageID: "msg1"},
		{Contents: []message.Content{&message.TextContent{Text: " world"}}, MessageID: "msg1"},
		// Empty MessageId should not create new message
		{Contents: []message.Content{&message.TextContent{Text: "!"}}, MessageID: ""},
		// Null MessageId should not create new message
		{Contents: []message.Content{&message.TextContent{Text: " How"}}},
		// Another message with empty MessageId
		{Contents: []message.Content{&message.TextContent{Text: " are"}}, MessageID: ""},
		{Contents: []message.Content{&message.TextContent{Text: " you?"}}},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello world! How are you?" {
		t.Errorf("expected 'Hello world! How are you?', got %q", msgs[0].String())
	}
	if msgs[0].ID != "msg1" {
		t.Errorf("expected ID 'msg1', got %q", msgs[0].ID)
	}
	if msgs[0].Role != message.RoleAssistant {
		t.Errorf("expected role Assistant, got %v", msgs[0].Role)
	}
}

func TestNewMessageFromUpdates_RoleNullToNonNullDoesNotCreateBoundary(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with no explicit Role (will default to Assistant)
		{Contents: []message.Content{&message.TextContent{Text: "Hello"}}, MessageID: "1"},
		{Contents: []message.Content{&message.TextContent{Text: " there"}}, MessageID: "1"},
		// Role becomes explicit Assistant - shouldn't create boundary
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: " from"}}, MessageID: "1"},
		{Contents: []message.Content{&message.TextContent{Text: " AI"}}, MessageID: "1"},
		// Second message - Role changes to User creates boundary
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "User message"}}, MessageID: "1"},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello there from AI" {
		t.Errorf("message 0: expected 'Hello there from AI', got %q", msgs[0].String())
	}
	if msgs[0].Role != message.RoleAssistant {
		t.Errorf("message 0: expected role Assistant, got %v", msgs[0].Role)
	}
	if msgs[0].ID != "1" {
		t.Errorf("message 0: expected ID '1', got %q", msgs[0].ID)
	}

	if msgs[1].String() != "User message" {
		t.Errorf("message 1: expected 'User message', got %q", msgs[1].String())
	}
	if msgs[1].Role != message.RoleUser {
		t.Errorf("message 1: expected role User, got %v", msgs[1].Role)
	}
	if msgs[1].ID != "1" {
		t.Errorf("message 1: expected ID '1', got %q", msgs[1].ID)
	}
}

func TestNewMessageFromUpdates_CustomRolesCreateBoundaries(t *testing.T) {
	updates := []*chatclient.ChatResponseUpdate{
		// First message with custom role "agent1"
		{Role: message.Role("agent1"), Contents: []message.Content{&message.TextContent{Text: "Hello"}}, MessageID: "1"},
		{Contents: []message.Content{&message.TextContent{Text: " from"}}, MessageID: "1"},
		{Role: message.Role("agent1"), Contents: []message.Content{&message.TextContent{Text: " agent1"}}, MessageID: "1"},
		// Second message - custom role changes to "agent2"
		{Role: message.Role("agent2"), Contents: []message.Content{&message.TextContent{Text: "Hi"}}, MessageID: "1"},
		{Contents: []message.Content{&message.TextContent{Text: " from"}}, MessageID: "1"},
		{Role: message.Role("agent2"), Contents: []message.Content{&message.TextContent{Text: " agent2"}}, MessageID: "1"},
		// Third message - changes to standard role
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Assistant here"}}, MessageID: "1"},
	}

	msgs := chatclient.NewMessageFromUpdates(updates)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	if msgs[0].String() != "Hello from agent1" {
		t.Errorf("message 0: expected 'Hello from agent1', got %q", msgs[0].String())
	}
	if msgs[0].Role != message.Role("agent1") {
		t.Errorf("message 0: expected role 'agent1', got %v", msgs[0].Role)
	}

	if msgs[1].String() != "Hi from agent2" {
		t.Errorf("message 1: expected 'Hi from agent2', got %q", msgs[1].String())
	}
	if msgs[1].Role != message.Role("agent2") {
		t.Errorf("message 1: expected role 'agent2', got %v", msgs[1].Role)
	}

	if msgs[2].String() != "Assistant here" {
		t.Errorf("message 2: expected 'Assistant here', got %q", msgs[2].String())
	}
	if msgs[2].Role != message.RoleAssistant {
		t.Errorf("message 2: expected role Assistant, got %v", msgs[2].Role)
	}
}

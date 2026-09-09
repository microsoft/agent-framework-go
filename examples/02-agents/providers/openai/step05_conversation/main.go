// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/conversations"
)

var model = cmp.Or(strings.TrimSpace(os.Getenv("OPENAI_CHAT_MODEL_NAME")), "gpt-5.4-mini")

func main() {
	if err := run(context.Background()); err != nil {
		demo.Panic(err)
	}
}

func run(ctx context.Context) error {
	client := openai.NewClient()

	conversation, err := client.Conversations.New(ctx, conversations.ConversationNewParams{})
	if err != nil {
		return fmt.Errorf("create conversation: %w", err)
	}
	cleanupConversation := true
	defer func() {
		if cleanupConversation {
			_, _ = client.Conversations.Delete(ctx, conversation.ID)
		}
	}()

	a := openaiprovider.NewResponsesAgent(
		client,
		openaiprovider.AgentConfig{
			Model:        model,
			Instructions: "You are a helpful assistant.",
			Config: agent.Config{
				Name: "ConversationAgent",
			},
		},
	)
	session, err := a.CreateSession(ctx, agent.WithServiceID(conversation.ID))
	if err != nil {
		return fmt.Errorf("create agent session: %w", err)
	}

	fmt.Println("=== Multi-turn Conversation Demo ===")
	for _, prompt := range []string{
		"What is the capital of France?",
		"What famous landmarks are located there?",
		"How tall is the most famous one?",
	} {
		if err := runTurn(ctx, a, session, prompt); err != nil {
			return err
		}
	}
	fmt.Println("=== End of Conversation ===")

	storedConversation, err := client.Conversations.Get(ctx, conversation.ID)
	if err != nil {
		return fmt.Errorf("get conversation: %w", err)
	}
	fmt.Printf("Conversation created.\n    Conversation ID: %s\n\n", storedConversation.ID)

	fmt.Println("Full Conversation History:")
	pager := client.Conversations.Items.ListAutoPaging(ctx, conversation.ID, conversations.ItemListParams{
		Order: conversations.ItemListParamsOrderAsc,
	})
	for pager.Next() {
		item := pager.Current()
		if item.Type != "message" {
			continue
		}
		msg := item.AsMessage()
		fmt.Printf("    Message ID: %s\n", msg.ID)
		fmt.Printf("    Message Role: %s\n", msg.Role)
		for _, content := range msg.Content {
			if content.Text != "" {
				fmt.Printf("    Message Text: %s\n", content.Text)
			}
		}
		fmt.Println()
	}
	if err := pager.Err(); err != nil {
		return fmt.Errorf("list conversation items: %w", err)
	}

	deleted, err := client.Conversations.Delete(ctx, conversation.ID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	cleanupConversation = false
	fmt.Printf("Conversation deleted.\n    Deleted: %t\n", deleted.Deleted)
	return nil
}

func runTurn(ctx context.Context, a *agent.Agent, session *agent.Session, prompt string) error {
	fmt.Printf("User: %s\n", prompt)
	resp, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
	if err != nil {
		return fmt.Errorf("run turn %q: %w", prompt, err)
	}
	fmt.Printf("Assistant: %s\n\n", resp)
	return nil
}

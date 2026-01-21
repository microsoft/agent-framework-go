// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"3rd-Party Thread Storage",
	"Demonstrates how to use a custom message store to persist conversation history to disk.",
	"Model", "gpt-4o-mini",
)

func main() {
	// Create a temporary directory to store messages.
	tmpDir, err := os.MkdirTemp("", "agent_thread_storage")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the agent with a custom message store that persists messages to disk.
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		NewMessageStore: func() memory.MessageStore {
			return &fsMessageStore{Dir: tmpDir}
		},
	})

	ctx := context.Background()

	// Start a new thread for the agent conversation.
	thread, err := a.NewThread(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with the thread that stores conversation history in the disk store.
	demo.Response(agent.RunText(ctx, a, "Tell me a joke about a pirate.", agentopt.Thread(thread)))

	// Serialize the thread state, so it can be stored for later use.
	// Since the chat history is stored in the disk store, the serialized thread
	// only contains the ID that the messages are stored under in the store.
	serializedThread, err := json.MarshalIndent(thread, "", "\t")
	if err != nil {
		demo.Panic(err)
	}
	fmt.Println("\n--- Serialized thread ---")
	fmt.Println(string(serializedThread))

	// The serialized thread can now be saved to a database, file, or any other storage mechanism
	// and loaded again later.

	// Deserialize the thread state after loading from storage.
	resumedThread, err := a.UnmarshalThread(serializedThread)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with the thread that stores conversation history in the vector store a second time.
	demo.Response(agent.RunText(ctx, a, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agentopt.Thread(resumedThread)))
}

type fsMessageStore struct {
	Dir   string
	Files []string
}

func (d *fsMessageStore) Add(ctx context.Context, msgs ...*message.Message) error {
	for _, msg := range msgs {
		if msg.ID == "" {
			// Skip messages without an ID.
			continue
		}
		if slices.Contains(d.Files, msg.ID) {
			return fmt.Errorf("duplicated message %q", msg.ID)
		}
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d.Dir, msg.ID), data, 0644); err != nil {
			return err
		}
		d.Files = append(d.Files, msg.ID)
	}
	return nil
}

func (d *fsMessageStore) All(ctx context.Context) iter.Seq2[*message.Message, error] {
	return func(yield func(*message.Message, error) bool) {
		for _, file := range d.Files {
			data, err := os.ReadFile(filepath.Join(d.Dir, file))
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}
			var msg message.Message
			err = json.Unmarshal(data, &msg)
			if !yield(&msg, err) {
				return
			}
		}
	}
}

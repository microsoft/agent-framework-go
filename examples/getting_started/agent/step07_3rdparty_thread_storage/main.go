// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates third-party thread storage and message persistence
// using a filesystem-based message store for agent conversations.

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
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
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
	}, chatagent.Options{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
		NewMessageStore: func() memory.MessageStore {
			return &fsMessageStore{Dir: tmpDir}
		},
	})

	ctx := context.Background()

	// Start a new thread for the agent conversation.
	thread := a.NewThread()

	// Run the agent with the thread that stores conversation history in the disk store.
	fmt.Println(agent.RunText(ctx, a, "Tell me a joke about a pirate.", agent.WithThread(thread)))

	// Serialize the thread state, so it can be stored for later use.
	// Since the chat history is stored in the disk store, the serialized thread
	// only contains the ID that the messages are stored under in the store.
	serializedThread, err := json.MarshalIndent(thread, "", "\t")
	if err != nil {
		panic(err)
	}
	fmt.Println("\n--- Serialized thread ---")
	fmt.Println(string(serializedThread))

	// The serialized thread can now be saved to a database, file, or any other storage mechanism
	// and loaded again later.

	// Deserialize the thread state after loading from storage.
	resumedThread, err := a.UnmarshalThread(serializedThread)
	if err != nil {
		panic(err)
	}

	// Run the agent with the thread that stores conversation history in the vector store a second time.
	fmt.Println(agent.RunText(ctx, a, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithThread(resumedThread)))
}

type fsMessageStore struct {
	Dir   string
	Files []string
}

func (d *fsMessageStore) Add(ctx context.Context, msgs ...*message.Message) error {
	for _, msg := range msgs {
		if msg.ID == "" {
			// Skip messages without an ID.
			return nil
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

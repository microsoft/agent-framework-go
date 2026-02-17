// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"3rd-Party Session Storage",
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
		Instructions:           "You are good at telling jokes.",
		Name:                   "Joker",
		RunOptions:             []agentopt.RunOption{middleware.With(logger)}, // for logging agent interactions
		MessageHistoryProvider: &fsMessageStore{Dir: tmpDir},
	})

	ctx := context.Background()

	// Start a new session for the agent conversation.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with the session that stores conversation history in the disk store.
	resp, err := a.RunText("Tell me a joke about a pirate.", agentopt.Session(session)).Collect(ctx)
	demo.Response(resp, err)

	// Serialize the session state, so it can be stored for later use.
	// Since the chat history is stored in the disk store, the serialized session
	// only contains the ID that the messages are stored under in the store.
	serializedSession, err := json.MarshalIndent(session, "", "\t")
	if err != nil {
		demo.Panic(err)
	}
	fmt.Println("\n--- Serialized session ---")
	fmt.Println(string(serializedSession))

	// The serialized session can now be saved to a database, file, or any other storage mechanism
	// and loaded again later.

	// Deserialize the session state after loading from storage.
	resumedSession, err := a.UnmarshalSession(ctx, serializedSession)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with the session that stores conversation history in the disk store a second time.
	resp, err = a.RunText("Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agentopt.Session(resumedSession)).Collect(ctx)
	demo.Response(resp, err)
}

type fsMessageStore struct {
	Dir string
}

func (d *fsMessageStore) getFiles(ctx *memory.InvokingContext) []string {
	if ctx.Session == nil {
		return nil
	}
	v, ok := ctx.Session.GetStateBag().Get("fsMessageStore.files")
	if !ok {
		return nil
	}
	files, _ := v.([]string)
	return files
}

func (d *fsMessageStore) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
	var msgs []*message.Message
	for _, file := range d.getFiles(ctx) {
		data, err := os.ReadFile(filepath.Join(d.Dir, file))
		if err != nil {
			return nil, err
		}
		var msg message.Message
		err = json.Unmarshal(data, &msg)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, &msg)
	}
	return &memory.Context{Messages: msgs}, nil
}

func (d *fsMessageStore) Invoked(ctx *memory.InvokedContext) error {
	if ctx.InvokeError != nil {
		return nil
	}
	var files []string
	if ctx.Session != nil {
		v, _ := ctx.Session.GetStateBag().Get("fsMessageStore.files")
		files, _ = v.([]string)
	}
	persist := func(msg *message.Message) error {
		if msg.ID == "" {
			// Skip messages without an ID.
			return nil
		}
		if slices.Contains(files, msg.ID) {
			return fmt.Errorf("duplicated message %q", msg.ID)
		}
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d.Dir, msg.ID), data, 0644); err != nil {
			return err
		}
		files = append(files, msg.ID)
		return nil
	}
	for _, msg := range ctx.RequestMessages {
		if err := persist(msg); err != nil {
			return err
		}
	}
	for _, msg := range ctx.ResponseMessages {
		if err := persist(msg); err != nil {
			return err
		}
	}
	if ctx.Session != nil {
		ctx.Session.GetStateBag().Set("fsMessageStore.files", files)
	}
	return nil
}

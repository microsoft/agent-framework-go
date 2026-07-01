// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"3rd-Party Session Storage",
	"Demonstrates how to use a custom message store to persist conversation history to disk.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	// Create a temporary directory to store messages.
	tmpDir, err := os.MkdirTemp("", "agent_session_storage")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create Microsoft Foundry agent with a custom message store that persists messages to disk.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:            "Joker",
				HistoryProvider: newFSHistoryProvider(tmpDir),
				Middlewares:     []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	// Start a new session for the agent conversation.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with the session that stores conversation history in the disk store.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Serialize the session state, so it can be stored for later use.
	// The disk store holds the chat history.
	// The serialized session only contains the message-store ID.
	serializedSession, err := json.Marshal(session)
	if err != nil {
		demo.Panic(err)
	}
	fmt.Println("\n--- Serialized session ---")
	fmt.Println(string(serializedSession))

	// The serialized session can now be saved and loaded again later.

	// Deserialize the session state after loading from storage.
	var resumedSession agent.Session
	if err := json.Unmarshal(serializedSession, &resumedSession); err != nil {
		demo.Panic(err)
	}

	// Run the agent with the session that stores conversation history in the disk store a second time.
	resp, err = a.RunText(ctx, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithSession(&resumedSession)).Collect()
	demo.Response(resp, err)
}

type fsMessageStore struct {
	Dir string
}

func newFSHistoryProvider(dir string) agent.HistoryProvider {
	store := &fsMessageStore{Dir: dir}
	return agent.NewHistoryProvider(agent.HistoryProviderConfig{
		SourceID: "fsMessageStore",
		Provide:  store.provideMessages,
		Store:    store.persistMessages,
	})
}

func (d *fsMessageStore) getFiles(session *agent.Session) []string {
	if session == nil {
		return nil
	}
	var files []string
	ok, err := session.Get("fsMessageStore.files", &files)
	if !ok || err != nil {
		return nil
	}
	return files
}

func (d *fsMessageStore) loadMessages(session *agent.Session) ([]*message.Message, error) {
	var msgs []*message.Message
	for _, file := range d.getFiles(session) {
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
	return msgs, nil
}

func (d *fsMessageStore) provideMessages(_ context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	if session == nil {
		return nil, nil
	}
	history, err := d.loadMessages(session)
	if err != nil {
		return nil, err
	}
	return history, nil
}

func (d *fsMessageStore) persistMessages(_ context.Context, invoked agent.InvokedContext) error {
	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	if session == nil {
		return nil
	}
	var files []string
	_, _ = session.Get("fsMessageStore.files", &files)
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
		if err := os.WriteFile(filepath.Join(d.Dir, msg.ID), data, 0o644); err != nil {
			return err
		}
		files = append(files, msg.ID)
		return nil
	}
	for _, msg := range invoked.RequestMessages {
		if err := persist(msg); err != nil {
			return err
		}
	}
	for _, msg := range invoked.ResponseMessages {
		if err := persist(msg); err != nil {
			return err
		}
	}
	session.Set("fsMessageStore.files", files)
	return nil
}

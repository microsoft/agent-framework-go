// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

var logger = demo.NewLogger(
	"3rd-Party Session Storage",
	"Demonstrates how to use a custom message store to persist conversation history to disk.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	// Create a temporary directory to store messages.
	tmpDir, err := os.MkdirTemp("", "agent_session_storage")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create Azure OpenAI agent with a custom message store that persists messages to disk.
	a := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: "You are good at telling jokes.",
				Name:         "Joker",
				Middlewares: []agent.Middleware{
					logger,                       // for logging agent interactions
					&fsMessageStore{Dir: tmpDir}, // for persistent message history
				},
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
	serializedSession, err := json.MarshalIndent(session, "", "\t")
	if err != nil {
		demo.Panic(err)
	}
	fmt.Println("\n--- Serialized session ---")
	fmt.Println(string(serializedSession))

	// The serialized session can now be saved and loaded again later.

	// Deserialize the session state after loading from storage.
	resumedSession, err := a.UnmarshalSession(ctx, serializedSession)
	if err != nil {
		demo.Panic(err)
	}

	// Run the agent with the session that stores conversation history in the disk store a second time.
	resp, err = a.RunText(ctx, "Now tell the same joke in the voice of a pirate, and add some emojis to the joke.", agent.WithSession(resumedSession)).Collect()
	demo.Response(resp, err)
}

type fsMessageStore struct {
	Dir string
}

func (d *fsMessageStore) getFiles(session agent.Session) []string {
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

func (d *fsMessageStore) loadMessages(session agent.Session) ([]*message.Message, error) {
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

func (d *fsMessageStore) persistMessages(session agent.Session, requestMessages, responseMessages []*message.Message) error {
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
	for _, msg := range requestMessages {
		if err := persist(msg); err != nil {
			return err
		}
	}
	for _, msg := range responseMessages {
		if err := persist(msg); err != nil {
			return err
		}
	}
	session.Set("fsMessageStore.files", files)
	return nil
}

func (d *fsMessageStore) Run(next agent.RunFunc, ctx context.Context, msgs []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	var session agent.Session
	if v, ok := agent.GetOption(opts, agent.WithSession); ok {
		session = v
	} else {
		// If no session is provided, we cannot persist messages, so just pass through to next middleware.
		return next(ctx, msgs, opts...)
	}
	history, err := d.loadMessages(session)
	if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	messagesForClient := append(history, msgs...)

	return func(yield func(*message.ResponseUpdate, error) bool) {
		var resp message.Response
		for update, err := range next(ctx, messagesForClient, opts...) {
			if err != nil {
				yield(nil, err)
				return
			}
			resp.Update(update)
			if !yield(update, nil) {
				return
			}
		}
		resp.Coalesce()
		if err := d.persistMessages(session, msgs, resp.Messages); err != nil {
			yield(nil, err)
		}
	}
}

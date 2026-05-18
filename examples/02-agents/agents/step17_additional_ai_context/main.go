// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to inject additional AI context into an agent using custom ContextProvider components.
// Multiple providers can be attached to an agent, and they will be called in sequence, each receiving the
// accumulated context from the previous one. This mechanism can be used for various purposes, such as injecting
// RAG search results or memories into the agent's context.

package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

var logger = demo.NewLogger(
	"Additional AI Context",
	"Demonstrates how to use context providers to inject todo-list and calendar context.",
	"Model", deployment,
)

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	// Create Azure OpenAI agent with multiple context providers.
	a := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model: deployment,
			Instructions: `You are a helpful personal assistant.
You manage a TODO list for the user. When the user has completed one of the tasks it can be removed from the TODO list. Only provide the list of TODO items if asked.
You remind users of upcoming calendar events when the user interacts with you.`,
			Config: agent.Config{
				Name:            "PersonalAssistant",
				HistoryProvider: newChatHistoryProvider(),
				ContextProviders: []*agent.ContextProvider{
					newTodoListContextProvider(),
					newCalendarSearchContextProvider(loadNextThreeCalendarEvents),
				},
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	// Invoke the agent and output the text result.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	prompts := []string{
		"I need to pick up milk from the supermarket.",
		"I need to take Sally for soccer practice.",
		"I need to make a dentist appointment for Jimmy.",
		"I've taken Sally to soccer practice.",
	}
	for _, prompt := range prompts {
		resp, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
		demo.Response(resp, err)
	}

	// Serialize the session. It contains the chat history plus state serialized by the context providers.
	serializedSession, err := json.Marshal(session)
	if err != nil {
		demo.Panic(err)
	}
	var prettySession bytes.Buffer
	if err := json.Indent(&prettySession, serializedSession, "", "  "); err != nil {
		demo.Panic(err)
	}
	fmt.Println(prettySession.String())

	// The serialized session can be stored long term in a persistent store. Here we deserialize it and continue.
	var resumedSession agent.Session
	if err := json.Unmarshal(serializedSession, &resumedSession); err != nil {
		demo.Panic(err)
	}
	session = &resumedSession

	resp, err := a.RunText(ctx, "Considering my appointments, can you create a plan for my day that plans out when I should complete the items on my todo list?", agent.WithSession(session)).Collect()
	demo.Response(resp, err)
}

func loadNextThreeCalendarEvents(context.Context) ([]string, error) {
	// In a real implementation, this function would connect to a calendar service.
	return []string{
		"Doctor's appointment today at 15:00",
		"Team meeting today at 17:00",
		"Birthday party today at 20:00",
	}, nil
}

const (
	chatHistorySourceID    = "chat_history"
	todoListSourceID       = "todo_list"
	calendarSearchSourceID = "calendar_search"
)

func newChatHistoryProvider() *agent.HistoryProvider {
	historyProvider := agent.NewInMemoryHistoryProvider(chatHistorySourceID)
	// Use StoreRequestFilter to provide a custom filter for request messages stored in chat history.
	// By default, the history provider stores request messages that did not come from chat history.
	// In this case, we explicitly exclude messages from chat history and AI context providers.
	// You may want to store these messages, depending on their content and your requirements.
	historyProvider.StoreRequestFilter = messagefilter.NotSources(
		chatHistorySourceID,
		todoListSourceID,
		calendarSearchSourceID,
	)
	return historyProvider
}

type todoListState struct {
	Items []string `json:"items,omitempty"`
}

type addTodoItemArgs struct {
	Item string `json:"item"`
}

type removeTodoItemArgs struct {
	Index int `json:"index"`
}

func newTodoListContextProvider() *agent.ContextProvider {
	return &agent.ContextProvider{
		SourceID: todoListSourceID,
		Provide:  provideTodoListContext,
	}
}

func getTodoListState(session *agent.Session) todoListState {
	if session == nil {
		return todoListState{}
	}
	var state todoListState
	_, _ = session.Get(todoListSourceID, &state)
	return state
}

func setTodoListState(session *agent.Session, state todoListState) {
	if session != nil {
		session.Set(todoListSourceID, state)
	}
}

func provideTodoListContext(_ context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	session, _ := agent.GetOption(options, agent.WithSession)
	state := getTodoListState(session)

	var output strings.Builder
	output.WriteString("Your todo list contains the following items:\n")
	if len(state.Items) == 0 {
		output.WriteString("  (no items)\n")
	} else {
		for i, item := range state.Items {
			_, _ = fmt.Fprintf(&output, "%d. %s\n", i, item)
		}
	}

	addTodoItemTool := functool.MustNew(functool.Config{
		Name:        "AddTodoItem",
		Description: "Adds an item to the todo list.",
	}, func(_ tool.Context, args addTodoItemArgs) (string, error) {
		if strings.TrimSpace(args.Item) == "" {
			return "", fmt.Errorf("item must have a value")
		}
		state := getTodoListState(session)
		state.Items = append(state.Items, args.Item)
		setTodoListState(session, state)
		return fmt.Sprintf("Added todo item: %s", args.Item), nil
	})
	removeTodoItemTool := functool.MustNew(functool.Config{
		Name:        "RemoveTodoItem",
		Description: "Removes an item from the todo list. Index is zero based.",
	}, func(_ tool.Context, args removeTodoItemArgs) (string, error) {
		state := getTodoListState(session)
		if args.Index < 0 || args.Index >= len(state.Items) {
			return "", fmt.Errorf("todo item index %d is out of range", args.Index)
		}
		removed := state.Items[args.Index]
		state.Items = append(state.Items[:args.Index], state.Items[args.Index+1:]...)
		setTodoListState(session, state)
		return fmt.Sprintf("Removed todo item: %s", removed), nil
	})

	messages = append(messages, &message.Message{
		Role: message.RoleUser,
		Contents: []message.Content{
			&message.TextContent{Text: output.String()},
		},
	})
	options = append(options, agent.WithTool(addTodoItemTool), agent.WithTool(removeTodoItemTool))
	return messages, options, nil
}

func newCalendarSearchContextProvider(loadNextThreeCalendarEvents func(context.Context) ([]string, error)) *agent.ContextProvider {
	return &agent.ContextProvider{
		SourceID: calendarSearchSourceID,
		Provide: func(ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			events, err := loadNextThreeCalendarEvents(ctx)
			if err != nil {
				return nil, nil, err
			}

			var output strings.Builder
			output.WriteString("You have the following upcoming calendar events:\n")
			for _, calendarEvent := range events {
				_, _ = fmt.Fprintf(&output, " - %s\n", calendarEvent)
			}

			messages = append(messages, &message.Message{
				Role: message.RoleUser,
				Contents: []message.Content{
					&message.TextContent{Text: output.String()},
				},
			})
			return messages, options, nil
		},
	}
}

// Copyright (c) Microsoft. All rights reserved.

// Package todo provides a context provider that gives agents todo list
// management tools for tracking work items during long-running complex tasks.
//
// The provider exposes five tools to the agent: TodoList_Add,
// TodoList_Complete, TodoList_Remove, TodoList_GetRemaining, and
// TodoList_GetAll.
//
// Todo state is stored in the agent session and persists across invocations.
package todo

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const stateKey = "todoProviderState"

const defaultInstructions = `## Todo Items

You have access to a todo list for tracking work items.
While planning, make sure that you break down complex tasks into manageable todo items and add them to the list.
Ask questions from the user where clarification is needed to create effective todos.
If the user provides feedback on your plan, adjust your todos accordingly by adding new items or removing irrelevant ones.
During execution, use the todo list to keep track of what needs to be done, mark items as complete when finished, and remove any items that are no longer needed.
When a user changes the topic or changes their mind, ensure that you update the todo list accordingly by removing irrelevant items or adding new ones as needed.

Use these tools to manage your tasks:
- Use TodoList_Add to break down complex work into trackable items (supports adding one or many at once).
- Use TodoList_Complete to mark items as done when finished (supports one or many at once).
- Use TodoList_GetRemaining to check what work is still pending.
- Use TodoList_GetAll to review the full list including completed items.
- Use TodoList_Remove to remove items that are no longer needed (supports one or many at once).`

// Item represents a single todo item.
type Item struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	IsComplete  bool   `json:"isComplete"`
}

// ItemInput is the input structure for adding a todo item.
type ItemInput struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type state struct {
	NextID int    `json:"nextId"`
	Items  []Item `json:"items"`
}

// Options configures the todo provider.
type Options struct {
	// Instructions overrides the default instructions provided to the agent.
	Instructions string

	// SuppressTodoListMessage, when true, prevents injecting the current todo
	// list summary message on each invocation.
	SuppressTodoListMessage bool

	// TodoListMessageBuilder, if set, is used to format the todo list summary
	// message instead of the default formatter.
	TodoListMessageBuilder func([]Item) string
}

// Provider is an agent context provider that manages todo items.
// Use [New] to create. The embedded [agent.ContextProvider] can be used
// directly in agent configuration.
type Provider struct {
	agent.ContextProvider
	instructions           string
	suppressTodoMessage    bool
	todoListMessageBuilder func([]Item) string
	sessionLocks           sync.Map // map[sessionKey]*sync.Mutex
	nullSessionLock        sync.Mutex
}

// New creates a new todo provider with the given options.
// If opts is nil, defaults are used.
func New(opts *Options) *Provider {
	p := &Provider{
		instructions: defaultInstructions,
	}
	if opts != nil {
		if opts.Instructions != "" {
			p.instructions = opts.Instructions
		}
		p.suppressTodoMessage = opts.SuppressTodoListMessage
		p.todoListMessageBuilder = opts.TodoListMessageBuilder
	}

	p.ContextProvider = agent.ContextProvider{
		SourceID: "TodoProvider",
		Provide:  p.provide,
	}
	return p
}

// GetAllItems returns all todo items from the session state.
func (p *Provider) GetAllItems(opts ...agent.Option) []Item {
	mu := p.getSessionLock(opts)
	mu.Lock()
	defer mu.Unlock()
	st := p.loadState(opts)
	result := make([]Item, len(st.Items))
	copy(result, st.Items)
	return result
}

// GetRemainingItems returns only the incomplete todo items from the session state.
func (p *Provider) GetRemainingItems(opts ...agent.Option) []Item {
	mu := p.getSessionLock(opts)
	mu.Lock()
	defer mu.Unlock()
	st := p.loadState(opts)
	var remaining []Item
	for _, item := range st.Items {
		if !item.IsComplete {
			remaining = append(remaining, item)
		}
	}
	return remaining
}

func (p *Provider) loadState(opts []agent.Option) *state {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return &state{}
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		return &s
	}
	return &state{}
}

func (p *Provider) saveState(opts []agent.Option, s *state) {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || s == nil {
		return
	}
	session.Set(stateKey, *s)
}

// getSessionLock returns a per-session mutex. If no session is available,
// a shared fallback lock is returned. This matches the .NET pattern of
// per-session SemaphoreSlim via ConditionalWeakTable.
func (p *Provider) getSessionLock(opts []agent.Option) *sync.Mutex {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return &p.nullSessionLock
	}
	key := session.ServiceID()
	if key == "" {
		key = "_default"
	}
	v, _ := p.sessionLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (p *Provider) provide(ctx context.Context, messages []*message.Message, opts ...agent.Option) ([]*message.Message, []agent.Option, error) {
	tools := p.createTools(opts)

	outOpts := slices.Clone(opts)
	for _, t := range tools {
		outOpts = append(outOpts, agent.WithTool(t))
	}

	// Add instructions.
	outOpts = append(outOpts, agent.WithInstructions(p.instructions))

	outMessages := messages

	// Inject current todo list summary so the agent sees outstanding work.
	if !p.suppressTodoMessage {
		mu := p.getSessionLock(opts)
		mu.Lock()
		st := p.loadState(opts)
		mu.Unlock()

		var todoMsg string
		if p.todoListMessageBuilder != nil {
			todoMsg = p.todoListMessageBuilder(st.Items)
		} else {
			todoMsg = formatTodoListMessage(st.Items)
		}
		outMessages = make([]*message.Message, 0, len(messages)+1)
		outMessages = append(outMessages, message.NewText(todoMsg))
		outMessages = append(outMessages, messages...)
	}

	return outMessages, outOpts, nil
}

func (p *Provider) createTools(opts []agent.Option) []tool.FuncTool {
	addTool := functool.MustNew(
		functool.Config{
			Name:        "TodoList_Add",
			Description: "Add one or more todo items. Each item has a title and an optional description. Returns the list of created todo items.",
		},
		func(ctx tool.Context, input []ItemInput) ([]Item, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			var created []Item
			for _, in := range input {
				item := Item{
					ID:    st.NextID,
					Title: strings.TrimSpace(in.Title),
				}
				if in.Description != "" {
					item.Description = strings.TrimSpace(in.Description)
				}
				st.NextID++
				st.Items = append(st.Items, item)
				created = append(created, item)
			}
			p.saveState(opts, st)
			return created, nil
		},
	)

	completeTool := functool.MustNew(
		functool.Config{
			Name:        "TodoList_Complete",
			Description: "Mark one or more todo items as complete by their IDs. Returns the number of items that were found and marked complete.",
		},
		func(ctx tool.Context, ids []int) (int, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			idSet := make(map[int]struct{}, len(ids))
			for _, id := range ids {
				idSet[id] = struct{}{}
			}
			completed := 0
			for i := range st.Items {
				if _, ok := idSet[st.Items[i].ID]; ok && !st.Items[i].IsComplete {
					st.Items[i].IsComplete = true
					completed++
				}
			}
			if completed > 0 {
				p.saveState(opts, st)
			}
			return completed, nil
		},
	)

	removeTool := functool.MustNew(
		functool.Config{
			Name:        "TodoList_Remove",
			Description: "Remove one or more todo items by their IDs. Returns the number of items that were found and removed.",
		},
		func(ctx tool.Context, ids []int) (int, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			idSet := make(map[int]struct{}, len(ids))
			for _, id := range ids {
				idSet[id] = struct{}{}
			}
			var remaining []Item
			removed := 0
			for _, item := range st.Items {
				if _, ok := idSet[item.ID]; ok {
					removed++
				} else {
					remaining = append(remaining, item)
				}
			}
			if removed > 0 {
				st.Items = remaining
				p.saveState(opts, st)
			}
			return removed, nil
		},
	)

	getRemainingTool := functool.MustNew(
		functool.Config{
			Name:        "TodoList_GetRemaining",
			Description: "Retrieve the list of incomplete todo items.",
		},
		func(ctx tool.Context, _ struct{}) ([]Item, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			var remaining []Item
			for _, item := range st.Items {
				if !item.IsComplete {
					remaining = append(remaining, item)
				}
			}
			return remaining, nil
		},
	)

	getAllTool := functool.MustNew(
		functool.Config{
			Name:        "TodoList_GetAll",
			Description: "Retrieve the full list of todo items, both complete and incomplete.",
		},
		func(ctx tool.Context, _ struct{}) ([]Item, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			return st.Items, nil
		},
	)

	return []tool.FuncTool{addTool, completeTool, removeTool, getRemainingTool, getAllTool}
}

func formatTodoListMessage(items []Item) string {
	if len(items) == 0 {
		return "### Current todo list\n- none yet"
	}
	var sb strings.Builder
	sb.WriteString("### Current todo list\n")
	for _, item := range items {
		status := "open"
		if item.IsComplete {
			status = "done"
		}
		fmt.Fprintf(&sb, "- %d [%s] %s", item.ID, status, item.Title)
		if item.Description != "" {
			fmt.Fprintf(&sb, ": %s", item.Description)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

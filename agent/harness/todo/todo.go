// Copyright (c) Microsoft. All rights reserved.

// Package todo provides a context provider that gives agents todo list
// management tools for tracking work items during long-running complex tasks.
//
// The provider exposes five tools to the agent: todos_add,
// todos_complete, todos_remove, todos_get_remaining, and
// todos_get_all.
//
// Todo state is stored in the agent session and persists across invocations.
package todo

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const stateKey = "todoProviderState"

const defaultInstructions = `## Todo Items

You have access to a todo list for tracking work items.
When a user asks you to perform a task, follow these steps to manage your work:
1. Determine whether the ask requires multiple steps to complete (complex) or can be completed using a single step (simple).
2. If complex, turn the task into manageable todo items and add them to the list.
3. If simple, don't add a todo item, but rather just complete the task directly.

### General TODO Guidelines
Ask questions from the user where clarification is needed to create effective todos.
If the user provides feedback on your plan, adjust your todos accordingly by adding new items or removing irrelevant/old ones.
During execution, use the todo list to keep track of what needs to be done, mark items as complete when finished, and remove any items that are no longer needed.
When a user changes the topic, changes their mind or switches to a new request, ensure that you update the todo list accordingly by removing irrelevant/old items, clearing the list, or adding new ones as needed.

Use these tools to manage your tasks:
- Use todos_add to break down complex work into trackable items (supports adding one or many at once).
- Use todos_complete to mark items as done when finished (supports one or many at once). Include a reason describing how the items were completed.
- Use todos_get_remaining to check what work is still pending.
- Use todos_get_all to review the full list including completed items.
- Use todos_remove to remove items that are no longer needed (supports one or many at once).`

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

// CompleteInput is the input structure for completing a single todo item.
// It carries the item ID and a reason describing how or why the item was completed.
type CompleteInput struct {
	ID     int    `json:"id"`
	Reason string `json:"reason"`
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
// Use [New] to create. Provider can be used directly in agent configuration.
type Provider struct {
	provider               agent.ContextProvider
	instructions           string
	suppressTodoMessage    bool
	todoListMessageBuilder func([]Item) string
	sessionLocks           sync.Map // map[weak.Pointer[agent.Session]]*sync.Mutex
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

	p.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "TodoProvider",
		Provide:  p.provide,
	})
	return p
}

func (p *Provider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.provider.Invoking(ctx, invoking)
}

func (p *Provider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.provider.Invoked(ctx, invoked)
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
	return remainingItems(st.Items)
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

// getSessionLock returns a per-session mutex that guards the todo state against
// concurrent tool invocations, which toolautocall runs on separate goroutines
// when AllowConcurrentInvocations is enabled.
//
// The registry is keyed by session object identity via a weak pointer, not by
// Session.ServiceID(): a service ID may be empty (causing unrelated sessions to
// collide on a shared lock), shared across distinct sessions, or mutated during
// a session's lifetime — any of which would break the guarantee that a given
// session always maps to the same lock. Identity keying also matches the .NET
// provider, which keys its per-session lock by object identity via
// ConditionalWeakTable. When no session is available, a shared fallback lock is
// returned so state access is still serialized.
//
// Weak keys do not keep sessions alive and do not remove map entries on their
// own, so a runtime cleanup deletes the entry once the session is collected,
// keeping the registry from growing unbounded.
func (p *Provider) getSessionLock(opts []agent.Option) *sync.Mutex {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || session == nil {
		return &p.nullSessionLock
	}
	key := weak.Make(session)
	if existing, ok := p.sessionLocks.Load(key); ok {
		return existing.(*sync.Mutex)
	}
	actual, loaded := p.sessionLocks.LoadOrStore(key, &sync.Mutex{})
	if !loaded {
		// First registration for this session: drop the entry when the session
		// is garbage collected. The cleanup captures only the weak key, never
		// the session itself, or it would keep the session alive.
		runtime.AddCleanup(session, func(k weak.Pointer[agent.Session]) {
			p.sessionLocks.Delete(k)
		}, key)
	}
	return actual.(*sync.Mutex)
}

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	opts := invoking.Options
	tools := p.createTools(opts)

	var outOpts []agent.Option
	for _, t := range tools {
		outOpts = append(outOpts, agent.WithTool(t))
	}

	// Add instructions.
	outOpts = append(outOpts, agent.WithInstructions(p.instructions))

	var outMessages []*message.Message

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
		outMessages = append(outMessages, message.NewText(todoMsg))
	}

	return outMessages, outOpts, nil
}

func (p *Provider) createTools(opts []agent.Option) []tool.FuncTool {
	addTool := functool.MustNew(
		functool.Config{
			Name:        "todos_add",
			Description: "Add one or more todo items. Each item has a title and an optional description. Returns the list of created todo items.",
		},
		func(ctx context.Context, input []ItemInput) ([]Item, error) {
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
			Name:        "todos_complete",
			Description: "Mark one or more todo items as complete. Each entry has an ID and a reason describing how/why the item was completed. Returns the number of items that were found and marked complete.",
		},
		func(ctx context.Context, items []CompleteInput) (int, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			idSet := make(map[int]struct{}, len(items))
			for _, item := range items {
				idSet[item.ID] = struct{}{}
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
			Name:        "todos_remove",
			Description: "Remove one or more todo items by their IDs. Returns the number of items that were found and removed.",
		},
		func(ctx context.Context, ids []int) (int, error) {
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
			Name:        "todos_get_remaining",
			Description: "Retrieve the list of incomplete todo items.",
		},
		func(ctx context.Context, _ struct{}) ([]Item, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			return remainingItems(st.Items), nil
		},
	)

	getAllTool := functool.MustNew(
		functool.Config{
			Name:        "todos_get_all",
			Description: "Retrieve the full list of todo items, both complete and incomplete.",
		},
		func(ctx context.Context, _ struct{}) ([]Item, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			return st.Items, nil
		},
	)

	return []tool.FuncTool{addTool, completeTool, removeTool, getRemainingTool, getAllTool}
}

func remainingItems(items []Item) []Item {
	var remaining []Item
	for _, item := range items {
		if !item.IsComplete {
			remaining = append(remaining, item)
		}
	}
	return remaining
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

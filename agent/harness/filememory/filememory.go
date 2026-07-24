// Copyright (c) Microsoft. All rights reserved.

// Package filememory provides a context provider that gives agents a small
// session-scoped virtual file store for persisting notes, scratch data, and
// intermediate results across turns of a long-running task.
//
// The provider exposes five tools to the agent: file_memory_write,
// file_memory_read, file_memory_delete, file_memory_ls, and
// file_memory_grep.
//
// File contents live in an in-memory store that is persisted in the agent
// session state and therefore survive across invocations of the same session.
// This mirrors the .NET Harness FileMemoryProvider, which backs its
// file_memory_* tools with a pluggable AgentFileStore; here the default store
// is a self-contained in-memory implementation with no external dependencies.
package filememory

import (
	"context"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"weak"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const stateKey = "fileMemoryProviderState"

const defaultInstructions = `## File Memory

You have access to a session-scoped virtual file store for persisting notes,
scratch data, and intermediate results while working on a task. Files written
here persist across turns of the current session.

Use these tools to manage your files:
- Use file_memory_write to create or overwrite a file with the given content.
- Use file_memory_read to read back the content of a previously written file.
- Use file_memory_delete to remove a file that is no longer needed.
- Use file_memory_ls to list the paths of all files currently stored.
- Use file_memory_grep to find files whose content matches a regular expression.`

// AgentFileStore is the storage backend used by the provider. It is a small,
// self-contained interface so the provider does not depend on any shared or
// not-yet-existing package. The default implementation is an in-memory store
// created by [newMemoryStore]; the whole store is serialized into the session
// state so it persists across invocations.
type AgentFileStore interface {
	// Write creates or overwrites the file at path with the given content.
	Write(path, content string)
	// Read returns the content of the file at path and whether it exists.
	Read(path string) (string, bool)
	// Delete removes the file at path and reports whether it existed.
	Delete(path string) bool
	// List returns the paths of all stored files, sorted lexicographically.
	List() []string
	// Grep returns the paths of files whose content matches the compiled
	// pattern, sorted lexicographically.
	Grep(pattern *regexp.Regexp) []string
}

// memoryStore is the default in-memory AgentFileStore. It is also the shape
// persisted in the session state bag.
type memoryStore struct {
	Files map[string]string `json:"files"`
}

func newMemoryStore() *memoryStore {
	return &memoryStore{Files: map[string]string{}}
}

func (s *memoryStore) Write(path, content string) {
	if s.Files == nil {
		s.Files = map[string]string{}
	}
	s.Files[path] = content
}

func (s *memoryStore) Read(path string) (string, bool) {
	content, ok := s.Files[path]
	return content, ok
}

func (s *memoryStore) Delete(path string) bool {
	if _, ok := s.Files[path]; !ok {
		return false
	}
	delete(s.Files, path)
	return true
}

func (s *memoryStore) List() []string {
	paths := make([]string, 0, len(s.Files))
	for p := range s.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func (s *memoryStore) Grep(pattern *regexp.Regexp) []string {
	var matched []string
	for p, content := range s.Files {
		if pattern.MatchString(content) {
			matched = append(matched, p)
		}
	}
	sort.Strings(matched)
	return matched
}

// Ensure the default store satisfies the exported interface.
var _ AgentFileStore = (*memoryStore)(nil)

// WriteInput is the input for the file_memory_write tool.
type WriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// GrepInput is the input for the file_memory_grep tool.
type GrepInput struct {
	Pattern string `json:"pattern"`
}

// Options configures the file memory provider.
type Options struct {
	// Instructions overrides the default instructions provided to the agent.
	Instructions string
}

// Provider is an agent context provider that manages session-scoped files.
// Use [New] to create. Provider can be used directly in agent configuration.
type Provider struct {
	provider     agent.ContextProvider
	instructions string

	sessionLocks    sync.Map // map[weak.Pointer[agent.Session]]*sync.Mutex
	nullSessionLock sync.Mutex
}

// New creates a new file memory provider with the given options.
// If opts is nil, defaults are used.
func New(opts *Options) *Provider {
	p := &Provider{
		instructions: defaultInstructions,
	}
	if opts != nil && opts.Instructions != "" {
		p.instructions = opts.Instructions
	}

	p.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "FileMemoryProvider",
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

// List returns the paths of all files stored in the session, sorted.
func (p *Provider) List(opts ...agent.Option) []string {
	mu := p.getSessionLock(opts)
	mu.Lock()
	defer mu.Unlock()
	return p.loadStore(opts).List()
}

func (p *Provider) loadStore(opts []agent.Option) *memoryStore {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return newMemoryStore()
	}
	var s memoryStore
	if found, _ := session.Get(stateKey, &s); found {
		if s.Files == nil {
			s.Files = map[string]string{}
		}
		return &s
	}
	return newMemoryStore()
}

func (p *Provider) saveStore(opts []agent.Option, s *memoryStore) {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || s == nil {
		return
	}
	session.Set(stateKey, *s)
}

// getSessionLock returns a per-session mutex that guards the file store against
// concurrent tool invocations, which toolautocall runs on separate goroutines
// when AllowConcurrentInvocations is enabled. It follows the same identity
// keying as the todo provider: a weak pointer to the session object, with a
// runtime cleanup that drops the entry once the session is collected, and a
// shared fallback lock when no session is available.
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
		runtime.AddCleanup(session, func(k weak.Pointer[agent.Session]) {
			p.sessionLocks.Delete(k)
		}, key)
	}
	return actual.(*sync.Mutex)
}

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	opts := invoking.Options
	tools := p.createTools(opts)

	outOpts := make([]agent.Option, 0, len(tools)+1)
	for _, t := range tools {
		outOpts = append(outOpts, agent.WithTool(t))
	}
	outOpts = append(outOpts, agent.WithInstructions(p.instructions))

	return nil, outOpts, nil
}

func (p *Provider) createTools(opts []agent.Option) []tool.FuncTool {
	writeTool := functool.MustNew(
		functool.Config{
			Name:        "file_memory_write",
			Description: "Create or overwrite a file in session memory with the given content. Returns the path that was written.",
		},
		func(ctx context.Context, input WriteInput) (string, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			store := p.loadStore(opts)
			store.Write(input.Path, input.Content)
			p.saveStore(opts, store)
			return input.Path, nil
		},
	)

	readTool := functool.MustNew(
		functool.Config{
			Name:        "file_memory_read",
			Description: "Read the content of a file previously written to session memory. Returns an empty string if the file does not exist.",
		},
		func(ctx context.Context, path string) (string, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			content, _ := p.loadStore(opts).Read(path)
			return content, nil
		},
	)

	deleteTool := functool.MustNew(
		functool.Config{
			Name:        "file_memory_delete",
			Description: "Delete a file from session memory. Returns true if the file existed and was removed.",
		},
		func(ctx context.Context, path string) (bool, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			store := p.loadStore(opts)
			removed := store.Delete(path)
			if removed {
				p.saveStore(opts, store)
			}
			return removed, nil
		},
	)

	lsTool := functool.MustNew(
		functool.Config{
			Name:        "file_memory_ls",
			Description: "List the paths of all files currently stored in session memory.",
		},
		func(ctx context.Context, _ struct{}) ([]string, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			return p.loadStore(opts).List(), nil
		},
	)

	grepTool := functool.MustNew(
		functool.Config{
			Name:        "file_memory_grep",
			Description: "Find files in session memory whose content matches the given regular expression. Returns the matching paths.",
		},
		func(ctx context.Context, input GrepInput) ([]string, error) {
			re, err := regexp.Compile(input.Pattern)
			if err != nil {
				return nil, err
			}
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			return p.loadStore(opts).Grep(re), nil
		},
	)

	return []tool.FuncTool{writeTool, readTool, deleteTool, lsTool, grepTool}
}

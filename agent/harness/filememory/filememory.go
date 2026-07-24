// Copyright (c) Microsoft. All rights reserved.

// Package filememory provides a context provider that exposes session-scoped
// file-based memory tools to agents.
package filememory

import (
	"context"
	"fmt"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	stateKey          = "fileMemoryProviderState"
	descriptionSuffix = "_description.md"
	indexFileName     = "memories.md"
	maxIndexEntries   = 50

	// WriteToolName writes or overwrites a memory file.
	WriteToolName = "file_memory_write"
	// ReadToolName reads a memory file.
	ReadToolName = "file_memory_read"
	// DeleteToolName deletes a memory file.
	DeleteToolName = "file_memory_delete"
	// LsToolName lists memory files.
	LsToolName = "file_memory_ls"
	// GrepToolName searches memory files by regex.
	GrepToolName = "file_memory_grep"
	// ReplaceToolName replaces substrings within a memory file.
	ReplaceToolName = "file_memory_replace"
	// ReplaceLinesToolName replaces whole lines within a memory file.
	ReplaceLinesToolName = "file_memory_replace_lines"
)

const defaultInstructions = `## File Based Memory

You have access to a session-scoped, file-based memory system via the ` + "`file_memory_*`" + ` tools for storing and retrieving information across interactions.
These files act as your working memory for the current session and are isolated from other sessions.
Use these tools to store plans, memories, processing results, or downloaded data.

- Use descriptive file names (for example, "projectarchitecture.md" or "userpreferences.md").
- Include a description when writing a file to help with future discovery.
- Before starting new tasks, use file_memory_ls and file_memory_grep to check for relevant existing memories to avoid duplicate work.
- Keep memories up-to-date by overwriting files when information changes, or by using file_memory_replace and file_memory_replace_lines to make small edits.
- When you receive large amounts of data (for example, downloaded pages, API responses, or research results), write them to files if they will be required later, so they remain available even if older context is compacted or truncated.`

// State represents the session state persisted by [Provider].
type State struct {
	WorkingFolder string `json:"workingFolder"`
}

// Options configures a [Provider].
type Options struct {
	// Instructions overrides the default usage instructions injected into the run.
	Instructions string
}

// ListEntry represents a memory file returned by the ls tool.
type ListEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// Provider is a session-scoped file memory context provider.
type Provider struct {
	provider         agent.ContextProvider
	store            filestore.FileStore
	instructions     string
	stateInitializer func(*agent.Session) State

	sessionLocks    sync.Map // map[weak.Pointer[agent.Session]]*sync.Mutex
	nullSessionLock sync.Mutex
}

// New creates a file memory provider backed by store.
//
// When stateInitializer is nil, new sessions default to an empty working folder.
// When opts is nil, default instructions are used.
func New(store filestore.FileStore, stateInitializer func(*agent.Session) State, opts *Options) *Provider {
	if store == nil {
		panic("filememory: store is required")
	}
	p := &Provider{
		store:        store,
		instructions: defaultInstructions,
	}
	if stateInitializer != nil {
		p.stateInitializer = stateInitializer
	} else {
		p.stateInitializer = func(*agent.Session) State { return State{} }
	}
	if opts != nil && strings.TrimSpace(opts.Instructions) != "" {
		p.instructions = opts.Instructions
	}
	p.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "FileMemoryProvider",
		Provide:  p.provide,
	})
	return p
}

// Invoking runs the provider before an agent invocation.
func (p *Provider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.provider.Invoking(ctx, invoking)
}

// Invoked runs the provider after an agent invocation.
func (p *Provider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.provider.Invoked(ctx, invoked)
}

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	opts := invoking.Options
	state := p.loadState(opts)
	if err := p.store.CreateDirectory(ctx, state.WorkingFolder); err != nil {
		return nil, nil, err
	}

	outOpts := make([]agent.Option, 0, 8)
	for _, tl := range p.createTools(opts) {
		outOpts = append(outOpts, agent.WithTool(tl))
	}
	outOpts = append(outOpts, agent.WithInstructions(p.instructions))

	indexContent, found, err := p.store.Read(ctx, resolvePath(state.WorkingFolder, indexFileName))
	if err != nil {
		return nil, nil, err
	}
	if !found || strings.TrimSpace(indexContent) == "" {
		return nil, outOpts, nil
	}

	return []*message.Message{
		message.NewText(
			"The following is your memory index — a list of files you have previously written. " +
				"You can read any of these files using the file_memory_read tool.\n\n" +
				indexContent,
		),
	}, outOpts, nil
}

func (p *Provider) createTools(opts []agent.Option) []tool.FuncTool {
	writeTool := functool.MustNew(functool.Config{
		Name:        WriteToolName,
		Description: "Write a memory file with the given file_name and content. Overwrites the file if it already exists. Include a description for large files to provide a summary that helps with future discovery.",
	}, func(ctx context.Context, input struct {
		FileName    string `json:"file_name" jsonschema:"The name of the file to write."`
		Content     string `json:"content" jsonschema:"The content to write to the file."`
		Description string `json:"description,omitempty" jsonschema:"An optional description of the file contents for discovery."`
	}) (string, error) {
		normalized, err := validateMemoryFileName(input.FileName)
		if err != nil {
			return "", err
		}
		state := p.loadState(opts)
		mu := p.getSessionLock(opts)
		mu.Lock()
		defer mu.Unlock()

		if err := p.store.Write(ctx, resolvePath(state.WorkingFolder, normalized), input.Content); err != nil {
			return "", err
		}
		descPath := resolvePath(state.WorkingFolder, descriptionFileName(normalized))
		if strings.TrimSpace(input.Description) != "" {
			if err := p.store.Write(ctx, descPath, input.Description); err != nil {
				return "", err
			}
		} else if _, err := p.store.Delete(ctx, descPath); err != nil {
			return "", err
		}
		if err := p.rebuildMemoryIndex(ctx, state); err != nil {
			return "", err
		}
		if strings.TrimSpace(input.Description) == "" {
			return fmt.Sprintf("File %q written.", input.FileName), nil
		}
		return fmt.Sprintf("File %q written with description.", input.FileName), nil
	})

	readTool := functool.MustNew(functool.Config{
		Name:        ReadToolName,
		Description: "Read the content of a memory file by file_name. Returns the file content or a message indicating the file was not found.",
	}, func(ctx context.Context, input struct {
		FileName string `json:"file_name" jsonschema:"The name of the file to read."`
	}) (string, error) {
		normalized, err := validateMemoryFileName(input.FileName)
		if err != nil {
			return "", err
		}
		state := p.loadState(opts)
		content, found, err := p.store.Read(ctx, resolvePath(state.WorkingFolder, normalized))
		if err != nil {
			return "", err
		}
		if !found {
			return fmt.Sprintf("File %q not found.", input.FileName), nil
		}
		return content, nil
	})

	deleteTool := functool.MustNew(functool.Config{
		Name:        DeleteToolName,
		Description: "Delete a memory file by file_name. Also removes its companion description file if one exists.",
	}, func(ctx context.Context, input struct {
		FileName string `json:"file_name" jsonschema:"The name of the file to delete."`
	}) (string, error) {
		normalized, err := validateMemoryFileName(input.FileName)
		if err != nil {
			return "", err
		}
		state := p.loadState(opts)
		mu := p.getSessionLock(opts)
		mu.Lock()
		defer mu.Unlock()

		deleted, err := p.store.Delete(ctx, resolvePath(state.WorkingFolder, normalized))
		if err != nil {
			return "", err
		}
		if _, err := p.store.Delete(ctx, resolvePath(state.WorkingFolder, descriptionFileName(normalized))); err != nil {
			return "", err
		}
		if err := p.rebuildMemoryIndex(ctx, state); err != nil {
			return "", err
		}
		if !deleted {
			return fmt.Sprintf("File %q not found.", input.FileName), nil
		}
		return fmt.Sprintf("File %q deleted.", input.FileName), nil
	})

	lsTool := functool.MustNew(functool.Config{
		Name:        LsToolName,
		Description: "List all memory files with their descriptions, if available. Optionally filter file names with glob_pattern. Internal files are not shown.",
	}, func(ctx context.Context, input struct {
		GlobPattern string `json:"glob_pattern,omitempty" jsonschema:"Optional glob pattern such as '*.md' matched against file names."`
	}) ([]ListEntry, error) {
		state := p.loadState(opts)
		children, err := p.store.ListChildren(ctx, state.WorkingFolder)
		if err != nil {
			return nil, err
		}
		pattern := strings.TrimSpace(input.GlobPattern)
		results := make([]ListEntry, 0)
		for _, entry := range children {
			if entry.Type != filestore.EntryTypeFile || isInternalFile(entry.Name) || !matchPattern(entry.Name, pattern) {
				continue
			}
			description, found, err := p.store.Read(ctx, resolvePath(state.WorkingFolder, descriptionFileName(entry.Name)))
			if err != nil {
				return nil, err
			}
			item := ListEntry{Name: entry.Name, Type: filestore.EntryTypeFile}
			if found {
				item.Description = description
			}
			results = append(results, item)
		}
		slices.SortFunc(results, func(a, b ListEntry) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		return results, nil
	})

	grepTool := functool.MustNew(functool.Config{
		Name:        GrepToolName,
		Description: "Search memory file contents using a case-insensitive regex_pattern. Optionally filter which files are searched using glob_pattern.",
	}, func(ctx context.Context, input struct {
		RegexPattern string `json:"regex_pattern" jsonschema:"A regular expression pattern to match against file contents (case-insensitive)."`
		GlobPattern  string `json:"glob_pattern,omitempty" jsonschema:"Optional glob pattern to filter which files are searched."`
	}) ([]filestore.SearchResult, error) {
		state := p.loadState(opts)
		results, err := p.store.Search(ctx, state.WorkingFolder, input.RegexPattern, input.GlobPattern, false)
		if err != nil {
			return nil, err
		}
		filtered := make([]filestore.SearchResult, 0, len(results))
		for _, result := range results {
			if isInternalFile(result.FileName) {
				continue
			}
			filtered = append(filtered, result)
		}
		return filtered, nil
	})

	replaceTool := functool.MustNew(functool.Config{
		Name:        ReplaceToolName,
		Description: "Replace occurrences of old_string with new_string in a memory file. Fails if old_string is not found, or if it occurs more than once and replace_all is false.",
	}, func(ctx context.Context, input struct {
		FileName   string `json:"file_name" jsonschema:"The name of the file to modify."`
		OldString  string `json:"old_string" jsonschema:"The substring to find and replace."`
		NewString  string `json:"new_string" jsonschema:"The replacement text."`
		ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"When true, replace every occurrence instead of requiring exactly one match."`
	}) (string, error) {
		normalized, err := validateMemoryFileName(input.FileName)
		if err != nil {
			return "", err
		}
		state := p.loadState(opts)
		mu := p.getSessionLock(opts)
		mu.Lock()
		defer mu.Unlock()

		content, found, err := p.store.Read(ctx, resolvePath(state.WorkingFolder, normalized))
		if err != nil {
			return "", err
		}
		if !found {
			return fmt.Sprintf("File %q not found.", input.FileName), nil
		}
		newContent, count, err := filestore.ApplyReplace(content, input.OldString, input.NewString, input.ReplaceAll)
		if err != nil {
			return "", err
		}
		if err := p.store.Write(ctx, resolvePath(state.WorkingFolder, normalized), newContent); err != nil {
			return "", err
		}
		return fmt.Sprintf("Replaced %d occurrence(s) in %q.", count, input.FileName), nil
	})

	replaceLinesTool := functool.MustNew(functool.Config{
		Name:        ReplaceLinesToolName,
		Description: "Replace lines in a memory file. Provide edits with a 1-based line_number and a literal new_line. An empty new_line deletes the line.",
	}, func(ctx context.Context, input struct {
		FileName string               `json:"file_name" jsonschema:"The name of the file to modify."`
		Edits    []filestore.LineEdit `json:"edits" jsonschema:"The list of line edits to apply."`
	}) (string, error) {
		normalized, err := validateMemoryFileName(input.FileName)
		if err != nil {
			return "", err
		}
		state := p.loadState(opts)
		mu := p.getSessionLock(opts)
		mu.Lock()
		defer mu.Unlock()

		content, found, err := p.store.Read(ctx, resolvePath(state.WorkingFolder, normalized))
		if err != nil {
			return "", err
		}
		if !found {
			return fmt.Sprintf("File %q not found.", input.FileName), nil
		}
		newContent, err := filestore.ApplyReplaceLines(content, input.Edits)
		if err != nil {
			return "", err
		}
		if err := p.store.Write(ctx, resolvePath(state.WorkingFolder, normalized), newContent); err != nil {
			return "", err
		}
		return fmt.Sprintf("Replaced %d line(s) in %q.", len(input.Edits), input.FileName), nil
	})

	return []tool.FuncTool{writeTool, readTool, deleteTool, lsTool, grepTool, replaceTool, replaceLinesTool}
}

func (p *Provider) rebuildMemoryIndex(ctx context.Context, state State) error {
	children, err := p.store.ListChildren(ctx, state.WorkingFolder)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(children))
	for _, entry := range children {
		if entry.Type == filestore.EntryTypeFile {
			names = append(names, entry.Name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})

	var sb strings.Builder
	sb.WriteString("# Memory Index\n\n")
	count := 0
	for _, name := range names {
		if isInternalFile(name) {
			continue
		}
		if count >= maxIndexEntries {
			break
		}
		description, found, err := p.store.Read(ctx, resolvePath(state.WorkingFolder, descriptionFileName(name)))
		if err != nil {
			return err
		}
		if found && strings.TrimSpace(description) != "" {
			fmt.Fprintf(&sb, "- **%s**: %s\n", name, description)
		} else {
			fmt.Fprintf(&sb, "- **%s**\n", name)
		}
		count++
	}
	return p.store.Write(ctx, resolvePath(state.WorkingFolder, indexFileName), sb.String())
}

func (p *Provider) loadState(opts []agent.Option) State {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || session == nil {
		return p.stateInitializer(nil)
	}
	var state State
	if found, _ := session.Get(stateKey, &state); found {
		return state
	}
	state = p.stateInitializer(session)
	session.Set(stateKey, state)
	return state
}

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

func validateMemoryFileName(fileName string) (string, error) {
	if strings.TrimSpace(fileName) == "" {
		return "", fmt.Errorf("file_name must not be empty")
	}
	normalized := strings.Trim(strings.ReplaceAll(fileName, "\\", "/"), "/")
	if normalized == "" {
		return "", fmt.Errorf("file_name must not be empty")
	}
	if strings.Contains(normalized, "/") {
		return "", fmt.Errorf("memory files must not be written into a subdirectory; choose a flat file name without path separators")
	}
	if normalized == "." || normalized == ".." || strings.HasPrefix(fileName, "/") || strings.HasPrefix(fileName, "\\") {
		return "", fmt.Errorf("invalid file_name %q", fileName)
	}
	if isInternalFile(normalized) {
		return "", fmt.Errorf("the provided file name is reserved by the system for internal use")
	}
	return normalized, nil
}

func descriptionFileName(fileName string) string {
	if dot := strings.LastIndexByte(fileName, '.'); dot > 0 {
		return fileName[:dot] + descriptionSuffix
	}
	return fileName + descriptionSuffix
}

func isInternalFile(fileName string) bool {
	lower := strings.ToLower(fileName)
	return lower == indexFileName || strings.HasSuffix(lower, strings.ToLower(descriptionSuffix))
}

func resolvePath(workingFolder, fileName string) string {
	base := strings.Trim(strings.ReplaceAll(workingFolder, "\\", "/"), "/")
	name := strings.Trim(strings.ReplaceAll(fileName, "\\", "/"), "/")
	if base == "" {
		return name
	}
	if name == "" {
		return base
	}
	return base + "/" + name
}

func matchPattern(name, pattern string) bool {
	if pattern == "" {
		return true
	}
	matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	return err == nil && matched
}

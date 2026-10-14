// Copyright (c) Microsoft. All rights reserved.

// Package filememory provides a context provider with tools for persistent
// file-based agent memory.
//
// Memory files have flat names within a working folder supplied by session
// state. The default folder is the store root, so sessions share memories unless
// a StateInitializer selects separate folders. Descriptions are stored in hidden
// companion files named by appending "_description.md" to the full memory
// filename. A hidden memories.md index summarizes up to 50 files and is supplied
// to the agent before each invocation when present.
//
// Searches are non-recursive and use Go regular expressions and case-insensitive
// filename patterns. Filename patterns support literal text and * wildcards;
// ? and brackets are literal characters. Backslashes are path separators, not
// escape characters. A leading **/ can match a direct file, and *.* matches all
// filenames, including those without extensions. Search matches and line edits
// share 1-based line numbering and preserve each line's LF, CRLF, or CR terminator.
package filememory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
	"github.com/microsoft/agent-framework-go/internal/storepaths"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// Names of the tools supplied by the provider.
const (
	WriteToolName        = "file_memory_write"
	ReadFileToolName     = "file_memory_read"
	DeleteFileToolName   = "file_memory_delete"
	LsToolName           = "file_memory_ls"
	GrepToolName         = "file_memory_grep"
	ReplaceToolName      = "file_memory_replace"
	ReplaceLinesToolName = "file_memory_replace_lines"
)

const (
	stateKey          = "fileMemoryProviderState"
	descriptionSuffix = "_description.md"
	memoryIndexName   = "memories.md"
	maxIndexEntries   = 50
)

const defaultInstructions = `## File Based Memory
You have access to a file-based memory system via the ` + "`file_memory_*`" + ` tools for storing and retrieving information across interactions.
These files act as your working memory and persist beyond the current conversation, so they may already contain memories written earlier,
and anything you write now may remain available later.
Use these tools to store plans, memories, processing results, or downloaded data.

- Use descriptive file names (e.g., "projectarchitecture.md", "userpreferences.md").
- Include a description when writing a file to help with future discovery.
- Before starting new tasks, use file_memory_ls and file_memory_grep to check for relevant existing memories to avoid duplicate work.
- Keep memories up-to-date by overwriting files when information changes, or by using file_memory_replace and file_memory_replace_lines to make small edits.
- When you receive large amounts of data (e.g., downloaded web pages, API responses, research results),
  write them to files if they will be required later, so that they are not lost when older context is compacted or truncated.
  This ensures important data remains accessible across long-running sessions.`

const memoryIndexIntroduction = "The following is your memory index \u2014 a list of files you have previously written. " +
	"You can read any of these files using the file_memory_read tool.\n\n"

// Config configures a file memory provider.
type Config struct {
	// Instructions replaces the built-in instructions. Nil uses the defaults;
	// a pointer to an empty string omits the provider's instructions.
	Instructions *string

	// StateInitializer supplies state when the session has none. Nil uses an
	// empty working folder. The callback may receive a nil session when the
	// provider is invoked without one; in that case state is not persisted.
	StateInitializer func(*agent.Session) State
}

// State holds the working folder for a session, relative to the store root.
// Its zero value selects the store root.
type State struct {
	WorkingFolder string `json:"workingFolder"`
}

// ListEntry describes a memory file returned by file_memory_ls.
type ListEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

var _ agent.ContextProvider = (*Provider)(nil)

// Provider supplies memory tools, instructions, and the current memory index.
// Use New to create a provider. The caller owns the backing store's lifetime.
// Provider is safe for concurrent use, including concurrent tool calls.
type Provider struct {
	provider         agent.ContextProvider
	store            filestore.Store
	instructions     string
	stateInitializer func(*agent.Session) State
	stateMu          sync.Mutex
	writeLock        *semaphore.Weighted
}

// New creates a file memory provider backed by store.
// A zero Config uses built-in instructions and the store root as the working
// folder. It panics if store is nil. Closing a filesystem store remains the
// caller's responsibility.
//
// Register the provider in [agent.Config.ContextProviders]. Custom backends
// using automatic tool-invocation middleware must apply that middleware through
// [agent.ProviderConfig.Middlewares], after context providers have supplied tools.
func New(store filestore.Store, cfg Config) *Provider {
	if store == nil || reflect.ValueOf(store).Kind() == reflect.Pointer && reflect.ValueOf(store).IsNil() {
		panic("filememory: store is required")
	}
	p := &Provider{
		store:            store,
		instructions:     defaultInstructions,
		stateInitializer: cfg.StateInitializer,
		writeLock:        semaphore.NewWeighted(1),
	}
	if cfg.Instructions != nil {
		p.instructions = *cfg.Instructions
	}
	p.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "FileMemoryProvider",
		Provide:  p.provide,
	})
	return p
}

// Invoking adds memory instructions, tools and any existing memory index to
// the invocation without modifying the caller's messages or options.
func (p *Provider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.provider.Invoking(ctx, invoking)
}

// Invoked completes the context-provider lifecycle. Memory mutations are
// persisted by the tools, so no additional storage occurs after a run.
func (p *Provider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.provider.Invoked(ctx, invoked)
}

// StateKeys returns the session-state keys owned by the provider.
func (p *Provider) StateKeys() []string {
	return []string{stateKey}
}

func (p *Provider) getState(ctx context.Context, session *agent.Session) (State, error) {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	var state State
	found, err := session.Get(stateKey, &state)
	if err != nil {
		return State{}, fmt.Errorf("filememory: read session state: %w", err)
	}
	if !found && p.stateInitializer != nil {
		state = p.stateInitializer(session)
	}
	folder, err := storepaths.Normalize(state.WorkingFolder, true)
	if err != nil {
		return State{}, fmt.Errorf("filememory: invalid working folder: %w", err)
	}
	if !found {
		session.Set(stateKey, state)
	}
	state.WorkingFolder = folder
	return state, nil
}

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	telemetry.MarkUsed(telemetry.FeatureFileMemoryProvider)
	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	state, err := p.getState(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	if state.WorkingFolder != "" {
		if err := p.store.CreateDirectory(ctx, state.WorkingFolder); err != nil {
			return nil, nil, fmt.Errorf("filememory: create working folder: %w", err)
		}
	}
	tools := p.createTools(session)
	options := make([]agent.Option, 0, len(tools)+1)
	options = append(options, agent.WithInstructions(p.instructions))
	for _, t := range tools {
		options = append(options, agent.WithTool(t))
	}
	index, err := p.store.Read(ctx, memoryPath(state.WorkingFolder, memoryIndexName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, options, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("filememory: read memory index: %w", err)
	}
	if strings.TrimSpace(index) == "" {
		return nil, options, nil
	}
	return []*message.Message{message.NewText(memoryIndexIntroduction + index)}, options, nil
}

func (p *Provider) createTools(session *agent.Session) []tool.FuncTool {
	type fileInput struct {
		FileName string `json:"fileName" jsonschema:"Flat memory filename, without directory separators."`
	}
	type writeInput struct {
		FileName    string `json:"fileName" jsonschema:"Flat memory filename, without directory separators."`
		Content     string `json:"content" jsonschema:"The text to store."`
		Description string `json:"description,omitempty" jsonschema:"Optional summary for future discovery."`
	}
	type listInput struct {
		GlobPattern string `json:"globPattern,omitempty" jsonschema:"Optional case-insensitive filename pattern using * wildcards, such as *.md. Question marks and brackets are literal."`
	}
	type grepInput struct {
		RegexPattern string `json:"regexPattern" jsonschema:"Case-insensitive Go regular expression to search for."`
		GlobPattern  string `json:"globPattern,omitempty" jsonschema:"Optional case-insensitive filename pattern using * wildcards, such as *.md. Question marks and brackets are literal."`
	}
	type replaceInput struct {
		FileName   string `json:"fileName" jsonschema:"Flat memory filename, without directory separators."`
		OldString  string `json:"oldString" jsonschema:"Literal text to replace; must occur exactly once unless replaceAll is true."`
		NewString  string `json:"newString" jsonschema:"Literal replacement text; empty text deletes the match."`
		ReplaceAll bool   `json:"replaceAll,omitempty" jsonschema:"Replace every occurrence instead of requiring exactly one."`
	}
	type replaceLinesInput struct {
		FileName string     `json:"fileName" jsonschema:"Flat memory filename, without directory separators."`
		Edits    []lineEdit `json:"edits" jsonschema:"Edits addressed by 1-based line numbers, with literal replacement text."`
	}
	return []tool.FuncTool{
		functool.MustNew(functool.Config{
			Name: WriteToolName, Description: "Write a memory file with the given name and content. Overwrites an existing file. An optional description helps with discovery.",
		}, func(ctx context.Context, input writeInput) (string, error) {
			return p.write(ctx, session, input.FileName, input.Content, input.Description)
		}),
		functool.MustNew(functool.Config{
			Name: ReadFileToolName, Description: "Read a memory file. Returns its content or a not-found message. Line numbers count LF, CRLF, or lone CR terminators without an extra empty final line.",
		}, func(ctx context.Context, input fileInput) (string, error) {
			return p.read(ctx, session, input.FileName)
		}),
		functool.MustNew(functool.Config{
			Name: DeleteFileToolName, Description: "Delete a memory file and its companion description file.",
		}, func(ctx context.Context, input fileInput) (string, error) {
			return p.delete(ctx, session, input.FileName)
		}),
		functool.MustNew(functool.Config{
			Name: LsToolName, Description: "List memory files with optional descriptions. Optionally filter names by globPattern using * wildcards and literal filename text. Description files and the memory index are hidden.",
		}, func(ctx context.Context, input listInput) ([]ListEntry, error) {
			return p.list(ctx, session, input.GlobPattern)
		}),
		functool.MustNew(functool.Config{
			Name: GrepToolName, Description: "Search memory files with a case-insensitive Go regular expression and optional filename pattern using * wildcards. Returns filenames, snippets, and 1-based matching lines with their original terminators.",
		}, func(ctx context.Context, input grepInput) ([]searchResult, error) {
			state, err := p.getState(ctx, session)
			if err != nil {
				return nil, err
			}
			results, err := searchFiles(ctx, p.store, state.WorkingFolder, input.RegexPattern, input.GlobPattern)
			if err != nil {
				return nil, err
			}
			return slices.DeleteFunc(results, func(result searchResult) bool { return isInternalFile(result.FileName) }), nil
		}),
		functool.MustNew(functool.Config{
			Name: ReplaceToolName, Description: "Replace literal text in a memory file. Fails if oldString is absent or occurs more than once unless replaceAll is true. Returns the replacement count.",
		}, func(ctx context.Context, input replaceInput) (string, error) {
			return p.replace(ctx, session, input.FileName, input.OldString, input.NewString, input.ReplaceAll)
		}),
		functool.MustNew(functool.Config{
			Name: ReplaceLinesToolName, Description: "Replace lines by 1-based line_number. new_line is literal text including any desired trailing newline; empty text deletes the whole line. Optional expected_line rejects stale edits. Duplicate and out-of-range line numbers fail.",
		}, func(ctx context.Context, input replaceLinesInput) (string, error) {
			return p.replaceLines(ctx, session, input.FileName, input.Edits)
		}),
	}
}

func (p *Provider) write(ctx context.Context, session *agent.Session, fileName, content, description string) (string, error) {
	name, err := validateMemoryFileName(fileName)
	if err != nil {
		return "", err
	}
	state, err := p.getState(ctx, session)
	if err != nil {
		return "", err
	}
	if err := p.writeLock.Acquire(ctx, 1); err != nil {
		return "", err
	}
	defer p.writeLock.Release(1)
	if err := p.store.Write(ctx, memoryPath(state.WorkingFolder, name), content); err != nil {
		return "", fmt.Errorf("filememory: write memory: %w", err)
	}
	descriptionPath := memoryPath(state.WorkingFolder, descriptionFileName(name))
	hasDescription := strings.TrimSpace(description) != ""
	if hasDescription {
		if err := p.store.Write(ctx, descriptionPath, description); err != nil {
			return "", fmt.Errorf("filememory: write description: %w", err)
		}
	} else if err := p.store.Delete(ctx, descriptionPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("filememory: remove description: %w", err)
	}
	if err := p.rebuildIndex(ctx, state.WorkingFolder); err != nil {
		return "", err
	}
	if hasDescription {
		return fmt.Sprintf("File '%s' written with description.", fileName), nil
	}
	return fmt.Sprintf("File '%s' written.", fileName), nil
}

func (p *Provider) read(ctx context.Context, session *agent.Session, fileName string) (string, error) {
	name, err := validateMemoryFileName(fileName)
	if err != nil {
		return "", err
	}
	state, err := p.getState(ctx, session)
	if err != nil {
		return "", err
	}
	content, err := p.store.Read(ctx, memoryPath(state.WorkingFolder, name))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("File '%s' not found.", fileName), nil
	}
	if err != nil {
		return "", fmt.Errorf("filememory: read memory: %w", err)
	}
	return content, nil
}

func (p *Provider) delete(ctx context.Context, session *agent.Session, fileName string) (string, error) {
	name, err := validateMemoryFileName(fileName)
	if err != nil {
		return "", err
	}
	state, err := p.getState(ctx, session)
	if err != nil {
		return "", err
	}
	if err := p.writeLock.Acquire(ctx, 1); err != nil {
		return "", err
	}
	defer p.writeLock.Release(1)
	err = p.store.Delete(ctx, memoryPath(state.WorkingFolder, name))
	deleted := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("filememory: delete memory: %w", err)
	}
	if err := p.store.Delete(ctx, memoryPath(state.WorkingFolder, descriptionFileName(name))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("filememory: delete description: %w", err)
	}
	if err := p.rebuildIndex(ctx, state.WorkingFolder); err != nil {
		return "", err
	}
	if deleted {
		return fmt.Sprintf("File '%s' deleted.", fileName), nil
	}
	return fmt.Sprintf("File '%s' not found.", fileName), nil
}

func (p *Provider) list(ctx context.Context, session *agent.Session, globPattern string) ([]ListEntry, error) {
	if _, err := matchesGlob("", globPattern); err != nil {
		return nil, err
	}
	state, err := p.getState(ctx, session)
	if err != nil {
		return nil, err
	}
	names, err := p.memoryNames(ctx, state.WorkingFolder)
	if err != nil {
		return nil, err
	}
	available := make(map[string]string, len(names))
	for _, name := range names {
		available[strings.ToLower(name)] = name
	}
	entries := make([]ListEntry, 0)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if isInternalFile(name) {
			continue
		}
		matched, err := matchesGlob(name, globPattern)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		entry := ListEntry{Name: name, Type: "file"}
		if descriptionName, found := available[strings.ToLower(descriptionFileName(name))]; found {
			entry.Description, err = p.store.Read(ctx, memoryPath(state.WorkingFolder, descriptionName))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("filememory: read description: %w", err)
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (p *Provider) replace(ctx context.Context, session *agent.Session, fileName, oldString, newString string, replaceAll bool) (string, error) {
	name, err := validateMemoryFileName(fileName)
	if err != nil {
		return "", err
	}
	state, err := p.getState(ctx, session)
	if err != nil {
		return "", err
	}
	if err := p.writeLock.Acquire(ctx, 1); err != nil {
		return "", err
	}
	defer p.writeLock.Release(1)
	content, err := p.store.Read(ctx, memoryPath(state.WorkingFolder, name))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("File '%s' not found.", fileName), nil
	}
	if err != nil {
		return "", fmt.Errorf("filememory: read memory for replacement: %w", err)
	}
	replaced, count, err := replaceContent(content, oldString, newString, replaceAll)
	if err != nil {
		return "", err
	}
	if err := p.store.Write(ctx, memoryPath(state.WorkingFolder, name), replaced); err != nil {
		return "", fmt.Errorf("filememory: write replaced memory: %w", err)
	}
	return fmt.Sprintf("Replaced %d occurrence(s) in '%s'.", count, fileName), nil
}

func (p *Provider) replaceLines(ctx context.Context, session *agent.Session, fileName string, edits []lineEdit) (string, error) {
	name, err := validateMemoryFileName(fileName)
	if err != nil {
		return "", err
	}
	state, err := p.getState(ctx, session)
	if err != nil {
		return "", err
	}
	if err := p.writeLock.Acquire(ctx, 1); err != nil {
		return "", err
	}
	defer p.writeLock.Release(1)
	content, err := p.store.Read(ctx, memoryPath(state.WorkingFolder, name))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("File '%s' not found.", fileName), nil
	}
	if err != nil {
		return "", fmt.Errorf("filememory: read memory for line replacement: %w", err)
	}
	replaced, err := replaceLines(content, edits)
	if err != nil {
		return "", err
	}
	if err := p.store.Write(ctx, memoryPath(state.WorkingFolder, name), replaced); err != nil {
		return "", fmt.Errorf("filememory: write edited memory: %w", err)
	}
	return fmt.Sprintf("Replaced %d line(s) in '%s'.", len(edits), fileName), nil
}

func (p *Provider) memoryNames(ctx context.Context, folder string) ([]string, error) {
	names, err := p.store.ListChildren(ctx, folder)
	if err != nil {
		return nil, fmt.Errorf("filememory: list memories: %w", err)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalized, err := storepaths.Normalize(name, false)
		if err != nil || normalized != name || strings.ContainsRune(normalized, '/') {
			return nil, fmt.Errorf("filememory: invalid listed filename %q: %w", name, fs.ErrInvalid)
		}
	}
	return names, nil
}

func (p *Provider) rebuildIndex(ctx context.Context, folder string) error {
	names, err := p.memoryNames(ctx, folder)
	if err != nil {
		return err
	}
	names = slices.Clone(names)
	slices.SortFunc(names, func(a, b string) int {
		if comparison := strings.Compare(strings.ToLower(a), strings.ToLower(b)); comparison != 0 {
			return comparison
		}
		return strings.Compare(a, b)
	})
	var index strings.Builder
	index.WriteString("# Memory Index\n\n")
	count := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if isInternalFile(name) {
			continue
		}
		if count == maxIndexEntries {
			break
		}
		description, err := p.store.Read(ctx, memoryPath(folder, descriptionFileName(name)))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("filememory: read description for index: %w", err)
		}
		fmt.Fprintf(&index, "- **%s**", name)
		if strings.TrimSpace(description) != "" {
			fmt.Fprintf(&index, ": %s", description)
		}
		index.WriteByte('\n')
		count++
	}
	if err := p.store.Write(ctx, memoryPath(folder, memoryIndexName), index.String()); err != nil {
		return fmt.Errorf("filememory: write memory index: %w", err)
	}
	return nil
}

func validateMemoryFileName(fileName string) (string, error) {
	portable := strings.TrimRight(strings.ReplaceAll(fileName, "\\", "/"), "/")
	if strings.ContainsRune(portable, '/') {
		return "", fmt.Errorf("filememory: memory filenames must not contain directory separators: %w", fs.ErrInvalid)
	}
	name, err := storepaths.Normalize(portable, false)
	if err != nil {
		return "", fmt.Errorf("filememory: %w", err)
	}
	if isInternalFile(name) {
		return "", fmt.Errorf("filememory: filename %q is reserved for internal use: %w", fileName, fs.ErrInvalid)
	}
	return name, nil
}

func descriptionFileName(fileName string) string {
	return fileName + descriptionSuffix
}

func isInternalFile(fileName string) bool {
	lower := strings.ToLower(fileName)
	return lower == memoryIndexName || strings.HasSuffix(lower, descriptionSuffix)
}

func memoryPath(folder, fileName string) string {
	if folder == "" {
		return fileName
	}
	return folder + "/" + fileName
}

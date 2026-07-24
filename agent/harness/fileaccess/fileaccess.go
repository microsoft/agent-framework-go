// Copyright (c) Microsoft. All rights reserved.

// Package fileaccess provides a context provider that gives agents file tools
// for reading and writing files inside a single caller-granted directory (a
// "shared folder"). It mirrors the .NET FileAccessProvider, exposing the same
// set of tools: file_access_read_file, file_access_save_file,
// file_access_list_files, file_access_list_subdirectories,
// file_access_search_files, and file_access_delete_file.
//
// All operations are constrained to the configured root directory. Paths are
// resolved relative to the root and any attempt to escape it (via "..", an
// absolute path, or symlink-style traversal in the supplied name) is rejected.
//
// The root directory is supplied by the caller via [Options.RootDir]; it is not
// read from or written to the agent session state. Set [Options.ReadOnly] to
// omit the save and delete tools, matching the .NET read-only shipping mode.
package fileaccess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// SourceID identifies this provider in emitted context messages.
const SourceID = "FileAccessProvider"

const defaultInstructions = `## File Access

You have access to a shared folder through a set of file tools. All paths are
relative to the root of the shared folder; you cannot read or write files
outside of it.

Use these tools to work with files:
- Use file_access_read_file to read the contents of a file.
- Use file_access_save_file to create or overwrite a file with new contents.
- Use file_access_list_files to list the files directly inside a folder.
- Use file_access_list_subdirectories to list the sub-folders directly inside a folder.
- Use file_access_search_files to find files whose contents match a regular expression.
- Use file_access_delete_file to delete a file that is no longer needed.`

const readOnlyInstructions = `## File Access (read-only)

You have read-only access to a shared folder through a set of file tools. All
paths are relative to the root of the shared folder; you cannot read files
outside of it, and you cannot create, modify, or delete files.

Use these tools to work with files:
- Use file_access_read_file to read the contents of a file.
- Use file_access_list_files to list the files directly inside a folder.
- Use file_access_list_subdirectories to list the sub-folders directly inside a folder.
- Use file_access_search_files to find files whose contents match a regular expression.`

// Options configures the file access provider.
type Options struct {
	// RootDir is the directory that scopes every file operation. All tool
	// arguments are interpreted relative to this directory and operations that
	// would escape it are rejected. If empty, the current working directory is
	// used.
	RootDir string

	// ReadOnly, when true, omits the file_access_save_file and
	// file_access_delete_file tools so the agent can only read.
	ReadOnly bool

	// Instructions overrides the default instructions provided to the agent.
	Instructions string
}

// Provider is an agent context provider that exposes shared-folder file tools.
// Use [New] to create. Provider can be used directly in agent configuration.
type Provider struct {
	provider     agent.ContextProvider
	store        *store
	readOnly     bool
	instructions string
}

// New creates a new file access provider with the given options.
// If opts is nil, defaults are used and the root is the current working directory.
func New(opts *Options) *Provider {
	var o Options
	if opts != nil {
		o = *opts
	}

	instructions := o.Instructions
	if instructions == "" {
		if o.ReadOnly {
			instructions = readOnlyInstructions
		} else {
			instructions = defaultInstructions
		}
	}

	p := &Provider{
		store:        newStore(o.RootDir),
		readOnly:     o.ReadOnly,
		instructions: instructions,
	}
	p.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: SourceID,
		Provide:  p.provide,
	})
	return p
}

// Invoking implements agent.ContextProvider.
func (p *Provider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.provider.Invoking(ctx, invoking)
}

// Invoked implements agent.ContextProvider.
func (p *Provider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.provider.Invoked(ctx, invoked)
}

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	var outOpts []agent.Option
	for _, t := range p.createTools() {
		outOpts = append(outOpts, agent.WithTool(t))
	}
	outOpts = append(outOpts, agent.WithInstructions(p.instructions))
	return nil, outOpts, nil
}

type readInput struct {
	Path string `json:"path"`
}

type saveInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type listInput struct {
	// Path is the folder to list, relative to the root. Empty means the root.
	Path string `json:"path,omitempty"`
}

type searchInput struct {
	// Pattern is a Go regular expression matched against file contents.
	Pattern string `json:"pattern"`
	// Path is the folder to search under, relative to the root. Empty means the root.
	Path string `json:"path,omitempty"`
}

func (p *Provider) createTools() []tool.FuncTool {
	readTool := functool.MustNew(
		functool.Config{
			Name:        "file_access_read_file",
			Description: "Read and return the full contents of a file in the shared folder. The path is relative to the shared folder root.",
		},
		func(ctx context.Context, in readInput) (string, error) {
			return p.store.ReadFile(in.Path)
		},
	)

	listFilesTool := functool.MustNew(
		functool.Config{
			Name:        "file_access_list_files",
			Description: "List the names of the files directly inside a folder of the shared folder. The path is relative to the shared folder root; leave it empty for the root.",
		},
		func(ctx context.Context, in listInput) ([]string, error) {
			return p.store.ListFiles(in.Path)
		},
	)

	listDirsTool := functool.MustNew(
		functool.Config{
			Name:        "file_access_list_subdirectories",
			Description: "List the names of the sub-folders directly inside a folder of the shared folder. The path is relative to the shared folder root; leave it empty for the root.",
		},
		func(ctx context.Context, in listInput) ([]string, error) {
			return p.store.ListSubdirectories(in.Path)
		},
	)

	searchTool := functool.MustNew(
		functool.Config{
			Name:        "file_access_search_files",
			Description: "Search the shared folder recursively and return the relative paths of files whose contents match the given regular expression.",
		},
		func(ctx context.Context, in searchInput) ([]string, error) {
			return p.store.SearchFiles(in.Path, in.Pattern)
		},
	)

	tools := []tool.FuncTool{readTool, listFilesTool, listDirsTool, searchTool}

	if !p.readOnly {
		saveTool := functool.MustNew(
			functool.Config{
				Name:        "file_access_save_file",
				Description: "Create or overwrite a file in the shared folder with the given contents. The path is relative to the shared folder root; parent folders are created as needed.",
			},
			func(ctx context.Context, in saveInput) (string, error) {
				if err := p.store.SaveFile(in.Path, in.Content); err != nil {
					return "", err
				}
				return fmt.Sprintf("saved %s", in.Path), nil
			},
		)

		deleteTool := functool.MustNew(
			functool.Config{
				Name:        "file_access_delete_file",
				Description: "Delete a file from the shared folder. The path is relative to the shared folder root.",
			},
			func(ctx context.Context, in readInput) (string, error) {
				if err := p.store.DeleteFile(in.Path); err != nil {
					return "", err
				}
				return fmt.Sprintf("deleted %s", in.Path), nil
			},
		)

		tools = append(tools, saveTool, deleteTool)
	}

	return tools
}

// store is a local-filesystem file store constrained to a root directory.
type store struct {
	root string
}

func newStore(root string) *store {
	if root == "" {
		root = "."
	}
	// Best-effort absolute path so prefix checks are stable regardless of the
	// process working directory. Fall back to a cleaned relative path.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	} else {
		root = filepath.Clean(root)
	}
	return &store{root: root}
}

// resolve maps a caller-supplied relative path to an absolute path guaranteed
// to live inside the root, rejecting any path that would escape it.
func (s *store) resolve(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the shared folder root", rel)
	}
	full := filepath.Clean(filepath.Join(s.root, rel))
	if full != s.root && !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes the shared folder root", rel)
	}
	return full, nil
}

// ReadFile returns the contents of the file at rel.
func (s *store) ReadFile(rel string) (string, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveFile writes content to the file at rel, creating parent folders as needed.
func (s *store) SaveFile(rel, content string) error {
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// DeleteFile removes the file at rel.
func (s *store) DeleteFile(rel string) error {
	full, err := s.resolve(rel)
	if err != nil {
		return err
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path %q is a directory, not a file", rel)
	}
	return os.Remove(full)
}

// ListFiles returns the names of the files that are direct children of rel.
func (s *store) ListFiles(rel string) ([]string, error) {
	return s.listNames(rel, false)
}

// ListSubdirectories returns the names of the sub-folders that are direct
// children of rel.
func (s *store) ListSubdirectories(rel string) ([]string, error) {
	return s.listNames(rel, true)
}

func (s *store) listNames(rel string, wantDirs bool) ([]string, error) {
	full, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() == wantDirs {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// SearchFiles walks the tree rooted at rel and returns the paths (relative to
// the store root) of files whose contents match pattern.
func (s *store) SearchFiles(rel, pattern string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid search pattern: %w", err)
	}
	base, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	var matches []string
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if re.Match(data) {
			relPath, relErr := filepath.Rel(s.root, path)
			if relErr != nil {
				return relErr
			}
			matches = append(matches, filepath.ToSlash(relPath))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

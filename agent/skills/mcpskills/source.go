// Copyright (c) Microsoft. All rights reserved.

package mcpskills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const indexURI = "skill://index.json"

// Config configures an MCP-backed skills source.
type Config struct {
	// Session is an initialized connection to a trusted MCP server. The source
	// borrows the session; the caller remains responsible for closing it.
	Session *mcp.ClientSession

	// ArchiveDirectory holds extracted archive skills. The source exclusively
	// manages its subdirectories and prunes skills no longer advertised by the
	// server. Each source must use a separate directory.
	// Existing regular files are preserved; colliding archive entries are skipped.
	// Empty uses a private temporary directory, removed by [Source.Close].
	ArchiveDirectory string

	// ArchiveResourceExtensions selects readable archive resource extensions.
	// Nil defaults to .md, .json, .yaml, .yml, .csv, .xml, and .txt.
	// An explicitly empty slice disables resource discovery. Script extensions
	// may be included here, but archive scripts are never executable.
	ArchiveResourceExtensions []string

	// ArchiveResourceSearchDepth limits resource discovery within each skill.
	// Nil defaults to 2; explicit values must be at least 1.
	ArchiveResourceSearchDepth *int

	// ArchiveMaxFileCount limits extracted files per archive. Nil defaults to
	// 20. Zero disallows files; negative values are invalid.
	ArchiveMaxFileCount *int

	// ArchiveMaxSizeBytes limits decoded archive downloads. Nil defaults to
	// 1 MiB. Zero disallows nonempty archives; negative values are invalid.
	ArchiveMaxSizeBytes *int64

	// ArchiveMaxUncompressedSizeBytes limits the actual total bytes extracted
	// per archive. Nil defaults to 1 MiB. Negative values are invalid.
	ArchiveMaxUncompressedSizeBytes *int64

	// Logger receives discovery and skipped-entry diagnostics.
	// Nil uses a discard logger.
	Logger *slog.Logger
}

// Source discovers skills from an MCP server's skill://index.json resource.
// Construct it with [NewSource] and release its filesystem resources with
// [Source.Close]. It implements [skills.Source].
//
// Discovery is not cached. Concurrent archive reconciliation is serialized;
// compose with [skills.NewCachingSource] or use the skills provider's default
// caching to share discovery work.
type Source struct {
	session             *mcp.ClientSession
	logger              *slog.Logger
	archiveDirectory    string
	fileOptions         fsskills.SourceOptions
	maxFileCount        int
	maxArchiveSize      int64
	maxUncompressedSize int64
	archiveGate         chan struct{}
	archiveRoot         *os.Root
	temporaryDirectory  string
	closed              atomic.Bool
	closeOnce           sync.Once
	closeErr            error
}

// NewSource creates a source, copying configuration values and slices.
// It panics if Session is nil, a size or file-count limit is negative, or the
// resource search depth is less than 1. No discovery or filesystem work occurs
// until [Source.Skills] is called.
func NewSource(config Config) *Source {
	if config.Session == nil {
		panic("mcpskills: Session is nil")
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	source := &Source{
		session:             config.Session,
		logger:              logger,
		archiveDirectory:    config.ArchiveDirectory,
		maxFileCount:        20,
		maxArchiveSize:      1 << 20,
		maxUncompressedSize: 1 << 20,
		archiveGate:         make(chan struct{}, 1),
		fileOptions: fsskills.SourceOptions{
			AllowedResourceExtensions: slices.Clone(config.ArchiveResourceExtensions),
			AllowedScriptExtensions:   []string{},
			Logger:                    logger,
		},
	}
	if config.ArchiveResourceSearchDepth != nil {
		if *config.ArchiveResourceSearchDepth < 1 {
			panic("mcpskills: ArchiveResourceSearchDepth must be at least 1")
		}
		source.fileOptions.SearchDepth = new(*config.ArchiveResourceSearchDepth)
	}
	if config.ArchiveMaxFileCount != nil {
		if *config.ArchiveMaxFileCount < 0 {
			panic("mcpskills: ArchiveMaxFileCount must not be negative")
		}
		source.maxFileCount = *config.ArchiveMaxFileCount
	}
	if config.ArchiveMaxSizeBytes != nil {
		if *config.ArchiveMaxSizeBytes < 0 {
			panic("mcpskills: ArchiveMaxSizeBytes must not be negative")
		}
		source.maxArchiveSize = *config.ArchiveMaxSizeBytes
	}
	if config.ArchiveMaxUncompressedSizeBytes != nil {
		if *config.ArchiveMaxUncompressedSizeBytes < 0 {
			panic("mcpskills: ArchiveMaxUncompressedSizeBytes must not be negative")
		}
		source.maxUncompressedSize = *config.ArchiveMaxUncompressedSizeBytes
	}
	return source
}

type skillIndex struct {
	Skills []indexEntry `json:"skills"`
}

type indexEntry struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Description string  `json:"description"`
	URL         string  `json:"url"`
	Digest      *string `json:"digest"`
}

// Skills reads the discovery index, constructs lazy skill-md entries, and
// refreshes archive entries. Unsupported or invalid entries are logged and
// skipped. An absent, empty, malformed, or protocol-unreadable index advertises
// no skills and still prunes previously extracted archives.
// Cancellation, transport failures, and archive-root access errors propagate.
// Individual archive extraction failures are logged and skipped.
// Missing extracted archive directories are also logged and skipped.
func (s *Source) Skills(ctx context.Context, sourceContext skills.SourceContext) ([]*skills.Skill, error) {
	if err := s.checkOpen(ctx); err != nil {
		return nil, err
	}
	telemetry.MarkUsed(telemetry.FeatureMCPSkillsSource)
	entries, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*skills.Skill, 0)
	var archives []indexEntry
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch strings.ToLower(entry.Type) {
		case "skill-md":
			frontmatter := skills.Frontmatter{Name: entry.Name, Description: entry.Description}
			if err := frontmatter.Validate(); err != nil {
				s.logger.DebugContext(ctx, "Skipping invalid MCP skill", "skillName", entry.Name, "error", err)
			} else if strings.TrimSpace(entry.URL) == "" {
				s.logger.DebugContext(ctx, "Skipping MCP skill without a URL", "skillName", entry.Name)
			} else {
				result = append(result, s.remoteSkill(frontmatter, entry.URL))
			}
		case "archive":
			if err := skills.ValidateName(entry.Name); err != nil {
				s.logger.DebugContext(ctx, "Skipping invalid archive skill name", "skillName", entry.Name, "error", err)
			} else if strings.TrimSpace(entry.URL) == "" {
				s.logger.DebugContext(ctx, "Skipping archive skill without a URL", "skillName", entry.Name)
			} else {
				archives = append(archives, entry)
			}
		default:
			s.logger.DebugContext(ctx, "Skipping unsupported MCP skill type", "skillName", entry.Name, "type", entry.Type)
		}
	}
	if err := lock(ctx, s.archiveGate); err != nil {
		return nil, err
	}
	defer func() { <-s.archiveGate }()
	if err := s.checkOpen(ctx); err != nil {
		return nil, err
	}
	extracted, err := s.loadArchives(ctx, archives, sourceContext)
	if err != nil {
		return nil, err
	}
	result = append(result, extracted...)
	s.logger.InfoContext(ctx, "Successfully loaded skills from MCP server", "count", len(result))
	return result, nil
}

func (s *Source) readIndex(ctx context.Context) ([]indexEntry, error) {
	result, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: indexURI})
	if err != nil {
		var remote *jsonrpc.Error
		if errors.As(err, &remote) {
			if resourceNotFound(err) {
				s.logger.DebugContext(ctx, "MCP server has no skill index", "error", err)
			} else {
				s.logger.WarnContext(ctx, "Failed to read MCP skill index", "error", err)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("mcpskills: read skill index: %w", err)
	}
	var text string
	for _, content := range result.Contents {
		if content != nil && content.Blob == nil {
			text = content.Text
			break
		}
	}
	if strings.TrimSpace(text) == "" {
		s.logger.DebugContext(ctx, "MCP skill index returned empty or non-text contents")
		return nil, nil
	}
	var index skillIndex
	if err := json.Unmarshal([]byte(text), &index); err != nil {
		s.logger.WarnContext(ctx, "Failed to parse MCP skill index", "error", err)
		return nil, nil
	}
	return index.Skills, nil
}

func (s *Source) remoteSkill(frontmatter skills.Frontmatter, uri string) *skills.Skill {
	rootURI := strings.TrimSuffix(uri, "SKILL.md")
	if !strings.HasSuffix(rootURI, "/") {
		rootURI += "/"
	}
	gate := make(chan struct{}, 1)
	var cached string
	var loaded bool
	return &skills.Skill{
		Frontmatter: frontmatter,
		GetContent: func(ctx context.Context) (string, error) {
			if err := lock(ctx, gate); err != nil {
				return "", err
			}
			defer func() { <-gate }()
			if err := s.checkOpen(ctx); err != nil {
				return "", err
			}
			if loaded {
				return cached, nil
			}
			result, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
			if err != nil {
				return "", fmt.Errorf("mcpskills: load skill %q: %w", frontmatter.Name, err)
			}
			text := resourceText(result)
			if text == "" {
				return "", fmt.Errorf("mcpskills: SKILL.md for skill %q contains no text", frontmatter.Name)
			}
			cached, loaded = text, true
			return text, nil
		},
		GetResource: func(ctx context.Context, name string) (*skills.Resource, error) {
			if err := s.checkOpen(ctx); err != nil {
				return nil, err
			}
			if name == "." || !fs.ValidPath(name) || strings.Contains(name, "\\") || strings.TrimSpace(name) == "" {
				s.logger.DebugContext(ctx, "Skipping invalid MCP skill resource name", "skillName", frontmatter.Name, "resourceName", name)
				return nil, nil
			}
			result, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: rootURI + name})
			if err != nil {
				if resourceNotFound(err) {
					s.logger.DebugContext(ctx, "MCP skill resource not found", "skillName", frontmatter.Name, "resourceName", name)
					return nil, nil
				}
				return nil, fmt.Errorf("mcpskills: read resource %q in skill %q: %w", name, frontmatter.Name, err)
			}
			return &skills.Resource{
				Name: name,
				Read: func(ctx context.Context) (any, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					for _, content := range result.Contents {
						if content == nil || content.Blob == nil {
							continue
						}
						mediaType := content.MIMEType
						if mediaType == "" {
							mediaType = "application/octet-stream"
						}
						data, err := message.NewDataContent(content.Blob, mediaType)
						if err != nil {
							return nil, err
						}
						data.Name = content.URI
						data.RawRepresentation = content
						data.AdditionalProperties = maps.Clone(content.Meta)
						return data, nil
					}
					if text := resourceText(result); text != "" {
						return text, nil
					}
					return nil, nil
				},
			}, nil
		},
	}
}

func resourceText(result *mcp.ReadResourceResult) string {
	var parts []string
	for _, content := range result.Contents {
		if content != nil && content.Blob == nil {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func resourceNotFound(err error) bool {
	var remote *jsonrpc.Error
	// SEP-2164 replaced the legacy resource-not-found code with Invalid Params.
	return errors.As(err, &remote) && (remote.Code == jsonrpc.CodeInvalidParams || remote.Code == -32002)
}

func lock(ctx context.Context, gate chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Source) checkOpen(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed.Load() {
		return fmt.Errorf("mcpskills: source is closed: %w", fs.ErrClosed)
	}
	return nil
}

// Close idempotently releases archive filesystem handles and removes the private
// temporary directory, if any. Caller-configured directories are retained.
// Close never closes the borrowed MCP session. New discovery and lazy remote
// reads fail with [fs.ErrClosed]; already fetched resource content remains usable.
func (s *Source) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.archiveGate <- struct{}{}
		defer func() { <-s.archiveGate }()
		if s.archiveRoot != nil {
			s.closeErr = s.archiveRoot.Close()
		}
		if s.temporaryDirectory != "" {
			s.closeErr = errors.Join(s.closeErr, os.RemoveAll(s.temporaryDirectory))
		}
	})
	return s.closeErr
}

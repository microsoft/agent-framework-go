// Copyright (c) Microsoft. All rights reserved.

package mcpskills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errUnsupportedArchiveFormat = errors.New("unsupported archive format; use ZIP instead")

type archiveSkillDirectory struct {
	name       string
	filesystem fs.FS
}

func (s *Source) loadArchives(ctx context.Context, entries []indexEntry, sourceContext skills.SourceContext) ([]*skills.Skill, error) {
	if err := s.openArchiveRoot(len(entries) != 0); err != nil {
		return nil, err
	}
	if s.archiveRoot == nil {
		return nil, nil
	}
	advertised := make(map[string]bool, len(entries))
	for _, entry := range entries {
		advertised[entry.Name] = true
	}
	children, err := fs.ReadDir(s.archiveRoot.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("mcpskills: list archive directory: %w", err)
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (!child.IsDir() && child.Type()&fs.ModeSymlink == 0) || advertised[child.Name()] {
			continue
		}
		if err := s.archiveRoot.RemoveAll(child.Name()); err != nil {
			s.logger.WarnContext(ctx, "Failed to prune archive skill", "skillName", child.Name(), "error", err)
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}
	var directories []archiveSkillDirectory
	for _, entry := range entries {
		extracted, err := s.extractEntry(ctx, entry)
		if err != nil {
			return nil, err
		}
		if !extracted {
			continue
		}
		sub, err := fs.Sub(s.archiveRoot.FS(), entry.Name)
		if err != nil {
			return nil, fmt.Errorf("mcpskills: open extracted skill %q: %w", entry.Name, err)
		}
		directories = append(directories, archiveSkillDirectory{name: entry.Name, filesystem: sub})
	}
	if len(directories) == 0 {
		return fsskills.NewSourceOptions(s.fileOptions).Skills(ctx, sourceContext)
	}
	var result []*skills.Skill
	for _, directory := range directories {
		children, err := fs.ReadDir(directory.filesystem, ".")
		if errors.Is(err, fs.ErrNotExist) {
			s.logger.WarnContext(ctx, "Skipping missing extracted archive skill directory", "skillName", directory.name)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mcpskills: inspect extracted skill %q: %w", directory.name, err)
		}
		hasRootSkill := false
		for _, child := range children {
			if child.Name() == "SKILL.md" && child.Type().IsRegular() {
				hasRootSkill = true
				break
			}
		}
		found, err := fsskills.NewSourceOptions(s.fileOptions, directory.filesystem).Skills(ctx, sourceContext)
		if err != nil {
			return nil, err
		}
		for _, skill := range found {
			// fs.Sub hides the root's name; nested skill directories are already
			// checked by the filesystem source against their own names.
			if hasRootSkill && skill.Frontmatter.Name != directory.name {
				s.logger.ErrorContext(ctx, "SKILL.md name does not match archive skill directory name",
					"skillName", skill.Frontmatter.Name, "directoryName", directory.name)
				continue
			}
			result = append(result, skill)
		}
	}
	return result, nil
}

func (s *Source) openArchiveRoot(create bool) error {
	if s.archiveRoot != nil {
		return nil
	}
	directory := s.archiveDirectory
	if directory == "" {
		if !create {
			return nil
		}
		var err error
		directory, err = os.MkdirTemp("", "agent-framework-mcp-skills-")
		if err != nil {
			return fmt.Errorf("mcpskills: create temporary archive directory: %w", err)
		}
		s.temporaryDirectory = directory
	} else if create {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("mcpskills: create archive directory: %w", err)
		}
	}
	root, err := os.OpenRoot(directory)
	if !create && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mcpskills: open archive directory: %w", err)
	}
	s.archiveRoot = root
	return nil
}

func (s *Source) extractEntry(ctx context.Context, entry indexEntry) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := s.archiveRoot.Lstat(entry.Name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logger.WarnContext(ctx, "Failed to inspect archive skill directory", "skillName", entry.Name, "error", err)
		return false, nil
	}
	if err == nil && !info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
		s.logger.WarnContext(ctx, "Skipping archive skill: target is not a directory", "skillName", entry.Name)
		return false, nil
	}
	if err := s.archiveRoot.RemoveAll(entry.Name); err != nil {
		s.logger.WarnContext(ctx, "Failed to clean archive skill directory", "skillName", entry.Name, "error", err)
		return false, nil
	}
	result, err := s.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: entry.URL})
	if err != nil {
		var remote *jsonrpc.Error
		if errors.As(err, &remote) {
			s.logger.WarnContext(ctx, "Failed to read archive skill", "skillName", entry.Name, "error", err)
			return false, nil
		}
		return false, fmt.Errorf("mcpskills: download archive skill %q: %w", entry.Name, err)
	}
	var blob *mcp.ResourceContents
	for _, content := range result.Contents {
		if content != nil && content.Blob != nil {
			blob = content
			break
		}
	}
	if blob == nil || len(blob.Blob) == 0 {
		s.logger.WarnContext(ctx, "Skipping archive skill without binary content", "skillName", entry.Name)
		return false, nil
	}
	if int64(len(blob.Blob)) > s.maxArchiveSize {
		s.logger.WarnContext(ctx, "Skipping archive skill exceeding download size limit", "skillName", entry.Name, "limit", s.maxArchiveSize)
		return false, nil
	}
	if entry.Digest != nil && *entry.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(blob.Blob)) {
		s.logger.WarnContext(ctx, "Skipping archive skill: digest does not match downloaded content", "skillName", entry.Name)
		return false, nil
	}
	if err := s.extractArchive(ctx, entry.Name, blob.Blob, blob.MIMEType, entry.URL); err != nil {
		if errors.Is(err, errUnsupportedArchiveFormat) {
			s.logger.WarnContext(ctx, "Skipping archive skill with unsupported format; use ZIP", "skillName", entry.Name)
			return false, nil
		}
		s.logger.WarnContext(ctx, "Failed to extract archive skill", "skillName", entry.Name, "error", err)
		if cleanupErr := s.archiveRoot.RemoveAll(entry.Name); cleanupErr != nil {
			s.logger.WarnContext(ctx, "Failed to clean partial archive skill", "skillName", entry.Name, "error", cleanupErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func isZIP(data []byte, mediaType, uri string) bool {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return false
	}
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && (data[2] == 3 || data[2] == 5 || data[2] == 7) {
		return true
	}
	mediaType = strings.TrimSpace(mediaType)
	return strings.EqualFold(mediaType, "application/zip") ||
		strings.EqualFold(mediaType, "application/x-zip-compressed") ||
		strings.HasSuffix(strings.ToLower(uri), ".zip")
}

func (s *Source) extractArchive(ctx context.Context, name string, data []byte, mediaType, uri string) (err error) {
	if !isZIP(data, mediaType, uri) {
		return errUnsupportedArchiveFormat
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if err := s.archiveRoot.Mkdir(name, 0o700); err != nil {
		return err
	}
	root, err := s.archiveRoot.OpenRoot(name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	remaining := s.maxUncompressedSize
	count := 0
	buffer := make([]byte, 32*1024)
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		count++
		if count > s.maxFileCount {
			return fmt.Errorf("archive exceeds maximum file count (%d)", s.maxFileCount)
		}
		normalized := strings.ReplaceAll(file.Name, "\\", "/")
		destination := path.Clean(normalized)
		if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") ||
			destination == "." || !fs.ValidPath(destination) || !filepath.IsLocal(filepath.FromSlash(destination)) {
			s.logger.WarnContext(ctx, "Skipping archive entry outside the skill directory", "skillName", name, "entry", file.Name)
			continue
		}
		if err := root.MkdirAll(filepath.FromSlash(path.Dir(destination)), 0o700); err != nil {
			return err
		}
		read, err := extractFile(ctx, root, filepath.FromSlash(destination), file, remaining, buffer)
		if err != nil {
			return err
		}
		remaining -= read
	}
	return nil
}

func extractFile(ctx context.Context, root *os.Root, name string, file *zip.File, remaining int64, buffer []byte) (read int64, err error) {
	input, err := file.Open()
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	limit := remaining
	if limit < math.MaxInt64 {
		limit++
	}
	read, err = io.CopyBuffer(output, contextReader{ctx, io.LimitReader(input, limit)}, buffer)
	if read > remaining {
		err = errors.Join(err, fmt.Errorf("archive exceeds maximum uncompressed size"))
	}
	return read, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

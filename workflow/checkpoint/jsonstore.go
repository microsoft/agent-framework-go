// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	"github.com/microsoft/agent-framework-go/workflow"
)

var _ Store[json.RawMessage] = (*FileSystemJSONStore)(nil)

// FileSystemJSONStore provides a file system-based implementation of a JSON [Store],
// that persists checkpoint data and index information to disk using JSON files.
//
// The store writes checkpoint files to a specified directory and maintains an
// index file for retrieval. It is intended for durable, process-exclusive
// checkpoint persistence. Instances are not safe for concurrent use by multiple
// goroutines without external synchronization. Call [FileSystemJSONStore.Close]
// when the store is no longer needed to release file handles and the process
// lock.
type FileSystemJSONStore struct {
	root            *os.Root
	indexFile       *os.File
	indexLock       *flock.Flock
	index           []indexEntry
	checkpointIndex map[workflow.CheckpointInfo]struct{}
}

// NewFileSystemJSONStore creates a FileSystemJSONStore that uses rootDir for
// checkpoint storage. The directory is created if it does not exist.
//
// An error is returned if rootDir cannot be created or resolved, if the store is
// already in use by another process, or if the existing index is corrupted.
func NewFileSystemJSONStore(rootDir string) (*FileSystemJSONStore, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("jsonstore: create root dir: %w", err)
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("jsonstore: resolve root dir: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("jsonstore: open root: %w", err)
	}

	indexPath := filepath.Join(abs, "index.jsonl")
	indexLock := flock.New(indexPath + ".lock")
	locked, err := indexLock.TryLock()
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("jsonstore: lock store at %q: %w", abs, err)
	}
	if !locked {
		_ = root.Close()
		return nil, fmt.Errorf("jsonstore: store at %q is already in use", abs)
	}

	indexFile, err := root.OpenFile("index.jsonl", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		_ = indexLock.Unlock()
		_ = root.Close()
		return nil, fmt.Errorf("jsonstore: open index: %w", err)
	}

	store := &FileSystemJSONStore{
		root:            root,
		indexFile:       indexFile,
		indexLock:       indexLock,
		checkpointIndex: make(map[workflow.CheckpointInfo]struct{}),
	}
	if err := store.loadIndex(); err != nil {
		_ = indexFile.Close()
		_ = indexLock.Unlock()
		_ = root.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the store's index file handle and process-exclusive lock.
func (s *FileSystemJSONStore) Close() error {
	if s.indexFile == nil {
		return nil
	}
	closeErr := s.indexFile.Close()
	unlockErr := s.indexLock.Unlock()
	rootCloseErr := s.root.Close()
	s.indexFile = nil
	s.indexLock = nil
	if closeErr != nil {
		return closeErr
	}
	if unlockErr != nil {
		return unlockErr
	}
	return rootCloseErr
}

func (s *FileSystemJSONStore) checkOpen() error {
	if s.indexFile == nil {
		return fmt.Errorf("jsonstore: store at %q is closed", s.root.Name())
	}
	return nil
}

func validateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("jsonstore: sessionID cannot be empty")
	}
	return nil
}

func validateCheckpointInfo(info workflow.CheckpointInfo) error {
	if info.SessionID == "" {
		return fmt.Errorf("jsonstore: checkpoint sessionID cannot be empty")
	}
	if info.CheckpointID == "" {
		return fmt.Errorf("jsonstore: checkpointID cannot be empty")
	}
	return nil
}

func validateCheckpointSession(sessionID string, info workflow.CheckpointInfo) error {
	if info.SessionID != sessionID {
		return fmt.Errorf("jsonstore: checkpoint sessionID %q does not match sessionID %q", info.SessionID, sessionID)
	}
	return nil
}

func validateCheckpointLookup(sessionID string, info workflow.CheckpointInfo) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if err := validateCheckpointInfo(info); err != nil {
		return err
	}
	return validateCheckpointSession(sessionID, info)
}

func validateParentSession(sessionID string, parent *workflow.CheckpointInfo) error {
	if parent != nil && *parent != (workflow.CheckpointInfo{}) && parent.SessionID != sessionID {
		return fmt.Errorf("jsonstore: parent sessionID %q does not match sessionID %q", parent.SessionID, sessionID)
	}
	return nil
}

func checkpointFileName(sessionID string, info workflow.CheckpointInfo) string {
	protoPath := sessionID + "_" + info.CheckpointID + ".json"
	// Escape the proto path so session and checkpoint IDs cannot introduce path
	// separators or root-relative names. Dots are escaped separately because
	// QueryEscape leaves them unchanged.
	escaped := url.QueryEscape(protoPath)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	return strings.ReplaceAll(escaped, ".", "%2E")
}

type indexEntry struct {
	CheckpointInfo workflow.CheckpointInfo  `json:"checkpointInfo"`
	FileName       string                   `json:"fileName"`
	Parent         *workflow.CheckpointInfo `json:"parent,omitempty"`
}

func (e indexEntry) info() workflow.CheckpointInfo {
	return e.CheckpointInfo
}

func (s *FileSystemJSONStore) loadIndex() error {
	if _, err := s.indexFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("jsonstore: load index: %w", err)
	}

	scanner := bufio.NewScanner(s.indexFile)
	scanner.Buffer(nil, 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry indexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("jsonstore: could not load store at %q; index corrupted: %w", s.root.Name(), err)
		}
		info := entry.info()
		if err := validateCheckpointInfo(info); err != nil {
			return fmt.Errorf("jsonstore: could not load store at %q; index corrupted: %w", s.root.Name(), err)
		}
		if entry.FileName == "" {
			entry.FileName = checkpointFileName(info.SessionID, info)
		}
		s.index = append(s.index, entry)
		s.checkpointIndex[info] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("jsonstore: load index: %w", err)
	}
	if _, err := s.indexFile.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("jsonstore: seek index: %w", err)
	}
	return nil
}

func (s *FileSystemJSONStore) getUnusedCheckpointInfo(sessionID string) workflow.CheckpointInfo {
	for {
		info := workflow.NewCheckpointInfo(sessionID)
		if _, ok := s.checkpointIndex[info]; !ok {
			return info
		}
	}
}

func (s *FileSystemJSONStore) appendIndexEntry(entry indexEntry) error {
	offset, err := s.indexFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := s.indexFile.Write(data); err != nil {
		_ = s.indexFile.Truncate(offset)
		_, _ = s.indexFile.Seek(offset, io.SeekStart)
		return err
	}
	if err := s.indexFile.Sync(); err != nil {
		_ = s.indexFile.Truncate(offset)
		_, _ = s.indexFile.Seek(offset, io.SeekStart)
		return err
	}
	return nil
}

// CreateCheckpoint implements [Store].
func (s *FileSystemJSONStore) CreateCheckpoint(_ context.Context, sessionID string, data json.RawMessage, parent *workflow.CheckpointInfo) (workflow.CheckpointInfo, error) {
	if err := validateSessionID(sessionID); err != nil {
		return workflow.CheckpointInfo{}, err
	}
	if err := validateParentSession(sessionID, parent); err != nil {
		return workflow.CheckpointInfo{}, err
	}
	if !json.Valid(data) {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: checkpoint data is not valid JSON")
	}

	if err := s.checkOpen(); err != nil {
		return workflow.CheckpointInfo{}, err
	}

	info := s.getUnusedCheckpointInfo(sessionID)
	checkpointName := checkpointFileName(sessionID, info)

	if err := s.root.WriteFile(checkpointName, data, 0o644); err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: write checkpoint: %w", err)
	}

	var parentCopy *workflow.CheckpointInfo
	if parent != nil {
		p := *parent
		parentCopy = &p
	}
	entry := indexEntry{CheckpointInfo: info, FileName: checkpointName, Parent: parentCopy}
	if err := s.appendIndexEntry(entry); err != nil {
		_ = s.root.Remove(checkpointName)
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: write index: %w", err)
	}
	s.index = append(s.index, entry)
	s.checkpointIndex[info] = struct{}{}

	return info, nil
}

// RetrieveCheckpoint implements [Store].
func (s *FileSystemJSONStore) RetrieveCheckpoint(_ context.Context, sessionID string, info workflow.CheckpointInfo) (json.RawMessage, error) {
	if err := validateCheckpointLookup(sessionID, info); err != nil {
		return nil, err
	}

	if err := s.checkOpen(); err != nil {
		return nil, err
	}

	if _, ok := s.checkpointIndex[info]; !ok {
		return nil, fmt.Errorf("jsonstore: checkpoint %s not found for session %s", info.CheckpointID, sessionID)
	}

	data, err := s.root.ReadFile(checkpointFileName(sessionID, info))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("jsonstore: checkpoint %s not found for session %s", info.CheckpointID, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("jsonstore: read checkpoint: %w", err)
	}
	return data, nil
}

// RetrieveIndex implements [Store].
func (s *FileSystemJSONStore) RetrieveIndex(_ context.Context, sessionID string, withParent *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	if err := validateParentSession(sessionID, withParent); err != nil {
		return nil, err
	}

	if err := s.checkOpen(); err != nil {
		return nil, err
	}

	var result []workflow.CheckpointInfo
	for _, entry := range s.index {
		info := entry.info()
		if info.SessionID != sessionID {
			continue
		}
		if withParent != nil {
			if entry.Parent == nil || *entry.Parent != *withParent {
				continue
			}
		}
		result = append(result, info)
	}
	return result, nil
}

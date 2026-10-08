// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

// IsolationKeyScopedTaskStoreConfig configures task-store partitioning.
type IsolationKeyScopedTaskStoreConfig struct {
	// ResolveKey resolves the isolation key for each operation. A nil resolver or
	// an empty key means no key is available. Resolver errors are always returned.
	// The resolver must be safe for concurrent calls and use a trusted identity,
	// not an unvalidated client-supplied tenant or task identifier.
	ResolveKey func(context.Context) (string, error)
	// AllowUnscoped permits native, unpartitioned access when no key is available.
	// The default is false: missing keys fail before accessing the inner store.
	AllowUnscoped bool
}

// NewIsolationKeyScopedTaskStore partitions inner by a context-resolved isolation key.
// It panics if inner is nil. Task and context identifiers are scoped on writes
// and restored on reads, including typed identifiers in messages and events.
// Identifiers are always scoped, even if they already resemble scoped values.
// Absent optional message task and context identifiers remain empty.
// Create does not generate identifiers; callers supply native task objects.
//
// The wrapper copies the objects and slices whose identifiers it changes; other
// payloads remain shared and read-only. It does not mutate caller-owned inputs
// or inner-owned results. Native ownership checks, versions, errors and context
// values are preserved; isolation does not replace authentication.
//
// List scopes nonempty context filters and walks native pages until the requested
// page is filled with partition-local tasks or the results are exhausted. It uses
// native cursors without caching tasks or introducing another cursor format.
// PageSize reports the returned task count. TotalSize counts partition-local tasks
// matching the filters before pagination. Computing it requires enumerating all
// matching native pages on each call; only the requested visible page is retained.
// Native cursors can contain scoped identifiers; configure inner to conceal them
// if necessary. Concurrent writes retain the inner store's pagination semantics;
// the enumeration does not provide a snapshot across native pages.
// Supply the result to [github.com/a2aproject/a2a-go/v2/a2asrv.WithTaskStore]
// when constructing a native handler.
func NewIsolationKeyScopedTaskStore(inner taskstore.Store, config IsolationKeyScopedTaskStoreConfig) taskstore.Store {
	if inner == nil {
		panic("a2aprovider: inner task store cannot be nil")
	}
	return &isolationKeyScopedTaskStore{inner: inner, config: config}
}

type isolationKeyScopedTaskStore struct {
	inner  taskstore.Store
	config IsolationKeyScopedTaskStoreConfig
}

func (s *isolationKeyScopedTaskStore) prefix(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var key string
	if s.config.ResolveKey != nil {
		var err error
		key, err = s.config.ResolveKey(ctx)
		if err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key == "" {
		if !s.config.AllowUnscoped {
			return "", errors.New("a2aprovider: task isolation key is required")
		}
		return "", nil
	}
	return strings.ReplaceAll(strings.ReplaceAll(key, `\`, `\\`), ":", `\:`) + "::", nil
}

func (s *isolationKeyScopedTaskStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	prefix, err := s.prefix(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	if task == nil {
		return taskstore.TaskVersionMissing, a2a.ErrInvalidParams
	}
	if prefix != "" {
		task = mapIsolationTask(task, scopeIsolationID(prefix))
	}
	return s.inner.Create(ctx, task)
}

func (s *isolationKeyScopedTaskStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	prefix, err := s.prefix(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	if req == nil || req.Task == nil {
		return taskstore.TaskVersionMissing, a2a.ErrInvalidParams
	}
	if prefix != "" {
		mapID := scopeIsolationID(prefix)
		copy := *req
		copy.Task = mapIsolationTask(req.Task, mapID)
		copy.PrevTask = mapIsolationTask(req.PrevTask, mapID)
		copy.Event = mapIsolationEvent(req.Event, mapID)
		req = &copy
	}
	return s.inner.Update(ctx, req)
}

func (s *isolationKeyScopedTaskStore) Get(ctx context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
	prefix, err := s.prefix(ctx)
	if err != nil {
		return nil, err
	}
	stored, err := s.inner.Get(ctx, a2a.TaskID(prefix+string(id)))
	if err != nil || stored == nil || prefix == "" {
		return stored, err
	}
	if !isolationTaskInScope(stored.Task, prefix) {
		return nil, a2a.ErrTaskNotFound
	}
	copy := *stored
	copy.Task = mapIsolationTask(stored.Task, unscopeIsolationID(prefix))
	return &copy, nil
}

func (s *isolationKeyScopedTaskStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	prefix, err := s.prefix(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, a2a.ErrInvalidParams
	}
	request := *req
	if prefix != "" && request.ContextID != "" {
		request.ContextID = prefix + request.ContextID
	}
	response, err := s.inner.List(ctx, &request)
	if err != nil || response == nil || prefix == "" {
		return response, err
	}
	copy := *response
	copy.Tasks = make([]*a2a.Task, 0, len(response.Tasks))
	copy.TotalSize = 0
	copy.NextPageToken = ""
	limit := request.PageSize
	if limit == 0 {
		limit = response.PageSize
	}
	countFromStart := request.PageToken == ""
	pageComplete := false
	mapID := unscopeIsolationID(prefix)
	for {
		for _, task := range response.Tasks {
			if !isolationTaskInScope(task, prefix) {
				continue
			}
			if countFromStart {
				copy.TotalSize++
			}
			if len(copy.Tasks) == limit {
				// This one-task lookahead belongs to the next visible page. Keep
				// the cursor from before it so the next call retrieves it again.
				if !pageComplete {
					copy.NextPageToken = request.PageToken
					pageComplete = true
				}
				continue
			}
			copy.Tasks = append(copy.Tasks, mapIsolationTask(task, mapID))
		}
		if response.NextPageToken == "" || (pageComplete && !countFromStart) {
			break
		}
		request.PageToken = response.NextPageToken
		request.PageSize = max(1, limit-len(copy.Tasks))
		if pageComplete {
			request.PageSize = max(1, limit)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err = s.inner.List(ctx, &request)
		if err != nil || response == nil {
			return response, err
		}
	}
	if !countFromStart {
		// A continuation cursor excludes earlier tasks, but TotalSize is the
		// count before pagination. Reuse the same filters and identity from the
		// beginning without resolving the isolation key again or retaining tasks.
		request.PageToken = ""
		request.PageSize = req.PageSize
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			response, err = s.inner.List(ctx, &request)
			if err != nil || response == nil {
				return response, err
			}
			for _, task := range response.Tasks {
				if isolationTaskInScope(task, prefix) {
					copy.TotalSize++
				}
			}
			if response.NextPageToken == "" {
				break
			}
			request.PageToken = response.NextPageToken
		}
	}
	copy.PageSize = len(copy.Tasks)
	return &copy, nil
}

func isolationTaskInScope(task *a2a.Task, prefix string) bool {
	return task != nil && strings.HasPrefix(string(task.ID), prefix) && strings.HasPrefix(task.ContextID, prefix)
}

func scopeIsolationID(prefix string) func(string) string {
	return func(id string) string { return prefix + id }
}

func unscopeIsolationID(prefix string) func(string) string {
	return func(id string) string { return strings.TrimPrefix(id, prefix) }
}

// Only identifier-bearing objects need ownership; payloads are read-only.
func mapIsolationTask(task *a2a.Task, mapID func(string) string) *a2a.Task {
	if task == nil {
		return nil
	}
	copy := *task
	copy.ID = a2a.TaskID(mapID(string(task.ID)))
	copy.ContextID = mapID(task.ContextID)
	copy.Status.Message = mapIsolationMessage(task.Status.Message, mapID)
	copy.History = slices.Clone(task.History)
	for i, msg := range copy.History {
		copy.History[i] = mapIsolationMessage(msg, mapID)
	}
	return &copy
}

func mapIsolationMessage(msg *a2a.Message, mapID func(string) string) *a2a.Message {
	if msg == nil {
		return nil
	}
	copy := *msg
	if msg.TaskID != "" {
		copy.TaskID = a2a.TaskID(mapID(string(msg.TaskID)))
	}
	if msg.ContextID != "" {
		copy.ContextID = mapID(msg.ContextID)
	}
	copy.ReferenceTasks = slices.Clone(msg.ReferenceTasks)
	for i, id := range copy.ReferenceTasks {
		copy.ReferenceTasks[i] = a2a.TaskID(mapID(string(id)))
	}
	return &copy
}

func mapIsolationEvent(event a2a.Event, mapID func(string) string) a2a.Event {
	switch event := event.(type) {
	case *a2a.Task:
		return mapIsolationTask(event, mapID)
	case *a2a.Message:
		return mapIsolationMessage(event, mapID)
	case *a2a.TaskStatusUpdateEvent:
		if event == nil {
			return event
		}
		copy := *event
		copy.TaskID = a2a.TaskID(mapID(string(event.TaskID)))
		copy.ContextID = mapID(event.ContextID)
		copy.Status.Message = mapIsolationMessage(event.Status.Message, mapID)
		return &copy
	case *a2a.TaskArtifactUpdateEvent:
		if event == nil {
			return event
		}
		copy := *event
		copy.TaskID = a2a.TaskID(mapID(string(event.TaskID)))
		copy.ContextID = mapID(event.ContextID)
		return &copy
	default:
		return event
	}
}

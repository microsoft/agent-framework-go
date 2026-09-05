// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

// ErrTaskStoreIsolationKeyRequired is returned by a strict
// [IsolationKeyScopedTaskStore] when no isolation key is available for an
// operation. Callers can match it with [errors.Is].
var ErrTaskStoreIsolationKeyRequired = errors.New("task store isolation key is required but was not provided")

// TaskStoreIsolationKeyProvider returns the current logical isolation key used
// to scope task-store operations. Returning an empty string disables scoping for
// that call unless [NewIsolationKeyScopedTaskStore] was configured as strict.
type TaskStoreIsolationKeyProvider func(context.Context) (string, error)

// IsolationKeyScopedTaskStore scopes A2A task-store operations by an isolation
// key so callers with different logical identities can reuse bare task and
// context IDs without seeing each other's tasks.
type IsolationKeyScopedTaskStore struct {
	inner       taskstore.Store
	keyProvider TaskStoreIsolationKeyProvider
	strict      bool
}

var _ taskstore.Store = (*IsolationKeyScopedTaskStore)(nil)

// NewIsolationKeyScopedTaskStore wraps an existing A2A task store with
// isolation-key scoping. It panics if inner is nil.
func NewIsolationKeyScopedTaskStore(inner taskstore.Store, keyProvider TaskStoreIsolationKeyProvider, strict bool) *IsolationKeyScopedTaskStore {
	if inner == nil {
		panic("a2aprovider: task store cannot be nil")
	}
	return &IsolationKeyScopedTaskStore{
		inner:       inner,
		keyProvider: keyProvider,
		strict:      strict,
	}
}

func (s *IsolationKeyScopedTaskStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	key, err := s.isolationKey(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	scopedTask, err := scopeTask(task, key)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	return s.inner.Create(ctx, scopedTask)
}

func (s *IsolationKeyScopedTaskStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	key, err := s.isolationKey(ctx)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	scopedTask, err := scopeTask(req.Task, key)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	scopedPrevTask, err := scopeTask(req.PrevTask, key)
	if err != nil {
		return taskstore.TaskVersionMissing, err
	}
	return s.inner.Update(ctx, &taskstore.UpdateRequest{
		Task:        scopedTask,
		Event:       req.Event,
		PrevTask:    scopedPrevTask,
		PrevVersion: req.PrevVersion,
	})
}

func (s *IsolationKeyScopedTaskStore) Get(ctx context.Context, taskID a2a.TaskID) (*taskstore.StoredTask, error) {
	key, err := s.isolationKey(ctx)
	if err != nil {
		return nil, err
	}
	storedTask, err := s.inner.Get(ctx, a2a.TaskID(scopeID(string(taskID), key)))
	if err != nil {
		return nil, err
	}
	task, err := unscopeTask(storedTask.Task, key)
	if err != nil {
		return nil, err
	}
	return &taskstore.StoredTask{
		Task:    task,
		Version: storedTask.Version,
		User:    storedTask.User,
	}, nil
}

func (s *IsolationKeyScopedTaskStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	key, err := s.isolationKey(ctx)
	if err != nil {
		return nil, err
	}
	request := req
	if key != "" && req.ContextID != "" {
		request = cloneListTasksRequestWithContextID(req, scopeID(req.ContextID, key))
	}
	response, err := s.inner.List(ctx, request)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return response, nil
	}

	scopedTasks := make([]*a2a.Task, 0, len(response.Tasks))
	for _, task := range response.Tasks {
		if !taskIsInScope(task, key) {
			continue
		}
		unscopedTask, err := unscopeTask(task, key)
		if err != nil {
			return nil, err
		}
		scopedTasks = append(scopedTasks, unscopedTask)
	}

	out := *response
	out.Tasks = scopedTasks
	out.PageSize = len(scopedTasks)
	return &out, nil
}

func (s *IsolationKeyScopedTaskStore) isolationKey(ctx context.Context) (string, error) {
	if s.keyProvider == nil {
		if s.strict {
			return "", ErrTaskStoreIsolationKeyRequired
		}
		return "", nil
	}

	key, err := s.keyProvider(ctx)
	if err != nil {
		return "", err
	}
	if key == "" && s.strict {
		return "", ErrTaskStoreIsolationKeyRequired
	}
	return key, nil
}

func scopeTask(task *a2a.Task, key string) (*a2a.Task, error) {
	if task == nil || key == "" {
		return task, nil
	}
	return cloneTask(task, func(clone *a2a.Task) {
		clone.ID = a2a.TaskID(scopeID(string(clone.ID), key))
		clone.ContextID = scopeID(clone.ContextID, key)
	})
}

func unscopeTask(task *a2a.Task, key string) (*a2a.Task, error) {
	if task == nil || key == "" {
		return task, nil
	}
	return cloneTask(task, func(clone *a2a.Task) {
		clone.ID = a2a.TaskID(unscopeID(string(clone.ID), key))
		clone.ContextID = unscopeID(clone.ContextID, key)
	})
}

func cloneTask(task *a2a.Task, mutate func(*a2a.Task)) (*a2a.Task, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	var clone a2a.Task
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	mutate(&clone)
	return &clone, nil
}

func cloneListTasksRequestWithContextID(req *a2a.ListTasksRequest, contextID string) *a2a.ListTasksRequest {
	clone := *req
	clone.ContextID = contextID
	return &clone
}

func taskIsInScope(task *a2a.Task, key string) bool {
	return strings.HasPrefix(task.ContextID, scopedPrefix(key))
}

func scopeID(id string, key string) string {
	if key == "" {
		return id
	}
	return scopedPrefix(key) + id
}

func unscopeID(scopedID string, key string) string {
	prefix := scopedPrefix(key)
	if !strings.HasPrefix(scopedID, prefix) {
		return scopedID
	}
	return scopedID[len(prefix):]
}

func scopedPrefix(key string) string {
	return escapeIsolationKey(key) + "::"
}

func escapeIsolationKey(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	return strings.ReplaceAll(key, ":", `\:`)
}

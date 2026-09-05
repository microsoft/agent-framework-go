// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

type isolationKeyContextKey struct{}

func contextWithIsolationKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, isolationKeyContextKey{}, key)
}

func isolationKeyProvider(ctx context.Context) (string, error) {
	key, _ := ctx.Value(isolationKeyContextKey{}).(string)
	return key, nil
}

type isolationTestInterceptor struct {
	a2asrv.PassthroughCallInterceptor
	user string
	key  string
}

func (i isolationTestInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	callCtx.User = a2asrv.NewAuthenticatedUser(i.user, nil)
	return contextWithIsolationKey(ctx, i.key), nil, nil
}

func TestIsolationKeyScopedTaskStore_AllowsDuplicateBareTaskIDsAcrossKeys(t *testing.T) {
	baseStore := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: func(context.Context) (string, error) { return "alice", nil },
	})
	store := a2aprovider.NewIsolationKeyScopedTaskStore(baseStore, isolationKeyProvider, true)

	taskA := &a2a.Task{ID: "task-1", ContextID: "ctx-1"}
	if _, err := store.Create(contextWithIsolationKey(context.Background(), "tenant-a"), taskA); err != nil {
		t.Fatalf("Create(tenant-a) returned error: %v", err)
	}
	taskB := &a2a.Task{ID: "task-1", ContextID: "ctx-1"}
	if _, err := store.Create(contextWithIsolationKey(context.Background(), "tenant-b"), taskB); err != nil {
		t.Fatalf("Create(tenant-b) returned error: %v", err)
	}

	gotA, err := store.Get(contextWithIsolationKey(context.Background(), "tenant-a"), "task-1")
	if err != nil {
		t.Fatalf("Get(tenant-a) returned error: %v", err)
	}
	if gotA.Task.ID != "task-1" || gotA.Task.ContextID != "ctx-1" {
		t.Fatalf("tenant-a task = (%q, %q), want bare IDs", gotA.Task.ID, gotA.Task.ContextID)
	}
	if _, err := store.Get(contextWithIsolationKey(context.Background(), "tenant-c"), "task-1"); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("Get(tenant-c) error = %v, want %v", err, a2a.ErrTaskNotFound)
	}

	listA, err := store.List(contextWithIsolationKey(context.Background(), "tenant-a"), &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatalf("List(tenant-a) returned error: %v", err)
	}
	if len(listA.Tasks) != 1 {
		t.Fatalf("List(tenant-a) task count = %d, want 1", len(listA.Tasks))
	}
	if listA.Tasks[0].ID != "task-1" || listA.Tasks[0].ContextID != "ctx-1" {
		t.Fatalf("List(tenant-a) task = (%q, %q), want bare IDs", listA.Tasks[0].ID, listA.Tasks[0].ContextID)
	}
	if listA.TotalSize != 2 {
		t.Fatalf("List(tenant-a) totalSize = %d, want 2", listA.TotalSize)
	}
	if listA.PageSize != 1 {
		t.Fatalf("List(tenant-a) pageSize = %d, want 1", listA.PageSize)
	}
}

func TestIsolationKeyScopedTaskStore_UpdateAndListUseScopedContext(t *testing.T) {
	baseStore := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: func(context.Context) (string, error) { return "alice", nil },
	})
	store := a2aprovider.NewIsolationKeyScopedTaskStore(baseStore, isolationKeyProvider, true)
	ctxA := contextWithIsolationKey(context.Background(), "tenant-a")
	ctxB := contextWithIsolationKey(context.Background(), "tenant-b")

	task := &a2a.Task{
		ID:        "task-1",
		ContextID: "ctx-1",
		Status:    a2a.TaskStatus{State: a2a.TaskStateSubmitted},
	}
	version, err := store.Create(ctxA, task)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	updated := &a2a.Task{
		ID:        "task-1",
		ContextID: "ctx-1",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	if _, err := store.Update(ctxA, &taskstore.UpdateRequest{
		Task:        updated,
		PrevTask:    task,
		PrevVersion: version,
	}); err != nil {
		t.Fatalf("Update(tenant-a) returned error: %v", err)
	}
	if _, err := store.Update(ctxB, &taskstore.UpdateRequest{
		Task:        updated,
		PrevTask:    task,
		PrevVersion: version,
	}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("Update(tenant-b) error = %v, want %v", err, a2a.ErrTaskNotFound)
	}

	listA, err := store.List(ctxA, &a2a.ListTasksRequest{ContextID: "ctx-1", Status: a2a.TaskStateCompleted})
	if err != nil {
		t.Fatalf("List(tenant-a) returned error: %v", err)
	}
	if len(listA.Tasks) != 1 {
		t.Fatalf("List(tenant-a) task count = %d, want 1", len(listA.Tasks))
	}
	if listA.Tasks[0].ContextID != "ctx-1" {
		t.Fatalf("List(tenant-a) context id = %q, want %q", listA.Tasks[0].ContextID, "ctx-1")
	}

	listB, err := store.List(ctxB, &a2a.ListTasksRequest{ContextID: "ctx-1"})
	if err != nil {
		t.Fatalf("List(tenant-b) returned error: %v", err)
	}
	if len(listB.Tasks) != 0 {
		t.Fatalf("List(tenant-b) task count = %d, want 0", len(listB.Tasks))
	}
}

func TestIsolationKeyScopedTaskStore_StrictModeRequiresKey(t *testing.T) {
	baseStore := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: func(context.Context) (string, error) { return "alice", nil },
	})
	store := a2aprovider.NewIsolationKeyScopedTaskStore(baseStore, isolationKeyProvider, true)

	_, err := store.Create(context.Background(), &a2a.Task{ID: "task-1", ContextID: "ctx-1"})
	if err == nil {
		t.Fatal("Create() error = nil, want non-nil")
	}
	if !errors.Is(err, a2aprovider.ErrTaskStoreIsolationKeyRequired) {
		t.Fatalf("Create() error = %v, want ErrTaskStoreIsolationKeyRequired", err)
	}
}

func TestRequestHandler_WithIsolationKeyScopedTaskStore_IsolatesTasksByKey(t *testing.T) {
	hostedAgent := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "m1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "hello from agent"}},
			}, nil)
		}
	})

	baseStore := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})
	store := a2aprovider.NewIsolationKeyScopedTaskStore(baseStore, isolationKeyProvider, true)
	handlerA := newRequestHandler(
		hostedAgent,
		a2aprovider.ExecutorConfig{},
		a2asrv.WithTaskStore(store),
		a2asrv.WithCallInterceptors(isolationTestInterceptor{user: "alice", key: "tenant-a"}),
	)
	handlerB := newRequestHandler(
		hostedAgent,
		a2aprovider.ExecutorConfig{},
		a2asrv.WithTaskStore(store),
		a2asrv.WithCallInterceptors(isolationTestInterceptor{user: "alice", key: "tenant-b"}),
	)

	task := collectFirstStreamingTask(t, handlerA.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	}))
	if task.ID == "" {
		t.Fatal("expected task id")
	}

	gotTask, err := handlerA.GetTask(context.Background(), &a2a.GetTaskRequest{ID: task.ID})
	if err != nil {
		t.Fatalf("GetTask(tenant-a) returned error: %v", err)
	}
	if gotTask.ID != task.ID {
		t.Fatalf("GetTask(tenant-a) task id = %q, want %q", gotTask.ID, task.ID)
	}
	if _, err := handlerB.GetTask(context.Background(), &a2a.GetTaskRequest{ID: task.ID}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("GetTask(tenant-b) error = %v, want %v", err, a2a.ErrTaskNotFound)
	}

	listB, err := handlerB.ListTasks(context.Background(), &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatalf("ListTasks(tenant-b) returned error: %v", err)
	}
	if len(listB.Tasks) != 0 {
		t.Fatalf("ListTasks(tenant-b) task count = %d, want 0", len(listB.Tasks))
	}
	if listB.TotalSize != 1 {
		t.Fatalf("ListTasks(tenant-b) totalSize = %d, want 1", listB.TotalSize)
	}
	if listB.PageSize != 0 {
		t.Fatalf("ListTasks(tenant-b) pageSize = %d, want 0", listB.PageSize)
	}
}

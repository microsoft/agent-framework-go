// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

type isolationStoreStub struct {
	taskstore.Store
	create func(context.Context, *a2a.Task) (taskstore.TaskVersion, error)
	update func(context.Context, *taskstore.UpdateRequest) (taskstore.TaskVersion, error)
	get    func(context.Context, a2a.TaskID) (*taskstore.StoredTask, error)
	list   func(context.Context, *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error)
}

func (s isolationStoreStub) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	return s.create(ctx, task)
}

func (s isolationStoreStub) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	return s.update(ctx, req)
}

func (s isolationStoreStub) Get(ctx context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
	return s.get(ctx, id)
}

func (s isolationStoreStub) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return s.list(ctx, req)
}

func isolationConfig(key string) a2aprovider.IsolationKeyScopedTaskStoreConfig {
	return a2aprovider.IsolationKeyScopedTaskStoreConfig{
		ResolveKey: func(context.Context) (string, error) { return key, nil },
	}
}

func TestIsolationKeyTaskStoreGetScopesTaskID(t *testing.T) {
	var got a2a.TaskID
	var calls int
	inner := isolationStoreStub{get: func(_ context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
		calls++
		got = id
		return nil, a2a.ErrTaskNotFound
	}}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	_, _ = store.Get(t.Context(), "task-001")
	if got != "alice::task-001" || calls != 1 {
		t.Fatalf("delegated ID = %q, calls = %d", got, calls)
	}
}

func TestIsolationKeyTaskStoreCreateScopesIdentifiers(t *testing.T) {
	var calls int
	inner := isolationStoreStub{create: func(_ context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
		calls++
		if task.ID != "alice::task-001" || task.ContextID != "alice::ctx-1" {
			t.Fatalf("delegated task = %+v", task)
		}
		return 1, nil
	}}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	task := &a2a.Task{ID: "task-001", ContextID: "ctx-1"}
	if _, err := store.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || task.ContextID != "ctx-1" {
		t.Fatalf("calls = %d, caller context = %q", calls, task.ContextID)
	}
}

func TestIsolationKeyTaskStoreListScopesContextFilter(t *testing.T) {
	var got string
	var calls int
	inner := isolationStoreStub{list: func(_ context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
		calls++
		got = req.ContextID
		return &a2a.ListTasksResponse{}, nil
	}}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	req := &a2a.ListTasksRequest{ContextID: "ctx-1"}
	if _, err := store.List(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if got != "alice::ctx-1" || req.ContextID != "ctx-1" || calls != 1 {
		t.Fatalf("delegated context = %q, caller context = %q", got, req.ContextID)
	}
}

func TestIsolationKeyTaskStoreListLeavesEmptyContextFilter(t *testing.T) {
	var got string
	var calls int
	inner := isolationStoreStub{list: func(_ context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
		calls++
		got = req.ContextID
		return &a2a.ListTasksResponse{}, nil
	}}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	if _, err := store.List(t.Context(), &a2a.ListTasksRequest{}); err != nil {
		t.Fatal(err)
	}
	if got != "" || calls != 1 {
		t.Fatalf("delegated context = %q, want empty", got)
	}
}

func TestIsolationKeyTaskStoreStrictMissingKey(t *testing.T) {
	store := a2aprovider.NewIsolationKeyScopedTaskStore(isolationStoreStub{}, isolationConfig(""))
	if _, err := store.Get(t.Context(), "task-001"); err == nil {
		t.Fatal("missing key succeeded")
	}
}

func TestIsolationKeyTaskStoreUnscopedMissingKey(t *testing.T) {
	var got a2a.TaskID
	var calls int
	inner := isolationStoreStub{get: func(_ context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
		calls++
		got = id
		return nil, a2a.ErrTaskNotFound
	}}
	cfg := isolationConfig("")
	cfg.AllowUnscoped = true
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, cfg)
	_, _ = store.Get(t.Context(), "task-001")
	if got != "task-001" || calls != 1 {
		t.Fatalf("delegated ID = %q", got)
	}
}

func TestIsolationKeyTaskStoreUnscopedNoResolver(t *testing.T) {
	var got a2a.TaskID
	var calls int
	inner := isolationStoreStub{get: func(_ context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
		calls++
		got = id
		return nil, a2a.ErrTaskNotFound
	}}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, a2aprovider.IsolationKeyScopedTaskStoreConfig{AllowUnscoped: true})
	_, _ = store.Get(t.Context(), "task-001")
	if got != "task-001" || calls != 1 {
		t.Fatalf("delegated ID = %q", got)
	}
}

func TestIsolationKeyTaskStoreEscapesColons(t *testing.T) {
	var got a2a.TaskID
	var calls int
	inner := isolationStoreStub{get: func(_ context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
		calls++
		got = id
		return nil, a2a.ErrTaskNotFound
	}}
	_, _ = a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("tenant:sub")).Get(t.Context(), "task-001")
	if got != `tenant\:sub::task-001` || calls != 1 {
		t.Fatalf("delegated ID = %q", got)
	}
}

func TestIsolationKeyTaskStoreEscapesBackslashes(t *testing.T) {
	var got a2a.TaskID
	var calls int
	inner := isolationStoreStub{get: func(_ context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
		calls++
		got = id
		return nil, a2a.ErrTaskNotFound
	}}
	_, _ = a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig(`domain\user`)).Get(t.Context(), "task-001")
	if got != `domain\\user::task-001` || calls != 1 {
		t.Fatalf("delegated ID = %q", got)
	}
}

func TestIsolationKeyTaskStoreNilInner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil inner store")
		}
	}()
	a2aprovider.NewIsolationKeyScopedTaskStore(nil, a2aprovider.IsolationKeyScopedTaskStoreConfig{AllowUnscoped: true})
}

type isolationContextKey struct{}

func isolationContext(ctx context.Context, key, user string) context.Context {
	ctx, call := a2asrv.NewCallContext(ctx, nil)
	call.User = &a2asrv.User{Name: user, Authenticated: true}
	ctx = a2a.AttachTenant(ctx, "wire-tenant")
	return context.WithValue(ctx, isolationContextKey{}, key)
}

func newIsolationStore() taskstore.Store {
	inner := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: a2asrv.NewTaskStoreAuthenticator(),
		TimeProvider:  func() time.Time { return time.Unix(100, 0) },
	})
	return a2aprovider.NewIsolationKeyScopedTaskStore(inner, a2aprovider.IsolationKeyScopedTaskStoreConfig{
		ResolveKey: func(ctx context.Context) (string, error) {
			key, _ := ctx.Value(isolationContextKey{}).(string)
			return key, nil
		},
	})
}

func isolationTask(id a2a.TaskID, contextID string) *a2a.Task {
	task := a2a.NewSubmittedTask(a2a.TaskInfo{TaskID: id, ContextID: contextID}, nil)
	task.History = []*a2a.Message{a2a.NewMessageForTask(a2a.MessageRoleUser, task, a2a.NewTextPart("input"))}
	task.History[0].ReferenceTasks = []a2a.TaskID{"reference"}
	task.Status.Message = a2a.NewMessageForTask(a2a.MessageRoleAgent, task, a2a.NewTextPart("status"))
	task.Artifacts = []*a2a.Artifact{{ID: "artifact", Parts: a2a.ContentParts{a2a.NewTextPart("output")}}}
	task.Metadata = map[string]any{"number": int64(42)}
	return task
}

func TestIsolationKeyTaskStorePartitions(t *testing.T) {
	store := newIsolationStore()
	alice := isolationContext(t.Context(), "alice", "owner")
	bob := isolationContext(t.Context(), "bob", "owner")
	task := isolationTask("same-task", "same-context")
	version, err := store.Create(alice, task)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "same-task" || task.ContextID != "same-context" || task.History[0].TaskID != task.ID {
		t.Fatal("Create changed caller-owned task")
	}
	if _, err := store.Get(bob, task.ID); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("Bob Get = %v", err)
	}
	if _, err := store.Update(bob, &taskstore.UpdateRequest{Task: task, PrevVersion: version}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("Bob Update = %v", err)
	}
	if _, err := store.Create(bob, task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(alice, task); !errors.Is(err, taskstore.ErrTaskAlreadyExists) {
		t.Fatalf("duplicate Create = %v", err)
	}
	stored, err := store.Get(alice, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Task, task) || stored.User != "owner" || stored.Version != version {
		t.Fatalf("round trip = %+v", stored)
	}
	previous := stored.Task
	updated := *previous
	updated.Status.State = a2a.TaskStateWorking
	newVersion, err := store.Update(alice, &taskstore.UpdateRequest{Task: &updated, PrevTask: previous, PrevVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(alice, &taskstore.UpdateRequest{Task: &updated, PrevVersion: version}); !errors.Is(err, taskstore.ErrConcurrentModification) {
		t.Fatalf("stale Update = %v", err)
	}
	for _, tc := range []struct {
		ctx     context.Context
		state   a2a.TaskState
		version taskstore.TaskVersion
	}{{alice, a2a.TaskStateWorking, newVersion}, {bob, a2a.TaskStateSubmitted, version}} {
		got, err := store.Get(tc.ctx, task.ID)
		if err != nil || got.Task.Status.State != tc.state || got.Version != tc.version {
			t.Fatalf("partition Get = %+v, %v", got, err)
		}
	}
	for _, ctx := range []context.Context{alice, bob} {
		for _, filter := range []string{"", "same-context", "alice::same-context", "bob::same-context"} {
			got, err := store.List(ctx, &a2a.ListTasksRequest{ContextID: filter, IncludeArtifacts: true})
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if filter != "" && filter != "same-context" {
				want = 0
			}
			if len(got.Tasks) != want {
				t.Fatalf("filter %q: got %d tasks, want %d", filter, len(got.Tasks), want)
			}
			if want == 1 && (got.Tasks[0].ID != task.ID || got.Tasks[0].ContextID != task.ContextID || got.Tasks[0].History[0].TaskID != task.ID) {
				t.Fatalf("scoped identifiers on wire: %+v", got.Tasks[0])
			}
		}
	}
	otherUser := isolationContext(t.Context(), "alice", "another-owner")
	if _, err := store.Get(otherUser, task.ID); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("owner Get = %v", err)
	}
	if _, err := store.Update(otherUser, &taskstore.UpdateRequest{Task: &updated}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("owner Update = %v", err)
	}
	if got, err := store.List(otherUser, &a2a.ListTasksRequest{}); err != nil || len(got.Tasks) != 0 {
		t.Fatalf("owner List = %+v, %v", got, err)
	}
}

func TestIsolationKeyTaskStorePagination(t *testing.T) {
	store := newIsolationStore()
	alice := isolationContext(t.Context(), "alice", "owner")
	bob := isolationContext(t.Context(), "bob", "owner")
	for _, ctx := range []context.Context{alice, bob} {
		for _, id := range []a2a.TaskID{"one", "two"} {
			if _, err := store.Create(ctx, isolationTask(id, "context")); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, ctx := range map[string]context.Context{"alice": alice, "bob": bob} {
		for _, pageSize := range []int{0, 1, 2, 3} {
			for _, filter := range []string{"", "context", "missing"} {
				t.Run(fmt.Sprintf("%s/size%d/filter=%s", name, pageSize, filter), func(t *testing.T) {
					req := &a2a.ListTasksRequest{PageSize: pageSize, ContextID: filter, IncludeArtifacts: true}
					seen := map[a2a.TaskID]bool{}
					wantTotal, wantTasks := 2, 2
					if filter == "missing" {
						wantTotal, wantTasks = 0, 0
					}
					for pages := 0; ; pages++ {
						if pages >= 2 {
							t.Fatal("extra or non-terminating visible page")
						}
						before := *req
						resp, err := store.List(ctx, req)
						if err != nil {
							t.Fatal(err)
						}
						if *req != before || resp.TotalSize != wantTotal || resp.PageSize != len(resp.Tasks) {
							t.Fatalf("pagination metadata or request changed: %+v, %+v", req, resp)
						}
						wantPage := wantTasks - len(seen)
						if pageSize != 0 {
							wantPage = min(wantPage, pageSize)
						}
						if len(resp.Tasks) != wantPage {
							t.Fatalf("page tasks = %d, want %d", len(resp.Tasks), wantPage)
						}
						for _, task := range resp.Tasks {
							if task.ContextID != "context" || (task.ID != "one" && task.ID != "two") || seen[task.ID] || len(task.Artifacts) != 1 || task.History[0].TaskID != task.ID {
								t.Fatalf("unexpected listed task: %+v", task)
							}
							seen[task.ID] = true
						}
						if resp.NextPageToken == "" {
							break
						}
						req.PageToken = resp.NextPageToken
					}
					if len(seen) != wantTasks {
						t.Fatalf("tasks = %v, want %d", seen, wantTasks)
					}
				})
			}
		}
	}
	if _, err := store.List(alice, &a2a.ListTasksRequest{PageToken: "invalid"}); !errors.Is(err, a2a.ErrParseError) {
		t.Fatalf("invalid cursor = %v", err)
	}
}

func TestIsolationKeyTaskStoreListPartitionTotal(t *testing.T) {
	store := newIsolationStore()
	alice := isolationContext(t.Context(), "alice", "owner")
	bob := isolationContext(t.Context(), "bob", "owner")
	for _, id := range []a2a.TaskID{"one", "two", "three"} {
		task := isolationTask(id, "context")
		if id == "three" {
			task.Status.State = a2a.TaskStateWorking
		}
		if _, err := store.Create(alice, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		filter string
		status a2a.TaskState
		total  int
	}{
		{"unfiltered", alice, "", a2a.TaskStateUnspecified, 3},
		{"context", alice, "context", a2a.TaskStateUnspecified, 3},
		{"status", alice, "", a2a.TaskStateSubmitted, 2},
		{"missing-context", alice, "missing", a2a.TaskStateUnspecified, 0},
		{"empty-partition", isolationContext(t.Context(), "charlie", "owner"), "", a2a.TaskStateUnspecified, 0},
		{"other-owner", isolationContext(t.Context(), "alice", "another-owner"), "", a2a.TaskStateUnspecified, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &a2a.ListTasksRequest{PageSize: 1, ContextID: tc.filter, Status: tc.status}
			first, err := store.List(tc.ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if first.TotalSize != tc.total || first.PageSize != min(1, tc.total) || len(first.Tasks) != min(1, tc.total) {
				t.Fatalf("first page = %+v, want total %d", first, tc.total)
			}
			foreign := isolationTask(a2a.TaskID(tc.name), "context")
			if _, err := store.Create(bob, foreign); err != nil {
				t.Fatal(err)
			}
			unchanged, err := store.List(tc.ctx, req)
			if err != nil || unchanged.TotalSize != tc.total {
				t.Fatalf("after foreign Create = %+v, %v, want total %d", unchanged, err, tc.total)
			}
			seen := len(first.Tasks)
			for token := first.NextPageToken; token != ""; {
				if seen >= tc.total {
					t.Fatal("extra or non-terminating visible page")
				}
				req.PageToken = token
				page, err := store.List(tc.ctx, req)
				if err != nil || page.TotalSize != tc.total || page.PageSize != 1 || len(page.Tasks) != 1 {
					t.Fatalf("continued page = %+v, %v, want total %d", page, err, tc.total)
				}
				seen += len(page.Tasks)
				token = page.NextPageToken
			}
			if seen != tc.total {
				t.Fatalf("listed %d tasks, want %d", seen, tc.total)
			}
		})
	}
}

func TestIsolationKeyTaskStoreEmptyContext(t *testing.T) {
	store := newIsolationStore()
	ctx := isolationContext(t.Context(), "alice", "owner")
	task := &a2a.Task{ID: "task", Status: a2a.TaskStatus{Message: &a2a.Message{
		ID: "message", Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("status")},
	}}}
	if _, err := store.Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, task.ID)
	if err != nil || !reflect.DeepEqual(got.Task, task) {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	listed, err := store.List(ctx, &a2a.ListTasksRequest{})
	if err != nil || len(listed.Tasks) != 1 || !reflect.DeepEqual(listed.Tasks[0], task) {
		t.Fatalf("List = %+v, %v", listed, err)
	}
}

func TestIsolationKeyTaskStoreListNativeCursors(t *testing.T) {
	updatedAt := time.Unix(100, 0)
	inner := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: a2asrv.NewTaskStoreAuthenticator(),
		TimeProvider: func() time.Time {
			updatedAt = updatedAt.Add(time.Second)
			return updatedAt
		},
	})
	aliceStore := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	bobStore := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("bob"))
	ctx := isolationContext(t.Context(), "alice", "owner")
	for i := range 7 {
		task := isolationTask(a2a.TaskID(fmt.Sprintf("task-%d", i)), "context")
		timestamp := time.Unix(int64(i), 0)
		task.Status.Timestamp = &timestamp
		for _, store := range []taskstore.Store{aliceStore, bobStore} {
			if _, err := store.Create(ctx, task); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		pageSize int
		filter   string
	}{
		{0, ""}, {1, ""}, {2, ""}, {3, ""},
		{0, "context"}, {1, "context"}, {2, "context"}, {3, "context"},
	} {
		t.Run(fmt.Sprintf("filter=%s/size%d", tc.filter, tc.pageSize), func(t *testing.T) {
			historyLength := 0
			after := time.Unix(2, 0)
			req := &a2a.ListTasksRequest{
				ContextID: tc.filter, Status: a2a.TaskStateSubmitted, PageSize: tc.pageSize,
				HistoryLength: &historyLength, StatusTimestampAfter: &after,
			}
			var seen int
			for {
				before := *req
				response, err := aliceStore.List(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if *req != before || response.TotalSize != 5 || response.PageSize != len(response.Tasks) || len(response.Tasks) == 0 {
					t.Fatalf("response = %+v, request = %+v", response, req)
				}
				for _, task := range response.Tasks {
					wantID := a2a.TaskID(fmt.Sprintf("task-%d", 6-seen))
					if seen >= 5 || task.ID != wantID || task.ContextID != "context" || len(task.History) != 0 || len(task.Artifacts) != 0 {
						t.Fatalf("task = %+v, want ID %q with native history/artifact filters", task, wantID)
					}
					seen++
				}
				if response.NextPageToken == "" {
					break
				}
				// The wrapper's cursor must be usable by the native store as-is.
				nativeRequest := *req
				nativeRequest.ContextID = "alice::context"
				nativeRequest.PageToken = response.NextPageToken
				nativeRequest.PageSize = 100
				native, err := inner.List(ctx, &nativeRequest)
				if seen >= 5 || err != nil || len(native.Tasks) != 5-seen || native.Tasks[0].ID != a2a.TaskID(fmt.Sprintf("alice::task-%d", 6-seen)) {
					t.Fatalf("native cursor continuation = %+v, %v", native, err)
				}
				req.PageToken = response.NextPageToken
			}
			if seen != 5 {
				t.Fatalf("listed %d tasks, want 5", seen)
			}
		})
	}
}

func TestIsolationKeyTaskStoreCopiesReadResults(t *testing.T) {
	raw := isolationTask("alice::task", "alice::context")
	raw.History[0].ReferenceTasks = []a2a.TaskID{"alice::reference"}
	stored := &taskstore.StoredTask{Task: raw, Version: 7, User: "owner"}
	response := &a2a.ListTasksResponse{Tasks: []*a2a.Task{
		raw, isolationTask("bob::task", "bob::context"), isolationTask("unscoped", "context"),
		isolationTask("bob::other", "alice::context"), isolationTask("alice::other", "bob::context"),
	}, TotalSize: 5, PageSize: 5, NextPageToken: "cursor"}
	inner := isolationStoreStub{
		get: func(context.Context, a2a.TaskID) (*taskstore.StoredTask, error) { return stored, nil },
		list: func(_ context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
			if req.PageToken != "" {
				return &a2a.ListTasksResponse{TotalSize: 5, PageSize: req.PageSize}, nil
			}
			return response, nil
		},
	}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	got, err := store.Get(t.Context(), "task")
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.ID != "task" || got.Task.ContextID != "context" || got.Task.History[0].TaskID != "task" || got.Task.History[0].ReferenceTasks[0] != "reference" || got.Task.Status.Message.ContextID != "context" || got.Version != 7 || got.User != "owner" {
		t.Fatalf("Get = %+v", got)
	}
	listed, err := store.List(t.Context(), &a2a.ListTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != "task" || listed.PageSize != 1 || listed.TotalSize != 1 || listed.NextPageToken != "" {
		t.Fatalf("List = %+v", listed)
	}
	for _, task := range []*a2a.Task{got.Task, listed.Tasks[0]} {
		task.ID = "changed"
		task.ContextID = "changed"
		task.History[0].TaskID = "changed"
		task.History[0].ReferenceTasks[0] = "changed"
		task.Status.Message.ContextID = "changed"
		task.History[0] = nil
	}
	if stored.Task.ID != "alice::task" || raw.ContextID != "alice::context" || raw.History[0].TaskID != "alice::task" || raw.History[0].ReferenceTasks[0] != "alice::reference" || raw.Status.Message.ContextID != "alice::context" || len(response.Tasks) != 5 || response.PageSize != 5 {
		t.Fatal("Get/List mutated inner-owned results")
	}
}

func TestIsolationKeyTaskStoreGetRejectsForeignResults(t *testing.T) {
	for _, task := range []*a2a.Task{
		nil,
		isolationTask("bob::task", "bob::context"),
		isolationTask("bob::task", "alice::context"),
		isolationTask("alice::task", "bob::context"),
		isolationTask("unscoped", "context"),
	} {
		inner := isolationStoreStub{get: func(context.Context, a2a.TaskID) (*taskstore.StoredTask, error) {
			return &taskstore.StoredTask{Task: task, Version: 7, User: "owner"}, nil
		}}
		store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
		if got, err := store.Get(t.Context(), "task"); got != nil || !errors.Is(err, a2a.ErrTaskNotFound) {
			t.Fatalf("Get = %+v, %v for foreign task %+v", got, err, task)
		}
	}
}

func TestIsolationKeyTaskStoreUpdateDelegation(t *testing.T) {
	for _, name := range []string{"task", "message", "status", "artifact", "none"} {
		t.Run(name, func(t *testing.T) {
			task := isolationTask("task", "context")
			var event a2a.Event
			switch name {
			case "task":
				event = task
			case "message":
				event = task.History[0]
			case "status":
				event = a2a.NewStatusUpdateEvent(task, a2a.TaskStateWorking, task.Status.Message)
			case "artifact":
				event = a2a.NewArtifactEvent(task, a2a.NewTextPart("output"))
			}
			req := &taskstore.UpdateRequest{Task: task, PrevTask: task, PrevVersion: 7, Event: event}
			inner := isolationStoreStub{update: func(ctx context.Context, got *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
				if ctx != t.Context() || got.Task.ID != "alice::task" || got.Task.ContextID != "alice::context" || got.PrevTask.ID != got.Task.ID || got.PrevVersion != 7 || got.Task.History[0].TaskID != got.Task.ID || got.Task.History[0].ReferenceTasks[0] != "alice::reference" {
					t.Fatalf("delegated update = %+v", got)
				}
				if got.Event != nil && got.Event.TaskInfo() != got.Task.TaskInfo() {
					t.Fatalf("event task info = %+v", got.Event.TaskInfo())
				}
				got.Task.History[0].ReferenceTasks[0] = "changed"
				got.Task.Status.Message.ContextID = "changed"
				got.PrevTask.ID = "changed"
				got.PrevTask.History[0].TaskID = "changed"
				switch event := got.Event.(type) {
				case *a2a.Task:
					event.ContextID = "changed"
				case *a2a.Message:
					event.TaskID = "changed"
					event.ReferenceTasks[0] = "changed"
				case *a2a.TaskStatusUpdateEvent:
					event.TaskID = "changed"
					event.Status.Message.ContextID = "changed"
				case *a2a.TaskArtifactUpdateEvent:
					event.TaskID = "changed"
				}
				return 8, nil
			}}
			store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
			version, err := store.Update(t.Context(), req)
			if err != nil || version != 8 {
				t.Fatalf("Update = %v, %v", version, err)
			}
			if task.ID != "task" || task.ContextID != "context" || task.History[0].TaskID != "task" || task.History[0].ReferenceTasks[0] != "reference" || task.Status.Message.ContextID != "context" || (event != nil && event.TaskInfo() != task.TaskInfo()) {
				t.Fatal("Update changed caller-owned input")
			}
		})
	}
}

func isolationOperations(task *a2a.Task) map[string]func(context.Context, taskstore.Store) error {
	return map[string]func(context.Context, taskstore.Store) error{
		"create": func(ctx context.Context, s taskstore.Store) error { _, err := s.Create(ctx, task); return err },
		"update": func(ctx context.Context, s taskstore.Store) error {
			_, err := s.Update(ctx, &taskstore.UpdateRequest{Task: task})
			return err
		},
		"get": func(ctx context.Context, s taskstore.Store) error { _, err := s.Get(ctx, task.ID); return err },
		"list": func(ctx context.Context, s taskstore.Store) error {
			_, err := s.List(ctx, &a2a.ListTasksRequest{})
			return err
		},
	}
}

func TestIsolationKeyTaskStoreResolutionFailures(t *testing.T) {
	resolverError := errors.New("resolver failed")
	for name, operation := range isolationOperations(isolationTask("task", "context")) {
		t.Run(name, func(t *testing.T) {
			for _, cfg := range []a2aprovider.IsolationKeyScopedTaskStoreConfig{
				{}, isolationConfig(""),
				{AllowUnscoped: true, ResolveKey: func(context.Context) (string, error) { return "", resolverError }},
			} {
				store := a2aprovider.NewIsolationKeyScopedTaskStore(isolationStoreStub{}, cfg)
				err := operation(t.Context(), store)
				if err == nil || (cfg.AllowUnscoped && !errors.Is(err, resolverError)) {
					t.Fatalf("resolution error = %v", err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			store := a2aprovider.NewIsolationKeyScopedTaskStore(isolationStoreStub{}, isolationConfig("alice"))
			if err := operation(ctx, store); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation = %v", err)
			}
			ctx, cancel = context.WithCancel(t.Context())
			store = a2aprovider.NewIsolationKeyScopedTaskStore(isolationStoreStub{}, a2aprovider.IsolationKeyScopedTaskStoreConfig{
				ResolveKey: func(context.Context) (string, error) { cancel(); return "alice", nil },
			})
			if err := operation(ctx, store); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled resolution = %v", err)
			}
		})
	}
}

func TestIsolationKeyTaskStoreInnerErrors(t *testing.T) {
	failure := errors.New("store failed")
	inner := isolationStoreStub{
		create: func(context.Context, *a2a.Task) (taskstore.TaskVersion, error) { return 0, failure },
		update: func(context.Context, *taskstore.UpdateRequest) (taskstore.TaskVersion, error) { return 0, failure },
		get:    func(context.Context, a2a.TaskID) (*taskstore.StoredTask, error) { return nil, failure },
		list:   func(context.Context, *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) { return nil, failure },
	}
	for name, operation := range isolationOperations(isolationTask("task", "context")) {
		t.Run(name, func(t *testing.T) {
			store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
			if err := operation(t.Context(), store); err != failure {
				t.Fatalf("inner error = %v", err)
			}
		})
	}
}

func TestIsolationKeyTaskStoreListPreservesOptions(t *testing.T) {
	ctx := isolationContext(t.Context(), "alice", "owner")
	historyLength := 2
	after := time.Unix(100, 0)
	req := &a2a.ListTasksRequest{
		Tenant: "request-tenant", ContextID: "context", Status: a2a.TaskStateWorking, PageSize: 2,
		PageToken: "start", HistoryLength: &historyLength, StatusTimestampAfter: &after, IncludeArtifacts: true,
	}
	before := *req
	var calls, resolutions int
	inner := isolationStoreStub{list: func(gotCtx context.Context, got *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
		calls++
		expected := before
		expected.ContextID = "alice::context"
		expected.PageSize = got.PageSize
		expected.PageToken = got.PageToken
		if gotCtx != ctx || !reflect.DeepEqual(*got, expected) {
			t.Fatalf("changed context/options: %+v", got)
		}
		call, ok := a2asrv.CallContextFrom(gotCtx)
		tenant, _ := a2a.TenantFrom(gotCtx)
		if !ok || call.User.Name != "owner" || tenant != "wire-tenant" {
			t.Fatal("changed native owner or tenant")
		}
		var task *a2a.Task
		var next string
		switch got.PageToken {
		case "", "start":
			task, next = isolationTask("bob::one", "bob::context"), "next-1"
		case "next-1":
			task, next = isolationTask("alice::one", "alice::context"), "next-2"
		case "next-2":
			task, next = isolationTask("bob::two", "bob::context"), "next-3"
		case "next-3":
			task = isolationTask("alice::two", "alice::context")
		default:
			t.Fatalf("unexpected cursor %q", got.PageToken)
		}
		return &a2a.ListTasksResponse{Tasks: []*a2a.Task{task}, TotalSize: 4, PageSize: got.PageSize, NextPageToken: next}, nil
	}}
	cfg := a2aprovider.IsolationKeyScopedTaskStoreConfig{ResolveKey: func(gotCtx context.Context) (string, error) {
		resolutions++
		if gotCtx != ctx {
			t.Fatal("resolver context changed")
		}
		return "alice", nil
	}}
	response, err := a2aprovider.NewIsolationKeyScopedTaskStore(inner, cfg).List(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 8 || resolutions != 1 || *req != before || len(response.Tasks) != 2 || response.Tasks[0].ID != "one" || response.Tasks[1].ID != "two" || response.TotalSize != 2 || response.NextPageToken != "" {
		t.Fatalf("calls = %d, resolutions = %d, response = %+v", calls, resolutions, response)
	}
}

func TestIsolationKeyTaskStoreListPageFailures(t *testing.T) {
	for _, name := range []string{"error", "cancel-between-pages", "cancel-and-error"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("next page failed")
			var calls int
			inner := isolationStoreStub{list: func(context.Context, *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
				calls++
				if calls == 1 {
					if name == "cancel-between-pages" {
						cancel()
					}
					return &a2a.ListTasksResponse{Tasks: []*a2a.Task{isolationTask("alice::one", "alice::context")}, PageSize: 2, NextPageToken: "next"}, nil
				}
				if name == "cancel-and-error" {
					cancel()
				}
				return nil, failure
			}}
			response, err := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice")).List(ctx, &a2a.ListTasksRequest{PageSize: 2})
			want, wantCalls := failure, 2
			if name == "cancel-between-pages" {
				want, wantCalls = context.Canceled, 1
			}
			if response != nil || err != want || calls != wantCalls {
				t.Fatalf("response = %+v, error = %v, calls = %d", response, err, calls)
			}
		})
	}
}

func TestIsolationKeyTaskStoreNativeAuthentication(t *testing.T) {
	store := newIsolationStore()
	task := isolationTask("task", "context")
	if _, err := store.Create(isolationContext(t.Context(), "alice", "owner"), task); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), isolationContextKey{}, "alice")
	for name, operation := range isolationOperations(task) {
		t.Run(name, func(t *testing.T) {
			if err := operation(ctx, store); !errors.Is(err, a2a.ErrUnauthenticated) {
				t.Fatalf("unauthenticated operation = %v", err)
			}
		})
	}
}

func TestIsolationKeyTaskStoreListCountFailures(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		for _, name := range []string{"error", "cancel-between-pages", "cancel-and-error", "nil-response"} {
			t.Run(fmt.Sprintf("resumed=%t/%s", resumed, name), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				failure := errors.New("count page failed")
				var calls int
				inner := isolationStoreStub{list: func(gotCtx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
					calls++
					if gotCtx != ctx {
						t.Fatal("native context changed")
					}
					if calls == 1 {
						next := "lookahead"
						if resumed {
							next = ""
						}
						return &a2a.ListTasksResponse{Tasks: []*a2a.Task{isolationTask("alice::one", "alice::context")}, TotalSize: 10, PageSize: 1, NextPageToken: next}, nil
					}
					if calls == 2 {
						wantToken := "lookahead"
						if resumed {
							wantToken = ""
						}
						if req.PageToken != wantToken {
							t.Fatalf("count cursor = %q, want %q", req.PageToken, wantToken)
						}
						if name == "cancel-between-pages" {
							cancel()
						}
						return &a2a.ListTasksResponse{Tasks: []*a2a.Task{isolationTask("alice::two", "alice::context")}, TotalSize: 10, PageSize: 1, NextPageToken: "count-next"}, nil
					}
					if calls != 3 || req.PageToken != "count-next" {
						t.Fatalf("unexpected count request %d: %+v", calls, req)
					}
					if name == "cancel-and-error" {
						cancel()
					}
					if name == "nil-response" {
						return nil, nil
					}
					return nil, failure
				}}
				req := &a2a.ListTasksRequest{PageSize: 1}
				if resumed {
					req.PageToken = "resume"
				}
				before := *req
				response, err := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice")).List(ctx, req)
				want, wantCalls := failure, 3
				if name == "cancel-between-pages" {
					want, wantCalls = context.Canceled, 2
				} else if name == "nil-response" {
					want = nil
				}
				if response != nil || err != want || calls != wantCalls || *req != before {
					t.Fatalf("response = %+v, error = %v, calls = %d, request = %+v", response, err, calls, req)
				}
			})
		}
	}
}

func TestIsolationKeyTaskStoreUnscopedListDelegation(t *testing.T) {
	for _, cfg := range []a2aprovider.IsolationKeyScopedTaskStoreConfig{
		{AllowUnscoped: true},
		{AllowUnscoped: true, ResolveKey: func(context.Context) (string, error) { return "", nil }},
	} {
		req := &a2a.ListTasksRequest{ContextID: "context", PageSize: 4, PageToken: "native-cursor"}
		before := *req
		response := &a2a.ListTasksResponse{
			Tasks:     []*a2a.Task{isolationTask("bob::task", "bob::context")},
			TotalSize: 17, PageSize: 4, NextPageToken: "native-next",
		}
		var calls int
		inner := isolationStoreStub{list: func(ctx context.Context, got *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
			calls++
			if ctx != t.Context() || *got != before {
				t.Fatalf("unscoped request changed: %+v", got)
			}
			return response, nil
		}}
		got, err := a2aprovider.NewIsolationKeyScopedTaskStore(inner, cfg).List(t.Context(), req)
		if err != nil || got != response || *req != before || calls != 1 {
			t.Fatalf("unscoped List = %+v, %v, calls = %d", got, err, calls)
		}
	}
}

func TestIsolationKeyTaskStoreNativeResults(t *testing.T) {
	failure := errors.New("store failed")
	inner := isolationStoreStub{
		create: func(context.Context, *a2a.Task) (taskstore.TaskVersion, error) { return 19, failure },
		update: func(context.Context, *taskstore.UpdateRequest) (taskstore.TaskVersion, error) { return 23, failure },
		get:    func(context.Context, a2a.TaskID) (*taskstore.StoredTask, error) { return nil, nil },
		list:   func(context.Context, *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) { return nil, nil },
	}
	store := a2aprovider.NewIsolationKeyScopedTaskStore(inner, isolationConfig("alice"))
	task := isolationTask("task", "context")
	if version, err := store.Create(t.Context(), task); version != 19 || err != failure {
		t.Fatalf("Create = %v, %v", version, err)
	}
	if version, err := store.Update(t.Context(), &taskstore.UpdateRequest{Task: task}); version != 23 || err != failure {
		t.Fatalf("Update = %v, %v", version, err)
	}
	if result, err := store.Get(t.Context(), task.ID); result != nil || err != nil {
		t.Fatalf("nil Get = %+v, %v", result, err)
	}
	if result, err := store.List(t.Context(), &a2a.ListTasksRequest{}); result != nil || err != nil {
		t.Fatalf("nil List = %+v, %v", result, err)
	}
}

func TestIsolationKeyTaskStoreScopedLookingIdentifiers(t *testing.T) {
	store := newIsolationStore()
	for _, key := range []string{"alice", "bob", "tenant:sub", `tenant\:sub`} {
		ctx := isolationContext(t.Context(), key, "owner")
		task := isolationTask("bob::task", "alice::context")
		task.Metadata["key"] = key
		if _, err := store.Create(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"alice", "bob", "tenant:sub", `tenant\:sub`} {
		ctx := isolationContext(t.Context(), key, "owner")
		got, err := store.Get(ctx, "bob::task")
		if err != nil || got.Task.ID != "bob::task" || got.Task.ContextID != "alice::context" || got.Task.Metadata["key"] != key {
			t.Fatalf("key %q Get = %+v, %v", key, got, err)
		}
		listed, err := store.List(ctx, &a2a.ListTasksRequest{ContextID: "alice::context"})
		if err != nil || len(listed.Tasks) != 1 || listed.Tasks[0].Metadata["key"] != key {
			t.Fatalf("key %q List = %+v, %v", key, listed, err)
		}
	}
}

func TestIsolationKeyTaskStoreConcurrentUpdates(t *testing.T) {
	store := newIsolationStore()
	ctx := isolationContext(t.Context(), "alice", "owner")
	task := isolationTask("task", "context")
	version, err := store.Create(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	var ready, done sync.WaitGroup
	ready.Add(writers)
	done.Add(writers)
	start := make(chan struct{})
	results := make(chan error, writers)
	for range writers {
		go func() {
			defer done.Done()
			updated := *task
			updated.Status.State = a2a.TaskStateWorking
			ready.Done()
			<-start
			_, err := store.Update(ctx, &taskstore.UpdateRequest{Task: &updated, PrevTask: task, PrevVersion: version})
			results <- err
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, taskstore.ErrConcurrentModification) {
			conflicts++
		} else {
			t.Errorf("Update = %v", err)
		}
	}
	got, err := store.Get(ctx, task.ID)
	if successes != 1 || conflicts != writers-1 || err != nil || !got.Version.After(version) || got.Task.Status.State != a2a.TaskStateWorking || task.Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("successes = %d, conflicts = %d, task = %+v, error = %v", successes, conflicts, got, err)
	}
}

func TestIsolationKeyTaskStoreConcurrentContexts(t *testing.T) {
	store := newIsolationStore()
	const partitions = 8
	var ready, done sync.WaitGroup
	ready.Add(partitions)
	done.Add(partitions)
	start := make(chan struct{})
	results := make(chan error, partitions)
	for i := range partitions {
		go func() {
			defer done.Done()
			ctx := isolationContext(t.Context(), fmt.Sprintf(`tenant\:%d`, i), "owner")
			ready.Done()
			<-start
			task := isolationTask("task", "context")
			task.Metadata["partition"] = i
			if _, err := store.Create(ctx, task); err != nil {
				results <- err
				return
			}
			got, err := store.Get(ctx, task.ID)
			if err == nil && !reflect.DeepEqual(got.Task, task) {
				err = fmt.Errorf("partition %d read another task", i)
			}
			results <- err
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}

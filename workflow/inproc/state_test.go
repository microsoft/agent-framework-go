package inproc_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type TestTurnToken struct {
	Count int
}

type StateTestExecutor[T any] struct {
	ID           string
	StateKey     workflow.ScopeKey
	Actions      []func(*T) *T
	Loop         bool
	Completed    bool
	currentIndex int
}

func NewStateTestExecutor[T any](id string, stateKey workflow.ScopeKey, loop bool, actions ...func(*T) *T) *StateTestExecutor[T] {
	return &StateTestExecutor[T]{
		ID:       id,
		StateKey: stateKey,
		Actions:  actions,
		Loop:     loop,
	}
}

func (e *StateTestExecutor[T]) Bind() workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:               e.ID,
		ImplementationID: "func",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: e.ID,

				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[TestTurnToken](), reflect.TypeFor[TestTurnToken](), func(ctx *workflow.Context, msg any) (any, error) {
						return e.Execute(ctx, msg.(TestTurnToken))
					})
					return rb, nil
				},
			}, nil
		},
		SupportsConcurrentSharedExecution: false,
	}
}

func (e *StateTestExecutor[T]) Execute(ctx *workflow.Context, turn TestTurnToken) (TestTurnToken, error) {
	// Read state
	val, err := ctx.ReadState(e.StateKey.Key, e.StateKey.ID.ScopeName)
	if err != nil {
		return TestTurnToken{}, err
	}

	var state *T
	if val != nil {
		if s, ok := val.(T); ok {
			state = &s
		} else if s, ok := val.(*T); ok {
			state = s
		}
	}

	// Get action
	if e.currentIndex >= len(e.Actions) {
		if !e.Loop {
			e.Completed = true
		} else {
			e.currentIndex = 0
		}
	}

	if e.currentIndex < len(e.Actions) {
		action := e.Actions[e.currentIndex]
		e.currentIndex++

		state = action(state)

		// Write state
		var stateVal any
		if state != nil {
			stateVal = *state
		}
		if err := ctx.QueueStateUpdate(e.StateKey.Key, e.StateKey.ID.ScopeName, stateVal); err != nil {
			return TestTurnToken{}, err
		}
	}

	if e.currentIndex >= len(e.Actions) && !e.Loop {
		e.Completed = true
	}

	return TestTurnToken{Count: turn.Count + 1}, nil
}

func createOrIncrement(defaultValue int) func(*int) *int {
	return func(currState *int) *int {
		if currState != nil {
			v := *currState + 1
			return &v
		}
		v := defaultValue
		return &v
	}
}

func validateState(t *testing.T, expectedValue int) func(*int) *int {
	return func(currState *int) *int {
		if currState == nil {
			t.Errorf("expected state %d, got nil", expectedValue)
		} else if *currState != expectedValue {
			t.Errorf("expected state %d, got %d", expectedValue, *currState)
		}
		return currState
	}
}

func maxTurns(limit int) func(any) bool {
	return func(maybeTurn any) bool {
		if turn, ok := maybeTurn.(TestTurnToken); ok {
			return turn.Count < limit
		}
		return false
	}
}

func TestInProcessRun_StateShouldPersist_NotCheckpointed(t *testing.T) {
	writer := NewStateTestExecutor(
		"Writer",
		workflow.ScopeKey{ID: workflow.ScopeID{ScopeName: "TestScope", ExecutorID: "Writer"}, Key: "TestKey"},
		false,
		createOrIncrement(0),
		createOrIncrement(0),
	)

	validator := NewStateTestExecutor(
		"Validator",
		workflow.ScopeKey{ID: workflow.ScopeID{ScopeName: "TestScope", ExecutorID: "Validator"}, Key: "TestKey"},
		false,
		validateState(t, 0),
		validateState(t, 1),
	)

	wf, err := workflow.NewBuilder(writer.Bind()).
		AddDirectEdge(writer.Bind(), validator.Bind(), false, maxTurns(4)).
		AddDirectEdge(validator.Bind(), writer.Bind(), false, maxTurns(4)).
		Build()
	if err != nil {
		t.Fatalf("Failed to build workflow: %v", err)
	}

	run, err := inproc.Default.Run(t.Context(), wf, TestTurnToken{})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}
	status, err := run.GetStatus(t.Context())
	if err != nil {
		t.Fatalf("Failed to get run status: %v", err)
	}
	if status != inproc.RunStatusIdle {
		t.Errorf("Expected run status to be Idle, got %v", status)
	}
	if !writer.Completed {
		t.Error("Writer should be completed")
	}
	if !validator.Completed {
		t.Error("Validator should be completed")
	}
}

func TestInProcessRun_StateShouldPersist_Checkpointed(t *testing.T) {
	writer := NewStateTestExecutor(
		"Writer",
		workflow.ScopeKey{ID: workflow.ScopeID{ScopeName: "TestScope", ExecutorID: "Writer"}, Key: "TestKey"},
		false,
		createOrIncrement(0),
		createOrIncrement(0),
	)

	validator := NewStateTestExecutor(
		"Validator",
		workflow.ScopeKey{ID: workflow.ScopeID{ScopeName: "TestScope", ExecutorID: "Validator"}, Key: "TestKey"},
		false,
		validateState(t, 0),
		validateState(t, 1),
	)

	wf, err := workflow.NewBuilder(writer.Bind()).
		AddDirectEdge(writer.Bind(), validator.Bind(), false, maxTurns(4)).
		AddDirectEdge(validator.Bind(), writer.Bind(), false, maxTurns(4)).
		Build()
	if err != nil {
		t.Fatalf("Failed to build workflow: %v", err)
	}

	run, err := inproc.Default.WithCheckpointing(checkpoint.NewInMemoryManager()).Run(t.Context(), wf, TestTurnToken{})
	if err != nil {
		t.Fatalf("Failed to create checkpointed runner: %v", err)
	}
	if len(run.Checkpoints()) != 4 {
		t.Errorf("Expected 4 checkpoints, got %d", len(run.Checkpoints()))
	}
	status, err := run.GetStatus(t.Context())
	if err != nil {
		t.Fatalf("Failed to get run status: %v", err)
	}
	if status != inproc.RunStatusIdle {
		t.Errorf("Expected run status to be Idle, got %v", status)
	}

	if !writer.Completed {
		t.Error("Writer should be completed")
	}
	if !validator.Completed {
		t.Error("Validator should be completed")
	}
}

func TestInProcessRun_StateShouldPersist_JSONCheckpointed(t *testing.T) {
	const stateKey = "value"
	binding := workflow.ExecutorBinding{
		ID:               "stateful",
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: binding.ID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					switch msg.(string) {
					case "write":
						return nil, ctx.QueueStateUpdate(stateKey, "", "persisted")
					case "read":
						value, err := ctx.ReadState(stateKey, "")
						if err != nil {
							return nil, err
						}
						return nil, ctx.YieldOutput(value)
					default:
						return nil, nil
					}
				})
				return rb, nil
			},
		}, nil
	}
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	_, manager := newFileSystemJSONCheckpointManager(t)
	env := inproc.Default.WithCheckpointing(manager)
	ctx := context.Background()
	writeRun, err := env.Run(ctx, wf, "write")
	if err != nil {
		t.Fatalf("Run write: %v", err)
	}
	checkpointInfo, ok := writeRun.LastCheckpoint()
	if !ok {
		t.Fatal("expected a checkpoint after state update")
	}
	if err := writeRun.Close(ctx); err != nil {
		t.Fatalf("Close write run: %v", err)
	}

	readRun, err := env.ResumeStreaming(ctx, wf, checkpointInfo)
	if err != nil {
		t.Fatalf("ResumeStreaming: %v", err)
	}
	defer func() {
		if err := readRun.Close(ctx); err != nil {
			t.Errorf("Close read run: %v", err)
		}
	}()
	if err := readRun.SendMessage(ctx, "read"); err != nil {
		t.Fatalf("SendMessage read: %v", err)
	}
	var got any
	for event, err := range readRun.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("WatchUntilHalt: %v", err)
		}
		if output, ok := event.(workflow.OutputEvent); ok {
			got = output.Output
		}
	}
	if got != "persisted" {
		t.Fatalf("output = %v, want persisted", got)
	}
}

func TestInProcessRun_ReadStateKeysLifecycle(t *testing.T) {
	tests := []struct {
		name           string
		scope          string
		wantAfterWrite []string
	}{
		{name: "shared scope", scope: "shared-scope", wantAfterWrite: []string{"key1"}},
		{name: "private scope", scope: "", wantAfterWrite: nil},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			writer, reader, observed := stateKeysLifecycleBindings(testCase.scope)
			wf, err := workflow.NewBuilder(writer).
				AddEdge(writer, reader).
				WithOutputFrom(writer, reader).
				Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			ctx := context.Background()
			stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
			if err != nil {
				t.Fatalf("RunStreaming: %v", err)
			}
			defer func() { _ = stream.CancelRun() }()

			if err := stream.SendMessage(ctx, "write"); err != nil {
				t.Fatalf("SendMessage write: %v", err)
			}
			for _, err := range stream.WatchUntilHalt(ctx) {
				if err != nil {
					t.Fatalf("WatchUntilHalt write: %v", err)
				}
			}

			if got := observed["writer:write"]; !slices.Equal(got, []string{"key1"}) {
				t.Fatalf("writer keys after write = %v, want [key1]", got)
			}
			if got := observed["reader:after-write"]; !slices.Equal(got, testCase.wantAfterWrite) {
				t.Fatalf("reader keys after write = %v, want %v", got, testCase.wantAfterWrite)
			}

			if err := stream.SendMessage(ctx, "delete"); err != nil {
				t.Fatalf("SendMessage delete: %v", err)
			}
			for _, err := range stream.WatchUntilHalt(ctx) {
				if err != nil {
					t.Fatalf("WatchUntilHalt delete: %v", err)
				}
			}

			if got := observed["writer:delete"]; len(got) != 0 {
				t.Fatalf("writer keys after delete = %v, want empty", got)
			}
			if got := observed["reader:after-delete"]; len(got) != 0 {
				t.Fatalf("reader keys after delete = %v, want empty", got)
			}
		})
	}
}

func stateKeysLifecycleBindings(scope string) (workflow.ExecutorBinding, workflow.ExecutorBinding, map[string][]string) {
	const key = "key1"
	observed := make(map[string][]string)
	reader := workflow.ExecutorBinding{
		ID:               "reader",
		ImplementationID: "*workflow.Executor",
	}

	writer := workflow.ExecutorBinding{
		ID:               "writer",
		ImplementationID: "*workflow.Executor",
	}
	writer.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: writer.ID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, message any) (any, error) {
					switch message.(string) {
					case "write":
						if err := ctx.QueueStateUpdate(key, scope, "value1"); err != nil {
							return nil, err
						}
						keys, err := readWorkflowStateKeys(ctx, scope)
						if err != nil {
							return nil, err
						}
						observed["writer:write"] = keys
						return nil, ctx.SendMessage(reader.ID, "after-write")
					case "delete":
						if err := ctx.QueueStateUpdate(key, scope, nil); err != nil {
							return nil, err
						}
						keys, err := readWorkflowStateKeys(ctx, scope)
						if err != nil {
							return nil, err
						}
						observed["writer:delete"] = keys
						return nil, ctx.SendMessage(reader.ID, "after-delete")
					default:
						return nil, nil
					}
				})
				return rb, nil
			},
		}, nil
	}

	reader.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: reader.ID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, message any) (any, error) {
					keys, err := readWorkflowStateKeys(ctx, scope)
					if err != nil {
						return nil, err
					}
					observed["reader:"+message.(string)] = keys
					return nil, nil
				})
				return rb, nil
			},
		}, nil
	}

	return writer, reader, observed
}

func readWorkflowStateKeys(ctx *workflow.Context, scope string) ([]string, error) {
	var keys []string
	for key, err := range ctx.ReadStateKeys(scope) {
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, nil
}

func TestInProcessRun_QueueClearScopeRemovesVisibleKeys(t *testing.T) {
	const scope = "clear-scope"
	observed := make(map[string][]string)

	binding := workflow.ExecutorBinding{
		ID:               "stateful",
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "stateful",

				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, message any) (any, error) {
						switch message.(string) {
						case "seed":
							if err := ctx.QueueStateUpdate("first", scope, "value"); err != nil {
								return nil, err
							}
							if err := ctx.QueueStateUpdate("second", scope, "value"); err != nil {
								return nil, err
							}
							return nil, ctx.SendMessage("", "clear")
						case "clear":
							if err := ctx.QueueClearScope(scope); err != nil {
								return nil, err
							}
							keys, err := readWorkflowStateKeys(ctx, scope)
							if err != nil {
								return nil, err
							}
							observed["after-clear"] = keys
						}
						return nil, nil
					})
					return rb, nil
				},
			}, nil
		},
	}

	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	run, err := inproc.Default.Run(t.Context(), wf, "seed")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range run.NewEvents() {
	}

	if got := observed["after-clear"]; len(got) != 0 {
		t.Fatalf("keys after clear = %v, want empty", got)
	}
}

func TestInProcessRun_StateShouldError_TwoExecutors(t *testing.T) {
	forward := workflow.NewExecutor("ForwardMessageExecutor", func(t TestTurnToken) TestTurnToken {
		return t
	}).Bind()

	testExecutor := NewStateTestExecutor(
		"StateTestExecutor",
		workflow.ScopeKey{ID: workflow.ScopeID{ScopeName: "TestScope", ExecutorID: "StateTestExecutor"}, Key: "TestKey"},
		false,
		createOrIncrement(0),
	)

	testExecutor2 := NewStateTestExecutor(
		"StateTestExecutor2",
		workflow.ScopeKey{ID: workflow.ScopeID{ScopeName: "TestScope", ExecutorID: "StateTestExecutor2"}, Key: "TestKey"},
		false,
		createOrIncrement(0),
	)

	wf, err := workflow.NewBuilder(forward).
		AddFanOutEdge(forward, []workflow.ExecutorBinding{testExecutor.Bind(), testExecutor2.Bind()}).
		Build()
	if err != nil {
		t.Fatalf("Failed to build workflow: %v", err)
	}

	runWithFailure, err := inproc.Default.Run(t.Context(), wf, TestTurnToken{})
	if err != nil {
		t.Fatalf("Failed to create runner: %v", err)
	}

	var hadFailure bool
	for evt := range runWithFailure.NewEvents() {
		if evt, ok := evt.(workflow.ErrorEvent); ok {
			if hadFailure {
				t.Fatalf("Multiple error events received")
			}
			hadFailure = true
			if !strings.Contains(evt.Error.Error(), "TestKey") {
				t.Errorf("Expected error containing 'TestKey', got: %v", evt.Error)
			}
		}
	}
	if !hadFailure {
		t.Errorf("Expected error event, but got none")
	}
}

func TestInProcessRun_ReadOrInitStateInitializerError(t *testing.T) {
	const want = "initializer failed"
	binding := workflow.ExecutorBinding{
		ID:               "stateful",
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "stateful",

				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
						_, err := ctx.ReadOrInitState("key", "", func(context.Context, string, string) (any, error) {
							return nil, errors.New(want)
						})
						return nil, err
					})
					return rb, nil
				},
			}, nil
		},
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(t.Context(), wf, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawErr bool
	for evt := range run.OutgoingEvents() {
		if evt, ok := evt.(workflow.ErrorEvent); ok && strings.Contains(evt.Error.Error(), want) {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected initializer error event")
	}
}

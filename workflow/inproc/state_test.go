package inproc_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
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

func (e *StateTestExecutor[T]) Bind() *workflow.ExecutorBinding {
	return &workflow.ExecutorBinding{
		ID:           e.ID,
		ExecutorType: reflect.TypeOf(e.Execute),
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: e.ID,
				Config: []*workflow.ExecutorConfig{
					{
						ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
							return rb.AddHandler(reflect.TypeFor[TestTurnToken](), reflect.TypeFor[TestTurnToken](), false, func(ctx *workflow.Context, msg any) (any, error) {
								return e.Execute(ctx, msg.(TestTurnToken))
							}), nil
						},
					},
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

	run, err := inproc.Default.WithCheckpointing(inproc.NewInMemoryCheckpointManager()).Run(t.Context(), wf, TestTurnToken{})
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

func TestInProcessRun_StateShouldError_TwoExecutors(t *testing.T) {
	forward := workflow.BindFunc("ForwardMessageExecutor", false, func(t TestTurnToken) TestTurnToken {
		return t
	})

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
		AddFanOutEdge(forward, []*workflow.ExecutorBinding{testExecutor.Bind(), testExecutor2.Bind()}).
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
	binding := &workflow.ExecutorBinding{
		ID:           "stateful",
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "stateful",
				Config: []*workflow.ExecutorConfig{{
					ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
						return rb.AddHandler(reflect.TypeFor[string](), nil, false, func(ctx *workflow.Context, _ any) (any, error) {
							_, err := ctx.ReadOrInitState("key", "", func(context.Context, string, string) (any, error) {
								return nil, errors.New(want)
							})
							return nil, err
						}), nil
					},
				}},
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

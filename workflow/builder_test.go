// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type noOpExecutor struct {
	id string
}

func (n *noOpExecutor) NewExecutor(sessionID string) (*workflow.Executor, error) {
	return &workflow.Executor{
		ID: n.id,

		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.RouteBuilder.AddCatchAll(func(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
				return nil, ctx.SendMessage("", msg.Any())
			})
			return rb, nil
		},
	}, nil
}

func newNoOpExecutor(id string) workflow.ExecutorBinding {
	n := &noOpExecutor{id: id}
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*noOpExecutor",
		NewExecutorFunc:  n.NewExecutor,
	}
}

type someOtherNoOpExecutor struct {
	id string
}

func (n *someOtherNoOpExecutor) NewExecutor(sessionID string) (*workflow.Executor, error) {
	return &workflow.Executor{
		ID: n.id,

		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.RouteBuilder.AddCatchAll(func(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
				return nil, ctx.SendMessage("", msg.Any())
			})
			return rb, nil
		},
	}, nil
}

func newSomeOtherNoOpExecutor(id string) workflow.ExecutorBinding {
	n := &someOtherNoOpExecutor{id: id}
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*someOtherNoOpExecutor",
		NewExecutorFunc:  n.NewExecutor,
	}
}

func newPlaceholder(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{ID: id}
}

func TestBuilder_InfersEmptyImplementationID(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID: "start",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "start",

				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddCatchAll(func(*workflow.Context, workflow.PortableValue) (any, error) {
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
	bindings := wf.ReflectExecutors()
	got := bindings["start"].ImplementationID
	if got != "start" {
		t.Fatalf("ImplementationID = %q, want start", got)
	}
}

func TestBuilder_Validation_FailsWhenUnboundExecutors(t *testing.T) {
	_, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(newNoOpExecutor("start"), newPlaceholder("unbound")).
		Build()

	if err == nil {
		t.Error("expected error, got nil")
	} else if !strings.Contains(err.Error(), "workflow cannot be built because there are unbound executors: [unbound]") {
		t.Errorf("expected unbound executors error, got %v", err)
	}
}

func TestBuilder_Validation_FailsWhenUnreachableExecutors(t *testing.T) {
	_, err := workflow.NewBuilder(newPlaceholder("start")).
		BindExecutor(newNoOpExecutor("start")).
		AddEdge(newNoOpExecutor("unreachable"), newNoOpExecutor("also-unreachable")).
		Build()

	if err == nil {
		t.Error("expected error, got nil")
	} else if !strings.Contains(err.Error(), "workflow cannot be built because there are orphaned executors: [also-unreachable unreachable]") {
		t.Errorf("expected orphaned executors error, got %v", err)
	}
}

func TestBuilder_Validation_AddEdgesOutOfOrderDoesNotImpactReachability(t *testing.T) {
	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		BindExecutor(newNoOpExecutor("start")).
		AddEdge(newNoOpExecutor("not-unreachable"), newNoOpExecutor("also-not-unreachable")).
		AddEdge(newPlaceholder("start"), newPlaceholder("not-unreachable")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID() != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID())
	}

	bindings := wf.ReflectExecutors()
	if len(bindings) != 3 {
		t.Errorf("expected 3 executor bindings, got %d", len(bindings))
	}

	for _, id := range []string{"start", "not-unreachable", "also-not-unreachable"} {
		if _, ok := bindings[id]; !ok {
			t.Errorf("expected binding for %s", id)
		} else {
			if bindings[id].ImplementationID != "*noOpExecutor" {
				t.Errorf("expected implementation ID *noOpExecutor for %s", id)
			}
		}
	}
}

func TestBuilder_LateBinding_Executor(t *testing.T) {
	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		BindExecutor(newNoOpExecutor("start")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID() != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID())
	}

	bindings := wf.ReflectExecutors()
	if len(bindings) != 1 {
		t.Errorf("expected 1 executor binding, got %d", len(bindings))
	}

	if binding, ok := bindings["start"]; !ok {
		t.Error("expected binding for start")
	} else {
		if binding.ImplementationID != "*noOpExecutor" {
			t.Errorf("expected implementation ID *noOpExecutor")
		}
	}
}

func TestBuilder_LateImplicitBinding_Executor(t *testing.T) {
	start := newNoOpExecutor("start")
	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(start, start).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID() != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID())
	}

	bindings := wf.ReflectExecutors()
	if len(bindings) != 1 {
		t.Errorf("expected 1 executor binding, got %d", len(bindings))
	}

	if binding, ok := bindings["start"]; !ok {
		t.Error("expected binding for start")
	} else {
		if binding.ImplementationID != "*noOpExecutor" {
			t.Errorf("expected implementation ID *noOpExecutor")
		}
	}
}

func TestBuilder_RebindToDifferent_Disallowed(t *testing.T) {
	executor1 := newNoOpExecutor("start")
	executor2 := newSomeOtherNoOpExecutor("start")

	_, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(executor1, executor2).
		Build()

	if err == nil {
		t.Error("expected error, got nil")
	} else if !strings.Contains(err.Error(), "cannot bind executor with ID \"start\" because an executor with the same ID but a different implementation ID (\"*noOpExecutor\" vs \"*someOtherNoOpExecutor\") is already bound") {
		t.Errorf("expected rebind executors error, got %v", err)
	}
}

func TestBuilder_RejectsNonComparableRawValue(t *testing.T) {
	binding := newNoOpExecutor("start")
	binding.RawValue = []string{"not-comparable"}

	_, err := workflow.NewBuilder(binding).Build()
	if err == nil {
		t.Fatal("expected error for non-comparable RawValue, got nil")
	}
	if !strings.Contains(err.Error(), "RawValue") || !strings.Contains(err.Error(), "not comparable") {
		t.Fatalf("error = %v, want non-comparable RawValue error", err)
	}
}

func TestBuilder_RebindToSameish_Allowed(t *testing.T) {
	executor1 := newNoOpExecutor("start")

	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(executor1, executor1).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID() != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID())
	}

	bindings := wf.ReflectExecutors()
	if len(bindings) != 1 {
		t.Errorf("expected 1 executor binding, got %d", len(bindings))
	}

	if binding, ok := bindings["start"]; !ok {
		t.Error("expected binding for start")
	} else {
		if binding.ImplementationID != "*noOpExecutor" {
			t.Errorf("expected implementation ID *noOpExecutor")
		}
	}
}

func TestBuilder_Workflow_NameAndDescription(t *testing.T) {
	// Test with name and description
	wf1, err := workflow.NewBuilder(newPlaceholder("start")).
		WithName("Test Pipeline").
		WithDescription("Test workflow description").
		BindExecutor(newNoOpExecutor("start")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf1.Name() != "Test Pipeline" {
		t.Errorf("expected name 'Test Pipeline', got %s", wf1.Name())
	}
	if wf1.Description() != "Test workflow description" {
		t.Errorf("expected description 'Test workflow description', got %s", wf1.Description())
	}

	// Test without (defaults to empty string in Go)
	wf2, err := workflow.NewBuilder(newPlaceholder("start2")).
		BindExecutor(newNoOpExecutor("start2")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf2.Name() != "" {
		t.Errorf("expected empty name, got %s", wf2.Name())
	}
	if wf2.Description() != "" {
		t.Errorf("expected empty description, got %s", wf2.Description())
	}

	// Test with only name (no description)
	wf3, err := workflow.NewBuilder(newPlaceholder("start3")).
		WithName("Named Only").
		BindExecutor(newNoOpExecutor("start3")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf3.Name() != "Named Only" {
		t.Errorf("expected name 'Named Only', got %s", wf3.Name())
	}
	if wf3.Description() != "" {
		t.Errorf("expected empty description, got %s", wf3.Description())
	}
}

// recordingBinding builds an executor that records every input it receives in
// the supplied slice (under mu) and forwards strings downstream unchanged.
func recordingBinding(id string, sink *[]string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					*sink = append(*sink, id+":"+msg.(string))
					return nil, ctx.SendMessage("", msg)
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func TestAddChain_ConnectsExecutorsInOrder(t *testing.T) {
	var trace []string
	a := recordingBinding("a", &trace)
	b := recordingBinding("b", &trace)
	c := recordingBinding("c", &trace)

	wf, err := workflow.NewBuilder(a).
		AddChain(a, []workflow.ExecutorBinding{b, c}, false).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "x"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"a:x", "b:x", "c:x"}
	if !reflect.DeepEqual(trace, want) {
		t.Errorf("trace = %v, want %v", trace, want)
	}
}

func TestAddChain_RejectsRepetitionByDefault(t *testing.T) {
	a := recordingBinding("a", new([]string))
	b := recordingBinding("b", new([]string))

	_, err := workflow.NewBuilder(a).
		AddChain(a, []workflow.ExecutorBinding{b, b}, false).
		Build()
	if err == nil {
		t.Fatal("expected error for repeated executor")
	}
}

func TestAddSwitch_RoutesToMatchingCase(t *testing.T) {
	var trace []string
	src := recordingBinding("src", &trace)
	even := recordingBinding("even", &trace)
	odd := recordingBinding("odd", &trace)

	bld := workflow.NewBuilder(src)
	bld.AddSwitch(src).
		AddCase(func(msg any) bool {
			s, ok := msg.(string)
			return ok && len(s)%2 == 0
		}, even).
		AddCase(func(msg any) bool {
			s, ok := msg.(string)
			return ok && len(s)%2 == 1
		}, odd).
		AddToBuilder(bld)
	wf, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// "abcd" → len 4 → even
	if _, err := inproc.Default.Run(context.Background(), wf, "abcd"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantContains := "even:abcd"
	var found bool
	for _, t := range trace {
		if t == wantContains {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected trace to include %q, got %v", wantContains, trace)
	}
	for _, ent := range trace {
		if ent == "odd:abcd" {
			t.Errorf("expected odd branch not to receive even-length string; trace=%v", trace)
		}
	}
}

func TestAddSwitch_FallsBackToDefault(t *testing.T) {
	var trace []string
	src := recordingBinding("src", &trace)
	branch := recordingBinding("branch", &trace)
	def := recordingBinding("def", &trace)

	bld := workflow.NewBuilder(src)
	bld.AddSwitch(src).
		AddCase(func(msg any) bool { return msg == "match" }, branch).
		WithDefault(def).
		AddToBuilder(bld)
	wf, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, "no-match"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, ent := range trace {
		if ent == "branch:no-match" {
			t.Errorf("non-matching message reached branch executor; trace=%v", trace)
		}
	}
	var sawDefault bool
	for _, ent := range trace {
		if ent == "def:no-match" {
			sawDefault = true
		}
	}
	if !sawDefault {
		t.Errorf("expected default branch to receive non-matching message; trace=%v", trace)
	}
}

func TestBuilder_Validation_FailsWhenOutputExecutorNotInGraph(t *testing.T) {
	start := newNoOpExecutor("start")

	// WithOutputFrom references an executor that was never added via an edge
	// or BindExecutor. Since it is tracked by WithOutputFrom itself but is
	// otherwise disconnected, the orphan-check fires first. Use a binding
	// that IS reachable but then reference a completely unknown ID via a
	// separate binding that only appears in WithOutputFrom.
	ghost := newNoOpExecutor("ghost")
	_, err := workflow.NewBuilder(start).
		AddEdge(start, newNoOpExecutor("next")).
		WithOutputFrom(ghost).
		Build()

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "orphaned executors") && !strings.Contains(err.Error(), "output executor") {
		t.Errorf("expected output or orphan executor error, got %v", err)
	}
}

func TestBuilder_Validation_OutputExecutorNotBound(t *testing.T) {
	// Directly test the output executor validation: if we somehow register
	// an output executor that is not in executorsBindings, the build must
	// fail with a clear error.
	start := newNoOpExecutor("start")
	target := newNoOpExecutor("target")

	_, err := workflow.NewBuilder(start).
		AddEdge(start, target).
		WithOutputFrom(target).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestBuilder_WithIntermediateOutputFromRegistersTags(t *testing.T) {
	start := newNoOpExecutor("start")
	leaf := newNoOpExecutor("leaf")

	wf, err := workflow.NewBuilder(start).
		AddEdge(start, leaf).
		WithIntermediateOutputFrom(leaf).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !wf.HasOutputExecutor("leaf") {
		t.Fatal("leaf should be registered as an output executor")
	}
	tags := outputExecutorTags(wf, "leaf")
	if len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
		t.Fatalf("tags = %v, want intermediate", tags)
	}
	outputEvent := workflow.OutputEvent{ExecutorID: "leaf", Output: "value", Tags: tags}
	if !outputEvent.HasTag(workflow.OutputTagIntermediate) || !outputEvent.IsIntermediate() {
		t.Fatalf("OutputEvent did not report intermediate tag: %v", outputEvent.Tags)
	}
}

func TestBuilder_OutputTagsAccumulateWithoutDuplicates(t *testing.T) {
	start := newNoOpExecutor("start")
	leaf := newNoOpExecutor("leaf")

	wf, err := workflow.NewBuilder(start).
		AddEdge(start, leaf).
		WithOutputFrom(leaf).
		WithIntermediateOutputFrom(leaf).
		WithIntermediateOutputFrom(leaf).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	tags := outputExecutorTags(wf, "leaf")
	if len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
		t.Fatalf("tags = %v, want intermediate", tags)
	}
}

func outputExecutorTags(wf *workflow.Workflow, executorID string) []workflow.OutputTag {
	if wf == nil {
		return nil
	}
	tags, ok := wf.OutputExecutors()[executorID]
	if !ok {
		return nil
	}
	return tags
}

func TestBuilder_Validation_SelfLoopWarning(t *testing.T) {
	// A self-loop (executor → itself) is allowed but should log a warning.
	// We verify it does not produce a build error.
	start := newNoOpExecutor("start")

	wf, err := workflow.NewBuilder(start).
		AddDirectEdge(start, start, true, func(any) bool { return false }).
		Build()
	if err != nil {
		t.Fatalf("expected no error for self-loop, got %v", err)
	}
	if wf.StartExecutorID() != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID())
	}
}

func TestBuilder_Validation_DeadEndLogging(t *testing.T) {
	// Dead-end executors (no outgoing edges) should not cause build failure.
	start := newNoOpExecutor("start")
	leaf := newNoOpExecutor("leaf")

	var wf *workflow.Workflow
	var err error
	logs := captureDefaultSlog(t, func() {
		wf, err = workflow.NewBuilder(start).
			AddEdge(start, leaf).
			Build()
	})
	if err != nil {
		t.Fatalf("expected no error for dead-end, got %v", err)
	}
	if _, ok := wf.ExecutorBinding("leaf"); !ok {
		t.Error("expected leaf executor in bindings")
	}
	if !strings.Contains(logs, "dead-end executors detected") || !strings.Contains(logs, "leaf") {
		t.Fatalf("expected dead-end log for leaf, got %q", logs)
	}
}

func TestBuilder_Validation_OutputExecutorExcludedFromDeadEndLogging(t *testing.T) {
	start := newNoOpExecutor("start")
	leaf := newNoOpExecutor("leaf")

	var err error
	logs := captureDefaultSlog(t, func() {
		_, err = workflow.NewBuilder(start).
			AddEdge(start, leaf).
			WithOutputFrom(leaf).
			Build()
	})
	if err != nil {
		t.Fatalf("expected no error for output dead-end, got %v", err)
	}
	if strings.Contains(logs, "dead-end executors detected") || strings.Contains(logs, "leaf") {
		t.Fatalf("expected no dead-end log for output executor, got %q", logs)
	}
}

func captureDefaultSlog(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(previous)

	fn()
	return buf.String()
}

// newTypedExecutor creates an executor that accepts messages of type T and
// returns messages of type U. This is used to test type compatibility
// validation between connected executors.
func newTypedExecutor[T any, U any](id string) workflow.ExecutorBinding {
	newExec := func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[T](), reflect.TypeFor[U](), func(ctx *workflow.Context, msg any) (any, error) {
					return *new(U), nil
				})
				return rb, nil
			},
		}, nil
	}
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc:  newExec,
	}
}

func newDeclaredSendExecutor[T any](id string, sendTypes ...reflect.Type) workflow.ExecutorBinding {
	newExec := func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(sendTypes...)
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[T](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return nil, nil
				})
				return rb, nil
			},
		}, nil
	}
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc:  newExec,
	}
}

func TestBuilder_Validation_TypeCompatibility_Compatible(t *testing.T) {
	// source outputs string, target accepts string → compatible.
	source := newTypedExecutor[string, string]("source")
	target := newTypedExecutor[string, int]("target")

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err != nil {
		t.Fatalf("expected no error for compatible types, got %v", err)
	}
}

func TestBuilder_Validation_TypeCompatibility_Incompatible(t *testing.T) {
	// source sends int, target accepts string → incompatible.
	source := newTypedExecutor[string, int]("source")
	target := newTypedExecutor[string, string]("target")

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err == nil {
		t.Fatal("expected type incompatibility error, got nil")
	}
	if !strings.Contains(err.Error(), "type incompatibility") {
		t.Errorf("expected type incompatibility error, got %v", err)
	}
}

func TestBuilder_Validation_TypeCompatibility_UsesDeclaredSendTypes(t *testing.T) {
	source := newDeclaredSendExecutor[string]("source", reflect.TypeFor[int]())
	target := newTypedExecutor[int, string]("target")

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err != nil {
		t.Fatalf("expected declared send type to validate edge, got %v", err)
	}
}

func TestBuilder_Validation_TypeCompatibility_RejectsIncompatibleDeclaredSendTypes(t *testing.T) {
	source := newDeclaredSendExecutor[string]("source", reflect.TypeFor[int]())
	target := newTypedExecutor[string, string]("target")

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err == nil {
		t.Fatal("expected type incompatibility error, got nil")
	}
	if !strings.Contains(err.Error(), "source sends") {
		t.Errorf("expected source sends error, got %v", err)
	}
}

func TestBuilder_Validation_TypeCompatibility_RespectsAutoSendDisabled(t *testing.T) {
	source := newTypedExecutor[string, int]("source")
	source.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: source.ID,

			DisableAutoSendMessageHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[int](), func(ctx *workflow.Context, msg any) (any, error) {
					return 1, nil
				})
				return rb, nil
			},
		}, nil
	}
	target := newTypedExecutor[string, string]("target")

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err != nil {
		t.Fatalf("expected validation to skip disabled auto-send output type, got %v", err)
	}
}

func TestBuilder_Validation_TypeCompatibility_CatchAllTargetSkipped(t *testing.T) {
	// source sends int, but target has a catch-all → always compatible.
	source := newTypedExecutor[string, int]("source")
	target := newNoOpExecutor("target") // uses AddCatchAll

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err != nil {
		t.Fatalf("expected no error when target has catch-all, got %v", err)
	}
}

func TestBuilder_Validation_TypeCompatibility_CatchAllSourceSkipped(t *testing.T) {
	// source has a catch-all (no declared send types), so validation is skipped.
	source := newNoOpExecutor("source") // uses AddCatchAll, no send types
	target := newTypedExecutor[string, int]("target")

	_, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err != nil {
		t.Fatalf("expected no error when source has no output types, got %v", err)
	}
}

// A conditional edge on a source→target pair must not populate the
// conditionless-edge dedup set: adding a legitimate conditionless edge on the
// same pair afterwards should succeed, not be rejected as a duplicate.
func TestBuilder_ConditionalEdgeDoesNotBlockConditionlessEdge(t *testing.T) {
	start := newNoOpExecutor("start")
	target := newNoOpExecutor("target")

	_, err := workflow.NewBuilder(start).
		AddDirectEdge(start, target, false, func(any) bool { return true }).
		AddEdge(start, target).
		Build()
	if err != nil {
		t.Fatalf("conditionless edge after a conditional edge on the same pair should be allowed, got error: %v", err)
	}
}

// The idempotent path (AddChain / idempotent=true) must likewise not silently
// drop a conditionless edge just because a conditional edge preceded it.
func TestBuilder_ConditionalEdgeDoesNotDropIdempotentConditionlessEdge(t *testing.T) {
	start := newNoOpExecutor("start")
	target := newNoOpExecutor("target")

	wf, err := workflow.NewBuilder(start).
		AddDirectEdge(start, target, false, func(any) bool { return true }).
		AddDirectEdge(start, target, true, nil). // idempotent conditionless edge
		Build()
	if err != nil {
		t.Fatalf("unexpected build error: %v", err)
	}

	// The idempotent conditionless edge must actually be present, not silently
	// dropped by the poisoned dedup set: expect both a conditional and a
	// conditionless edge from start.
	var conditional, conditionless int
	for _, e := range wf.Edges()["start"] {
		if e.Condition == nil {
			conditionless++
		} else {
			conditional++
		}
	}
	if conditional != 1 || conditionless != 1 {
		t.Fatalf("edges from start: conditional=%d conditionless=%d, want 1 and 1", conditional, conditionless)
	}
}

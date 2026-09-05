// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestRunnerEnqueueMessageUntypedExternalResponseDeclaredAsAnyUsesMessagePath(t *testing.T) {
	start := workflow.NewExecutor("start", func(any) {}).Bind()
	wf, err := workflow.NewBuilder(start).Build()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := createTopLevelRunner(wf, nil, "session", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.RequestEndRun(t.Context()) })

	accepted, err := runner.EnqueueMessageUntyped(
		t.Context(),
		&workflow.ExternalResponse{},
		reflect.TypeFor[any](),
	)
	if err != nil || !accepted {
		t.Fatalf("EnqueueMessageUntyped() = (%v, %v), want (true, nil)", accepted, err)
	}
}

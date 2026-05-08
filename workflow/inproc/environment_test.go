// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func TestExecutionEnvironmentWithCheckpointingNilDisablesCheckpointing(t *testing.T) {
	env := inproc.Default.WithCheckpointing(nil)
	if env.IsCheckpointingEnabled() {
		t.Fatal("expected checkpointing to be disabled")
	}
}

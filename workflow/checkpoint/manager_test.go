// Copyright (c) Microsoft. All rights reserved.

package checkpoint_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
)

func TestNewJSONManagerNilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()

	_ = checkpoint.NewJSONManager(nil)
}

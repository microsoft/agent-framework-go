// Copyright (c) Microsoft. All rights reserved.

package execution_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

func TestUpdateKey_Equality(t *testing.T) {
	const key1 = "key1"
	const key2 = "key2"

	privateScope1Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1"}, Key: key1}
	privateScope1Key2 := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1"}, Key: key2}
	if privateScope1Key.Equal(privateScope1Key2) {
		t.Fatal("private-scope keys with different state keys should not be equal")
	}

	privateScope2Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2"}, Key: key1}
	if privateScope1Key.Equal(privateScope2Key) {
		t.Fatal("private-scope keys with different executors should not be equal")
	}

	sharedScope1Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1", ScopeName: "shared"}, Key: key1}
	sharedScope2Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2", ScopeName: "shared"}, Key: key1}
	if sharedScope1Key.Equal(sharedScope2Key) {
		t.Fatal("shared-scope update keys from different executors should not be equal")
	}
}

func TestUpdateKey_IsMatchingScope(t *testing.T) {
	const key1 = "key1"

	privateScope1Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1"}, Key: key1}
	privateScope2Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2"}, Key: key1}
	privateScope1 := workflow.ScopeID{ExecutorID: "executor1"}
	privateScope2 := workflow.ScopeID{ExecutorID: "executor2"}

	requireScopeMatch(t, privateScope1Key, privateScope1, true, true)
	requireScopeMatch(t, privateScope1Key, privateScope2, false, false)
	requireScopeMatch(t, privateScope2Key, privateScope1, false, false)
	requireScopeMatch(t, privateScope2Key, privateScope2, true, true)

	sharedScope1Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1", ScopeName: "shared"}, Key: key1}
	sharedScope2Key := execution.UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2", ScopeName: "shared"}, Key: key1}
	sharedScope1 := workflow.ScopeID{ExecutorID: "executor1", ScopeName: "shared"}
	sharedScope2 := workflow.ScopeID{ExecutorID: "executor2", ScopeName: "shared"}

	requireScopeMatch(t, sharedScope1Key, sharedScope1, true, true)
	requireScopeMatch(t, sharedScope1Key, sharedScope2, false, true)
	requireScopeMatch(t, sharedScope2Key, sharedScope1, false, true)
	requireScopeMatch(t, sharedScope2Key, sharedScope2, true, true)

	requireScopeMatch(t, privateScope1Key, sharedScope1, false, false)
	requireScopeMatch(t, privateScope1Key, sharedScope2, false, false)
	requireScopeMatch(t, privateScope2Key, sharedScope1, false, false)
	requireScopeMatch(t, privateScope2Key, sharedScope2, false, false)

	requireScopeMatch(t, sharedScope1Key, privateScope1, false, false)
	requireScopeMatch(t, sharedScope1Key, privateScope2, false, false)
	requireScopeMatch(t, sharedScope2Key, privateScope1, false, false)
	requireScopeMatch(t, sharedScope2Key, privateScope2, false, false)
}

func requireScopeMatch(t *testing.T, key execution.UpdateKey, scope workflow.ScopeID, wantStrict bool, wantLoose bool) {
	t.Helper()
	if got := key.IsMatchingScope(scope, true); got != wantStrict {
		t.Fatalf("strict match for %+v against %+v = %v, want %v", key, scope, got, wantStrict)
	}
	if got := key.IsMatchingScope(scope, false); got != wantLoose {
		t.Fatalf("loose match for %+v against %+v = %v, want %v", key, scope, got, wantLoose)
	}
}

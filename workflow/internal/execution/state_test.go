// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"testing"

	"github.com/microsoft/agent-framework-go/internal/hashmap"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateStateUpdateNilValueIsDelete(t *testing.T) {
	update := UpdateStateUpdate("key", nil)
	if !update.IsDelete {
		t.Fatal("nil state update should be marked as delete")
	}
}

// TestStateManager_ClearByIDDropsUnpublishedWriteOnFreshScope verifies that
// clearing a scope also removes writes still queued (unpublished) when the
// scope has never been materialized in sm.scopes. Writing then clearing the
// same scope within a superstep must not leave the write readable, nor commit
// it on publish.
func TestStateManager_ClearByIDDropsUnpublishedWriteOnFreshScope(t *testing.T) {
	manager := NewStateManager()
	scope := workflow.ScopeID{ExecutorID: "executor1", ScopeName: ""}

	mustSucceed(t, manager.WriteStateByID(scope, "key1", "value1"))
	mustSucceed(t, manager.ClearStateByID(scope))

	if _, ok, err := manager.ReadStateByID(scope, "key1"); err != nil {
		t.Fatalf("ReadStateByID: %v", err)
	} else if ok {
		t.Fatal("cleared key should be absent, but the queued write survived the clear")
	}

	mustSucceed(t, manager.PublishUpdates(nil))

	if _, ok, err := manager.ReadStateByID(scope, "key1"); err != nil {
		t.Fatalf("ReadStateByID after publish: %v", err)
	} else if ok {
		t.Fatal("cleared key must not be committed on publish")
	}
}

func TestScopeSharedScope_ReadKeys(t *testing.T) {
	scopeName := "sharedScope"
	runScopeKeysTest(t, scopeName, true)
}

func TestScopePrivateScope_ReadKeys(t *testing.T) {
	runScopeKeysTest(t, "", false)
}

func runScopeKeysTest(t *testing.T, scopeName string, isSharedScope bool) {
	const (
		SelfExecutorId  = "executor1"
		OtherExecutorId = "executor2"
		Key1            = "key1"
	)

	manager := NewStateManager()
	sharedScopeSelfView := workflow.ScopeID{ExecutorID: SelfExecutorId, ScopeName: scopeName}
	sharedScopeOtherView := workflow.ScopeID{ExecutorID: OtherExecutorId, ScopeName: scopeName}

	// Assert baseline: neither executor sees any keys
	selfKeys := manager.ReadKeysByID(sharedScopeSelfView)
	if len(selfKeys) != 0 {
		t.Errorf("there should be no keys in an empty StateManager")
	}

	otherKeys := manager.ReadKeysByID(sharedScopeOtherView)
	if len(otherKeys) != 0 {
		t.Errorf("there should be no keys in an empty StateManager")
	}

	// Act 1: Write a key from the self executor's view of the shared scope
	mustSucceed(t, manager.WriteStateByID(sharedScopeSelfView, Key1, "value1"))

	// Assert 1: The self executor should see the key immediately, but the other executor should not
	selfKeys = manager.ReadKeysByID(sharedScopeSelfView)
	if _, ok := selfKeys[Key1]; !ok || len(selfKeys) != 1 {
		t.Errorf("writes should be visible immediately to the writing executor")
	}

	otherKeys = manager.ReadKeysByID(sharedScopeOtherView)
	if len(otherKeys) != 0 {
		if isSharedScope {
			t.Errorf("writes should not be visible to other executors until published")
		} else {
			t.Errorf("writes to private scopes should not be visible across executors")
		}
	}

	// Act 2: Publish the updates
	mustSucceed(t, manager.PublishUpdates(nil))

	// Assert 2: Both executors should see the key now, if sharedScope
	selfKeys = manager.ReadKeysByID(sharedScopeSelfView)
	if _, ok := selfKeys[Key1]; !ok || len(selfKeys) != 1 {
		t.Errorf("published writes should be visible to all executors")
	}

	otherKeys = manager.ReadKeysByID(sharedScopeOtherView)
	if isSharedScope {
		if _, ok := otherKeys[Key1]; !ok || len(otherKeys) != 1 {
			t.Errorf("published writes should be visible to all executors")
		}
	} else {
		if len(otherKeys) != 0 {
			t.Errorf("writes to private scopes should not be visible across executors")
		}
	}

	// Act 3: Clear the state from the self executor's view of the shared scope
	mustSucceed(t, manager.ClearStateKeyByID(sharedScopeSelfView, Key1))

	// Assert 3: The self executor should not see the key immediately, but the other executor should still see it if sharedScope
	selfKeys = manager.ReadKeysByID(sharedScopeSelfView)
	if len(selfKeys) != 0 {
		t.Errorf("deletes should be visible immediately to the writing executor")
	}

	otherKeys = manager.ReadKeysByID(sharedScopeOtherView)
	if isSharedScope {
		if _, ok := otherKeys[Key1]; !ok || len(otherKeys) != 1 {
			t.Errorf("published writes should be visible to all executors")
		}
	} else {
		if len(otherKeys) != 0 {
			t.Errorf("writes to private scopes should not be visible across executors")
		}
	}

	// Act 4: Publish the updates
	mustSucceed(t, manager.PublishUpdates(nil))

	// Assert 4: Neither executor should see the key now
	selfKeys = manager.ReadKeysByID(sharedScopeSelfView)
	if len(selfKeys) != 0 {
		t.Errorf("published deletes should be visible to all executors")
	}

	otherKeys = manager.ReadKeysByID(sharedScopeOtherView)
	if len(otherKeys) != 0 {
		if isSharedScope {
			t.Errorf("published deletes should be visible to all executors")
		} else {
			t.Errorf("writes to private scopes should not be visible across executors")
		}
	}
}

func TestScopeSharedScope_ValueLifecycle(t *testing.T) {
	scopeName := "sharedScope"
	runValueLifecycleTest(t, scopeName, true)
}

func TestScopePrivateScope_ValueLifecycle(t *testing.T) {
	runValueLifecycleTest(t, "", false)
}

func runValueLifecycleTest(t *testing.T, scopeName string, isSharedScope bool) {
	const (
		SelfExecutorId  = "executor1"
		OtherExecutorId = "executor2"
		Key1            = "key1"
		Key2            = "key2"
		Value1          = "value1"
		Value2          = "value2"
	)

	manager := NewStateManager()
	scopeSelfView := workflow.ScopeID{ExecutorID: SelfExecutorId, ScopeName: scopeName}
	scopeOtherView := workflow.ScopeID{ExecutorID: OtherExecutorId, ScopeName: scopeName}

	if isSharedScope != scopeSelfView.Equal(scopeOtherView) {
		t.Errorf("isSharedScope mismatch")
	}

	// Assert baseline: neither executor sees any keys or values
	checkValue := func(scope workflow.ScopeID, key string, expected any, msg string) {
		t.Helper()
		val, ok, _ := manager.ReadStateByID(scope, key)
		if expected == nil {
			if ok {
				t.Errorf("%s: expected nil, got %v", msg, val)
			}
		} else {
			if !ok {
				t.Errorf("%s: expected %v, got nil", msg, expected)
			} else if val.Any() != expected {
				t.Errorf("%s: expected %v, got %v", msg, expected, val.Any())
			}
		}
	}

	checkValue(scopeSelfView, Key1, nil, "there should be no values in an empty StateManager")
	checkValue(scopeSelfView, Key2, nil, "there should be no values in an empty StateManager")
	checkValue(scopeOtherView, Key1, nil, "there should be no values in an empty StateManager")
	checkValue(scopeOtherView, Key2, nil, "there should be no values in an empty StateManager")

	// Act 1: Write a value from the self executor's view of the shared scope
	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, Value1))

	// Assert 1
	checkValue(scopeSelfView, Key1, Value1, "writes should be visible immediately to the writing executor")
	checkValue(scopeSelfView, Key2, nil, "uninvolved keys' state/value should not change after a write")
	checkValue(scopeOtherView, Key1, nil, "writes should not be visible to other executors until published")
	checkValue(scopeOtherView, Key2, nil, "uninvolved keys' state/value should not change after a write")

	// Act 2: Write a value from the other executor's view of the shared scope
	mustSucceed(t, manager.WriteStateByID(scopeOtherView, Key2, Value2))

	// Assert 2
	checkValue(scopeSelfView, Key1, Value1, "uninvolved keys' state/value should not change after a write")
	checkValue(scopeSelfView, Key2, nil, "writes should not be visible to other executors until published")
	checkValue(scopeOtherView, Key1, nil, "writes should not be visible to other executors until published")
	checkValue(scopeOtherView, Key2, Value2, "writes should be visible immediately to the writing executor")

	// Act 3: Publish the updates
	mustSucceed(t, manager.PublishUpdates(nil))

	// Assert 3
	checkValue(scopeSelfView, Key1, Value1, "published writes should be visible to all executors")
	if isSharedScope {
		checkValue(scopeSelfView, Key2, Value2, "published writes should be visible to all executors")
		checkValue(scopeOtherView, Key1, Value1, "published writes should be visible to all executors")
	} else {
		checkValue(scopeSelfView, Key2, nil, "writes to private scopes should not be visible across executors")
		checkValue(scopeOtherView, Key1, nil, "writes to private scopes should not be visible across executors")
	}
	checkValue(scopeOtherView, Key2, Value2, "published writes should be visible to all executors")

	// Act 4: Clear the value from the self executor's view of the shared scope
	mustSucceed(t, manager.ClearStateByID(scopeSelfView))

	// Assert 4
	checkValue(scopeSelfView, Key1, nil, "clears should be visible immediately to the writing executor")
	checkValue(scopeSelfView, Key2, nil, "clears should be visible immediately to the writing executor")

	if isSharedScope {
		checkValue(scopeOtherView, Key1, Value1, "clears should not be visible to other executors until published")
		checkValue(scopeOtherView, Key2, Value2, "clears should not be visible to other executors until published")
	} else {
		checkValue(scopeOtherView, Key1, nil, "writes to private scopes should not be visible across executors")
		checkValue(scopeOtherView, Key2, Value2, "writes to private scopes should not be visible across executors")
	}

	// Act 5: Publish the updates
	mustSucceed(t, manager.PublishUpdates(nil))

	// Assert 5
	checkValue(scopeSelfView, Key1, nil, "published clears should be visible to all executors")
	checkValue(scopeSelfView, Key2, nil, "published clears should be visible to all executors")
	checkValue(scopeOtherView, Key1, nil, "published clears should be visible to all executors")
	if isSharedScope {
		checkValue(scopeOtherView, Key2, nil, "published clears should be visible to all executors")
	} else {
		checkValue(scopeOtherView, Key2, Value2, "writes to private scopes should not be visible across executors")
	}

	// Restore the written state of both keys
	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, Value1))
	mustSucceed(t, manager.WriteStateByID(scopeOtherView, Key2, Value2))
	mustSucceed(t, manager.PublishUpdates(nil))

	// Act 6: Delete Key1 from the other executor's view of the shared scope
	mustSucceed(t, manager.ClearStateKeyByID(scopeOtherView, Key1))

	// Assert 6
	if isSharedScope {
		checkValue(scopeSelfView, Key1, Value1, "deletes should not be visible to other executors until published")
	} else {
		checkValue(scopeSelfView, Key1, Value1, "writes to private scopes should not be visible across executors")
	}
	if isSharedScope {
		checkValue(scopeSelfView, Key2, Value2, "uninvolved keys' state/value should not change after a delete")
	} else {
		checkValue(scopeSelfView, Key2, nil, "writes to private scopes should not be visible across executors")
	}
	checkValue(scopeOtherView, Key1, nil, "deletes should be visible immediately to the writing executor")
	checkValue(scopeOtherView, Key2, Value2, "uninvolved keys' state/value should not change after a delete")

	// Act 7: Delete Key2 from the self executor's view of the shared scope
	mustSucceed(t, manager.ClearStateKeyByID(scopeSelfView, Key2))

	// Assert 7
	if isSharedScope {
		checkValue(scopeSelfView, Key1, Value1, "deletes should not be visible to other executors until published")
	} else {
		checkValue(scopeSelfView, Key1, Value1, "writes to private scopes should not be visible across executors")
	}
	checkValue(scopeSelfView, Key2, nil, "deletes should be visible immediately to the writing executor")
	checkValue(scopeOtherView, Key1, nil, "deletes should be visible immediately to the writing executor")
	if isSharedScope {
		checkValue(scopeOtherView, Key2, Value2, "deletes should not be visible to other executors until published")
	} else {
		checkValue(scopeOtherView, Key2, Value2, "writes to private scopes should not be visible across executors")
	}

	// Act 8: Publish the updates
	mustSucceed(t, manager.PublishUpdates(nil))

	// Assert 8
	if isSharedScope {
		checkValue(scopeSelfView, Key1, nil, "published deletes should be visible to all executors")
	} else {
		checkValue(scopeSelfView, Key1, Value1, "writes to private scopes should not be visible across executors")
	}
	checkValue(scopeSelfView, Key2, nil, "published deletes should be visible to all executors")
	checkValue(scopeOtherView, Key1, nil, "published deletes should be visible to all executors")
	if isSharedScope {
		checkValue(scopeOtherView, Key2, nil, "published deletes should be visible to all executors")
	} else {
		checkValue(scopeOtherView, Key2, Value2, "writes to private scopes should not be visible across executors")
	}
}

func TestScopeSharedScope_ConflictingUpdates(t *testing.T) {
	scopeName := "sharedScope"
	runConflictingUpdatesTest_WriteVsWrite(t, scopeName, true)
	runConflictingUpdatesTest_WriteVsDelete(t, scopeName, true)
	runConflictingUpdatesTest_WriteVsClear(t, scopeName, true)
}

func TestScopePrivateScope_ConflictingUpdates(t *testing.T) {
	runConflictingUpdatesTest_WriteVsWrite(t, "", false)
	runConflictingUpdatesTest_WriteVsDelete(t, "", false)
	runConflictingUpdatesTest_WriteVsClear(t, "", false)
}

func runConflictingUpdatesTest_WriteVsWrite(t *testing.T, scopeName string, isSharedScope bool) {
	const (
		SelfExecutorId  = "executor1"
		OtherExecutorId = "executor2"
		Key1            = "key1"
		Value1          = "value"
		Value2          = "value"
	)

	manager := NewStateManager()
	scopeSelfView := workflow.ScopeID{ExecutorID: SelfExecutorId, ScopeName: scopeName}
	scopeOtherView := workflow.ScopeID{ExecutorID: OtherExecutorId, ScopeName: scopeName}

	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, Value1))
	mustSucceed(t, manager.WriteStateByID(scopeOtherView, Key1, Value2))

	err := manager.PublishUpdates(nil)
	if isSharedScope {
		if err == nil {
			t.Errorf("conflicting writes to the same key should raise an exception when published")
		}
	} else {
		if err != nil {
			t.Errorf("writes to private scopes should not be visible across executors: %v", err)
		}
	}
}

func runConflictingUpdatesTest_WriteVsDelete(t *testing.T, scopeName string, isSharedScope bool) {
	const (
		SelfExecutorId  = "executor1"
		OtherExecutorId = "executor2"
		Key1            = "key1"
		Key2            = "key2"
		Value1          = "value"
		Value2          = "value"
	)

	manager := NewStateManager()
	scopeSelfView := workflow.ScopeID{ExecutorID: SelfExecutorId, ScopeName: scopeName}
	scopeOtherView := workflow.ScopeID{ExecutorID: OtherExecutorId, ScopeName: scopeName}

	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, Value1))
	mustSucceed(t, manager.WriteStateByID(scopeOtherView, Key2, Value2))
	mustSucceed(t, manager.PublishUpdates(nil))

	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, "newValue"))
	mustSucceed(t, manager.ClearStateKeyByID(scopeOtherView, Key1))

	err := manager.PublishUpdates(nil)
	if isSharedScope {
		if err == nil {
			t.Errorf("conflicting writes (update vs delete) should raise an exception when published")
		}
	} else {
		if err != nil {
			t.Errorf("writes to private scopes should not be visible across executors: %v", err)
		}
	}
}

func runConflictingUpdatesTest_WriteVsClear(t *testing.T, scopeName string, isSharedScope bool) {
	const (
		SelfExecutorId  = "executor1"
		OtherExecutorId = "executor2"
		Key1            = "key1"
		Key2            = "key2"
		Value1          = "value"
		Value2          = "value"
	)

	manager := NewStateManager()
	scopeSelfView := workflow.ScopeID{ExecutorID: SelfExecutorId, ScopeName: scopeName}
	scopeOtherView := workflow.ScopeID{ExecutorID: OtherExecutorId, ScopeName: scopeName}

	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, Value1))
	mustSucceed(t, manager.WriteStateByID(scopeOtherView, Key2, Value2))
	mustSucceed(t, manager.PublishUpdates(nil))

	mustSucceed(t, manager.WriteStateByID(scopeSelfView, Key1, "newValue"))
	mustSucceed(t, manager.ClearStateByID(scopeOtherView))

	err := manager.PublishUpdates(nil)
	if isSharedScope {
		if err == nil {
			t.Errorf("conflicting writes (update vs clear) should raise an exception when published")
		}
	} else {
		if err != nil {
			t.Errorf("writes to private scopes should not be visible across executors: %v", err)
		}
	}
}

func TestScopeLoadPortableValueState(t *testing.T) {
	t.Run("Publish", func(t *testing.T) {
		testLoadPortableValueState(t, true)
	})
	t.Run("NoPublish", func(t *testing.T) {
		testLoadPortableValueState(t, false)
	})
}

func testLoadPortableValueState(t *testing.T, publishStateUpdates bool) {
	scope := workflow.ScopeID{ExecutorID: "executor1"}
	const (
		StringValue = "string"
		IntValue    = 42
	)
	ScopeKey := workflow.ScopeKey{ID: workflow.ScopeID{ExecutorID: "executor1", ScopeName: "scope"}, Key: "key"}
	PortableValueValue := workflow.AnyPortableValue(StringValue)

	manager := NewStateManager()
	mustSucceed(t, manager.WriteStateByID(scope, "StringValue", StringValue))
	mustSucceed(t, manager.WriteStateByID(scope, "IntValue", IntValue))
	mustSucceed(t, manager.WriteStateByID(scope, "ScopeKey", ScopeKey))
	mustSucceed(t, manager.WriteStateByID(scope, "PortableValueValue", PortableValueValue))

	if publishStateUpdates {
		mustSucceed(t, manager.PublishUpdates(nil))
	}

	// Act & Assert - Read as the original types
	checkType := func(key string, expected any) {
		t.Helper()
		pv, ok, _ := manager.ReadStateByID(scope, key)
		if !ok {
			t.Errorf("key %s not found", key)
			return
		}

		actual := pv.Any()
		if expectedSK, ok := expected.(workflow.ScopeKey); ok {
			if actualSK, ok := actual.(workflow.ScopeKey); ok {
				if !expectedSK.Equal(actualSK) {
					t.Errorf("key %s: expected %v, got %v", key, expected, actual)
				}
				return
			}
		}

		if actual != expected {
			t.Errorf("key %s: expected %v, got %v", key, expected, actual)
		}
	}

	checkType("StringValue", StringValue)
	checkType("IntValue", IntValue)
	checkType("ScopeKey", ScopeKey)
	checkType("PortableValueValue", StringValue)

	// Verify types
	pv, _, _ := manager.ReadStateByID(scope, "StringValue")
	if _, ok := workflow.PortableValueAs[string](pv); !ok {
		t.Errorf("expected string")
	}
	if _, ok := workflow.PortableValueAs[int](pv); ok {
		t.Errorf("expected not int")
	}
}

func TestScopeLoadPortableValueState_AfterSerialization(t *testing.T) {
	scope := workflow.ScopeID{ExecutorID: "executor1"}
	const (
		StringValue = "string"
		IntValue    = 42
	)
	ScopeKey := workflow.ScopeKey{ID: workflow.ScopeID{ExecutorID: "executor1", ScopeName: "scope"}, Key: "key"}
	PortableValueValue := workflow.AnyPortableValue(StringValue)

	manager := NewStateManager()
	mustSucceed(t, manager.WriteStateByID(scope, "StringValue", StringValue))
	mustSucceed(t, manager.WriteStateByID(scope, "IntValue", IntValue))
	mustSucceed(t, manager.WriteStateByID(scope, "ScopeKey", ScopeKey))
	mustSucceed(t, manager.WriteStateByID(scope, "PortableValueValue", PortableValueValue))

	mustSucceed(t, manager.PublishUpdates(nil))

	exportedState, err := manager.ExportState()
	if err != nil {
		t.Fatalf("ExportState failed: %v", err)
	}

	stateData := hashmap.NewMap[workflow.ScopeKey, workflow.PortableValue](ScopeKeyHasher)
	for k, v := range exportedState {
		stateData.Set(k, v)
	}

	testCheckpoint := &checkpoint.Checkpoint{
		StateData: *stateData,
	}

	manager = NewStateManager()
	mustSucceed(t, manager.ImportState(testCheckpoint))

	// Act & Assert - Read as the original types
	checkType := func(key string, expected any) {
		t.Helper()
		pv, ok, _ := manager.ReadStateByID(scope, key)
		if !ok {
			t.Errorf("key %s not found", key)
			return
		}

		actual := pv.Any()
		if expectedSK, ok := expected.(workflow.ScopeKey); ok {
			if actualSK, ok := actual.(workflow.ScopeKey); ok {
				if !expectedSK.Equal(actualSK) {
					t.Errorf("key %s: expected %v, got %v", key, expected, actual)
				}
				return
			}
		}

		if actual != expected {
			t.Errorf("key %s: expected %v, got %v", key, expected, actual)
		}
	}

	checkType("StringValue", StringValue)
	checkType("IntValue", IntValue)
	checkType("ScopeKey", ScopeKey)
	checkType("PortableValueValue", StringValue)
}

func TestScopeID_Equality(t *testing.T) {
	// The rules of ScopeId are simple: Private executor scopes (executorId, scopeId=null) are only equal to
	// themselves. Public ScopeIds are equal when their scopeNames are equal, regardless of executorId.

	privateScope1 := workflow.ScopeID{ExecutorID: "executor1"}
	privateScope2 := workflow.ScopeID{ExecutorID: "executor2"}

	if privateScope1.Equal(privateScope2) {
		t.Errorf("privateScope1 should not equal privateScope2")
	}
	if !privateScope1.Equal(workflow.ScopeID{ExecutorID: "executor1"}) {
		t.Errorf("privateScope1 should equal itself")
	}

	sharedScope1 := workflow.ScopeID{ExecutorID: "executor1", ScopeName: "sharedScope"}
	sharedScope2 := workflow.ScopeID{ExecutorID: "executor2", ScopeName: "sharedScope"}

	if !sharedScope1.Equal(sharedScope2) {
		t.Errorf("sharedScope1 should equal sharedScope2")
	}
	if sharedScope1.Equal(workflow.ScopeID{ExecutorID: "executor1", ScopeName: "differentScope"}) {
		t.Errorf("sharedScope1 should not equal differentScope")
	}
	if sharedScope1.Equal(privateScope1) {
		t.Errorf("sharedScope1 should not equal privateScope1")
	}
}

func TestUpdateKey_Equality(t *testing.T) {
	// The rules of UpdateKey are different from ScopeId. In the case of "shared scope",
	// two update keys with different ExecutorIds are not the same.

	const Key1 = "key1"
	const Key2 = "key2"
	privateScope1Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1"}, Key: Key1}
	privateScope1Key2 := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1"}, Key: Key2}

	if privateScope1Key.Equal(privateScope1Key2) {
		t.Errorf("privateScope1Key should not equal privateScope1Key2")
	}

	privateScope2Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2"}, Key: Key1}

	if privateScope1Key.Equal(privateScope2Key) {
		t.Errorf("privateScope1Key should not equal privateScope2Key")
	}

	scope1Executor1Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1", ScopeName: "sharedScope"}, Key: Key1}
	scope1Executor2Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2", ScopeName: "sharedScope"}, Key: Key1}

	if scope1Executor1Key.Equal(scope1Executor2Key) {
		t.Errorf("scope1Executor1Key should not equal scope1Executor2Key")
	}
}

func TestUpdateKey_IsMatchingScope(t *testing.T) {
	const Key1 = "key1"

	privateScope1Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1"}, Key: Key1}
	privateScope2Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2"}, Key: Key1}

	privateScope1 := workflow.ScopeID{ExecutorID: "executor1"}
	privateScope2 := workflow.ScopeID{ExecutorID: "executor2"}

	validateMatch := func(key UpdateKey, scope workflow.ScopeID, expectedStrict, expectedLoose bool) {
		t.Helper()
		if key.IsMatchingScope(scope, true) != expectedStrict {
			t.Errorf("key %v matching scope %v strict: expected %v, got %v", key, scope, expectedStrict, !expectedStrict)
		}
		if key.IsMatchingScope(scope, false) != expectedLoose {
			t.Errorf("key %v matching scope %v loose: expected %v, got %v", key, scope, expectedLoose, !expectedLoose)
		}
	}

	validateMatch(privateScope1Key, privateScope1, true, true)
	validateMatch(privateScope1Key, privateScope2, false, false)
	validateMatch(privateScope2Key, privateScope1, false, false)
	validateMatch(privateScope2Key, privateScope2, true, true)

	sharedScope1Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor1", ScopeName: "sharedScope"}, Key: Key1}
	sharedScope2Key := UpdateKey{ScopeID: workflow.ScopeID{ExecutorID: "executor2", ScopeName: "sharedScope"}, Key: Key1}

	sharedScope1 := workflow.ScopeID{ExecutorID: "executor1", ScopeName: "sharedScope"}
	sharedScope2 := workflow.ScopeID{ExecutorID: "executor2", ScopeName: "sharedScope"}

	validateMatch(sharedScope1Key, sharedScope1, true, true)
	validateMatch(sharedScope1Key, sharedScope2, false, true)
	validateMatch(sharedScope2Key, sharedScope1, false, true)
	validateMatch(sharedScope2Key, sharedScope2, true, true)

	// Cross checks between private and shared scopes should never match
	validateMatch(privateScope1Key, sharedScope1, false, false)
	validateMatch(privateScope1Key, sharedScope2, false, false)
	validateMatch(privateScope2Key, sharedScope1, false, false)
	validateMatch(privateScope2Key, sharedScope2, false, false)

	validateMatch(sharedScope1Key, privateScope1, false, false)
	validateMatch(sharedScope1Key, privateScope2, false, false)
	validateMatch(sharedScope2Key, privateScope1, false, false)
	validateMatch(sharedScope2Key, privateScope2, false, false)
}

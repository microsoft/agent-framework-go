// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
)

type person struct {
	Name string
}

type nullablePerson struct {
	Name string
}

type sessionMarshalCallback struct {
	session *agent.Session
}

func (v sessionMarshalCallback) MarshalJSON() ([]byte, error) {
	v.session.Set("from-marshaler", "saved")
	return []byte(`"callback"`), nil
}

type sessionUnmarshalCallback struct {
	session *agent.Session
	value   string
}

func (v *sessionUnmarshalCallback) UnmarshalJSON(data []byte) error {
	v.session.Set("from-unmarshaler", "saved")
	return json.Unmarshal(data, &v.value)
}

func TestSessionState_ConcurrentReadsAndWrites(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key", "initial")
	const operations = 200
	start := make(chan struct{})
	errs := make(chan error, operations)
	var wg sync.WaitGroup
	wg.Add(operations)
	for i := range operations {
		go func(index int) {
			defer wg.Done()
			<-start
			if index%2 == 0 {
				var got string
				if ok, err := session.Get("key", &got); err != nil || !ok || got == "" {
					errs <- fmt.Errorf("Get(key) = (%q, %v, %v)", got, ok, err)
				}
				return
			}
			session.Set("key", fmt.Sprintf("value%d", index))
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var got string
	if ok, err := session.Get("key", &got); err != nil || !ok || got == "" {
		t.Fatalf("final Get(key) = (%q, %v, %v)", got, ok, err)
	}
}

func TestSessionState_ConcurrentWritesAndSerialize(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("shared", "initial")
	const operations = 100
	start := make(chan struct{})
	errs := make(chan error, operations)
	var wg sync.WaitGroup
	wg.Add(operations)
	for i := range operations {
		go func(index int) {
			defer wg.Done()
			<-start
			session.Set("shared", fmt.Sprintf("value%d", index))
			data, err := json.Marshal(session)
			if err != nil {
				errs <- err
				return
			}
			var payload struct{ State map[string]json.RawMessage }
			if err := json.Unmarshal(data, &payload); err != nil {
				errs <- err
			} else if len(payload.State["shared"]) == 0 {
				errs <- fmt.Errorf("serialized state missing shared key: %s", data)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var got string
	if ok, err := session.Get("shared", &got); err != nil || !ok || got == "" {
		t.Fatalf("final Get(shared) = (%q, %v, %v)", got, ok, err)
	}
}

func TestSession_ConcurrentStateAndJSONReplacement(t *testing.T) {
	session := agenttest.CreateSession()
	const operations = 100
	start := make(chan struct{})
	errs := make(chan error, 2*operations)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := range operations {
			session.SetServiceID(fmt.Sprintf("service%d", i))
			session.Set("key", fmt.Sprintf("value%d", i))
			session.Delete("other")
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range operations {
			if err := json.Unmarshal([]byte(`{"State":{"other":"restored"},"ServiceID":"restored"}`), session); err != nil {
				errs <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range operations {
			_ = session.ServiceID()
			var value string
			if _, err := session.Get("other", &value); err != nil {
				errs <- err
			}
			data, err := json.Marshal(session)
			if err != nil {
				errs <- err
				continue
			}
			if !json.Valid(data) {
				errs <- fmt.Errorf("invalid session JSON: %s", data)
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSession_ConcurrentJSONReplacementKeepsServiceIDAndStateTogether(t *testing.T) {
	var session agent.Session
	first := []byte(`{"State":{"first":1},"ServiceID":"first"}`)
	second := []byte(`{"State":{"second":2},"ServiceID":"second"}`)
	if err := json.Unmarshal(first, &session); err != nil {
		t.Fatal(err)
	}

	const operations = 300
	start := make(chan struct{})
	errs := make(chan error, operations)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := range operations {
			data := first
			if i%2 == 0 {
				data = second
			}
			if err := json.Unmarshal(data, &session); err != nil {
				errs <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range operations {
			data, err := json.Marshal(&session)
			if err != nil {
				errs <- err
				continue
			}
			var payload struct {
				State     map[string]json.RawMessage
				ServiceID string
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				errs <- err
				continue
			}
			if len(payload.State) != 1 || len(payload.State[payload.ServiceID]) == 0 {
				errs <- fmt.Errorf("session snapshot mixed state and service ID: %s", data)
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSession_ConcurrentFirstWritesPreserveState(t *testing.T) {
	var session agent.Session
	const keys = 100
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(keys + 1)
	for i := range keys {
		go func(index int) {
			defer wg.Done()
			<-start
			session.Set(fmt.Sprintf("key%d", index), fmt.Sprintf("value%d", index))
		}(i)
	}
	go func() {
		defer wg.Done()
		<-start
		session.SetServiceID("service")
	}()
	close(start)
	wg.Wait()

	if got := session.ServiceID(); got != "service" {
		t.Fatalf("ServiceID() = %q, want service", got)
	}
	for i := range keys {
		var got string
		want := fmt.Sprintf("value%d", i)
		if ok, err := session.Get(fmt.Sprintf("key%d", i), &got); err != nil || !ok || got != want {
			t.Fatalf("Get(key%d) = (%q, %v, %v), want (%q, true, nil)", i, got, ok, err, want)
		}
	}
}

func TestSession_JSONCallbacksMayAccessSession(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("callback", sessionMarshalCallback{session: session})
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State map[string]json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload.State["callback"]) != `"callback"` || payload.State["from-marshaler"] != nil {
		t.Fatalf("serialized snapshot = %s, want callback without later write", data)
	}
	var saved string
	if ok, err := session.Get("from-marshaler", &saved); err != nil || !ok || saved != "saved" {
		t.Fatalf("reentrant Set not retained: (%q, %v, %v)", saved, ok, err)
	}

	var decoded agent.Session
	if err := json.Unmarshal([]byte(`{"State":{"callback":"decoded"}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	v := sessionUnmarshalCallback{session: &decoded}
	if ok, err := decoded.Get("callback", &v); err != nil || !ok || v.value != "decoded" {
		t.Fatalf("reentrant Get = (%q, %v, %v)", v.value, ok, err)
	}
	if ok, err := decoded.Get("from-unmarshaler", &saved); err != nil || !ok || saved != "saved" {
		t.Fatalf("reentrant UnmarshalJSON Set not retained: (%q, %v, %v)", saved, ok, err)
	}
}

func TestSessionState_Get_NonexistentKey_ReturnsNotFound(t *testing.T) {
	session := agenttest.CreateSession()
	var v string
	ok, err := session.Get("nonexistent", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected not found for nonexistent key")
	}
}

func TestSessionState_Set_And_Get_Roundtrips(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	var v string
	ok, err := session.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if v != "value1" {
		t.Errorf("expected 'value1', got %v", v)
	}
}

func TestSessionState_Set_OverwritesExistingValue(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "original")
	session.Set("key1", "updated")

	var v string
	ok, err := session.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if v != "updated" {
		t.Errorf("expected 'updated', got %v", v)
	}
}

func TestSessionState_Delete_ExistingKey(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")
	if !session.Delete("key1") {
		t.Fatal("expected existing key to be removed")
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected marshal error after deletion: %v", err)
	}
	var payload struct {
		State map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unexpected unmarshal error after deletion: %v", err)
	}
	if payload.State == nil || len(payload.State) != 0 {
		t.Fatalf("expected empty State object after deletion, got %#v", payload.State)
	}

	var v string
	ok, err := session.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected key to be removed")
	}
}

func TestSessionState_Delete_NonexistentKey_ReturnsFalse(t *testing.T) {
	session := agenttest.CreateSession()
	if session.Delete("nonexistent") {
		t.Fatal("expected nonexistent key to report not removed")
	}
}

func TestSessionState_Delete_NilSession_ReturnsFalse(t *testing.T) {
	var session *agent.Session
	if session.Delete("key") {
		t.Fatal("expected nil session to report not removed")
	}
}

func TestSessionState_Set_DifferentValueTypes(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("string", "hello")
	session.Set("another-string", "world")
	session.Set("int", 42)
	session.Set("bool", true)

	var str string
	if ok, err := session.Get("string", &str); err != nil || !ok || str != "hello" {
		t.Fatalf("string value mismatch: ok=%v err=%v value=%v", ok, err, str)
	}
	var other string
	if ok, err := session.Get("another-string", &other); err != nil || !ok || other != "world" {
		t.Fatalf("second string value mismatch: ok=%v err=%v value=%v", ok, err, other)
	}
	var num int
	if ok, err := session.Get("int", &num); err != nil || !ok || num != 42 {
		t.Fatalf("int value mismatch: ok=%v err=%v value=%v", ok, err, num)
	}
	var b bool
	if ok, err := session.Get("bool", &b); err != nil || !ok || !b {
		t.Fatalf("bool value mismatch: ok=%v err=%v value=%v", ok, err, b)
	}
}

func TestSessionState_Set_NilPointerValue_RoundtripsForSameType(t *testing.T) {
	session := agenttest.CreateSession()
	var p *nullablePerson
	session.Set("person", p)

	var out *nullablePerson
	if ok, err := session.Get("person", &out); err != nil || !ok || out != nil {
		t.Fatalf("Get = (%#v, %v, %v), want (nil, true, nil)", out, ok, err)
	}
}

func TestSessionState_Get_TypeMismatchReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("count", 42)

	var s string
	ok, err := session.Get("count", &s)
	if ok {
		t.Fatal("expected type mismatch to report not found")
	}
	if err != nil {
		t.Fatalf("expected no error on type mismatch, got %v", err)
	}
	if s != "" {
		t.Fatalf("mismatched destination = %q, want unchanged empty string", s)
	}
}

func TestSessionState_Get_InvalidDestinationReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("count", 42)

	var nonPtr int
	ok, err := session.Get("count", nonPtr)
	if ok {
		t.Fatal("expected unreadable destination to report not found")
	}
	if err == nil {
		t.Fatal("expected destination error")
	}
}

func TestSessionState_BlankKeysPanic(t *testing.T) {
	session := agenttest.CreateSession()
	for _, key := range []string{"", " ", "\t"} {
		for name, call := range map[string]func(){
			"Get":    func() { var value string; _, _ = session.Get(key, &value) },
			"Set":    func() { session.Set(key, "value") },
			"Delete": func() { session.Delete(key) },
		} {
			t.Run(fmt.Sprintf("%s/%q", name, key), func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Fatal("expected blank session state key to panic")
					}
				}()
				call()
			})
		}
	}
	session.Set(" key ", "value")
	var value string
	if ok, err := session.Get(" key ", &value); err != nil || !ok || value != "value" {
		t.Fatalf("nonblank key = (%q, %v, %v), want value", value, ok, err)
	}
	if ok, err := session.Get("key", &value); err != nil || ok {
		t.Fatalf("whitespace was trimmed from state key: (%v, %v)", ok, err)
	}
}

func TestSession_MarshalJSON_EmptyState(t *testing.T) {
	session := agenttest.CreateSession()
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload struct {
		State map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if len(payload.State) != 0 {
		t.Errorf("expected marshaled session to contain empty State, got %q", string(data))
	}
}

func TestSession_MarshalJSON_DirectMarshalIncludesState(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload struct {
		ServiceID string
		State     map[string]string
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if payload.ServiceID != "" {
		t.Fatalf("ServiceID = %q, want empty string", payload.ServiceID)
	}
	if len(payload.State) != 1 || payload.State["key1"] != "value1" {
		t.Fatalf("State = %#v, want map[string]string{\"key1\": \"value1\"}", payload.State)
	}
}

func TestSession_UnmarshalJSON_IntoCreatedSession(t *testing.T) {
	session := agenttest.CreateSession()
	err := json.Unmarshal([]byte(`{"State":{"key1":"value1"}}`), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var value string
	if ok, err := session.Get("key1", &value); err != nil || !ok || value != "value1" {
		t.Fatalf("session.Get(key1) = ok %v, value %q, err %v", ok, value, err)
	}
}

func TestSession_UnmarshalJSON_NullStateValueRoundtripsAndCanBeOverwritten(t *testing.T) {
	source := agenttest.CreateSession()
	var original *nullablePerson
	source.Set("nullKey", original)
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var session agent.Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var value *nullablePerson
	ok, err := session.Get("nullKey", &value)
	if err != nil || !ok || value != nil {
		t.Fatalf("session.Get(nullKey) = ok %v, value %#v, err %v; want true, nil, nil", ok, value, err)
	}

	session.Set("nullKey", "replacement")
	var replacement string
	ok, err = session.Get("nullKey", &replacement)
	if err != nil || !ok || replacement != "replacement" {
		t.Fatalf("session.Get(nullKey) after overwrite = ok %v, value %q, err %v; want true, replacement, nil", ok, replacement, err)
	}
}

func TestSession_UnmarshalJSON_IntoZeroValueSession(t *testing.T) {
	var session agent.Session
	err := json.Unmarshal([]byte(`{"ServiceID":"service-123","State":{"key1":"value1"}}`), &session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := session.ServiceID(); got != "service-123" {
		t.Fatalf("session.ServiceID() = %q, want %q", got, "service-123")
	}

	var value string
	if ok, err := session.Get("key1", &value); err != nil || !ok || value != "value1" {
		t.Fatalf("session.Get(key1) = ok %v, value %q, err %v", ok, value, err)
	}
}

func TestSession_MarshalJSON_ZeroValueSession(t *testing.T) {
	var session agent.Session
	data, err := json.Marshal(&session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := string(data), `{"State":{},"ServiceID":""}`; got != want {
		t.Fatalf("json.Marshal(session) = %s, want %s", got, want)
	}
}

func TestSession_UnmarshalJSON_WithStateValues(t *testing.T) {
	session := agenttest.CreateSession()
	mustUnmarshalJSON(t, []byte(`{"State":{"key1":"value1","key2":42}}`), session)

	var s string
	if ok, err := session.Get("key1", &s); err != nil || !ok || s != "value1" {
		t.Fatalf("unexpected key1 result: ok=%v err=%v value=%q", ok, err, s)
	}
	var n float64
	if ok, err := session.Get("key2", &n); err != nil || !ok || n != 42 {
		t.Fatalf("unexpected key2 result: ok=%v err=%v value=%v", ok, err, n)
	}
}

func TestSessionState_Get_LazyDecodesAndCaches(t *testing.T) {
	session := agenttest.CreateSession()
	mustUnmarshalJSON(t, []byte(`{"State":{"person":{"Name":"Ada"}}}`), session)

	var person person
	ok, err := session.Get("person", &person)
	if !ok || err != nil {
		t.Fatalf("expected typed value, ok=%v err=%v", ok, err)
	}
	if person.Name != "Ada" {
		t.Fatalf("expected Ada, got %q", person.Name)
	}
}

func TestSessionState_Get_LazyDecodedValueTypeMismatchReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	mustUnmarshalJSON(t, []byte(`{"State":{"person":{"Name":"Ada"}}}`), session)

	var p person
	if ok, err := session.Get("person", &p); err != nil || !ok {
		t.Fatalf("expected successful decode, ok=%v err=%v", ok, err)
	}
	if p.Name != "Ada" {
		t.Fatalf("expected Ada, got %q", p.Name)
	}

	var n int
	ok, err := session.Get("person", &n)
	if ok {
		t.Fatal("expected type mismatch to report not found")
	}
	if err != nil {
		t.Fatalf("expected no error on type mismatch, got %v", err)
	}
	if n != 0 {
		t.Fatalf("mismatched destination = %d, want unchanged zero value", n)
	}
}

func mustUnmarshalJSON(t *testing.T, data []byte, session *agent.Session) {
	t.Helper()
	if err := json.Unmarshal(data, session); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
}

func TestSession_MarshalAfterGet_PreservesUnreadFields(t *testing.T) {
	// Reading a key into a narrower struct must not discard fields that were
	// never decoded when the session is saved again.
	const original = `{"State":{"k":{"A":1,"B":2}},"ServiceID":""}`
	var s agent.Session
	if err := json.Unmarshal([]byte(original), &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var partial struct {
		A int
	}
	if ok, err := s.Get("k", &partial); err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != original {
		t.Errorf("round-trip corrupted after Get:\n got:  %s\n want: %s", out, original)
	}
}

func TestSession_MarshalAfterGet_RequiresSetToPersistCachedObjectEdits(t *testing.T) {
	var session agent.Session
	if err := json.Unmarshal([]byte(`{"State":{"person":{"Name":"Ada"}}}`), &session); err != nil {
		t.Fatal(err)
	}
	var value *person
	if ok, err := session.Get("person", &value); err != nil || !ok || value == nil {
		t.Fatalf("Get(person) = (%v, %v, %v), want decoded person", value, ok, err)
	}
	value.Name = "Grace"
	data, err := json.Marshal(&session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Grace") || !strings.Contains(string(data), "Ada") {
		t.Fatalf("cached edit replaced original raw JSON without Set: %s", data)
	}
	session.Set("person", value)
	data, err = json.Marshal(&session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	var got *person
	if ok, err := restored.Get("person", &got); err != nil || !ok || got == nil || got.Name != "Grace" {
		t.Fatalf("restored person = (%+v, %v, %v), want Grace", got, ok, err)
	}
}

func TestSessionStateBag_GetValue_WithExistingKey_ReturnsValue(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	var result string
	ok, err := session.Get("key1", &result)
	if err != nil || !ok || result != "value1" {
		t.Fatalf("Get(key1) = (%q, %v, %v), want (value1, true, nil)", result, ok, err)
	}
}

func TestSessionStateBag_GetValue_WithNonexistentKey_ReturnsNull(t *testing.T) {
	session := agenttest.CreateSession()

	var result *string
	ok, err := session.Get("nonexistent", &result)
	if err != nil || ok || result != nil {
		t.Fatalf("Get(nonexistent) = (%v, %v, %v), want (nil, false, nil)", result, ok, err)
	}
}

func TestSessionStateBag_TryGetValue_WithExistingKey_ReturnsTrueAndValue(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	var result string
	found, err := session.Get("key1", &result)
	if err != nil || !found || result != "value1" {
		t.Fatalf("Get(key1) = (%q, %v, %v), want (value1, true, nil)", result, found, err)
	}
}

func TestSessionStateBag_TryGetValue_WithNonexistentKey_ReturnsFalseAndNull(t *testing.T) {
	session := agenttest.CreateSession()

	var result *string
	found, err := session.Get("nonexistent", &result)
	if err != nil || found || result != nil {
		t.Fatalf("Get(nonexistent) = (%v, %v, %v), want (nil, false, nil)", result, found, err)
	}
}

func TestSessionStateBag_SetValue_OverwriteWithNull_ReturnsNull(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	var value *string
	session.Set("key1", value)

	var result *string
	found, err := session.Get("key1", &result)
	if err != nil || !found || result != nil {
		t.Fatalf("Get(key1) = (%v, %v, %v), want (nil, true, nil)", result, found, err)
	}
}

func TestSessionStateBag_SetValue_OverwriteNullWithValue_ReturnsValue(t *testing.T) {
	session := agenttest.CreateSession()
	var value *string
	session.Set("key1", value)

	session.Set("key1", "newValue")

	var result string
	found, err := session.Get("key1", &result)
	if err != nil || !found || result != "newValue" {
		t.Fatalf("Get(key1) = (%q, %v, %v), want (newValue, true, nil)", result, found, err)
	}
}

func TestSessionStateBag_TryRemoveValue_DoesNotAffectOtherKeys(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")
	session.Set("key2", "value2")

	session.Delete("key1")

	var removed string
	if found, err := session.Get("key1", &removed); err != nil || found {
		t.Fatalf("removed key found=%v, err=%v", found, err)
	}
	var remaining string
	if found, err := session.Get("key2", &remaining); err != nil || !found || remaining != "value2" {
		t.Fatalf("remaining key = (%q, %v, %v), want (value2, true, nil)", remaining, found, err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State map[string]json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.State) != 1 {
		t.Fatalf("remaining state = %v, err=%v; want one key", payload.State, err)
	}
}

func TestSessionStateBag_TryRemoveValue_ThenSetValue_Works(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "original")

	session.Delete("key1")
	session.Set("key1", "replacement")

	var result string
	found, err := session.Get("key1", &result)
	if err != nil || !found || result != "replacement" {
		t.Fatalf("Get(key1) = (%q, %v, %v), want (replacement, true, nil)", result, found, err)
	}
}

func TestSessionStateBag_Serialize_EmptyStateBag_ReturnsEmptyObject(t *testing.T) {
	session := agenttest.CreateSession()

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil || string(payload.State) != "{}" {
		t.Fatalf("serialized State = %s, err=%v; want {}", payload.State, err)
	}
}

func TestSessionStateBag_Serialize_WithStringValue_ReturnsJsonWithValue(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("stringKey", "stringValue")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State map[string]json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload.State["stringKey"]) != `"stringValue"` {
		t.Fatalf("serialized State[stringKey] = %s", payload.State["stringKey"])
	}
}

func TestSessionStateBag_Deserialize_FromJsonDocument_ReturnsEmptyStateBag(t *testing.T) {
	var session agent.Session

	if err := json.Unmarshal([]byte(`{"State":{}}`), &session); err != nil {
		t.Fatal(err)
	}

	var value string
	if found, err := session.Get("nonexistent", &value); err != nil || found {
		t.Fatalf("Get(nonexistent) = (%v, %v), want (false, nil)", found, err)
	}
}

func TestSessionStateBag_SerializeDeserialize_WithStringValue_Roundtrips(t *testing.T) {
	original := agenttest.CreateSession()
	original.Set("stringKey", "stringValue")

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	var result string
	if found, err := restored.Get("stringKey", &result); err != nil || !found || result != "stringValue" {
		t.Fatalf("restored value = (%q, %v, %v), want (stringValue, true, nil)", result, found, err)
	}
}

func TestSessionStateBag_SerializeDeserialize_WithComplexObject_Roundtrips(t *testing.T) {
	type animal struct {
		ID       int
		FullName string
		Species  string
	}
	original := agenttest.CreateSession()
	original.Set("animal", animal{ID: 4, FullName: "Polly", Species: "Bear"})

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	var result animal
	if found, err := restored.Get("animal", &result); err != nil || !found || result != (animal{ID: 4, FullName: "Polly", Species: "Bear"}) {
		t.Fatalf("restored animal = (%+v, %v, %v)", result, found, err)
	}
}

func TestSessionStateBag_JsonSerializerRoundtrip_WithStringValue_PreservesData(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("greeting", "hello world")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	var result string
	if found, err := restored.Get("greeting", &result); err != nil || !found || result != "hello world" {
		t.Fatalf("restored greeting = (%q, %v, %v)", result, found, err)
	}
}

func TestSessionStateBag_TryGetValue_WithDifferentTypeAfterDeserializedRead_ReturnsFalse(t *testing.T) {
	original := agenttest.CreateSession()
	original.Set("key1", "hello")
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	var cached string
	if found, err := restored.Get("key1", &cached); err != nil || !found || cached != "hello" {
		t.Fatalf("first read = (%q, %v, %v)", cached, found, err)
	}

	var result person
	if found, err := restored.Get("key1", &result); err != nil || found || result != (person{}) {
		t.Fatalf("mismatched read = (%+v, %v, %v), want zero, false, nil", result, found, err)
	}
}

func TestSessionStateBag_TryGetValue_WithDifferentTypeAfterSet_ReturnsFalse(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "hello")

	var result person
	if found, err := session.Get("key1", &result); err != nil || found || result != (person{}) {
		t.Fatalf("mismatched read = (%+v, %v, %v), want zero, false, nil", result, found, err)
	}
}

func TestAgentSession_StateBag_MultipleKeys_StoreAndRetrieveIndependently(t *testing.T) {
	session := agenttest.CreateSession()

	session.Set("key1", "value1")
	session.Set("key2", "value2")

	for key, want := range map[string]string{"key1": "value1", "key2": "value2"} {
		var got string
		if found, err := session.Get(key, &got); err != nil || !found || got != want {
			t.Errorf("Get(%s) = (%q, %v, %v), want %q", key, got, found, err, want)
		}
	}
}

func TestAgentSession_StateBag_Values_Roundtrips(t *testing.T) {
	session := agenttest.CreateSession()

	session.Set("key1", "value1")
	var got string
	if found, err := session.Get("key1", &got); err != nil || !found || got != "value1" {
		t.Fatalf("Get(key1) = (%q, %v, %v), want value1", got, found, err)
	}
}

func TestAgentSession_StateBag_OverwriteValue_ReturnsUpdatedValue(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "original")

	session.Set("key1", "updated")

	var got string
	if found, err := session.Get("key1", &got); err != nil || !found || got != "updated" {
		t.Fatalf("Get(key1) = (%q, %v, %v), want updated", got, found, err)
	}
}

func TestSessionStateBag_SetValue_WithComplexObject_StoresValue(t *testing.T) {
	type animal struct {
		ID       int
		FullName string
		Species  string
	}
	session := agenttest.CreateSession()
	value := &animal{ID: 1, FullName: "Buddy", Species: "Bear"}

	session.Set("animal", value)

	var result *animal
	if found, err := session.Get("animal", &result); err != nil || !found || result == nil || *result != *value {
		t.Fatalf("Get(animal) = (%+v, %v, %v), want %+v", result, found, err, value)
	}
}

func TestSessionStateBag_TryGetValue_WithComplexObject_ReturnsTrueAndValue(t *testing.T) {
	type animal struct {
		ID       int
		FullName string
		Species  string
	}
	session := agenttest.CreateSession()
	session.Set("animal", &animal{ID: 3, FullName: "Goldie", Species: "Walrus"})

	var result *animal
	found, err := session.Get("animal", &result)
	if err != nil || !found || result == nil || *result != (animal{ID: 3, FullName: "Goldie", Species: "Walrus"}) {
		t.Fatalf("Get(animal) = (%+v, %v, %v)", result, found, err)
	}
}

func TestSessionStateBag_Serialize_WithComplexObject_ReturnsJsonWithProperties(t *testing.T) {
	type animal struct {
		ID       int    `json:"id"`
		FullName string `json:"fullName"`
		Species  string `json:"species"`
	}
	session := agenttest.CreateSession()
	session.Set("animal", animal{ID: 7, FullName: "Spot", Species: "Walrus"})

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		State map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(payload.State["animal"], &result); err != nil {
		t.Fatal(err)
	}
	if string(result["id"]) != "7" || string(result["fullName"]) != `"Spot"` || string(result["species"]) != `"Walrus"` {
		t.Fatalf("serialized animal = %s", payload.State["animal"])
	}
}

func TestSessionStateBag_SetValue_WithNullValue_StoresNull(t *testing.T) {
	session := agenttest.CreateSession()

	var value *string
	session.Set("key1", value)

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State map[string]json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.State) != 1 || string(payload.State["key1"]) != "null" {
		t.Fatalf("State = %v, err=%v; want key1: null", payload.State, err)
	}
}

func TestSessionStateBag_SerializeDeserialize_WithNullValue_SerializesAsNull(t *testing.T) {
	session := agenttest.CreateSession()
	var value *string
	session.Set("nullKey", value)

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ State map[string]json.RawMessage }
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.State) != 1 || string(payload.State["nullKey"]) != "null" {
		t.Fatalf("serialized State = %v, want nullKey: null", payload.State)
	}

	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	var got *string
	if found, err := restored.Get("nullKey", &got); err != nil || !found || got != nil {
		t.Fatalf("restored nullKey = (%v, %v, %v), want (nil, true, nil)", got, found, err)
	}
}

func TestSessionStateBag_GetValue_WithNullValue_ReturnsNull(t *testing.T) {
	session := agenttest.CreateSession()
	var value *string
	session.Set("key1", value)

	var result *string
	found, err := session.Get("key1", &result)
	if err != nil || !found || result != nil {
		t.Fatalf("Get(key1) = (%v, %v, %v), want (nil, true, nil)", result, found, err)
	}
}

func TestSessionStateBag_TryGetValue_ComplexTypeAfterSetString_ReturnsFalse(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("animal", "not an animal")

	var result *person
	found, err := session.Get("animal", &result)
	if err != nil || found || result != nil {
		t.Fatalf("Get(animal) = (%v, %v, %v), want (nil, false, nil)", result, found, err)
	}
}

func TestSessionStateBag_JsonSerializerRoundtrip_WithComplexObject_PreservesData(t *testing.T) {
	type animal struct {
		ID       int
		FullName string
		Species  string
	}
	session := agenttest.CreateSession()
	session.Set("animal", animal{ID: 10, FullName: "Rex", Species: "Tiger"})

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}

	var result animal
	found, err := restored.Get("animal", &result)
	if err != nil || !found || result != (animal{ID: 10, FullName: "Rex", Species: "Tiger"}) {
		t.Fatalf("restored animal = (%+v, %v, %v)", result, found, err)
	}
}

func TestSessionStateBag_JsonSerializerDeserialize_NullJson_ReturnsNull(t *testing.T) {
	var session *agent.Session

	if err := json.Unmarshal([]byte("null"), &session); err != nil {
		t.Fatal(err)
	}

	if session != nil {
		t.Fatalf("unmarshaled null = %v, want nil", session)
	}
}

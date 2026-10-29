// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
)

func TestAggregatingSource_PreservesOrderAndContext(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "first", Description: "First"}, "First.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "second", Description: "Second"}, "Second.", nil, nil)
	request := skills.SourceContext{Agent: agenttest.New(nil), Session: agenttest.CreateSession()}
	var calls []int
	children := []skills.Source{
		skills.SourceFunc(func(ctx context.Context, actual skills.SourceContext) ([]*skills.Skill, error) {
			if ctx != t.Context() || actual != request {
				t.Errorf("first source context = %#v", actual)
			}
			calls = append(calls, 1)
			return []*skills.Skill{first, second}, nil
		}),
		skills.SourceFunc(func(ctx context.Context, actual skills.SourceContext) ([]*skills.Skill, error) {
			if ctx != t.Context() || actual != request {
				t.Errorf("second source context = %#v", actual)
			}
			calls = append(calls, 2)
			return []*skills.Skill{first}, nil
		}),
	}
	source := skills.NewAggregatingSource(children...)
	children[0] = nil
	result, err := source.Skills(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []int{1, 2}) || !slices.Equal(result, []*skills.Skill{first, second, first}) {
		t.Fatalf("aggregation = %v, call order = %v", result, calls)
	}
}

func TestAggregatingSource_EmptySourceList(t *testing.T) {
	result, err := skills.NewAggregatingSource().Skills(t.Context(), skills.SourceContext{})
	if err != nil || len(result) != 0 {
		t.Fatalf("empty aggregation = (%v, %v)", result, err)
	}
}

func TestAggregatingSource_PreservesFailureAndStopsLoading(t *testing.T) {
	failure := errors.New("source failed")
	source := skills.NewAggregatingSource(
		skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
			return nil, failure
		}),
		skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
			t.Error("loaded a later source after failure")
			return nil, nil
		}),
	)
	if result, err := source.Skills(t.Context(), skills.SourceContext{}); !errors.Is(err, failure) || result != nil {
		t.Fatalf("aggregation failure = (%v, %v)", result, err)
	}
}

func TestSourceConstructors_RejectNilInnerSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		new  func()
	}{
		{"aggregate", func() { skills.NewAggregatingSource(nil) }},
		{"cache", func() { skills.NewCachingSource(nil, skills.CachingSourceConfig{}) }},
		{"filter", func() {
			skills.NewFilteringSource(nil, func(context.Context, *skills.Skill, skills.SourceContext) bool { return true }, nil)
		}},
		{"deduplicate", func() { skills.NewDeduplicatingSource(nil, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got == nil {
					t.Error("nil source was accepted")
				}
			}()
			tc.new()
		})
	}
}

func TestFilteringSource_NilPredicatePanics(t *testing.T) {
	defer func() {
		if got := recover(); got != "skills: filter is nil" {
			t.Fatalf("nil predicate panic = %v", got)
		}
	}()
	skills.NewFilteringSource(skills.NewInMemorySource(), nil, nil)
}

func TestCachingSource_CachesSuccessfulListsIncludingEmpty(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "cached", Description: "Cached"}, "Body.", nil, nil)
	for _, result := range [][]*skills.Skill{nil, {}, {skill}} {
		calls := 0
		source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
			calls++
			return result, nil
		}), skills.CachingSourceConfig{})
		for range 2 {
			actual, err := source.Skills(t.Context(), skills.SourceContext{})
			if err != nil || !slices.Equal(actual, result) {
				t.Fatalf("cached list = (%v, %v), want %v", actual, err, result)
			}
			if len(result) > 0 && &actual[0] != &result[0] {
				t.Fatal("cache returned a different skill list")
			}
		}
		if calls != 1 {
			t.Fatalf("source calls = %d, want 1", calls)
		}
	}
}

func TestCachingSource_IsolationUsesCurrentSourceContext(t *testing.T) {
	first, second := agenttest.CreateSession(), agenttest.CreateSession()
	var observed []*agent.Session
	source := skills.NewCachingSource(skills.SourceFunc(func(_ context.Context, request skills.SourceContext) ([]*skills.Skill, error) {
		observed = append(observed, request.Session)
		return nil, nil
	}), skills.CachingSourceConfig{
		CacheIsolationKeySelector: func(_ context.Context, request skills.SourceContext) *string {
			if request.Session == first {
				return new("first")
			}
			return new("second")
		},
	})
	for _, session := range []*agent.Session{first, second, first, second} {
		if _, err := source.Skills(t.Context(), skills.SourceContext{Session: session}); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(observed, []*agent.Session{first, second}) {
		t.Fatalf("loaded session contexts = %v", observed)
	}
}

func TestCachingSource_EmptyKeyDiffersFromSharedKey(t *testing.T) {
	var key *string
	calls := 0
	source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
		calls++
		return nil, nil
	}), skills.CachingSourceConfig{
		CacheIsolationKeySelector: func(context.Context, skills.SourceContext) *string { return key },
	})
	for _, selected := range []*string{nil, new(""), nil, new("")} {
		key = selected
		if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("source loads = %d, want distinct nil and empty-key buckets", calls)
	}
}

func TestCachingSource_CapturesRefreshIntervalAndExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		interval := time.Minute
		source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
			calls++
			return nil, nil
		}), skills.CachingSourceConfig{RefreshInterval: &interval})
		interval = 0
		for range 2 {
			if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Fatalf("fresh source loads = %d, want captured interval", calls)
		}
		time.Sleep(time.Minute)
		if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("expired source loads = %d, want 2", calls)
		}
	})
}

func TestCachingSource_RetriesFailuresWithoutMaskingThem(t *testing.T) {
	failure := errors.New("fetch failed")
	recovered := mustInlineSkill(skills.Frontmatter{Name: "recovered-skill", Description: "Recovered"}, "Body.", nil, nil)
	calls := 0
	source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return []*skills.Skill{recovered}, nil
	}), skills.CachingSourceConfig{})
	if _, err := source.Skills(t.Context(), skills.SourceContext{}); !errors.Is(err, failure) {
		t.Fatalf("fetch error = %v", err)
	}
	for range 2 {
		if result, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil || !slices.Equal(result, []*skills.Skill{recovered}) {
			t.Fatalf("retry result = (%v, %v), want recovered skill", result, err)
		}
	}
	if calls != 2 {
		t.Fatalf("source loads = %d, want one failure and one cached success", calls)
	}
}

func TestCachingSource_ConcurrentCallersShareOneFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		skill := mustInlineSkill(skills.Frontmatter{Name: "cached", Description: "Cached"}, "Body.", nil, nil)
		var calls atomic.Int32
		started, release := make(chan struct{}), make(chan struct{})
		source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return []*skills.Skill{skill}, nil
		}), skills.CachingSourceConfig{})
		results := make([][]*skills.Skill, 10)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() {
				result, err := source.Skills(t.Context(), skills.SourceContext{})
				if err != nil || !slices.Equal(result, []*skills.Skill{skill}) {
					t.Errorf("concurrent cached result = (%v, %v)", result, err)
				}
				results[i] = result
			})
		}
		<-started
		synctest.Wait()
		close(release)
		wg.Wait()
		if count := calls.Load(); count != 1 {
			t.Fatalf("source loads = %d, want 1", count)
		}
		if len(results[0]) != 1 {
			t.Fatalf("first result = %v, want one skill", results[0])
		}
		for _, result := range results {
			if len(result) != 1 || &result[0] != &results[0][0] {
				t.Fatal("concurrent callers received different skill lists")
			}
		}
	})
}

func TestCachingSource_CancelledOwnerAllowsWaiterToRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		skill := mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil)
		var calls atomic.Int32
		started := make(chan context.Context, 2)
		release := make(chan struct{})
		source := skills.NewCachingSource(skills.SourceFunc(func(ctx context.Context, _ skills.SourceContext) ([]*skills.Skill, error) {
			calls.Add(1)
			started <- ctx
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return []*skills.Skill{skill}, nil
			}
		}), skills.CachingSourceConfig{})
		owner, cancel := context.WithCancel(t.Context())
		type result struct {
			skills []*skills.Skill
			err    error
		}
		ownerDone, waiterDone := make(chan error, 1), make(chan result, 1)
		go func() { _, err := source.Skills(owner, skills.SourceContext{}); ownerDone <- err }()
		<-started
		go func() {
			loaded, err := source.Skills(t.Context(), skills.SourceContext{})
			waiterDone <- result{loaded, err}
		}()
		synctest.Wait()
		cancel()
		if err := <-ownerDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("owner failure = %v", err)
		}
		waiterContext := <-started
		close(release)
		got := <-waiterDone
		if got.err != nil || !slices.Equal(got.skills, []*skills.Skill{skill}) {
			t.Fatalf("waiter result = (%v, %v), want skill-a", got.skills, got.err)
		}
		if count := calls.Load(); count != 2 || waiterContext.Err() != nil {
			t.Fatalf("source loads = %d, waiter context error = %v", count, waiterContext.Err())
		}
	})
}

func TestCachingSource_ReturnsSourceSkills(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-x", Description: "X"}, "Body X.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "skill-y", Description: "Y"}, "Body Y.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{})
	result, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Frontmatter.Name != "skill-x" || result[1].Frontmatter.Name != "skill-y" {
		t.Fatalf("cached skills = %v, want skill-x followed by skill-y", result)
	}
}

func TestCachingSource_SameIsolationKeySharesList(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{
		CacheIsolationKeySelector: func(context.Context, skills.SourceContext) *string { return new("same-key") },
	})
	first, err := source.Skills(t.Context(), skills.SourceContext{Agent: agenttest.New(nil), Session: agenttest.CreateSession()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.Skills(t.Context(), skills.SourceContext{Agent: agenttest.New(nil), Session: agenttest.CreateSession()})
	if err != nil {
		t.Fatal(err)
	}
	if inner.count != 1 || len(first) != 1 || len(second) != 1 || &first[0] != &second[0] {
		t.Fatalf("source loads = %d; same isolation key did not share the cached list", inner.count)
	}
}

func TestCachingSource_DifferentIsolationKeysUseAgentIdentity(t *testing.T) {
	first, second := agenttest.New(nil), agenttest.New(nil)
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{
		CacheIsolationKeySelector: func(_ context.Context, request skills.SourceContext) *string {
			if request.Agent == first {
				return new("agent-1")
			}
			return new("agent-2")
		},
	})
	for _, a := range []*agent.Agent{first, second} {
		if _, err := source.Skills(t.Context(), skills.SourceContext{Agent: a, Session: agenttest.CreateSession()}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.count != 2 {
		t.Fatalf("source loads = %d, want two agent-specific cache buckets", inner.count)
	}
}

func TestCachingSource_NilIsolationKeySharesList(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{
		CacheIsolationKeySelector: func(context.Context, skills.SourceContext) *string { return nil },
	})
	first, err := source.Skills(t.Context(), skills.SourceContext{Agent: agenttest.New(nil), Session: agenttest.CreateSession()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.Skills(t.Context(), skills.SourceContext{Agent: agenttest.New(nil), Session: agenttest.CreateSession()})
	if err != nil {
		t.Fatal(err)
	}
	if inner.count != 1 || len(first) != 1 || len(second) != 1 || &first[0] != &second[0] {
		t.Fatalf("source loads = %d; nil isolation keys did not share the cached list", inner.count)
	}
}

func TestCachingSource_AllCallersCancel_CancelsSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &gatedCacheSource{started: make(chan context.Context, 2), release: make(chan struct{})}
		source := skills.NewCachingSource(inner, skills.CachingSourceConfig{})
		first, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		second, cancelSecond := context.WithCancel(t.Context())
		defer cancelSecond()
		done := make(chan error, 2)
		go func() {
			_, err := source.Skills(first, skills.SourceContext{})
			done <- err
		}()
		sourceContext := <-inner.started
		go func() {
			_, err := source.Skills(second, skills.SourceContext{})
			done <- err
		}()
		synctest.Wait()
		cancelFirst()
		cancelSecond()
		for range 2 {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("source error = %v, want cancellation", err)
			}
		}
		if !errors.Is(sourceContext.Err(), context.Canceled) {
			t.Fatalf("inner source context error = %v, want cancellation", sourceContext.Err())
		}
	})
}

func TestCachingSource_SingleCallerCancels_CancelsSource(t *testing.T) {
	inner := &gatedCacheSource{started: make(chan context.Context, 1), release: make(chan struct{})}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := source.Skills(ctx, skills.SourceContext{})
		done <- err
	}()
	sourceContext := <-inner.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("source error = %v, want cancellation", err)
	}
	if !errors.Is(sourceContext.Err(), context.Canceled) {
		t.Fatalf("inner source context error = %v, want cancellation", sourceContext.Err())
	}
}

func TestCachingSource_CanceledFetchDoesNotPoisonNextCall(t *testing.T) {
	inner := &gatedCacheSource{started: make(chan context.Context, 2), release: make(chan struct{})}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := source.Skills(ctx, skills.SourceContext{})
		done <- err
	}()
	<-inner.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("first source error = %v, want cancellation", err)
	}
	close(inner.release)
	result, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil || len(result) != 1 || result[0].Frontmatter.Name != "skill-a" {
		t.Fatalf("retry result = (%v, %v), want skill-a", result, err)
	}
	if count := inner.calls.Load(); count != 2 {
		t.Fatalf("source loads = %d, want cancelled fetch and successful retry", count)
	}
}

func TestCachingSource_WithinRefreshIntervalSharesList(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{RefreshInterval: new(time.Hour)})
	request := skills.SourceContext{}
	first, err := source.Skills(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.Skills(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if inner.count != 1 || len(first) != 1 || len(second) != 1 || &first[0] != &second[0] {
		t.Fatalf("source loads = %d; fresh cache did not reuse the skill list", inner.count)
	}
}

func TestCachingSource_ZeroRefreshIntervalRefetches(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{RefreshInterval: new(time.Duration(0))})
	for range 2 {
		if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.count != 2 {
		t.Fatalf("source loads = %d, want zero interval to refetch", inner.count)
	}
}

func TestCachingSource_NegativeRefreshIntervalRefetches(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{RefreshInterval: new(-time.Minute)})
	for range 2 {
		if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.count != 2 {
		t.Fatalf("source loads = %d, want negative interval to refetch", inner.count)
	}
}

func TestCachingSource_NilRefreshIntervalSharesList(t *testing.T) {
	inner := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	source := skills.NewCachingSource(inner, skills.CachingSourceConfig{RefreshInterval: nil})
	request := skills.SourceContext{}
	first, err := source.Skills(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.Skills(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if inner.count != 1 || len(first) != 1 || len(second) != 1 || &first[0] != &second[0] {
		t.Fatalf("source loads = %d; nil refresh interval did not reuse the skill list", inner.count)
	}
}

func TestCachingSource_PanicDoesNotPoisonCache(t *testing.T) {
	calls := 0
	source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
		calls++
		if calls == 1 {
			panic("fetch panic")
		}
		return nil, nil
	}), skills.CachingSourceConfig{})
	func() {
		defer func() {
			if got := recover(); got != "fetch panic" {
				t.Errorf("source panic = %v", got)
			}
		}()
		_, _ = source.Skills(t.Context(), skills.SourceContext{})
	}()
	if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("source loads after panic = %d", calls)
	}
}

func TestFilteringSource_PassesContextToPredicate(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil)
	expected := skills.SourceContext{Agent: agenttest.New(nil), Session: agenttest.CreateSession()}
	var actual skills.SourceContext
	var actualSkill *skills.Skill
	source := skills.NewFilteringSource(skills.NewInMemorySource(skill),
		func(_ context.Context, skill *skills.Skill, sourceContext skills.SourceContext) bool {
			actualSkill = skill
			actual = sourceContext
			return true
		}, nil)

	result, err := source.Skills(t.Context(), expected)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0] != actualSkill || result[0] != skill {
		t.Fatalf("filtered skills = %v, predicate skill = %p, want original skill %p", result, actualSkill, skill)
	}
	if actual != expected {
		t.Fatalf("predicate source context = %#v, want %#v", actual, expected)
	}
}

func TestFilteringSource_RunsOutsideCacheWithoutChangingItsList(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "first", Description: "First"}, "First.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "second", Description: "Second"}, "Second.", nil, nil)
	original := []*skills.Skill{first, second}
	calls := 0
	cache := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
		calls++
		return original, nil
	}), skills.CachingSourceConfig{})
	firstSession, secondSession := agenttest.CreateSession(), agenttest.CreateSession()
	source := skills.NewFilteringSource(cache, func(_ context.Context, skill *skills.Skill, request skills.SourceContext) bool {
		return request.Session == firstSession && skill == first || request.Session == secondSession && skill == second
	}, nil)
	for _, tc := range []struct {
		session *agent.Session
		want    *skills.Skill
	}{{firstSession, first}, {secondSession, second}} {
		result, err := source.Skills(t.Context(), skills.SourceContext{Session: tc.session})
		if err != nil || !slices.Equal(result, []*skills.Skill{tc.want}) {
			t.Fatalf("filtered cache = (%v, %v), want %p", result, err, tc.want)
		}
	}
	if calls != 1 || !slices.Equal(original, []*skills.Skill{first, second}) {
		t.Fatalf("source calls = %d, original list = %v", calls, original)
	}
}

func TestDeduplicatingSource_KeepsFirstAndDoesNotMutateSource(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "duplicate", Description: "First"}, "First.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "duplicate", Description: "Second"}, "Second.", nil, nil)
	unique := mustInlineSkill(skills.Frontmatter{Name: "unique", Description: "Unique"}, "Unique.", nil, nil)
	original := []*skills.Skill{first, second, unique}
	var logs bytes.Buffer
	source := skills.NewDeduplicatingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
		return original, nil
	}), slog.New(slog.NewTextHandler(&logs, nil)))
	result, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil || !slices.Equal(result, []*skills.Skill{first, unique}) {
		t.Fatalf("deduplicated result = (%v, %v)", result, err)
	}
	if !slices.Equal(original, []*skills.Skill{first, second, unique}) || !strings.Contains(logs.String(), "Duplicate skill name") {
		t.Fatalf("original list = %v, duplicate diagnostics = %q", original, logs.String())
	}
}

func TestSourcePipeline_CloseCascadesOnceAndPreservesFailures(t *testing.T) {
	failure := errors.New("close failed")
	first := &closeTrackingSkillsSource{closeErr: failure}
	second := new(closeTrackingSkillsSource)
	aggregate := skills.NewAggregatingSource(first, second)
	cache := skills.NewCachingSource(aggregate, skills.CachingSourceConfig{})
	filter := skills.NewFilteringSource(cache, func(context.Context, *skills.Skill, skills.SourceContext) bool { return true }, nil)
	source := skills.NewDeduplicatingSource(filter, nil)
	for range 2 {
		if err := source.Close(); !errors.Is(err, failure) {
			t.Fatalf("pipeline close error = %v", err)
		}
	}
	if first.closeCalls.Load() != 1 || second.closeCalls.Load() != 1 {
		t.Fatalf("child close counts = (%d, %d)", first.closeCalls.Load(), second.closeCalls.Load())
	}
	for _, closed := range []skills.Source{aggregate, cache, filter, source} {
		if _, err := closed.Skills(t.Context(), skills.SourceContext{}); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Errorf("closed source %T returned %v", closed, err)
		}
	}
}

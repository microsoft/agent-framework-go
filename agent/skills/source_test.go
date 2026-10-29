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
	calls := 0
	source := skills.NewCachingSource(skills.SourceFunc(func(context.Context, skills.SourceContext) ([]*skills.Skill, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return nil, nil
	}), skills.CachingSourceConfig{})
	if _, err := source.Skills(t.Context(), skills.SourceContext{}); !errors.Is(err, failure) {
		t.Fatalf("fetch error = %v", err)
	}
	for range 2 {
		if _, err := source.Skills(t.Context(), skills.SourceContext{}); err != nil {
			t.Fatal(err)
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
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				result, err := source.Skills(t.Context(), skills.SourceContext{})
				if err != nil || !slices.Equal(result, []*skills.Skill{skill}) {
					t.Errorf("concurrent cached result = (%v, %v)", result, err)
				}
			})
		}
		<-started
		synctest.Wait()
		close(release)
		wg.Wait()
		if count := calls.Load(); count != 1 {
			t.Fatalf("source loads = %d, want 1", count)
		}
	})
}

func TestCachingSource_CancelledOwnerAllowsWaiterToRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
				return nil, nil
			}
		}), skills.CachingSourceConfig{})
		owner, cancel := context.WithCancel(t.Context())
		ownerDone, waiterDone := make(chan error, 1), make(chan error, 1)
		go func() { _, err := source.Skills(owner, skills.SourceContext{}); ownerDone <- err }()
		<-started
		go func() { _, err := source.Skills(t.Context(), skills.SourceContext{}); waiterDone <- err }()
		synctest.Wait()
		cancel()
		if err := <-ownerDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("owner failure = %v", err)
		}
		<-started
		close(release)
		if err := <-waiterDone; err != nil {
			t.Fatal(err)
		}
		if count := calls.Load(); count != 2 {
			t.Fatalf("source loads = %d, want cancelled owner plus waiter retry", count)
		}
	})
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

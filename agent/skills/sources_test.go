// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/skills"
)

type blockingSource struct {
	mu      sync.Mutex
	count   int
	skills  []*skills.Skill
	release chan struct{}
}

func (s *blockingSource) Skills(context.Context) ([]*skills.Skill, error) {
	s.mu.Lock()
	s.count++
	release := s.release
	s.mu.Unlock()
	<-release
	return s.skills, nil
}

func TestNewInMemorySource_ReturnsExportedConcreteType(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "my-skill", Description: "A valid skill."}, "Instructions.", nil, nil),
	)

	if _, ok := source.(*skills.InMemorySource); !ok {
		t.Fatalf("expected *skills.InMemorySource, got %T", source)
	}
}

func TestAggregatingSource_PreservesRegistrationOrder(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "first", Description: "First skill."}, "First.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "second", Description: "Second skill."}, "Second.", nil, nil)

	source := skills.NewAggregatingSource(
		skills.NewInMemorySource(first),
		skills.NewInMemorySource(second),
	)

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "first" || loaded[1].Frontmatter.Name != "second" {
		t.Fatalf("unexpected skill order: %q, %q", loaded[0].Frontmatter.Name, loaded[1].Frontmatter.Name)
	}
}

func TestDelegatingSource_DelegatesToInnerSource(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "delegated", Description: "Delegated skill."}, "Delegated.", nil, nil)
	inner := &countingSource{skills: []*skills.Skill{skill}}

	source := skills.NewDelegatingSource(inner)
	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "delegated" {
		t.Fatalf("unexpected skills: %#v", loaded)
	}
	if inner.count != 1 {
		t.Fatalf("expected inner source to be called once, got %d", inner.count)
	}
}

func TestFilteringSource_FiltersSkills(t *testing.T) {
	keep := mustInlineSkill(skills.Frontmatter{Name: "keep", Description: "Keep skill."}, "Keep.", nil, nil)
	skip := mustInlineSkill(skills.Frontmatter{Name: "skip", Description: "Skip skill."}, "Skip.", nil, nil)

	source := skills.NewFilteringSource(
		skills.NewInMemorySource(keep, skip),
		func(skill *skills.Skill) bool {
			return skill.Frontmatter.Name == "keep"
		},
		nil,
	)

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "keep" {
		t.Fatalf("expected only keep skill, got %#v", loaded)
	}
}

func TestDeduplicatingSource_RemovesLaterDuplicates(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "duplicate", Description: "First skill."}, "First.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "duplicate", Description: "Second skill."}, "Second.", nil, nil)

	source := skills.NewDeduplicatingSource(
		skills.NewInMemorySource(first, second),
		nil,
	)

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 deduplicated skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "duplicate" {
		t.Fatalf("expected first skill to win, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestCachingSource_CachesResultsAfterFirstLoad(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "cached", Description: "Cached skill."}, "Cached.", nil, nil)
	inner := &countingSource{skills: []*skills.Skill{skill}}

	source := skills.NewCachingSource(inner)
	_, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if inner.count != 1 {
		t.Fatalf("expected inner source to be loaded once, got %d", inner.count)
	}
}

func TestCachingSource_SharesInFlightLoadAcrossConcurrentCallers(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "cached", Description: "Cached skill."}, "Cached.", nil, nil)
	inner := &blockingSource{
		skills:  []*skills.Skill{skill},
		release: make(chan struct{}),
	}
	source := skills.NewCachingSource(inner)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := source.Skills(t.Context())
			errs <- err
		}()
	}

	close(inner.release)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if inner.count != 1 {
		t.Fatalf("expected one shared load, got %d", inner.count)
	}
}

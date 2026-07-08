// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// InMemorySource is a skills source backed by an in-memory skill slice.
type InMemorySource struct {
	skills []*Skill
}

func newSkillSliceSource(skills ...*Skill) *InMemorySource {
	cloned := append([]*Skill(nil), skills...)
	for i, skill := range cloned {
		if skill == nil {
			panic(fmt.Sprintf("skill %d is nil", i))
		}
		if err := skill.Frontmatter.Validate(); err != nil {
			panic(fmt.Sprintf("skill %d has invalid frontmatter: %v", i, err))
		}
	}
	return &InMemorySource{skills: cloned}
}

// Skills returns the in-memory skills in registration order.
func (s *InMemorySource) Skills(context.Context) ([]*Skill, error) {
	return s.skills, nil
}

// AggregatingSource is a skills source that returns the concatenated skills from
// multiple child sources in registration order.
type AggregatingSource struct {
	sources []Source
}

// NewAggregatingSource creates a source that aggregates child sources in order.
func NewAggregatingSource(sources ...Source) *AggregatingSource {
	cloned := append([]Source(nil), sources...)
	for i, source := range cloned {
		if source == nil {
			panic(fmt.Sprintf("source %d is nil", i))
		}
	}
	return &AggregatingSource{sources: cloned}
}

// Skills loads skills from each child source in registration order.
func (s *AggregatingSource) Skills(ctx context.Context) ([]*Skill, error) {
	var loaded []*Skill
	for _, source := range s.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceSkills, err := source.Skills(ctx)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, sourceSkills...)
	}
	return loaded, nil
}

// DelegatingSource forwards skill loading to an inner source.
type DelegatingSource struct {
	Inner Source
}

// NewDelegatingSource creates a delegating source around an inner source.
func NewDelegatingSource(inner Source) *DelegatingSource {
	if inner == nil {
		panic("inner source is nil")
	}
	return &DelegatingSource{Inner: inner}
}

// Skills delegates to the inner source.
func (s *DelegatingSource) Skills(ctx context.Context) ([]*Skill, error) {
	return s.Inner.Skills(ctx)
}

// FilteringSource filters skills returned by an inner source with a predicate.
type FilteringSource struct {
	*DelegatingSource
	predicate func(*Skill) bool
	logger    *slog.Logger
}

// NewFilteringSource creates a source that keeps only skills matching the predicate.
func NewFilteringSource(inner Source, predicate func(*Skill) bool, logger *slog.Logger) *FilteringSource {
	if predicate == nil {
		panic("predicate is nil")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &FilteringSource{
		DelegatingSource: NewDelegatingSource(inner),
		predicate:        predicate,
		logger:           logger,
	}
}

// Skills loads skills from the inner source and filters them.
func (s *FilteringSource) Skills(ctx context.Context) ([]*Skill, error) {
	allSkills, err := s.Inner.Skills(ctx)
	if err != nil {
		return nil, err
	}

	filtered := make([]*Skill, 0, len(allSkills))
	for _, skill := range allSkills {
		if s.predicate(skill) {
			filtered = append(filtered, skill)
			continue
		}
		s.logger.Debug("Skill excluded by filter predicate", "skillName", skill.Frontmatter.Name)
	}

	return filtered, nil
}

// DeduplicatingSource removes duplicate skill names, keeping the first occurrence.
type DeduplicatingSource struct {
	*DelegatingSource
	logger *slog.Logger
}

// NewDeduplicatingSource creates a source that removes later duplicate skill names.
func NewDeduplicatingSource(inner Source, logger *slog.Logger) *DeduplicatingSource {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &DeduplicatingSource{
		DelegatingSource: NewDelegatingSource(inner),
		logger:           logger,
	}
}

// Skills loads skills from the inner source and deduplicates them by name.
func (s *DeduplicatingSource) Skills(ctx context.Context) ([]*Skill, error) {
	allSkills, err := s.Inner.Skills(ctx)
	if err != nil {
		return nil, err
	}
	return deduplicateSkillsByName(allSkills, s.logger), nil
}

// CachingSource caches the skills loaded from an inner source.
type CachingSource struct {
	*DelegatingSource

	mu           sync.Mutex
	cached       []*Skill
	lastLoaded   time.Time
	loading      chan struct{}
	refreshAfter time.Duration
}

// NewCachingSource creates a source that caches the skills returned by the inner source.
func NewCachingSource(inner Source) *CachingSource {
	return &CachingSource{
		DelegatingSource: NewDelegatingSource(inner),
	}
}

// Skills returns cached skills after the first successful load.
func (s *CachingSource) Skills(ctx context.Context) ([]*Skill, error) {
	s.mu.Lock()
	if cached, ok := s.cachedSkillsLocked(); ok {
		s.mu.Unlock()
		return cached, nil
	}
	if s.loading != nil {
		loading := s.loading
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-loading:
		}

		s.mu.Lock()
		if cached, ok := s.cachedSkillsLocked(); ok {
			s.mu.Unlock()
			return cached, nil
		}
		s.mu.Unlock()
		return s.Skills(ctx)
	}
	s.loading = make(chan struct{})
	loading := s.loading
	s.mu.Unlock()

	loaded, err := s.Inner.Skills(ctx)

	s.mu.Lock()
	if err == nil {
		s.cached = loaded
		s.lastLoaded = time.Now().UTC()
	}
	close(loading)
	s.loading = nil
	s.mu.Unlock()

	return loaded, err
}

func (s *CachingSource) cachedSkillsLocked() ([]*Skill, bool) {
	if len(s.cached) == 0 && s.lastLoaded.IsZero() {
		return nil, false
	}
	if s.refreshAfter > 0 && time.Since(s.lastLoaded) >= s.refreshAfter {
		return nil, false
	}
	return s.cached, true
}

func deduplicateSkillsByName(skills []*Skill, logger *slog.Logger) []*Skill {
	seen := make(map[string]struct{}, len(skills))
	deduplicated := skills[:0]
	for _, skill := range skills {
		resolvedKey := strings.ToLower(skill.Frontmatter.Name)
		if _, ok := seen[resolvedKey]; ok {
			logger.Warn("Duplicate skill name: subsequent skill skipped in favor of first occurrence", "skillName", skill.Frontmatter.Name)
			continue
		}
		seen[resolvedKey] = struct{}{}
		deduplicated = append(deduplicated, skill)
	}
	return deduplicated
}

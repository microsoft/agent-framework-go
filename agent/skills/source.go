// Copyright (c) Microsoft. All rights reserved.

package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type sourceLifetime struct {
	sources []Source
	once    sync.Once
	err     error
	closed  atomic.Bool
}

func newSourceLifetime(sources ...Source) *sourceLifetime {
	for i, source := range sources {
		if source == nil {
			panic(fmt.Sprintf("skills: source %d is nil", i))
		}
	}
	return &sourceLifetime{sources: slices.Clone(sources)}
}

func (s *sourceLifetime) checkOpen() error {
	if s.closed.Load() {
		return errors.New("skills: source is closed")
	}
	return nil
}

func (s *sourceLifetime) close() error {
	s.once.Do(func() {
		s.closed.Store(true)
		for i, source := range s.sources {
			if closer, ok := source.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					s.err = errors.Join(s.err, fmt.Errorf("close skills source %d: %w", i, err))
				}
			}
		}
	})
	return s.err
}

// AggregatingSource concatenates its child sources in registration order,
// preserving each child's skill order without filtering or deduplication.
type AggregatingSource struct {
	lifetime *sourceLifetime
}

// NewAggregatingSource creates an aggregate and takes responsibility for closing
// children that implement [io.Closer]. It copies the registration slice.
// It panics if a child is nil.
func NewAggregatingSource(sources ...Source) *AggregatingSource {
	return &AggregatingSource{lifetime: newSourceLifetime(sources...)}
}

// Skills loads children sequentially and stops at the first error.
func (s *AggregatingSource) Skills(ctx context.Context, sourceContext SourceContext) ([]*Skill, error) {
	if err := s.lifetime.checkOpen(); err != nil {
		return nil, err
	}
	all := make([]*Skill, 0)
	for _, source := range s.lifetime.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		skills, err := source.Skills(ctx, sourceContext)
		if err != nil {
			return nil, err
		}
		all = append(all, skills...)
	}
	return all, nil
}

// Close idempotently closes child closers and returns all cleanup errors.
// Do not overlap Close with Skills calls.
func (s *AggregatingSource) Close() error { return s.lifetime.close() }

// CachingSourceConfig configures [NewCachingSource].
type CachingSourceConfig struct {
	// CacheIsolationKeySelector chooses a stable, low-cardinality cache bucket.
	// Nil, or a nil returned key, uses the shared bucket. An explicit empty key
	// is distinct from that shared bucket.
	CacheIsolationKeySelector func(context.Context, SourceContext) *string

	// RefreshInterval limits how long a successful result remains fresh.
	// Nil never expires. Zero or negative values reload on every call.
	RefreshInterval *time.Duration
}

// CachingSource caches successful skill lists, coordinating one active fetch
// per key. A fetch uses its initiating caller's context. Failed or cancelled
// fetches are not cached; waiting callers may retry with their own contexts.
// Returned slices and skills are shared with the cache and must be read-only.
type CachingSource struct {
	lifetime *sourceLifetime
	config   CachingSourceConfig
	mu       sync.Mutex
	entries  map[string]*sourceCacheEntry
}

type sourceCacheEntry struct {
	skills    []*Skill
	loaded    bool
	refreshed time.Time
	loading   chan struct{}
}

// NewCachingSource creates a cache and takes responsibility for closing source
// if it implements [io.Closer]. Configuration is captured at construction.
// It panics if source is nil.
func NewCachingSource(source Source, config CachingSourceConfig) *CachingSource {
	if config.RefreshInterval != nil {
		config.RefreshInterval = new(*config.RefreshInterval)
	}
	return &CachingSource{
		lifetime: newSourceLifetime(source),
		config:   config,
		entries:  make(map[string]*sourceCacheEntry),
	}
}

// Skills returns a fresh cached result or loads one using the current source
// context. Empty successful results are cached as well.
func (s *CachingSource) Skills(ctx context.Context, sourceContext SourceContext) ([]*Skill, error) {
	if err := s.lifetime.checkOpen(); err != nil {
		return nil, err
	}
	key := ""
	if s.config.CacheIsolationKeySelector != nil {
		if selected := s.config.CacheIsolationKeySelector(ctx, sourceContext); selected != nil {
			key = "key:" + *selected
		}
	}
	for {
		s.mu.Lock()
		entry := s.entries[key]
		if entry == nil {
			entry = new(sourceCacheEntry)
			s.entries[key] = entry
		}
		if entry.loaded && (s.config.RefreshInterval == nil || time.Since(entry.refreshed) < *s.config.RefreshInterval) {
			skills := entry.skills
			s.mu.Unlock()
			return skills, nil
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if entry.loading != nil {
			loading := entry.loading
			s.mu.Unlock()
			select {
			case <-loading:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		loading := make(chan struct{})
		entry.loading = loading
		s.mu.Unlock()
		return s.fetch(ctx, sourceContext, entry, loading)
	}
}

func (s *CachingSource) fetch(ctx context.Context, sourceContext SourceContext, entry *sourceCacheEntry, loading chan struct{}) (skills []*Skill, err error) {
	completed := false
	defer func() {
		s.mu.Lock()
		if completed && err == nil {
			entry.skills = skills
			entry.loaded = true
			entry.refreshed = time.Now()
		}
		entry.loading = nil
		close(loading)
		s.mu.Unlock()
	}()
	skills, err = s.lifetime.sources[0].Skills(ctx, sourceContext)
	completed = true
	return skills, err
}

// Close idempotently closes the wrapped source if it implements [io.Closer].
// It does not cancel in-flight calls. Do not overlap Close with Skills calls.
func (s *CachingSource) Close() error { return s.lifetime.close() }

// FilteringSource evaluates its predicate on every call, preserving the order
// of included skills. Place it outside a cache for invocation-specific filtering.
type FilteringSource struct {
	lifetime *sourceLifetime
	filter   func(context.Context, *Skill, SourceContext) bool
	logger   *slog.Logger
}

// NewFilteringSource creates a filter and takes responsibility for closing the
// wrapped source if it implements [io.Closer]. Nil logger discards diagnostics.
// It panics if source or filter is nil.
func NewFilteringSource(source Source, filter func(context.Context, *Skill, SourceContext) bool, logger *slog.Logger) *FilteringSource {
	lifetime := newSourceLifetime(source)
	if filter == nil {
		panic("skills: filter is nil")
	}
	return &FilteringSource{lifetime: lifetime, filter: filter, logger: sourceLogger(logger)}
}

// Skills loads the inner source and evaluates the predicate with the current
// agent and session, without modifying the source's slice.
func (s *FilteringSource) Skills(ctx context.Context, sourceContext SourceContext) ([]*Skill, error) {
	if err := s.lifetime.checkOpen(); err != nil {
		return nil, err
	}
	all, err := s.lifetime.sources[0].Skills(ctx, sourceContext)
	if err != nil {
		return nil, err
	}
	filtered := make([]*Skill, 0, len(all))
	for _, skill := range all {
		if s.filter(ctx, skill, sourceContext) {
			filtered = append(filtered, skill)
		} else {
			s.logger.DebugContext(ctx, "Skill excluded by filter predicate", "skillName", skill.Frontmatter.Name)
		}
	}
	return filtered, nil
}

// Close idempotently closes the wrapped source if it implements [io.Closer].
// Do not overlap Close with Skills calls.
func (s *FilteringSource) Close() error { return s.lifetime.close() }

// DeduplicatingSource keeps the first occurrence of each case-insensitive skill
// name, preserving source order and logging duplicates.
type DeduplicatingSource struct {
	lifetime *sourceLifetime
	logger   *slog.Logger
}

// NewDeduplicatingSource creates a deduplicator and takes responsibility for
// closing the wrapped source if it implements [io.Closer]. Nil logger discards
// diagnostics. It panics if source is nil.
func NewDeduplicatingSource(source Source, logger *slog.Logger) *DeduplicatingSource {
	return &DeduplicatingSource{lifetime: newSourceLifetime(source), logger: sourceLogger(logger)}
}

// Skills returns the first skill for each name without modifying the inner list.
func (s *DeduplicatingSource) Skills(ctx context.Context, sourceContext SourceContext) ([]*Skill, error) {
	if err := s.lifetime.checkOpen(); err != nil {
		return nil, err
	}
	all, err := s.lifetime.sources[0].Skills(ctx, sourceContext)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(all))
	result := make([]*Skill, 0, len(all))
	for _, skill := range all {
		key := strings.ToLower(skill.Frontmatter.Name)
		if seen[key] {
			s.logger.WarnContext(ctx, "Duplicate skill name: subsequent skill skipped in favor of first occurrence", "skillName", skill.Frontmatter.Name)
			continue
		}
		seen[key] = true
		result = append(result, skill)
	}
	return result, nil
}

// Close idempotently closes the wrapped source if it implements [io.Closer].
// Do not overlap Close with Skills calls.
func (s *DeduplicatingSource) Close() error { return s.lifetime.close() }

func sourceLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return logger
}

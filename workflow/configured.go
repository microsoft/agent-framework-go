// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"fmt"
)

type identifier interface {
	ID() string
}

type Config struct {
	ID string
}

type ConfigOf[O any] struct {
	Config

	Options *O
}

// Configured represents a preconfigured, lazy-instantiatable instance of T.
type Configured[T any] struct {
	ID  string
	Raw any
	New func(Config, string) (T, error)
}

func (c *Configured[T]) Configuration() Config {
	return Config{
		ID: c.ID,
	}
}

func (c *Configured[T]) NewBound(runID string) (T, error) {
	return c.New(c.Configuration(), runID)
}

// ConfiguredOf represents a preconfigured, lazy-instantiatable instance of T
// that can be created with options of type O.
type ConfiguredOf[T, O any] struct {
	ID      string
	Raw     any
	Options *O
	New     func(ConfigOf[O], string) (T, error)
}

func (c *ConfiguredOf[T, O]) Configuration() ConfigOf[O] {
	return ConfigOf[O]{
		Config: Config{
			ID: c.ID,
		},
		Options: c.Options,
	}
}

func (c *ConfiguredOf[T, O]) Memoize() Configured[T] {
	return Configured[T]{
		ID:  c.ID,
		New: c.createValidatingMemoizedFunc(),
	}
}

func (c *ConfiguredOf[T, O]) NewBound(runID string) (T, error) {
	return c.createValidatingMemoizedFunc()(Config{ID: c.ID}, runID)
}

func (c *ConfiguredOf[T, O]) createValidatingMemoizedFunc() func(Config, string) (T, error) {
	return func(config Config, runID string) (s T, err error) {
		if c.ID != config.ID {
			return s, fmt.Errorf("requested instance ID %q does not match configured ID %q", config.ID, c.ID)
		}
		s, err = c.New(c.Configuration(), runID)
		if err != nil {
			return s, err
		}
		if s, ok := any(s).(identifier); ok && c.ID != "" {
			if s.ID() != c.ID {
				return s.(T), fmt.Errorf("created instance ID %q does not match configured ID %q", s.ID(), c.ID)
			}
		}
		return s, nil
	}
}

// NewConfiguredFromInstance creates a Configured[T] from an existing instance of T.
func NewConfiguredFromInstance[T any](subject T, id string, raw any) (*Configured[T], error) {
	if subject, ok := any(subject).(identifier); ok {
		if id != "" && subject.ID() != id {
			return nil, fmt.Errorf("provided ID %q does not match the subjects ID %q", id, subject.ID())
		}
	} else if id == "" {
		return nil, errors.New("ID must be provided when the subject is not identifiable")
	}

	if raw == nil {
		raw = subject
	}
	return &Configured[T]{
		ID:  id,
		Raw: raw,
		New: func(Config, string) (T, error) {
			return subject, nil
		},
	}, nil
}

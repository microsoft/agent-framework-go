// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"encoding/json"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
)

type SessionCache[T any] struct {
	CheckpointIndex []workflow.CheckpointInfo
	Cache           map[workflow.CheckpointInfo]T
}

// sessionCacheEntry is the JSON-friendly representation of a Cache entry,
// since JSON object keys cannot represent a CheckpointInfo.
type sessionCacheEntry[T any] struct {
	Key   workflow.CheckpointInfo
	Value T
}

type sessionCacheJSON[T any] struct {
	CheckpointIndex []workflow.CheckpointInfo
	Cache           []sessionCacheEntry[T]
}

// MarshalJSON implements [json.Marshaler] for SessionCache.
func (c *SessionCache[T]) MarshalJSON() ([]byte, error) {
	var entries []sessionCacheEntry[T]
	for _, info := range c.CheckpointIndex {
		if value, ok := c.Cache[info]; ok {
			entries = append(entries, sessionCacheEntry[T]{Key: info, Value: value})
		}
	}
	return json.Marshal(sessionCacheJSON[T]{
		CheckpointIndex: c.CheckpointIndex,
		Cache:           entries,
	})
}

// UnmarshalJSON implements [json.Unmarshaler] for SessionCache.
func (c *SessionCache[T]) UnmarshalJSON(data []byte) error {
	var v sessionCacheJSON[T]
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var cache map[workflow.CheckpointInfo]T
	if len(v.Cache) > 0 {
		cache = make(map[workflow.CheckpointInfo]T, len(v.Cache))
		for _, entry := range v.Cache {
			cache[entry.Key] = entry.Value
		}
	}
	c.CheckpointIndex = v.CheckpointIndex
	c.Cache = cache
	return nil
}

func (c *SessionCache[T]) IsInIndex(info workflow.CheckpointInfo) bool {
	return slices.Contains(c.CheckpointIndex, info)
}

func (c *SessionCache[T]) Get(info workflow.CheckpointInfo) (T, bool) {
	value, ok := c.Cache[info]
	return value, ok
}

func (c *SessionCache[T]) Add(sessionID string, value T) workflow.CheckpointInfo {
	var key workflow.CheckpointInfo
	for {
		key = workflow.NewCheckpointInfo(sessionID)
		if c.AddCheckpointInfo(key, value) {
			break
		}
	}
	return key
}

func (c *SessionCache[T]) AddCheckpointInfo(info workflow.CheckpointInfo, value T) bool {
	if c.IsInIndex(info) {
		return false
	}
	c.CheckpointIndex = append(c.CheckpointIndex, info)
	if c.Cache == nil {
		c.Cache = make(map[workflow.CheckpointInfo]T)
	}
	c.Cache[info] = value
	return true
}

func (c *SessionCache[T]) HasCheckpoints() bool {
	return len(c.CheckpointIndex) > 0
}

func (c *SessionCache[T]) LastCheckpointInfo() (workflow.CheckpointInfo, bool) {
	if c.HasCheckpoints() {
		return c.CheckpointIndex[len(c.CheckpointIndex)-1], true
	}
	return workflow.CheckpointInfo{}, false
}

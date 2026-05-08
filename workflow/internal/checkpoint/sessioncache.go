// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
)

type SessionCache[T any] struct {
	CheckpointIndex []workflow.CheckpointInfo
	Cache           map[workflow.CheckpointInfo]T
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

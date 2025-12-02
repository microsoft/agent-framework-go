// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
)

type checkpointingHandle interface {
	Checkpoints() []CheckpointInfo
	RestoreCheckpoint(context.Context, CheckpointInfo) error
}

type Checkpointed[T any] struct {
	run    T
	runner checkpointingHandle
}

func NewCheckpointed[T any](run T, runner checkpointingHandle) *Checkpointed[T] {
	return &Checkpointed[T]{
		run:    run,
		runner: runner,
	}
}

func (c *Checkpointed[T]) Run() T {
	return c.run
}

func (c *Checkpointed[T]) Checkpoints() []CheckpointInfo {
	return c.runner.Checkpoints()
}

func (c *Checkpointed[T]) LastCheckpoint() (CheckpointInfo, bool) {
	checkpoints := c.runner.Checkpoints()
	if len(checkpoints) == 0 {
		return CheckpointInfo{}, false
	}

	return checkpoints[len(checkpoints)-1], true
}

func (c *Checkpointed[T]) RestoreCheckpoint(ctx context.Context, ch CheckpointInfo) error {
	return c.runner.RestoreCheckpoint(ctx, ch)
}

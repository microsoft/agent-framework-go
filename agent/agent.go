// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
)

type Identity struct {
	id          string
	name        string
	description string
}

func NewIdentity(id, name, description string) Identity {
	if id == "" {
		id = uuid.NewString()
	}
	return Identity{
		id:          id,
		name:        name,
		description: description,
	}
}

func (iden Identity) ID() string {
	return iden.id
}

func (iden Identity) Name() string {
	return iden.name
}

func (iden Identity) Description() string {
	return iden.description
}

type Agent interface {
	Identity() Identity

	Run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

	NewThread(ctx context.Context, options ...agentopt.NewThreadOption) (memory.Thread, error)
	UnmarshalThread(data []byte) (memory.Thread, error)
}

type StructuredOutputAgent interface {
	Agent

	RunOf(ctx context.Context, v any, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

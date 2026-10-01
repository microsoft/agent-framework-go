// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func TestGetService(t *testing.T) {
	a := agent.New(agent.ProviderConfig{
		Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(func(*agent.ResponseUpdate, error) bool) {}
		},
	}, agent.Config{})

	if got := a.GetService(reflect.TypeFor[*agent.Agent](), nil); got != a {
		t.Fatalf("GetService(*Agent, nil) = %v, want the agent", got)
	}
	if got := a.GetService(reflect.TypeFor[agent.Agent](), nil); got != nil {
		t.Fatalf("GetService(Agent, nil) = %v, want nil", got)
	}
	if got := a.GetService(reflect.TypeFor[*agent.Agent](), "key"); got != nil {
		t.Fatalf("GetService(*Agent, key) = %v, want nil", got)
	}

	got, ok := agent.GetService[*agent.Agent](a, nil)
	if !ok || got != a {
		t.Fatalf("GetService[*Agent] = (%v, %v), want (agent, true)", got, ok)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("GetService(nil, nil) did not panic")
		}
	}()
	a.GetService(nil, nil)
}

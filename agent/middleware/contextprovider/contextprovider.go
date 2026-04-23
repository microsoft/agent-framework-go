// Copyright (c) Microsoft. All rights reserved.

package contextprovider

import (
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/internal/middleware/contextprovider"
	"github.com/microsoft/agent-framework-go/memory"
)

// New returns a middleware that invokes the provided context providers in order
// before the wrapped run and persists them in reverse order after the run.
func New(providers ...*memory.ContextProvider) agent.Middleware {
	return contextprovider.New(providers...)
}

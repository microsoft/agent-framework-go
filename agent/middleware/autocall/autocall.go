// Copyright (c) Microsoft. All rights reserved.

package autocall

import (
	"log/slog"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/internal/middleware/autocall"
	"github.com/microsoft/agent-framework-go/tool"
)

type Config struct {
	Logger                             *slog.Logger
	LogSensitiveData                   bool
	AdditionalTools                    []tool.Tool
	IncludeDetailedErrors              bool
	TerminateOnUnknownCalls            bool
	AllowConcurrentInvocations         bool
	MaximumConsecutiveErrorsPerRequest int
	MaximumIterationsPerRequest        int // Default: 40
	NewID                              func() string
}

// New creates a new function-invoking chat client that wraps the provided client.
func New(cfg Config) agent.Middleware {
	return autocall.New(autocall.Config(cfg))
}

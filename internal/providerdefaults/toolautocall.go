// Copyright (c) Microsoft. All rights reserved.

package providerdefaults

import (
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
)

// ToolAutoCallMiddleware builds the default provider-installed tool auto-call
// middleware from shared agent config. It returns nil when auto-call is
// disabled.
func ToolAutoCallMiddleware(cfg agent.Config) agent.Middleware {
	if cfg.DisableFuncAutoCall {
		return nil
	}
	return toolautocall.New(toolautocall.Config{
		Logger:                     cfg.Logger,
		LogSensitiveData:           cfg.LogSensitiveData,
		AllowConcurrentInvocations: cfg.AllowConcurrentInvocations,
	})
}

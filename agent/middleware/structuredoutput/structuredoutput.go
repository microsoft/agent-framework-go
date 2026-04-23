// Copyright (c) Microsoft. All rights reserved.

package structuredoutput

import (
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/internal/middleware/structuredoutput"
	"github.com/microsoft/agent-framework-go/format"
)

type Config struct {
	Format    func(v any) (format.Format, error)
	Unmarshal func(format format.Format, data []byte, v any) error
}

func New(cfg Config) agent.Middleware {
	return structuredoutput.New(structuredoutput.Config(cfg))
}

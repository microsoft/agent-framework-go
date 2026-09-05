// Copyright (c) Microsoft. All rights reserved.

package harness_test

import (
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness"
)

func ExampleConfigure() {
	cfg := harness.Configure(agent.Config{Name: "ResearchAgent"}, harness.Config{})

	fmt.Println(len(cfg.ContextProviders), len(cfg.Middlewares), len(cfg.RunOptions))
	// Output: 2 1 1
}

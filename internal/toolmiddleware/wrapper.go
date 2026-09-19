// Copyright (c) Microsoft. All rights reserved.

// Package toolmiddleware shares tool wrappers between agent middleware and tool execution.
package toolmiddleware

import "github.com/microsoft/agent-framework-go/tool"

// Wrapper is an internal run option for wrapping tools registered outside agent options.
// Tools already present in agent options are wrapped by the originating middleware.
type Wrapper func(tool.FuncTool) tool.FuncTool

// MAFValue implements agent.Option without depending on the agent package.
func (w Wrapper) MAFValue() any { return w }

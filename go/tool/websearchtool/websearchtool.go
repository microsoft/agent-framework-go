// Copyright (c) Microsoft. All rights reserved.

package websearchtool

import "github.com/microsoft/agent-framework/go/tool"

var _ tool.Tool = (*HostedWebSearch)(nil)

// HostedWebSearch represents a hosted tool that can be specified to an
// AI service to enable it to perform web searches.
//
// This tool does not itself implement web searches. It is a marker that can
// be used to inform a service that the service is allowed to perform web
// searches if the service is capable of doing so.
type HostedWebSearch struct {
	Description          string
	AdditionalProperties map[string]any
}

func (t *HostedWebSearch) ToolInfo() (name string, description string) {
	return "web_search", t.Description
}

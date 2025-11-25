// Copyright (c) Microsoft. All rights reserved.

package hostedtool

import "github.com/microsoft/agent-framework/go/tool"

var _ tool.Tool = (*WebSearch)(nil)

// WebSearch represents a hosted tool that can be specified to an
// AI service to enable it to perform web searches.
//
// This tool does not itself implement web searches. It is a marker that can
// be used to inform a service that the service is allowed to perform web
// searches if the service is capable of doing so.
type WebSearch struct {
	AdditionalProperties map[string]any
}

func (t *WebSearch) Name() string {
	return "web_search"
}

func (t *WebSearch) Description() string {
	return ""
}

type FileSearch struct {
	AdditionalProperties map[string]any
}

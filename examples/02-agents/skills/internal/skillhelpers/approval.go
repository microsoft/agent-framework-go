// Copyright (c) Microsoft. All rights reserved.

package skillhelpers

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// RunWithApprovals runs a prompt and resumes pending tool approvals in the same
// session until the agent finishes. approve is called for each pending request;
// both approvals and rejections are sent back to the agent.
func RunWithApprovals(ctx context.Context, a *agent.Agent, session *agent.Session, prompt string, approve func(*message.ToolApprovalRequestContent) bool) (*agent.Response, error) {
	response, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
	if err != nil {
		return nil, err
	}
	for {
		var replies []message.Content
		for content := range response.Contents() {
			if request, ok := content.(*message.ToolApprovalRequestContent); ok {
				replies = append(replies, request.CreateResponse(approve(request), ""))
			}
		}
		if len(replies) == 0 {
			return response, nil
		}
		response, err = a.RunMessage(ctx, message.New(replies...), agent.WithSession(session)).Collect()
		if err != nil {
			return nil, err
		}
	}
}

// Copyright (c) Microsoft. All rights reserved.

// This sample discovers a skill from a trusted, in-process MCP server.
// Skill instructions and reference resources are fetched only when requested.

package main

import (
	"context"
	"errors"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/mcpskills"
	"github.com/microsoft/agent-framework-go/examples/02-agents/skills/internal/skillhelpers"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var logger = demo.NewLogger(
	"MCP Skills",
	"Discovering a remote skill and lazily reading its instructions and reference data.",
	"Model", demo.FoundryModel,
)

func main() {
	if err := run(context.Background()); err != nil {
		demo.Panic(err)
	}
}

func run(ctx context.Context) (err error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "trusted-skills", Version: "1.0.0"}, nil)
	for _, resource := range []struct {
		uri       string
		mediaType string
		text      string
	}{
		{
			uri:       "skill://index.json",
			mediaType: "application/json",
			text: `{"skills":[{"name":"unit-converter","type":"skill-md",
"description":"Convert miles and kilometers using a reference table.",
"url":"skill://unit-converter/SKILL.md"}]}`,
		},
		{
			uri:       "skill://unit-converter/SKILL.md",
			mediaType: "text/markdown",
			text: `---
name: unit-converter
description: Convert miles and kilometers using a reference table.
---
Read references/conversion-table.md with read_skill_resource to find the
conversion factor, then multiply the input by that factor.`,
		},
		{
			uri:       "skill://unit-converter/references/conversion-table.md",
			mediaType: "text/markdown",
			text:      "Miles to kilometers: multiply by 1.60934.\nKilometers to miles: multiply by 0.621371.",
		},
	} {
		server.AddResource(&mcp.Resource{URI: resource.uri, Name: resource.uri, MIMEType: resource.mediaType},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
					URI: resource.uri, MIMEType: resource.mediaType, Text: resource.text,
				}}}, nil
			})
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, serverSession.Close()) }()
	clientSession, err := mcptool.Connect(ctx, clientTransport)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, clientSession.Close()) }()

	skillsProvider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{mcpskills.NewSource(mcpskills.Config{Session: clientSession})},
		OwnsSources: true,
	})
	defer func() { err = errors.Join(err, skillsProvider.Close()) }()
	converter := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "Use the available skills to answer unit-conversion questions.",
			Config: agent.Config{
				Name: "MCPSkillsAgent",
				Middlewares: []agent.Middleware{logger, toolapproval.New(toolapproval.Config{
					AutoApprovalRules: []toolapproval.AutoApprovalRule{skills.ReadOnlyToolsAutoApprovalRule},
				})},
				ContextProviders: []agent.ContextProvider{skillsProvider},
			},
		},
	)
	session, err := converter.CreateSession(ctx)
	if err != nil {
		return err
	}
	response, err := skillhelpers.RunWithApprovals(ctx, converter, session,
		"Using the unit-converter skill, how many kilometers is a marathon (26.2 miles)?",
		demo.UserInputRequest)
	if err != nil {
		return err
	}
	demo.Response(response, nil)
	return nil
}

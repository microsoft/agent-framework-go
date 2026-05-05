// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/a2aagent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
	cardURL    = cmp.Or(os.Getenv("A2A_AGENT_HOST"), "http://127.0.0.1:5000")
)

var invalidToolNameChars = regexp.MustCompile(`[^0-9A-Za-z]+`)

var logger = demo.NewLogger(
	"A2A Agent As Function Tools",
	"Advertises the remote A2A agent's skills as function tools for a host agent.",
	"Model", deployment,
	"Agent", cardURL,
)

func main() {
	ctx := context.Background()
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	card, err := agentcard.DefaultResolver.Resolve(ctx, cardURL)
	if err != nil {
		demo.Panicf("failed to resolve agent card: %v", err)
	}
	if len(card.Skills) == 0 {
		demo.Panic("resolved agent card does not advertise any skills")
	}

	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		demo.Panicf("failed to create A2A client: %v", err)
	}

	remoteAgent := a2aagent.New(
		client,
		a2aagent.Config{
			Config: agent.Config{
				Name:        cmp.Or(card.Name, "RemoteA2AAgent"),
				Description: card.Description,
			},
		},
	)

	hostAgent := openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: "You are a helpful assistant that helps people with travel planning.",
			Config: agent.Config{
				Name:        "TravelPlanner",
				Middlewares: []agent.Middleware{logger},
				Tools:       createSkillTools(remoteAgent, card.Skills),
			},
		},
	)

	resp, err := hostAgent.RunText(
		ctx,
		"Plan a route from '1600 Amphitheatre Parkway, Mountain View, CA' to 'San Francisco International Airport' avoiding tolls.",
	).Collect()
	demo.Response(resp, err)
}

func createSkillTools(remoteAgent *agent.Agent, skills []a2a.AgentSkill) []tool.Tool {
	tools := make([]tool.Tool, 0, len(skills))
	for _, skill := range skills {
		skill := skill
		tools = append(tools, functool.MustNew(functool.Config{
			Name:        sanitizeToolName(cmp.Or(skill.Name, skill.ID, "a2a_skill")),
			Description: formatSkillDescription(skill),
		}, func(ctx tool.Context, query string) (string, error) {
			resp, err := remoteAgent.RunText(ctx, skillPrompt(skill, query)).Collect()
			if err != nil {
				return "", err
			}
			return resp.String(), nil
		}))
	}
	return tools
}

func sanitizeToolName(name string) string {
	name = invalidToolNameChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	name = strings.ToLower(name)
	if name == "" {
		return "a2a_skill"
	}
	return name
}

func formatSkillDescription(skill a2a.AgentSkill) string {
	lines := make([]string, 0, 6)
	if skill.Description != "" {
		lines = append(lines, skill.Description)
	}
	if skill.ID != "" {
		lines = append(lines, fmt.Sprintf("Skill ID: %s", skill.ID))
	}
	if len(skill.Tags) > 0 {
		lines = append(lines, fmt.Sprintf("Tags: %s", strings.Join(skill.Tags, ", ")))
	}
	if len(skill.Examples) > 0 {
		lines = append(lines, fmt.Sprintf("Examples: %s", strings.Join(skill.Examples, " | ")))
	}
	if len(skill.InputModes) > 0 {
		lines = append(lines, fmt.Sprintf("Input modes: %s", strings.Join(skill.InputModes, ", ")))
	}
	if len(skill.OutputModes) > 0 {
		lines = append(lines, fmt.Sprintf("Output modes: %s", strings.Join(skill.OutputModes, ", ")))
	}
	if len(lines) == 0 {
		return "Invoke the remote A2A skill with a user query."
	}
	return strings.Join(lines, "\n")
}

func skillPrompt(skill a2a.AgentSkill, query string) string {
	if skill.Name == "" {
		return query
	}
	return fmt.Sprintf("Use the %q skill for this request:\n%s", skill.Name, query)
}

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/openai"
	"github.com/microsoft/agent-framework/go/pkg/workflow"
)

// AgentExecutor wraps a chat agent to be used as a workflow executor.
type AgentExecutor struct {
	id    string
	name  string
	agent *agent.Agent
}

func NewAgentExecutor(id, name string, agnt *agent.Agent) *AgentExecutor {
	return &AgentExecutor{id: id, name: name, agent: agnt}
}

func (e *AgentExecutor) ID() string {
	return e.id
}

func (e *AgentExecutor) Name() string {
	return e.name
}

func (e *AgentExecutor) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	// Extract message from input
	message, ok := input["message"].(string)
	if !ok {
		return nil, fmt.Errorf("input must contain 'message' field")
	}

	log.Printf("Agent %s processing: %s\n", e.name, message)

	// Run the agent
	resp, err := e.agent.Run(ctx, nil, nil, agent.NewMessage(agent.RoleUser, &agent.TextContent{Text: message}))
	if err != nil {
		return nil, fmt.Errorf("agent execution failed: %w", err)
	}

	// Return output
	output := make(map[string]interface{})
	output["response"] = resp.Text()
	output["message"] = message

	return output, nil
}

func main() {
	// Create OpenAI chat client
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create agents for different roles
	client := openai.NewChatClient(openai.AgentConfig{
		Model:  "gpt-4o-mini",
		APIKey: apiKey,
	})

	// Create executor for each agent
	writerExecutor := NewAgentExecutor("writer", "Story Writer", agent.New(client, &agent.Config{
		Name:               "Story Writer",
		SystemInstructions: "You are a creative writer. Write a short story based on the input in 2-3 sentences.",
	}, nil))
	editorExecutor := NewAgentExecutor("editor", "Story Editor", agent.New(client, &agent.Config{
		Name:               "Story Editor",
		SystemInstructions: "You are an editor. Review and improve the following text. Make it more concise and impactful.",
	}, nil))

	// Build workflow
	builder := workflow.NewWorkflowBuilder().
		SetName("Agent Pipeline Workflow").
		SetDescription("Demonstrates using agents as workflow executors in a pipeline").
		AddExecutor(writerExecutor.ID(), writerExecutor).
		AddExecutor(editorExecutor.ID(), editorExecutor).
		AddEdge(workflow.Edge{From: "writer", To: "editor"}).
		SetEntryPoint("writer")

	wf, err := builder.Build()
	if err != nil {
		log.Fatalf("Failed to build workflow: %v\n", err)
	}

	log.Printf("Created workflow: %s\n", wf.Name())
	log.Printf("Description: %s\n\n", wf.Description())

	// Execute workflow
	ctx := context.Background()
	input := map[string]interface{}{
		"message": "A robot discovers it can feel emotions",
	}

	log.Println("=== Executing Agent Pipeline ===")
	result, err := wf.Execute(ctx, input)
	if err != nil {
		log.Fatalf("Workflow execution failed: %v\n", err)
	}

	log.Println("\n=== Final Result ===")
	log.Printf("Original topic: %v\n", result["message"])
	log.Printf("Final output: %v\n", result["response"])
}

package main

import (
	"context"
	"log"

	"github.com/microsoft/agent-framework/go/pkg/workflow"
)

// RoutingExecutor routes requests based on priority
type RoutingExecutor struct {
	id   string
	name string
}

func NewRoutingExecutor(id, name string) *RoutingExecutor {
	return &RoutingExecutor{id: id, name: name}
}

func (e *RoutingExecutor) ID() string {
	return e.id
}

func (e *RoutingExecutor) Name() string {
	return e.name
}

func (e *RoutingExecutor) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	log.Printf("Executing %s\n", e.name)

	output := make(map[string]interface{})
	for k, v := range input {
		output[k] = v
	}

	// Determine priority based on score
	score, ok := input["score"].(float64)
	if !ok {
		score = 50.0
	}

	if score > 70 {
		output["priority"] = "high"
		output["router_message"] = "Routing to high-priority handler"
	} else {
		output["priority"] = "low"
		output["router_message"] = "Routing to low-priority handler"
	}

	return output, nil
}

// PriorityHandler handles different priority levels
type PriorityHandler struct {
	id       string
	name     string
	priority string
}

func NewPriorityHandler(id, name, priority string) *PriorityHandler {
	return &PriorityHandler{id: id, name: name, priority: priority}
}

func (e *PriorityHandler) ID() string {
	return e.id
}

func (e *PriorityHandler) Name() string {
	return e.name
}

func (e *PriorityHandler) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	log.Printf("Executing %s\n", e.name)

	output := make(map[string]interface{})
	for k, v := range input {
		output[k] = v
	}

	if e.priority == "high" {
		output["handler_result"] = "URGENT: Priority processing completed"
		output["sla"] = "1 hour"
	} else {
		output["handler_result"] = "Standard processing completed"
		output["sla"] = "24 hours"
	}

	return output, nil
}

func main() {
	// Create executors
	router := NewRoutingExecutor("router", "Request Router")
	highHandler := NewPriorityHandler("high", "High Priority Handler", "high")
	lowHandler := NewPriorityHandler("low", "Low Priority Handler", "low")

	// Build workflow with conditional edges
	builder := workflow.NewWorkflowBuilder().
		SetName("Conditional Routing Workflow").
		SetDescription("Routes to different handlers based on priority score").
		AddExecutor(router.ID(), router).
		AddExecutor(highHandler.ID(), highHandler).
		AddExecutor(lowHandler.ID(), lowHandler).
		SetEntryPoint(router.ID())

	// Add conditional edges based on priority
	builder.AddEdge(workflow.Edge{
		From: "router",
		To:   "high",
		Condition: func(output map[string]interface{}) bool {
			priority, ok := output["priority"].(string)
			return ok && priority == "high"
		},
	})

	builder.AddEdge(workflow.Edge{
		From: "router",
		To:   "low",
		Condition: func(output map[string]interface{}) bool {
			priority, ok := output["priority"].(string)
			return ok && priority == "low"
		},
	})

	wf, err := builder.Build()
	if err != nil {
		log.Fatalf("Failed to build workflow: %v\n", err)
	}

	log.Printf("Created workflow: %s\n\n", wf.Name())

	// Test case 1: High priority
	log.Println("=== Test Case 1: High Priority Score (85) ===")
	ctx := context.Background()
	input1 := map[string]interface{}{
		"score":       85.0,
		"request_id":  "REQ-001",
		"description": "Critical bug fix",
	}

	result1, err := wf.Execute(ctx, input1)
	if err != nil {
		log.Fatalf("Workflow execution failed: %v\n", err)
	}

	log.Printf("Priority: %v\n", result1["priority"])
	log.Printf("Result: %v\n", result1["handler_result"])
	log.Printf("SLA: %v\n\n", result1["sla"])

	// Test case 2: Low priority
	log.Println("=== Test Case 2: Low Priority Score (35) ===")
	input2 := map[string]interface{}{
		"score":       35.0,
		"request_id":  "REQ-002",
		"description": "Feature enhancement request",
	}

	result2, err := wf.Execute(ctx, input2)
	if err != nil {
		log.Fatalf("Workflow execution failed: %v\n", err)
	}

	log.Printf("Priority: %v\n", result2["priority"])
	log.Printf("Result: %v\n", result2["handler_result"])
	log.Printf("SLA: %v\n", result2["sla"])
}

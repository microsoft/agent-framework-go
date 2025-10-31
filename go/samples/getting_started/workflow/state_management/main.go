package main

import (
	"context"
	"log"

	"github.com/microsoft/agent-framework/go/workflow"
)

// DataProcessingExecutor processes data and manages state
type DataProcessingExecutor struct {
	id   string
	name string
	step int
}

func NewDataProcessingExecutor(id, name string, step int) *DataProcessingExecutor {
	return &DataProcessingExecutor{id: id, name: name, step: step}
}

func (e *DataProcessingExecutor) ID() string {
	return e.id
}

func (e *DataProcessingExecutor) Name() string {
	return e.name
}

func (e *DataProcessingExecutor) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
	log.Printf("Executing %s\n", e.name)

	// Get workflow context
	wfCtx, ok := workflow.GetWorkflowContext(ctx)
	if !ok {
		log.Println("Warning: Could not get workflow context")
	}

	output := make(map[string]interface{})
	for k, v := range input {
		output[k] = v
	}

	// Get previous steps' data from shared state
	var stepResults []string
	if wfCtx != nil {
		if prev, ok := wfCtx.GetSharedState("step_results"); ok {
			if prevSteps, ok := prev.([]string); ok {
				stepResults = prevSteps
			}
		}
	}

	// Process data
	result := ""
	switch e.step {
	case 1:
		result = "Data extraction completed"
		if val, ok := input["raw_data"].(string); ok {
			result = "Extracted: " + val
		}
	case 2:
		result = "Data transformation completed"
		if val, ok := input["extracted_data"].(string); ok {
			result = "Transformed: " + val + " (uppercase)"
		}
	case 3:
		result = "Data validation completed"
		result = "All validations passed"
	}

	// Store step result in shared state
	stepResults = append(stepResults, result)
	if wfCtx != nil {
		wfCtx.SetSharedState("step_results", stepResults)
		log.Printf("Stored result in shared state. Total steps completed: %d\n", len(stepResults))
	}

	output["step_result"] = result
	output["step_number"] = e.step

	return output, nil
}

func main() {
	// Create executors
	extractExecutor := NewDataProcessingExecutor("extract", "Data Extraction", 1)
	transformExecutor := NewDataProcessingExecutor("transform", "Data Transformation", 2)
	validateExecutor := NewDataProcessingExecutor("validate", "Data Validation", 3)

	// Build workflow
	builder := workflow.NewWorkflowBuilder().
		SetName("State Management Workflow").
		SetDescription("Demonstrates sharing state across workflow steps").
		AddExecutor(extractExecutor.ID(), extractExecutor).
		AddExecutor(transformExecutor.ID(), transformExecutor).
		AddExecutor(validateExecutor.ID(), validateExecutor).
		AddEdge(workflow.Edge{From: "extract", To: "transform"}).
		AddEdge(workflow.Edge{From: "transform", To: "validate"}).
		SetEntryPoint("extract")

	wf, err := builder.Build()
	if err != nil {
		log.Fatalf("Failed to build workflow: %v\n", err)
	}

	log.Printf("Created workflow: %s\n", wf.Name())
	log.Printf("Description: %s\n\n", wf.Description())

	// Execute workflow
	ctx := context.Background()
	input := map[string]interface{}{
		"raw_data":       "hello world",
		"extracted_data": "hello",
	}

	log.Println("=== Executing Workflow with State Management ===")
	result, err := wf.Execute(ctx, input)
	if err != nil {
		log.Fatalf("Workflow execution failed: %v\n", err)
	}

	log.Println("\n=== Final Result ===")
	log.Printf("Final step result: %v\n", result["step_result"])
	log.Printf("Final step number: %v\n", result["step_number"])
}

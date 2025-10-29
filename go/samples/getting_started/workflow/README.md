# Workflow Samples

This directory contains essential workflow examples demonstrating key patterns and features.

## Samples Overview

### 1. **conditional_routing** - Conditional Edge Routing
Demonstrates branching logic based on input conditions.

**Key Features:**
- Conditional edges with predicates
- Dynamic routing based on scores/priorities
- Multiple execution paths

**Run:**
```bash
cd conditional_routing
go run main.go
```

**What it shows:**
- How to use edge conditions to route to different executors
- Practical example: routing requests to high/low priority handlers based on score

---

### 2. **state_management** - Shared State Management
Demonstrates how to manage and share state across workflow steps.

**Key Features:**
- WorkflowContext state sharing
- Multi-step sequential processing
- State accumulation across steps

**Run:**
```bash
cd state_management
go run main.go
```

**What it shows:**
- How to use `WorkflowContext` to store/retrieve shared state
- How multiple executors can collaborate through shared state
- Practical example: multi-stage data processing pipeline

---

### 3. **agents_in_workflow** - Agents as Executors
Demonstrates integrating AI agents as workflow executors.

**Key Features:**
- Using OpenAI chat agents in workflows
- Agent-based processing steps
- Pipeline orchestration with agents

**Run:**
```bash
export OPENAI_API_KEY=your-api-key
cd agents_in_workflow
go run main.go
```

**What it shows:**
- How to wrap agents as workflow executors
- Creating multi-agent pipelines
- Practical example: writer → editor pipeline

---

## Architecture Patterns

### Sequential Pipeline
```
Executor A → Executor B → Executor C
```
Basic linear flow where output of one becomes input of next.

### Conditional Routing
```
        ├─→ High Priority Handler
Router ─┤
        └─→ Low Priority Handler
```
Route to different executors based on conditions.

### State Management
All executors share access to:
- `WorkflowContext.SharedState`: Shared across all executors
- `WorkflowContext.State`: Per-executor state
- Input/Output data passed between executors

## Creating Your Own Executor

```go
type MyExecutor struct {
    id   string
    name string
}

func (e *MyExecutor) ID() string {
    return e.id
}

func (e *MyExecutor) Name() string {
    return e.name
}

func (e *MyExecutor) Execute(ctx context.Context, input map[string]interface{}) (map[string]interface{}, error) {
    // Get workflow context if needed
    wfCtx, ok := workflow.GetWorkflowContext(ctx)
    
    // Process input
    output := make(map[string]interface{})
    // ... implementation ...
    
    return output, nil
}
```

## Building Workflows

```go
builder := workflow.NewWorkflowBuilder().
    SetName("My Workflow").
    SetDescription("What it does").
    AddExecutor("step1_id", executor1).
    AddExecutor("step2_id", executor2).
    AddEdge(workflow.Edge{From: "step1_id", To: "step2_id"}).
    SetEntryPoint("step1_id")

wf, err := builder.Build()
if err != nil {
    log.Fatal(err)
}

// Execute
result, err := wf.Execute(context.Background(), input)
```

## Next Steps

- Fan-out/Fan-in workflows (parallel execution)
- Sub-workflows (nested workflows)
- Loop workflows (cyclic patterns)
- Advanced state management

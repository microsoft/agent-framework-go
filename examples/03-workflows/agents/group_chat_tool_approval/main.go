// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var logger = demo.NewLogger(
	"Group Chat Tool Approval",
	"This sample runs a QA and DevOps group chat where production deployment requires approval.",
	"Model", demo.FoundryModel,
)

type runTestsInput struct {
	TestSuite string `json:"testSuite"`
}

type createRollbackPlanInput struct {
	Version string `json:"version"`
}

type deployToProductionInput struct {
	Version    string `json:"version"`
	Components string `json:"components"`
}

var runTestsTool = functool.MustNew(functool.Config{
	Name:        "RunTests",
	Description: "Run automated tests for the application.",
}, func(_ context.Context, input runTestsInput) (string, error) {
	return fmt.Sprintf("Test suite %q completed: 47 passed, 0 failed, 0 skipped", input.TestSuite), nil
})

var checkStagingStatusTool = functool.MustNew(functool.Config{
	Name:        "CheckStagingStatus",
	Description: "Check the current status of the staging environment.",
}, func(context.Context, struct{}) (string, error) {
	return "Staging environment: Healthy, Version 2.3.0 deployed, All services running", nil
})

var createRollbackPlanTool = functool.MustNew(functool.Config{
	Name:        "CreateRollbackPlan",
	Description: "Create a rollback plan for the deployment.",
}, func(_ context.Context, input createRollbackPlanInput) (string, error) {
	return fmt.Sprintf("Rollback plan created for version %s: Automated rollback to v2.2.0 if health checks fail within 5 minutes", input.Version), nil
})

var deployToProductionTool = functool.MustNew(functool.Config{
	Name:        "DeployToProduction",
	Description: "Deploy specified components to production. Requires human approval.",
}, func(_ context.Context, input deployToProductionInput) (string, error) {
	return fmt.Sprintf("Production deployment complete: Version %s, Components: %s", input.Version, input.Components), nil
})

func main() {
	qaEngineer := newDeploymentAgent(
		"QAEngineer",
		"QA engineer who runs tests",
		"You are a QA engineer responsible for running tests before deployment. Run the appropriate test suites and report the results clearly in your response, including pass/fail counts.",
		runTestsTool,
	)
	devopsEngineer := newDeploymentAgent(
		"DevOpsEngineer",
		"DevOps engineer who handles deployments",
		"You are a DevOps engineer responsible for deployments. Call CheckStagingStatus, then CreateRollbackPlan, then DeployToProduction - in that order. Do not ask for confirmation before deploying; deployment approval is handled automatically by the system. After all tools complete, summarize each step and its result in your text response.",
		checkStagingStatusTool,
		createRollbackPlanTool,
		tool.ApprovalRequiredFunc(deployToProductionTool),
	)

	wf, err := agentworkflow.NewGroupChatWorkflowBuilder(newDeploymentGroupChatManager, qaEngineer, devopsEngineer).
		WithName("Software Deployment Group Chat").
		Build()
	if err != nil {
		demo.Panic(err)
	}

	demo.Assistant("Starting group chat workflow for software deployment...")
	demo.Assistantf("Agents: [%s, %s]", qaEngineer.Name(), devopsEngineer.Name())
	fmt.Println(strings.Repeat("-", 60))

	ctx := context.Background()
	run, err := inproc.Default.RunStreaming(ctx, wf, []*message.Message{
		message.NewText("We need to deploy version 2.4.0 to production. Please coordinate the deployment."),
	})
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		demo.Panic(err)
	}

	if err := watchDeploymentWorkflow(ctx, run); err != nil {
		demo.Panic(err)
	}
	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	demo.Assistant("Deployment workflow completed successfully!")
	demo.Assistant("All agents have finished their tasks.")
}

func newDeploymentAgent(name string, description string, instructions string, tools ...tool.Tool) *agent.Agent {
	return foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		demo.FoundryTokenCredential(),
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: instructions,
			Config: agent.Config{
				Name:        name,
				Description: description,
				Middlewares: []agent.Middleware{logger},
				Tools:       tools,
			},
		},
	)
}

func newDeploymentGroupChatManager(agents []*agent.Agent) *agentworkflow.GroupChatManager {
	manager := &deploymentGroupChatManager{agents: agents}
	return &agentworkflow.GroupChatManager{
		SelectNextAgent: manager.selectNextAgent,
		ShouldTerminate: manager.shouldTerminate,
		Reset:           manager.reset,
	}
}

type deploymentGroupChatManager struct {
	agents        []*agent.Agent
	selectedTurns int
}

func (m *deploymentGroupChatManager) selectNextAgent(_ context.Context, history []*message.Message) (*agent.Agent, error) {
	if len(history) == 0 {
		return nil, fmt.Errorf("conversation is empty; cannot select next speaker")
	}
	if m.selectedTurns == 0 {
		m.selectedTurns++
		return m.agentByName("QAEngineer")
	}
	m.selectedTurns++
	return m.agentByName("DevOpsEngineer")
}

func (m *deploymentGroupChatManager) shouldTerminate(_ context.Context, history []*message.Message, iterationCount int) (bool, error) {
	if iterationCount >= 2 && hasDeploymentSummary(history) {
		return true, nil
	}
	return iterationCount >= 4, nil
}

func (m *deploymentGroupChatManager) reset() {
	m.selectedTurns = 0
}

func (m *deploymentGroupChatManager) agentByName(name string) (*agent.Agent, error) {
	for _, currentAgent := range m.agents {
		if currentAgent.Name() == name {
			return currentAgent, nil
		}
	}
	return nil, fmt.Errorf("agent %q is not part of the deployment group chat", name)
}

func watchDeploymentWorkflow(ctx context.Context, run *inproc.StreamingRun) error {
	lastExecutorID := ""
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			return err
		}
		switch e := evt.(type) {
		case workflow.RequestInfoEvent:
			if err := approveToolRequest(ctx, run, e.Request); err != nil {
				return err
			}
		case workflow.OutputEvent:
			if update, ok := e.Output.(*agent.ResponseUpdate); ok {
				printAgentUpdate(e.ExecutorID, update, &lastExecutorID)
				continue
			}
			if transcript, ok := e.Output.([]*message.Message); ok {
				printTranscript(transcript)
			}
		case workflow.ErrorEvent:
			return e.Error
		case workflow.ExecutorFailedEvent:
			return fmt.Errorf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
	return nil
}

func approveToolRequest(ctx context.Context, run *inproc.StreamingRun, request *workflow.ExternalRequest) error {
	approvalRequest, ok := workflow.PortableValueAs[*message.ToolApprovalRequestContent](request.Data)
	if !ok {
		return fmt.Errorf("request %q did not contain ToolApprovalRequestContent, got %T", request.RequestID, request.Data.Any())
	}
	toolName, arguments := approvalToolCallDetails(approvalRequest.ToolCall)
	fmt.Println()
	demo.Assistantf("[APPROVAL REQUIRED] From agent: %s", request.PortInfo.PortID)
	demo.Assistantf("  Tool: %s", toolName)
	demo.Assistantf("  Arguments: %s", arguments)
	fmt.Println()
	demo.Assistantf("Tool: %s approved", toolName)

	response, err := request.CreateResponse(approvalRequest.CreateResponse(true, "Approved for sample deployment."))
	if err != nil {
		return err
	}
	return run.SendResponse(ctx, response)
}

func approvalToolCallDetails(toolCall message.ToolCallContent) (string, string) {
	switch toolCall := toolCall.(type) {
	case *message.FunctionCallContent:
		if toolCall != nil {
			return toolCall.Name, toolCall.Arguments
		}
	case *message.MCPServerToolCallContent:
		if toolCall != nil {
			return toolCall.Name, toolCall.Arguments
		}
	}
	return "unknown", ""
}

func printAgentUpdate(executorID string, update *agent.ResponseUpdate, lastExecutorID *string) {
	if update == nil {
		return
	}
	if executorID != *lastExecutorID {
		if *lastExecutorID != "" {
			fmt.Println()
		}
		fmt.Printf("- %s:\n", executorID)
		*lastExecutorID = executorID
	}
	if text := update.String(); strings.TrimSpace(text) != "" {
		fmt.Print(text)
	}
}

func hasDeploymentSummary(messages []*message.Message) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	if last == nil || last.Role != message.RoleAssistant {
		return false
	}
	text := strings.ToLower(last.String())
	return strings.Contains(text, "deployment") && strings.Contains(text, "production") && strings.Contains(text, "2.4.0")
}

func printTranscript(messages []*message.Message) {
	if len(messages) == 0 {
		return
	}
	demo.Assistant("Final transcript:")
	for _, msg := range messages {
		name := msg.AuthorName
		if name == "" {
			name = string(msg.Role)
		}
		demo.Assistantf("%s: %s", name, msg.String())
	}
}

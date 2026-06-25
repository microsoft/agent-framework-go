// Copyright (c) Microsoft. All rights reserved.

package workflowhosting

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func newGroupChatWorkflow(name string, managerFactory GroupChatManagerFactory, agents ...*agent.Agent) (*workflow.Workflow, error) {
	return NewGroupChatWorkflowBuilder(managerFactory, agents...).WithName(name).Build()
}

func TestGroupChatWorkflowBuilder_RoundRobinManagerProducesTranscript(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	agentC := newGroupChatLabelAgent("c", "C", "from-c")

	wf, err := newGroupChatWorkflow("round-robin", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 3})
	}, agentA, agentB, agentC)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.Name() != "round-robin" {
		t.Fatalf("workflow name = %q, want round-robin", wf.Name())
	}

	events := runGroupChatWorkflowTurn(t, wf, "hello")
	if got := collectGroupChatUpdateTexts(events); !slices.Equal(got, []string{"from-a", "from-b", "from-c"}) {
		t.Fatalf("update texts = %v, want [from-a from-b from-c]", got)
	}
	if got := collectGroupChatOutputTexts(events); !slices.Equal(got, []string{"hello", "from-a", "from-b", "from-c"}) {
		t.Fatalf("output transcript = %v, want [hello from-a from-b from-c]", got)
	}
}

func TestGroupChatWorkflowBuilder_WithNameOnlySetsWorkflowName(t *testing.T) {
	const workflowName = "named group chat"
	wf, err := newGroupChatWorkflow(workflowName, func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}, newGroupChatLabelAgent("a", "A", "from-a"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := wf.Name(); got != workflowName {
		t.Fatalf("workflow name = %q, want %q", got, workflowName)
	}
	if got := wf.Description(); got != "" {
		t.Fatalf("workflow description = %q, want empty", got)
	}
}

func TestGroupChatWorkflowBuilder_WithoutNameDefaultsToEmptyMetadata(t *testing.T) {
	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}, newGroupChatLabelAgent("a", "A", "from-a"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := wf.Name(); got != "" {
		t.Fatalf("workflow name = %q, want empty", got)
	}
	if got := wf.Description(); got != "" {
		t.Fatalf("workflow description = %q, want empty", got)
	}
}

func TestGroupChatWorkflowBuilder_DefaultOutputMetadataDesignatesHostAndIntermediateParticipants(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	agentC := newGroupChatLabelAgent("c", "C", "from-c")
	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}, agentA, agentB, agentC)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	outputIDs := wf.OutputExecutorIDs()
	slices.Sort(outputIDs)
	wantIDs := []string{groupChatHostExecutorID}
	for _, currentAgent := range []*agent.Agent{agentA, agentB, agentC} {
		wantIDs = append(wantIDs, New(currentAgent, Config{DisableForwardIncomingMessages: true}).ID)
	}
	slices.Sort(wantIDs)
	if !slices.Equal(outputIDs, wantIDs) {
		t.Fatalf("output executor IDs = %v, want %v", outputIDs, wantIDs)
	}
	if !wf.HasOutputExecutor(groupChatHostExecutorID) {
		t.Fatalf("expected %q to be an output executor", groupChatHostExecutorID)
	}
	if tags := outputExecutorTags(wf, groupChatHostExecutorID); len(tags) != 0 {
		t.Fatalf("host tags = %v, want terminal output with no tags", tags)
	}
	for _, currentAgent := range []*agent.Agent{agentA, agentB, agentC} {
		participantID := New(currentAgent, Config{DisableForwardIncomingMessages: true}).ID
		if !wf.HasOutputExecutor(participantID) {
			t.Fatalf("participant %q was not designated as an output executor", participantID)
		}
		if tags := outputExecutorTags(wf, participantID); len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
			t.Fatalf("participant %q tags = %v, want intermediate", participantID, tags)
		}
	}
}

func TestGroupChatWorkflowBuilder_ExplicitOutputDesignationSuppressesDefaults(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	wf, err := NewGroupChatWorkflowBuilder(func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}, agentA, agentB).
		WithOutputFrom(agentA).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantOutputID := New(agentA, Config{DisableForwardIncomingMessages: true}).ID
	outputIDs := wf.OutputExecutorIDs()
	slices.Sort(outputIDs)
	if !slices.Equal(outputIDs, []string{wantOutputID}) {
		t.Fatalf("output executor IDs = %v, want [%s]", outputIDs, wantOutputID)
	}
	if wf.HasOutputExecutor(groupChatHostExecutorID) {
		t.Fatalf("host output should be suppressed when explicit output designations are present")
	}
}

func TestGroupChatWorkflowBuilder_ExplicitOutputDesignationRejectsNonParticipant(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	nonParticipant := newGroupChatLabelAgent("outside", "Outside", "from-outside")
	managerFactory := func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}

	for _, testCase := range []struct {
		name      string
		configure func(*GroupChatWorkflowBuilder) *GroupChatWorkflowBuilder
	}{
		{
			name: "terminal",
			configure: func(builder *GroupChatWorkflowBuilder) *GroupChatWorkflowBuilder {
				return builder.WithOutputFrom(nonParticipant)
			},
		},
		{
			name: "intermediate",
			configure: func(builder *GroupChatWorkflowBuilder) *GroupChatWorkflowBuilder {
				return builder.WithIntermediateOutputFrom(nonParticipant)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := testCase.configure(NewGroupChatWorkflowBuilder(managerFactory, agentA)).Build()
			if err == nil {
				t.Fatal("expected error for non-participant output designation, got nil")
			}
			if !strings.Contains(err.Error(), "not a participant") {
				t.Fatalf("error = %q, want it to mention not a participant", err.Error())
			}
		})
	}
}

func outputExecutorTags(wf *workflow.Workflow, executorID string) []workflow.OutputTag {
	if wf == nil {
		return nil
	}
	tags, ok := wf.OutputExecutors()[executorID]
	if !ok {
		return nil
	}
	return tags
}

func TestGroupChatWorkflowBuilder_RegistersParticipantRequestPorts(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}, agentA, agentB)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, currentAgent := range []*agent.Agent{agentA, agentB} {
		participantID := New(currentAgent, Config{DisableForwardIncomingMessages: true}).ID
		approvalPort, ok := wf.RequestPort(participantID + "_UserInput")
		if !ok {
			t.Fatalf("missing user-input request port for participant %q", participantID)
		}
		if approvalPort.Request != reflect.TypeFor[*message.ToolApprovalRequestContent]() {
			t.Fatalf("approval request type = %v, want %v", approvalPort.Request, reflect.TypeFor[*message.ToolApprovalRequestContent]())
		}
		if approvalPort.Response != reflect.TypeFor[*message.ToolApprovalResponseContent]() {
			t.Fatalf("approval response type = %v, want %v", approvalPort.Response, reflect.TypeFor[*message.ToolApprovalResponseContent]())
		}

		functionPort, ok := wf.RequestPort(participantID + "_FunctionCall")
		if !ok {
			t.Fatalf("missing function-call request port for participant %q", participantID)
		}
		if functionPort.Request != reflect.TypeFor[*message.FunctionCallContent]() {
			t.Fatalf("function-call request type = %v, want %v", functionPort.Request, reflect.TypeFor[*message.FunctionCallContent]())
		}
		if functionPort.Response != reflect.TypeFor[*message.FunctionResultContent]() {
			t.Fatalf("function-call response type = %v, want %v", functionPort.Response, reflect.TypeFor[*message.FunctionResultContent]())
		}
	}
}

func TestGroupChatWorkflowBuilder_AgentsRunInOrder(t *testing.T) {
	for _, maxIterations := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("max_%d", maxIterations), func(t *testing.T) {
			agents := []*agent.Agent{
				newGroupChatDoubleEchoAgent("agent1"),
				newGroupChatDoubleEchoAgent("agent2"),
				newGroupChatDoubleEchoAgent("agent3"),
			}
			wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
				return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: maxIterations})
			}, agents...)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			events := runGroupChatWorkflowTurn(t, wf, "abc")
			wantTranscript := expectedGroupChatDoubleEchoTranscript(maxIterations, "abc")
			wantUpdates := wantTranscript[1:]
			if got := collectGroupChatUpdateTexts(events); !slices.Equal(got, wantUpdates) {
				t.Fatalf("update texts = %v, want %v", got, wantUpdates)
			}
			if got := collectGroupChatOutputTexts(events); !slices.Equal(got, wantTranscript) {
				t.Fatalf("output transcript = %v, want %v", got, wantTranscript)
			}
		})
	}
}

func TestGroupChatWorkflowBuilder_BroadcastsDeltaAndTargetsTurnTokenToSpeakerOnly(t *testing.T) {
	agentA := newGroupChatRecordingAgent("agentA")
	agentB := newGroupChatRecordingAgent("agentB")
	agentC := newGroupChatRecordingAgent("agentC")

	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 4})
	}, agentA.Agent, agentB.Agent, agentC.Agent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runGroupChatWorkflowTurn(t, wf, "hello")
	if got := collectGroupChatOutputTexts(events); !slices.Equal(got, []string{"hello", "agentA", "agentB", "agentC", "agentA"}) {
		t.Fatalf("output transcript = %v, want [hello agentA agentB agentC agentA]", got)
	}
	if got := agentA.Invocations(); !equalGroupChatInvocations(got, [][]string{{"hello"}, {"agentB", "agentC"}}) {
		t.Fatalf("agentA invocations = %v, want [[hello] [agentB agentC]]", got)
	}
	if got := agentB.Invocations(); !equalGroupChatInvocations(got, [][]string{{"hello", "agentA"}}) {
		t.Fatalf("agentB invocations = %v, want [[hello agentA]]", got)
	}
	if got := agentC.Invocations(); !equalGroupChatInvocations(got, [][]string{{"hello", "agentA", "agentB"}}) {
		t.Fatalf("agentC invocations = %v, want [[hello agentA agentB]]", got)
	}
}

func TestGroupChatWorkflowBuilder_UpdateHistoryFiltersBroadcastPayload(t *testing.T) {
	agentA := newGroupChatRecordingAgent("agentA")
	agentB := newGroupChatRecordingAgent("agentB")

	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		manager := NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 2})
		manager.UpdateHistory = func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return prefixGroupChatMessages("[broadcast] ", messages), nil
		}
		return manager
	}, agentA.Agent, agentB.Agent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	_ = runGroupChatWorkflowTurn(t, wf, "hello")
	if got := agentA.Invocations(); !equalGroupChatInvocations(got, [][]string{{"[broadcast] hello"}}) {
		t.Fatalf("agentA invocations = %v, want [[broadcast hello]]", got)
	}
	if got := agentB.Invocations(); !equalGroupChatInvocations(got, [][]string{{"[broadcast] hello", "[broadcast] agentA"}}) {
		t.Fatalf("agentB invocations = %v, want [[broadcast hello] [broadcast agentA]]", got)
	}
}

func TestGroupChatWorkflowBuilder_ToolApprovalCheckpointResumePreservesFunctionCallContent(t *testing.T) {
	approvalAgent := newGroupChatApprovalAgent("approval-agent")
	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 1})
	}, approvalAgent.Agent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := t.Context()
	checkpointManager := newGroupChatJSONCheckpointManager(t)
	first, err := inproc.Default.WithCheckpointing(checkpointManager).Run(ctx, wf, []*message.Message{textMessage("go")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	pendingRequest := firstGroupChatRequest(t, first.OutgoingEvents())
	checkpointInfo, ok := first.LastCheckpoint()
	if !ok {
		t.Fatal("expected checkpoint")
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("Close first run: %v", err)
	}

	preCheckpointApproval := groupChatApprovalRequest(t, pendingRequest)
	if _, ok := preCheckpointApproval.ToolCall.(*message.FunctionCallContent); !ok {
		t.Fatalf("pre-checkpoint ToolCall = %T, want *message.FunctionCallContent", preCheckpointApproval.ToolCall)
	}

	resumed, err := inproc.Default.WithCheckpointing(checkpointManager).Resume(ctx, wf, checkpointInfo)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = resumed.Close(ctx) }()

	replayedRequest := firstGroupChatRequest(t, resumed.NewEvents())
	replayedApproval := groupChatApprovalRequest(t, replayedRequest)
	if _, ok := replayedApproval.ToolCall.(*message.FunctionCallContent); !ok {
		t.Fatalf("replayed ToolCall = %T, want *message.FunctionCallContent", replayedApproval.ToolCall)
	}

	response, err := replayedRequest.CreateResponse(replayedApproval.CreateResponse(true, ""))
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := resumed.Resume(ctx, response); err != nil {
		t.Fatalf("Resume with response: %v", err)
	}
	postResponseEvents := slices.Collect(resumed.NewEvents())
	assertNoGroupChatErrors(t, postResponseEvents)
	if got := collectGroupChatOutputTexts(postResponseEvents); !slices.Equal(got, []string{"go", "approved"}) {
		t.Fatalf("output transcript = %v, want [go approved]", got)
	}
}

func TestGroupChatWorkflowBuilder_ToolApprovalDeniedResponseConversationContinues(t *testing.T) {
	approvalAgent := newGroupChatApprovalAgent("approval-agent")
	nextAgent := newGroupChatRecordingAgent("next-agent")
	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 2})
	}, approvalAgent.Agent, nextAgent.Agent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := t.Context()
	run, err := inproc.Default.Run(ctx, wf, []*message.Message{textMessage("go")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer func() { _ = run.Close(ctx) }()

	pendingRequest := firstGroupChatRequest(t, run.OutgoingEvents())
	approvalRequest := groupChatApprovalRequest(t, pendingRequest)
	if _, ok := approvalRequest.ToolCall.(*message.FunctionCallContent); !ok {
		t.Fatalf("ToolCall = %T, want *message.FunctionCallContent", approvalRequest.ToolCall)
	}
	response, err := pendingRequest.CreateResponse(approvalRequest.CreateResponse(false, "Denied"))
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(ctx, response); err != nil {
		t.Fatalf("Resume with denial: %v", err)
	}
	postResponseEvents := slices.Collect(run.NewEvents())
	assertNoGroupChatErrors(t, postResponseEvents)
	if got := collectGroupChatOutputTexts(postResponseEvents); !slices.Equal(got, []string{"go", "denied", "next-agent"}) {
		t.Fatalf("output transcript = %v, want [go denied next-agent]", got)
	}
	if got := nextAgent.Invocations(); !equalGroupChatInvocations(got, [][]string{{"go", "denied"}}) {
		t.Fatalf("next-agent invocations = %v, want [[go denied]]", got)
	}
}

func TestGroupChatWorkflowBuilder_FunctionCallExternallyResolvedConversationContinues(t *testing.T) {
	functionAgent := newGroupChatFunctionCallAgent("function-agent")
	nextAgent := newGroupChatRecordingAgent("next-agent")
	wf, err := newGroupChatWorkflow("", func(agents []*agent.Agent) *GroupChatManager {
		return NewRoundRobinGroupChatManager(agents, RoundRobinGroupChatOptions{MaximumIterationCount: 2})
	}, functionAgent.Agent, nextAgent.Agent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := t.Context()
	run, err := inproc.Default.Run(ctx, wf, []*message.Message{textMessage("go")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer func() { _ = run.Close(ctx) }()

	pendingRequest := firstGroupChatRequest(t, run.OutgoingEvents())
	functionCall := groupChatFunctionCall(t, pendingRequest)
	if functionCall.Name != "FetchExternalData" {
		t.Fatalf("function name = %q, want FetchExternalData", functionCall.Name)
	}
	response, err := pendingRequest.CreateResponse(&message.FunctionResultContent{CallID: functionCall.CallID, Result: "external-data"})
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(ctx, response); err != nil {
		t.Fatalf("Resume with response: %v", err)
	}
	postResponseEvents := slices.Collect(run.NewEvents())
	assertNoGroupChatErrors(t, postResponseEvents)
	if got := collectGroupChatOutputTexts(postResponseEvents); !slices.Equal(got, []string{"go", "got:external-data", "next-agent"}) {
		t.Fatalf("output transcript = %v, want [go got:external-data next-agent]", got)
	}
	if got := nextAgent.Invocations(); !equalGroupChatInvocations(got, [][]string{{"go", "got:external-data"}}) {
		t.Fatalf("next-agent invocations = %v, want [[go got:external-data]]", got)
	}
}

func TestGroupChatWorkflowBuilder_ReturnsErrorsForInvalidInputs(t *testing.T) {
	_, err := newGroupChatWorkflow("", func([]*agent.Agent) *GroupChatManager { return nil })
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}

	validAgent := newGroupChatLabelAgent("a", "A", "from-a")
	_, err = newGroupChatWorkflow("", nil, validAgent)
	if err == nil {
		t.Fatal("expected error for nil manager factory, got nil")
	}

	_, err = newGroupChatWorkflow("", func([]*agent.Agent) *GroupChatManager { return nil }, validAgent, nil)
	if err == nil {
		t.Fatal("expected error for nil agent, got nil")
	}
}

func TestRoundRobinGroupChatManager_CheckpointRestoresCursor(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	manager := NewRoundRobinGroupChatManager([]*agent.Agent{agentA, agentB}, RoundRobinGroupChatOptions{})
	if _, err := manager.SelectNextAgent(t.Context(), nil); err != nil {
		t.Fatalf("SelectNextAgent: %v", err)
	}

	const iterationCount = 4
	state := make(map[string]any)
	ctx := newGroupChatStateContext(t.Context(), state)
	if err := checkpointGroupChatManager(ctx, manager, iterationCount); err != nil {
		t.Fatalf("checkpointGroupChatManager: %v", err)
	}
	if _, ok := state[groupChatManagerStateKey]; !ok {
		t.Fatalf("missing manager state key %q", groupChatManagerStateKey)
	}
	if _, ok := state[groupChatManagerSubclassStateKeyPref+roundRobinGroupChatManagerStateKey]; !ok {
		t.Fatalf("missing prefixed round robin state key")
	}
	if _, ok := state[roundRobinGroupChatManagerStateKey]; ok {
		t.Fatalf("round robin state was written without prefix")
	}

	restored := NewRoundRobinGroupChatManager([]*agent.Agent{agentA, agentB}, RoundRobinGroupChatOptions{})
	restoredIterationCount, err := restoreGroupChatManagerCheckpoint(newGroupChatStateContext(t.Context(), state), restored)
	if err != nil {
		t.Fatalf("restoreGroupChatManagerCheckpoint: %v", err)
	}
	if got := restoredIterationCount; got != iterationCount {
		t.Fatalf("iteration count = %d, want 4", got)
	}
	nextAgent, err := restored.SelectNextAgent(t.Context(), nil)
	if err != nil {
		t.Fatalf("SelectNextAgent restored: %v", err)
	}
	if nextAgent != agentB {
		t.Fatalf("restored next agent = %s, want B", nextAgent.ID())
	}
}

func TestRoundRobinGroupChatManager_SelectNextAgentCyclesAndWraps(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	agentC := newGroupChatLabelAgent("c", "C", "from-c")
	manager := NewRoundRobinGroupChatManager([]*agent.Agent{agentA, agentB, agentC}, RoundRobinGroupChatOptions{})

	var got []*agent.Agent
	for range 4 {
		next, err := manager.SelectNextAgent(t.Context(), nil)
		if err != nil {
			t.Fatalf("SelectNextAgent: %v", err)
		}
		got = append(got, next)
	}
	want := []*agent.Agent{agentA, agentB, agentC, agentA}
	if !slices.Equal(got, want) {
		t.Fatalf("selected agents = %v, want %v", agentIDs(got), agentIDs(want))
	}
}

func TestRoundRobinGroupChatManager_ShouldTerminate(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	manager := NewRoundRobinGroupChatManager([]*agent.Agent{agentA}, RoundRobinGroupChatOptions{MaximumIterationCount: 3})

	terminate, err := manager.ShouldTerminate(t.Context(), nil, 2)
	if err != nil {
		t.Fatalf("ShouldTerminate before max: %v", err)
	}
	if terminate {
		t.Fatal("ShouldTerminate before max = true, want false")
	}
	terminate, err = manager.ShouldTerminate(t.Context(), nil, 3)
	if err != nil {
		t.Fatalf("ShouldTerminate at max: %v", err)
	}
	if !terminate {
		t.Fatal("ShouldTerminate at max = false, want true")
	}

	custom := NewRoundRobinGroupChatManager([]*agent.Agent{agentA}, RoundRobinGroupChatOptions{
		MaximumIterationCount: 100,
		ShouldTerminate: func(_ context.Context, history []*message.Message, _ int) (bool, error) {
			return slices.Contains(collectGroupChatMessageTexts(history), "done"), nil
		},
	})
	terminate, err = custom.ShouldTerminate(t.Context(), []*message.Message{textMessage("continue")}, 0)
	if err != nil {
		t.Fatalf("custom ShouldTerminate continue: %v", err)
	}
	if terminate {
		t.Fatal("custom ShouldTerminate without marker = true, want false")
	}
	terminate, err = custom.ShouldTerminate(t.Context(), []*message.Message{textMessage("done")}, 0)
	if err != nil {
		t.Fatalf("custom ShouldTerminate done: %v", err)
	}
	if !terminate {
		t.Fatal("custom ShouldTerminate with marker = false, want true")
	}
}

func TestNewRoundRobinGroupChatManager_DoesNotValidateInputs(t *testing.T) {
	validAgent := newGroupChatLabelAgent("a", "A", "from-a")
	for _, testCase := range []struct {
		name   string
		agents []*agent.Agent
		opts   RoundRobinGroupChatOptions
		want   *agent.Agent
	}{
		{name: "no agents"},
		{name: "nil agent", agents: []*agent.Agent{nil}},
		{name: "negative max", agents: []*agent.Agent{validAgent}, opts: RoundRobinGroupChatOptions{MaximumIterationCount: -1}, want: validAgent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := NewRoundRobinGroupChatManager(testCase.agents, testCase.opts)
			if manager == nil {
				t.Fatal("manager = nil, want non-nil")
			}
			got, err := manager.SelectNextAgent(t.Context(), nil)
			if err != nil {
				t.Fatalf("SelectNextAgent: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("selected agent = %v, want %v", got, testCase.want)
			}
			if testCase.opts.MaximumIterationCount < 0 {
				terminate, err := manager.ShouldTerminate(t.Context(), nil, 0)
				if err != nil {
					t.Fatalf("ShouldTerminate: %v", err)
				}
				if terminate {
					t.Fatal("ShouldTerminate at iteration 0 with negative max = true, want false")
				}
			}
		})
	}
}

func TestRoundRobinGroupChatManager_RestoreWithoutCheckpointDefaultsToZeroState(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "A", "from-a")
	agentB := newGroupChatLabelAgent("b", "B", "from-b")
	manager := NewRoundRobinGroupChatManager([]*agent.Agent{agentA, agentB}, RoundRobinGroupChatOptions{})
	if _, err := manager.SelectNextAgent(t.Context(), nil); err != nil {
		t.Fatalf("SelectNextAgent: %v", err)
	}

	iterationCount, err := restoreGroupChatManagerCheckpoint(newGroupChatStateContext(t.Context(), map[string]any{}), manager)
	if err != nil {
		t.Fatalf("restoreGroupChatManagerCheckpoint: %v", err)
	}
	if iterationCount != 0 {
		t.Fatalf("iterationCount = %d, want 0", iterationCount)
	}
	next, err := manager.SelectNextAgent(t.Context(), nil)
	if err != nil {
		t.Fatalf("SelectNextAgent after restore: %v", err)
	}
	if next != agentA {
		t.Fatalf("next after empty restore = %s, want agentA", next.ID())
	}
}

func newGroupChatLabelAgent(id string, name string, label string) *agent.Agent {
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: name,
				Contents:   []message.Content{&message.TextContent{Text: label}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "group-chat-label", Run: run},
		agent.Config{ID: id, Name: name, DisableFuncAutoCall: true},
	)
}

func newGroupChatDoubleEchoAgent(id string) *agent.Agent {
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			inputText := ""
			for _, msg := range messages {
				inputText += msg.String()
			}
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: id,
				Contents: []message.Content{
					&message.TextContent{Text: id + inputText + inputText},
				},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "group-chat-double-echo", Run: run},
		agent.Config{ID: id, Name: id, DisableFuncAutoCall: true, HistoryProvider: &agent.HistoryProvider{SourceID: "noop-history"}},
	)
}

func expectedGroupChatDoubleEchoTranscript(maxIterations int, userInput string) []string {
	agentIDs := []string{"agent1", "agent2", "agent3"}
	buffers := make([][]string, len(agentIDs))
	for index := range buffers {
		buffers[index] = []string{userInput}
	}
	transcript := []string{userInput}
	for turn := range maxIterations {
		speakerIndex := turn % len(agentIDs)
		inputText := ""
		for _, text := range buffers[speakerIndex] {
			inputText += text
		}
		outputText := agentIDs[speakerIndex] + inputText + inputText
		transcript = append(transcript, outputText)
		buffers[speakerIndex] = nil
		for index := range buffers {
			if index != speakerIndex {
				buffers[index] = append(buffers[index], outputText)
			}
		}
	}
	return transcript
}

type groupChatRecordingAgent struct {
	Agent *agent.Agent
	name  string

	mu          sync.Mutex
	invocations [][]string
}

type groupChatApprovalAgent struct {
	Agent *agent.Agent

	mu   sync.Mutex
	step int
}

func newGroupChatApprovalAgent(id string) *groupChatApprovalAgent {
	agentState := &groupChatApprovalAgent{}
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			agentState.mu.Lock()
			step := agentState.step
			agentState.step++
			agentState.mu.Unlock()

			if step == 0 {
				yield(&agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{&message.ToolApprovalRequestContent{
						RequestID: "approval-call-1",
						ToolCall: &message.FunctionCallContent{
							CallID: "approval-call-1",
							Name:   "DoPrivilegedThing",
						},
					}},
				}, nil)
				return
			}

			approvalText := "no-approval"
			for _, msg := range messages {
				for _, content := range msg.Contents {
					approval, ok := content.(*message.ToolApprovalResponseContent)
					if !ok {
						continue
					}
					if approval.Approved {
						approvalText = "approved"
					} else {
						approvalText = "denied"
					}
				}
			}
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: id,
				Contents:   []message.Content{&message.TextContent{Text: approvalText}},
			}, nil)
		}
	}
	agentState.Agent = agent.New(
		agent.ProviderConfig{ProviderName: "group-chat-approval", Run: run},
		agent.Config{ID: id, Name: id, DisableFuncAutoCall: true, HistoryProvider: &agent.HistoryProvider{SourceID: "noop-history"}},
	)
	return agentState
}

type groupChatFunctionCallAgent struct {
	Agent *agent.Agent

	mu   sync.Mutex
	step int
}

func newGroupChatFunctionCallAgent(id string) *groupChatFunctionCallAgent {
	agentState := &groupChatFunctionCallAgent{}
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			agentState.mu.Lock()
			step := agentState.step
			agentState.step++
			agentState.mu.Unlock()

			if step == 0 {
				yield(&agent.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{&message.FunctionCallContent{
						CallID: "function-call-1",
						Name:   "FetchExternalData",
					}},
				}, nil)
				return
			}

			text := "no-result"
			for _, msg := range messages {
				for _, content := range msg.Contents {
					result, ok := content.(*message.FunctionResultContent)
					if !ok {
						continue
					}
					if resultText, ok := result.Result.(string); ok {
						text = "got:" + resultText
					}
				}
			}
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: id,
				Contents:   []message.Content{&message.TextContent{Text: text}},
			}, nil)
		}
	}
	agentState.Agent = agent.New(
		agent.ProviderConfig{ProviderName: "group-chat-function-call", Run: run},
		agent.Config{ID: id, Name: id, DisableFuncAutoCall: true, HistoryProvider: &agent.HistoryProvider{SourceID: "noop-history"}},
	)
	return agentState
}

func newGroupChatRecordingAgent(name string) *groupChatRecordingAgent {
	recorder := &groupChatRecordingAgent{name: name}
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			recorder.mu.Lock()
			recorder.invocations = append(recorder.invocations, collectGroupChatMessageTexts(messages))
			recorder.mu.Unlock()

			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    name,
				AuthorName: name,
				Contents:   []message.Content{&message.TextContent{Text: name}},
			}, nil)
		}
	}
	recorder.Agent = agent.New(
		agent.ProviderConfig{ProviderName: "group-chat-recording", Run: run},
		agent.Config{
			ID:              name,
			Name:            name,
			HistoryProvider: &agent.HistoryProvider{SourceID: "noop-history"},
		},
	)
	return recorder
}

func (recorder *groupChatRecordingAgent) Invocations() [][]string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	invocations := make([][]string, len(recorder.invocations))
	for index, invocation := range recorder.invocations {
		invocations[index] = slices.Clone(invocation)
	}
	return invocations
}

func equalGroupChatInvocations(left, right [][]string) bool {
	return slices.EqualFunc(left, right, func(leftInvocation []string, rightInvocation []string) bool {
		return slices.Equal(leftInvocation, rightInvocation)
	})
}

func prefixGroupChatMessages(prefix string, messages []*message.Message) []*message.Message {
	prefixed := make([]*message.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		prefixed = append(prefixed, &message.Message{
			Role:       msg.Role,
			AuthorName: msg.AuthorName,
			Contents: []message.Content{
				&message.TextContent{Text: prefix + msg.String()},
			},
		})
	}
	return prefixed
}

func collectGroupChatMessageTexts(messages []*message.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg != nil && msg.String() != "" {
			texts = append(texts, msg.String())
		}
	}
	return texts
}

func textMessage(text string) *message.Message {
	return &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: text}},
	}
}

func agentIDs(agents []*agent.Agent) []string {
	ids := make([]string, 0, len(agents))
	for _, currentAgent := range agents {
		ids = append(ids, currentAgent.ID())
	}
	return ids
}

func firstGroupChatRequest(t *testing.T, events iter.Seq[workflow.Event]) *workflow.ExternalRequest {
	t.Helper()
	for event := range events {
		if requestEvent, ok := event.(workflow.RequestInfoEvent); ok {
			return requestEvent.Request
		}
	}
	t.Fatal("expected RequestInfoEvent")
	return nil
}

func groupChatApprovalRequest(t *testing.T, request *workflow.ExternalRequest) *message.ToolApprovalRequestContent {
	t.Helper()
	approval, ok := workflow.PortableValueAs[*message.ToolApprovalRequestContent](request.Data)
	if !ok {
		t.Fatalf("request data = %T, want *message.ToolApprovalRequestContent", request.Data.Any())
	}
	return approval
}

func groupChatFunctionCall(t *testing.T, request *workflow.ExternalRequest) *message.FunctionCallContent {
	t.Helper()
	functionCall, ok := workflow.PortableValueAs[*message.FunctionCallContent](request.Data)
	if !ok {
		t.Fatalf("request data = %T, want *message.FunctionCallContent", request.Data.Any())
	}
	return functionCall
}

func assertNoGroupChatErrors(t *testing.T, events []workflow.Event) {
	t.Helper()
	for _, event := range events {
		switch event := event.(type) {
		case workflow.ErrorEvent:
			t.Fatalf("unexpected workflow error: %v", event.Error)
		case workflow.ExecutorFailedEvent:
			t.Fatalf("unexpected executor failure from %q: %v", event.ExecutorID, event.Error)
		}
	}
}

func newGroupChatJSONCheckpointManager(t *testing.T) checkpoint.Manager {
	t.Helper()
	store, err := checkpoint.NewFileSystemJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileSystemJSONStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close checkpoint store: %v", err)
		}
	})
	return checkpoint.NewJSONManager(store)
}

func runGroupChatWorkflowTurn(t *testing.T, wf *workflow.Workflow, inputText string) []workflow.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() {
		if err := stream.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if err := stream.SendMessage(ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: inputText}},
	}}); err != nil {
		t.Fatalf("SendMessage input: %v", err)
	}
	emitEvents := true
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		t.Fatalf("SendMessage turn token: %v", err)
	}

	var events []workflow.Event
	for event, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("WatchStream: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func collectGroupChatUpdateTexts(events []workflow.Event) []string {
	var texts []string
	for _, event := range events {
		output, ok := event.(workflow.OutputEvent)
		if !ok {
			continue
		}
		update, ok := output.Output.(*agent.ResponseUpdate)
		if !ok {
			continue
		}
		for _, content := range update.Contents {
			if textContent, ok := content.(*message.TextContent); ok {
				texts = append(texts, textContent.Text)
			}
		}
	}
	return texts
}

func collectGroupChatOutputTexts(events []workflow.Event) []string {
	var texts []string
	for _, event := range events {
		output, ok := event.(workflow.OutputEvent)
		if !ok {
			continue
		}
		messages, ok := output.Output.([]*message.Message)
		if !ok {
			continue
		}
		for _, currentMessage := range messages {
			for _, content := range currentMessage.Contents {
				if textContent, ok := content.(*message.TextContent); ok {
					texts = append(texts, textContent.Text)
				}
			}
		}
	}
	return texts
}

func newGroupChatStateContext(ctx context.Context, state map[string]any) *workflow.Context {
	return &workflow.Context{
		Context: ctx,
		ReadState: func(key string, scope string) (any, error) {
			return state[key], nil
		},
		ReadOrInitState: func(key string, scope string, initFunc func(context.Context, string, string) (any, error)) (any, error) {
			if value, ok := state[key]; ok {
				return value, nil
			}
			value, err := initFunc(ctx, key, scope)
			if err != nil {
				return nil, err
			}
			state[key] = value
			return value, nil
		},
		ReadStateKeys: func(string) iter.Seq2[string, error] {
			return func(yield func(string, error) bool) {
				for key := range state {
					if !yield(key, nil) {
						return
					}
				}
			}
		},
		QueueStateUpdate: func(key string, scope string, value any) error {
			if value == nil {
				delete(state, key)
				return nil
			}
			state[key] = value
			return nil
		},
	}
}

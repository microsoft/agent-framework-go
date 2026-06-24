// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

// TestSequentialWorkflowBuilder_ReturnsErrorForNoAgents checks that the
// sequential builder rejects an empty agent list.
func TestSequentialWorkflowBuilder_ReturnsErrorForNoAgents(t *testing.T) {
	_, err := workflowhosting.NewSequentialWorkflowBuilder().Build()
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}
}

func TestSequentialWorkflowBuilder_ReturnsErrorForNilAgent(t *testing.T) {
	validAgent := newLabeledEchoAgent("a", "A", "from-a")
	for _, tt := range []struct {
		name   string
		agents []*agent.Agent
	}{
		{name: "only_nil", agents: []*agent.Agent{nil}},
		{name: "second_nil", agents: []*agent.Agent{validAgent, nil}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workflowhosting.NewSequentialWorkflowBuilder(tt.agents...).Build()
			if err == nil {
				t.Fatal("expected error for nil agent, got nil")
			}
			if !strings.Contains(err.Error(), "nil") {
				t.Fatalf("error = %q, want it to mention nil", err.Error())
			}
		})
	}
}

// TestSequentialWorkflowBuilder_SingleAgent verifies that a single-agent sequential
// workflow builds successfully and emits the agent's output.
func TestSequentialWorkflowBuilder_SingleAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	wf, err := workflowhosting.NewSequentialWorkflowBuilder(a).WithName("single-agent").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.Name() != "single-agent" {
		t.Fatalf("workflow name = %q, want %q", wf.Name(), "single-agent")
	}

	texts := collectOutputTexts(runBuiltWorkflow(t, wf))
	if len(texts) != 1 || texts[0] != "from-a" {
		t.Errorf("got texts %v, want [from-a]", texts)
	}
}

// TestSequentialWorkflowBuilder_MultiAgent verifies that a multi-agent sequential
// workflow builds successfully and that the last agent's output is emitted.
func TestSequentialWorkflowBuilder_MultiAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	c := newLabeledEchoAgent("c", "C", "from-c")
	wf, err := workflowhosting.NewSequentialWorkflowBuilder(a, b, c).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.Name() != "" {
		t.Fatalf("workflow name = %q, want empty", wf.Name())
	}

	texts := collectOutputTexts(runBuiltWorkflow(t, wf))
	// Only the last agent (c) is the output node, so we expect "from-c".
	if len(texts) == 0 {
		t.Fatal("expected at least one output text")
	}
	last := texts[len(texts)-1]
	if last != "from-c" {
		t.Errorf("last output text = %q, want %q", last, "from-c")
	}
}

func TestSequentialWorkflowBuilder_ExplicitOutputDesignationSuppressesDefaults(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	wf, err := workflowhosting.NewSequentialWorkflowBuilder(a, b).
		WithOutputFrom(b).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantOutputID := workflowhosting.New(b, workflowhosting.Config{}).ID
	outputIDs := wf.OutputExecutorIDs()
	slices.Sort(outputIDs)
	if !slices.Equal(outputIDs, []string{wantOutputID}) {
		t.Fatalf("output executor IDs = %v, want [%s]", outputIDs, wantOutputID)
	}

	events := runBuiltWorkflow(t, wf)
	texts := collectOutputTexts(events)
	if !slices.Equal(texts, []string{"from-b"}) {
		t.Fatalf("output texts = %v, want [from-b]", texts)
	}
	if messages := collectOutputMessages(events); len(messages) != 0 {
		t.Fatalf("terminal aggregator output should be suppressed, got %v", collectMessageTexts(messages))
	}
}

func TestSequentialWorkflowBuilder_ExplicitIntermediateDesignationSuppressesDefaults(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	wf, err := workflowhosting.NewSequentialWorkflowBuilder(a, b).
		WithIntermediateOutputFrom(a).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantOutputID := workflowhosting.New(a, workflowhosting.Config{}).ID
	outputIDs := wf.OutputExecutorIDs()
	slices.Sort(outputIDs)
	if !slices.Equal(outputIDs, []string{wantOutputID}) {
		t.Fatalf("output executor IDs = %v, want [%s]", outputIDs, wantOutputID)
	}
	if tags := outputExecutorTags(wf, wantOutputID); len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
		t.Fatalf("tags = %v, want intermediate", tags)
	}
	texts := collectOutputTexts(runBuiltWorkflow(t, wf))
	if !slices.Equal(texts, []string{"from-a"}) {
		t.Fatalf("output texts = %v, want [from-a]", texts)
	}
}

func TestSequentialWorkflowBuilder_ExplicitOutputDesignationRejectsNonParticipant(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	nonParticipant := newLabeledEchoAgent("outside", "Outside", "from-outside")

	for _, testCase := range []struct {
		name      string
		configure func(*workflowhosting.SequentialWorkflowBuilder) *workflowhosting.SequentialWorkflowBuilder
	}{
		{
			name: "terminal",
			configure: func(builder *workflowhosting.SequentialWorkflowBuilder) *workflowhosting.SequentialWorkflowBuilder {
				return builder.WithOutputFrom(nonParticipant)
			},
		},
		{
			name: "intermediate",
			configure: func(builder *workflowhosting.SequentialWorkflowBuilder) *workflowhosting.SequentialWorkflowBuilder {
				return builder.WithIntermediateOutputFrom(nonParticipant)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := testCase.configure(workflowhosting.NewSequentialWorkflowBuilder(a)).Build()
			if err == nil {
				t.Fatal("expected error for non-participant output designation, got nil")
			}
			if !strings.Contains(err.Error(), "not a participant") {
				t.Fatalf("error = %q, want it to mention not a participant", err.Error())
			}
		})
	}
}

// TestSequentialWorkflowBuilder_AgentsRunInOrder verifies that each agent receives the
// accumulated conversation produced by the agents before it.
func TestSequentialWorkflowBuilder_AgentsRunInOrder(t *testing.T) {
	for _, numAgents := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("%d_agents", numAgents), func(t *testing.T) {
			agents := make([]*agent.Agent, 0, numAgents)
			for agentNumber := 1; agentNumber <= numAgents; agentNumber++ {
				agents = append(agents, newDoubleEchoAgent(fmt.Sprintf("agent%d", agentNumber)))
			}

			wf, err := workflowhosting.NewSequentialWorkflowBuilder(agents...).Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			for range 3 {
				const inputText = "abc"
				events := runBuiltWorkflowWithText(t, wf, inputText)
				texts := collectOutputTexts(events)
				want := expectedSequentialDoubleEchoOutputs(numAgents, inputText)
				if !slices.Equal(texts, want) {
					t.Fatalf("output texts = %v, want %v", texts, want)
				}

				resultMessages := collectOutputMessages(events)
				wantResultTexts := append([]string{inputText}, want...)
				if gotResultTexts := collectMessageTexts(resultMessages); !slices.Equal(gotResultTexts, wantResultTexts) {
					t.Fatalf("result texts = %v, want %v", gotResultTexts, wantResultTexts)
				}
				if len(resultMessages) != numAgents+1 {
					t.Fatalf("result count = %d, want %d", len(resultMessages), numAgents+1)
				}
				if resultMessages[0].Role != message.RoleUser {
					t.Fatalf("result[0].Role = %q, want %q", resultMessages[0].Role, message.RoleUser)
				}
				for resultIndex, resultMessage := range resultMessages[1:] {
					wantAuthorName := fmt.Sprintf("agent%d", resultIndex+1)
					if resultMessage.Role != message.RoleAssistant {
						t.Fatalf("result[%d].Role = %q, want %q", resultIndex+1, resultMessage.Role, message.RoleAssistant)
					}
					if resultMessage.AuthorName != wantAuthorName {
						t.Fatalf("result[%d].AuthorName = %q, want %q", resultIndex+1, resultMessage.AuthorName, wantAuthorName)
					}
				}
			}
		})
	}
}

func TestSequentialWorkflowBuilder_ChainOnlyAgentResponses(t *testing.T) {
	agents := []*agent.Agent{
		newDoubleEchoAgent("agent1"),
		newDoubleEchoAgent("agent2"),
		newDoubleEchoAgent("agent3"),
	}
	wf, err := workflowhosting.NewSequentialWorkflowBuilder(agents...).WithChainOnlyAgentResponses(true).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	const inputText = "abc"
	events := runBuiltWorkflowWithText(t, wf, inputText)
	texts := collectOutputTexts(events)
	want := expectedSequentialChainOnlyDoubleEchoOutputs(len(agents), inputText)
	if !slices.Equal(texts, want) {
		t.Fatalf("output texts = %v, want %v", texts, want)
	}

	resultMessages := collectOutputMessages(events)
	if gotResultTexts := collectMessageTexts(resultMessages); !slices.Equal(gotResultTexts, []string{want[len(want)-1]}) {
		t.Fatalf("result texts = %v, want [%s]", gotResultTexts, want[len(want)-1])
	}
	if len(resultMessages) != 1 {
		t.Fatalf("result count = %d, want 1", len(resultMessages))
	}
	if resultMessages[0].Role != message.RoleAssistant {
		t.Fatalf("result[0].Role = %q, want %q", resultMessages[0].Role, message.RoleAssistant)
	}
	if resultMessages[0].AuthorName != "agent3" {
		t.Fatalf("result[0].AuthorName = %q, want agent3", resultMessages[0].AuthorName)
	}
}

func expectedSequentialDoubleEchoOutputs(numAgents int, inputText string) []string {
	transcript := inputText
	outputs := make([]string, 0, numAgents)
	for agentNumber := 1; agentNumber <= numAgents; agentNumber++ {
		agentID := fmt.Sprintf("agent%d", agentNumber)
		outputText := agentID + transcript + transcript
		outputs = append(outputs, outputText)
		transcript += outputText
	}
	return outputs
}

func expectedSequentialChainOnlyDoubleEchoOutputs(numAgents int, inputText string) []string {
	previous := inputText
	outputs := make([]string, 0, numAgents)
	for agentNumber := 1; agentNumber <= numAgents; agentNumber++ {
		agentID := fmt.Sprintf("agent%d", agentNumber)
		outputText := agentID + previous + previous
		outputs = append(outputs, outputText)
		previous = outputText
	}
	return outputs
}

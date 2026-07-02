// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

const (
	maxIterations  = 3
	flowStateKey   = "singleton"
	flowStateScope = "FlowStateScope"
)

var logger = demo.NewLogger(
	"Writer Critic Workflow",
	"This sample iterates between writer and critic agents until content is approved.",
	"Model", demo.FoundryModel,
)

type CriticDecision struct {
	Approved  bool   `json:"approved"`
	Feedback  string `json:"feedback"`
	Content   string `json:"-"`
	Iteration int    `json:"-"`
}

type flowState struct {
	Iteration int
	History   []*message.Message
}

func main() {
	writer := newWriterExecutor("Writer")
	critic := newCriticExecutor("Critic")
	summary := newSummaryExecutor("Summary")

	b := workflow.NewBuilder(writer).
		AddEdge(writer, critic)
	b.AddSwitch(critic).
		AddCase(func(msg any) bool { return msg.(CriticDecision).Approved }, summary).
		AddCase(func(msg any) bool { return !msg.(CriticDecision).Approved }, writer).
		AddToBuilder(b).
		WithOutputFrom(summary)

	wf, err := b.Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(context.Background(), wf, "Write a 200-word blog post about AI ethics. Make it thoughtful and engaging.")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if output, ok := e.Output.(*message.Message); ok {
				demo.Assistantf("Final approved content:\n%s", output.String())
			}
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		}
	}
}

func newWriterExecutor(id string) workflow.ExecutorBinding {
	token := demo.FoundryTokenCredential()
	ag := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: `You are a skilled writer. Create clear, engaging content.
If you receive feedback, carefully revise the content to address all concerns.
Maintain the same topic and length requirements.`,
			Config: agent.Config{
				Name:        id,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[*message.Message](), func(ctx *workflow.Context, msg any) (any, error) {
						return runWriter(ctx, ag, message.NewText(msg.(string)), "")
					}).
					AddHandlerRaw(reflect.TypeFor[CriticDecision](), reflect.TypeFor[*message.Message](), func(ctx *workflow.Context, msg any) (any, error) {
						decision := msg.(CriticDecision)
						prompt := fmt.Sprintf("Revise the following content based on this feedback:\n\nFeedback: %s\n\nOriginal Content:\n%s", decision.Feedback, decision.Content)
						return runWriter(ctx, ag, message.NewText(prompt), decision.Content)
					})
				return rb, nil
			},
		}, nil
	})
}

func runWriter(ctx *workflow.Context, ag *agent.Agent, prompt *message.Message, priorContent string) (*message.Message, error) {
	state, err := readFlowState(ctx)
	if err != nil {
		return nil, err
	}
	demo.Assistantf("=== Writer (Iteration %d) ===", state.Iteration)
	resp, err := ag.RunText(ctx, prompt.String(), agent.Stream(true)).Collect()
	if err != nil {
		return nil, err
	}
	content := resp.String()
	if strings.TrimSpace(content) == "" {
		content = priorContent
	}
	state.History = append(state.History, textMessage(message.RoleAssistant, content))
	if err := saveFlowState(ctx, state); err != nil {
		return nil, err
	}
	return message.NewText(content), nil
}

func newCriticExecutor(id string) workflow.ExecutorBinding {
	token := demo.FoundryTokenCredential()
	ag := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: `You are a constructive critic. Review the content and provide specific feedback.
Always try to provide actionable suggestions for improvement and strive to identify improvement points.
Only approve if the content is high quality, clear, and meets the original requirements and you see no improvement points.

Provide your decision as structured output with:
- approved: true if content is good, false if revisions needed
- feedback: specific improvements needed (empty if approved)

Be concise but specific in your feedback.`,
			Config: agent.Config{
				Name:        id,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	return workflow.NewExecutor(id, func(ctx *workflow.Context, content *message.Message) (CriticDecision, error) {
		state, err := readFlowState(ctx)
		if err != nil {
			return CriticDecision{}, err
		}
		demo.Assistantf("=== Critic (Iteration %d) ===", state.Iteration)
		var decision CriticDecision
		_, err = ag.Run(ctx, []*message.Message{content}, agent.WithStructuredOutput(&decision), agent.Stream(true)).Collect()
		if err != nil {
			return CriticDecision{}, err
		}
		demo.Assistantf("Decision: approved=%t", decision.Approved)
		if decision.Feedback != "" {
			demo.Assistantf("Feedback: %s", decision.Feedback)
		}
		if !decision.Approved && state.Iteration >= maxIterations {
			decision.Approved = true
			decision.Feedback = ""
		}
		if !decision.Approved {
			state.Iteration++
		}
		state.History = append(state.History, textMessage(message.RoleAssistant, fmt.Sprintf("[Decision: %s] %s", decisionStatus(decision.Approved), decision.Feedback)))
		if err := saveFlowState(ctx, state); err != nil {
			return CriticDecision{}, err
		}
		decision.Content = content.String()
		decision.Iteration = state.Iteration
		return decision, nil
	}).Bind()
}

func newSummaryExecutor(id string) workflow.ExecutorBinding {
	token := demo.FoundryTokenCredential()
	ag := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: `You present the final approved content to the user.
Simply output the polished content - no additional commentary needed.`,
			Config: agent.Config{
				Name:        id,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	return workflow.NewExecutor(id, func(ctx *workflow.Context, decision CriticDecision) (*message.Message, error) {
		demo.Assistant("=== Summary ===")
		resp, err := ag.RunText(ctx, "Present this approved content:\n\n"+decision.Content, agent.Stream(true)).Collect()
		if err != nil {
			return nil, err
		}
		content := resp.String()
		return textMessage(message.RoleAssistant, content), nil
	}).Bind()
}

func readFlowState(ctx *workflow.Context) (flowState, error) {
	value, err := ctx.ReadOrInitState(flowStateKey, flowStateScope, func(context.Context, string, string) (any, error) {
		return flowState{Iteration: 1}, nil
	})
	if err != nil {
		return flowState{}, err
	}
	state, ok := value.(flowState)
	if !ok {
		return flowState{}, fmt.Errorf("unexpected flow state type %T", value)
	}
	if state.Iteration == 0 {
		state.Iteration = 1
	}
	return state, nil
}

func saveFlowState(ctx *workflow.Context, state flowState) error {
	return ctx.QueueStateUpdate(flowStateKey, flowStateScope, state)
}

func textMessage(role message.Role, text string) *message.Message {
	return &message.Message{
		Role:     role,
		Contents: []message.Content{&message.TextContent{Text: text}},
	}
}

func decisionStatus(approved bool) string {
	if approved {
		return "Approved"
	}
	return "Needs Revision"
}

// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
	internalcheckpoint "github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

func sampleEnvironments() []checkpointTestEnvironment {
	return []checkpointTestEnvironment{
		{name: "lockstep", env: inproc.Lockstep},
		{name: "off_thread", env: inproc.OffThread},
		{name: "concurrent", env: inproc.Concurrent},
	}
}

func sampleSequentialWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	uppercase := workflow.NewExecutor("UppercaseExecutor", strings.ToUpper).SetCrossRunShareable(true).Bind()
	reverse := workflow.NewExecutor("ReverseTextExecutor", func(ctx *workflow.Context, text string) (string, error) {
		runes := []rune(text)
		slices.Reverse(runes)
		result := string(runes)
		return result, ctx.YieldOutput(result)
	}).SetCrossRunShareable(true).Bind()
	wf, err := workflow.NewBuilder(uppercase).AddEdge(uppercase, reverse).WithOutputFrom(reverse).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func sampleCompletedLines(events []workflow.Event) []string {
	var lines []string
	for _, event := range events {
		if completed, ok := event.(workflow.ExecutorCompletedEvent); ok {
			lines = append(lines, fmt.Sprintf("%s: %v", completed.ExecutorID, completed.Data()))
		}
	}
	return lines
}

func assertSampleSequentialLines(t *testing.T, events []workflow.Event) {
	t.Helper()
	lines := sampleCompletedLines(events)
	want := []string{"UppercaseExecutor: HELLO, WORLD!", "ReverseTextExecutor: !DLROW ,OLLEH"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if !strings.Contains(lines[i], want[i]) {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestSample_Step1_StreamingSequential(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			stream, err := environment.env.RunStreaming(t.Context(), sampleSequentialWorkflow(t), "Hello, World!")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close(t.Context()) }()
			assertSampleSequentialLines(t, readStreamToHalt(t, t.Context(), stream))
		})
	}
}

func TestSample_Step1a_NonStreamingSequential(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			run, err := environment.env.Run(t.Context(), sampleSequentialWorkflow(t), "Hello, World!")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = run.Close(t.Context()) }()
			if status, err := run.GetStatus(t.Context()); err != nil || status != inproc.RunStatusIdle {
				t.Fatalf("status = %v, %v", status, err)
			}
			assertSampleSequentialLines(t, slices.Collect(run.NewEvents()))
		})
	}
}

func sampleSpamWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	detect := workflow.NewExecutor("DetectSpam", func(text string) bool {
		text = strings.ToLower(text)
		return strings.Contains(text, "spam") || strings.Contains(text, "advertisement") || strings.Contains(text, "offer")
	}).SetCrossRunShareable(true).Bind()
	respond := workflow.NewExecutor("RespondToMessage", func(spam bool) (string, error) {
		if spam {
			return "", fmt.Errorf("received a spam message that should not get a reply")
		}
		return "Message processed successfully.", nil
	}).SetCrossRunShareable(true).Bind()
	remove := workflow.NewExecutor("RemoveSpam", func(spam bool) (string, error) {
		if !spam {
			return "", fmt.Errorf("received a non-spam message that should not get removed")
		}
		return "Spam message removed.", nil
	}).SetCrossRunShareable(true).Bind()
	wf, err := workflow.NewBuilder(detect).
		AddEdge(detect, respond, workflow.WithEdgeCondition(func(value any) bool { spam, ok := value.(bool); return ok && !spam })).
		AddEdge(detect, remove, workflow.WithEdgeCondition(func(value any) bool { spam, ok := value.(bool); return ok && spam })).
		WithOutputFrom(respond, remove).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestSample_Step2_ConditionalSpamRouting(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			for _, test := range []struct{ input, want string }{
				{"This is a spam message.", "Spam message removed."},
				{"This is a valid message.", "Message processed successfully."},
			} {
				stream, err := environment.env.RunStreaming(t.Context(), sampleSpamWorkflow(t), test.input)
				if err != nil {
					t.Fatal(err)
				}
				events := readStreamToHalt(t, t.Context(), stream)
				if err := stream.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				outputs := outputEvents(events)
				if hasErrorEvents(events) || len(outputs) != 1 || outputs[0].Output != test.want {
					t.Fatalf("events = %v, want %q", events, test.want)
				}
			}
		})
	}
}

type sampleNumberSignal int

const (
	sampleNumberInit sampleNumberSignal = iota
	sampleNumberAbove
	sampleNumberBelow
	sampleNumberMatched
)

type sampleNumberBounds struct{ Lower, Upper int }
type sampleTryCount struct{ Tries int }

func sampleJudge() workflow.ExecutorBinding {
	executor := workflow.NewExecutor("Judge", func(ctx *workflow.Context, guess int) (sampleNumberSignal, error) {
		value, err := ctx.ReadState("TryCount", "")
		if err != nil {
			return 0, err
		}
		count, _ := value.(int)
		if err := ctx.YieldOutput(sampleTryCount{Tries: count + 1}); err != nil {
			return 0, err
		}
		if guess == 42 {
			return sampleNumberMatched, nil
		}
		if guess < 42 {
			return sampleNumberBelow, nil
		}
		return sampleNumberAbove, nil
	}).SetCrossRunShareable(true)
	executor.Extend(&workflow.Executor{ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
		return pb.YieldsOutputType(reflect.TypeFor[sampleTryCount]()), nil
	}})
	return executor.Bind()
}

func sampleGuessLoopWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	guess := workflow.NewExecutor("GuessNumber", func(ctx *workflow.Context, signal sampleNumberSignal) (int, error) {
		value, err := ctx.ReadState("NumberBounds", "")
		if err != nil {
			return 0, err
		}
		bounds, ok := value.(sampleNumberBounds)
		if !ok {
			bounds = sampleNumberBounds{1, 100}
		}
		current := (bounds.Lower + bounds.Upper) / 2
		switch signal {
		case sampleNumberMatched:
			if err := ctx.YieldOutput(fmt.Sprintf("Guessed the number: %d", current)); err != nil {
				return 0, err
			}
		case sampleNumberAbove:
			bounds.Upper = current - 1
		case sampleNumberBelow:
			bounds.Lower = current + 1
		}
		if err := ctx.QueueStateUpdate("NumberBounds", "", bounds); err != nil {
			return 0, err
		}
		return (bounds.Lower + bounds.Upper) / 2, nil
	}).SetCrossRunShareable(true)
	guess.AutoYieldOutputHandlerResultObject = new(false)
	guess.Extend(&workflow.Executor{ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
		return pb.YieldsOutputType(reflect.TypeFor[string]()), nil
	}})
	binding, judge := guess.Bind(), sampleJudge()
	wf, err := workflow.NewBuilder(binding).AddEdge(binding, judge).AddEdge(judge, binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestSample_Step3_NumberGuessingLoop(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			stream, err := environment.env.RunStreaming(t.Context(), sampleGuessLoopWorkflow(t), sampleNumberInit)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close(t.Context()) }()
			found := false
			for event, err := range stream.WatchStream(t.Context()) {
				if err != nil {
					t.Fatal(err)
				}
				if failure, ok := event.(workflow.ErrorEvent); ok {
					t.Fatal(failure.Error)
				}
				if output, ok := event.(workflow.OutputEvent); ok {
					if output.Output != "Guessed the number: 42" {
						t.Fatalf("result = %v", output.Output)
					}
					found = true
					break
				}
			}
			if !found {
				t.Fatal("workflow did not yield its guessed number")
			}
		})
	}
}

func sampleGuessRequestWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	port := workflow.RequestPort{ID: "GuessNumber", Request: reflect.TypeFor[sampleNumberSignal](), Response: reflect.TypeFor[int]()}
	binding, judge := port.Bind(), sampleJudge()
	wf, err := workflow.NewBuilder(binding).AddEdge(binding, judge).
		AddEdge(judge, binding, workflow.WithEdgeCondition(func(value any) bool {
			signal, ok := value.(sampleNumberSignal)
			return ok && signal != sampleNumberMatched
		})).
		WithOutputFrom(judge).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func sampleGuessPrompt(previous string, signal sampleNumberSignal) string {
	switch signal {
	case sampleNumberAbove:
		return "Your guess was too high. Try again."
	case sampleNumberBelow:
		return "Your guess was too low. Try again."
	case sampleNumberMatched:
		return "You guessed correctly! You Win!"
	default:
		return previous
	}
}

type sampleGuessReply struct {
	prompt string
	guess  int
}

func driveSampleGuess(t *testing.T, stream *inproc.StreamingRun, replies []sampleGuessReply, position *int, prompt *string, maxStep int, snapshots map[workflow.CheckpointInfo]string) ([]workflow.CheckpointInfo, bool) {
	t.Helper()
	var requests []*workflow.ExternalRequest
	var checkpoints []workflow.CheckpointInfo
	for event, err := range stream.WatchStream(t.Context()) {
		if err != nil {
			t.Fatal(err)
		}
		switch event := event.(type) {
		case workflow.ErrorEvent:
			t.Fatal(event.Error)
		case workflow.ExecutorFailedEvent:
			t.Fatal(event.Error)
		case workflow.OutputEvent:
			if signal, ok := event.Output.(sampleNumberSignal); ok {
				*prompt = sampleGuessPrompt(*prompt, signal)
			}
		case workflow.RequestInfoEvent:
			requests = append(requests, event.Request)
		case workflow.SuperStepCompletedEvent:
			if event.CompletionInfo != nil && event.CompletionInfo.CheckpointInfo != nil {
				info := *event.CompletionInfo.CheckpointInfo
				checkpoints = append(checkpoints, info)
				if snapshots != nil {
					snapshots[info] = *prompt
				}
			}
			if maxStep > 0 && event.StepNumber >= maxStep-1 {
				return checkpoints, false
			}
			for _, request := range requests {
				if *position >= len(replies) {
					t.Fatal("unexpected extra request")
				}
				reply := replies[*position]
				actualPrompt := *prompt
				if actualPrompt == "" {
					actualPrompt = "Guess the number."
				}
				if actualPrompt != reply.prompt {
					t.Fatalf("request %d prompt = %q, want %q", *position, actualPrompt, reply.prompt)
				}
				*position++
				response, err := request.CreateResponse(reply.guess)
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.SendResponse(t.Context(), response); err != nil {
					t.Fatal(err)
				}
			}
			requests = nil
		}
	}
	return checkpoints, true
}

func TestSample_Step4_ExternalGuessRequests(t *testing.T) {
	replies := []sampleGuessReply{{"Guess the number.", 50}, {"Your guess was too high. Try again.", 23}, {"Your guess was too low. Try again.", 42}}
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			stream, err := environment.env.RunStreaming(t.Context(), sampleGuessRequestWorkflow(t), sampleNumberInit)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close(t.Context()) }()
			position, prompt := 0, ""
			_, completed := driveSampleGuess(t, stream, replies, &position, &prompt, 0, nil)
			if !completed || prompt != "You guessed correctly! You Win!" || position != len(replies) {
				t.Fatalf("result = %q, completed=%t, replies=%d", prompt, completed, position)
			}
		})
	}
}

type sampleJSONStore struct {
	values map[workflow.CheckpointInfo]json.RawMessage
	index  []workflow.CheckpointInfo
}

func (store *sampleJSONStore) CreateCheckpoint(_ context.Context, sessionID string, value json.RawMessage, _ *workflow.CheckpointInfo) (workflow.CheckpointInfo, error) {
	info := workflow.NewCheckpointInfo(sessionID)
	if store.values == nil {
		store.values = map[workflow.CheckpointInfo]json.RawMessage{}
	}
	store.values[info] = slices.Clone(value)
	store.index = append(store.index, info)
	return info, nil
}
func (store *sampleJSONStore) RetrieveCheckpoint(_ context.Context, _ string, info workflow.CheckpointInfo) (json.RawMessage, error) {
	value, ok := store.values[info]
	if !ok {
		return nil, fmt.Errorf("checkpoint %v not found", info)
	}
	return slices.Clone(value), nil
}
func (store *sampleJSONStore) RetrieveIndex(_ context.Context, sessionID string, _ *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	var result []workflow.CheckpointInfo
	for _, info := range store.index {
		if info.SessionID == sessionID {
			result = append(result, info)
		}
	}
	return result, nil
}

func runSampleGuessCheckpoint(t *testing.T, environment *inproc.ExecutionEnvironment, rehydrate, useJSON bool) {
	t.Helper()
	manager := checkpoint.NewInMemoryManager()
	if useJSON {
		manager = checkpoint.NewJSONManager(&sampleJSONStore{})
	}
	environment = environment.WithCheckpointing(manager)
	wf := sampleGuessRequestWorkflow(t)
	stream, err := environment.RunStreaming(t.Context(), wf, sampleNumberInit)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close(t.Context()) }()
	replies := []sampleGuessReply{{"Guess the number.", 50}, {"Your guess was too high. Try again.", 23}, {"Your guess was too high. Try again.", 23}, {"Your guess was too low. Try again.", 42}}
	position, prompt := 0, ""
	snapshots := map[workflow.CheckpointInfo]string{}
	checkpoints, completed := driveSampleGuess(t, stream, replies, &position, &prompt, 6, snapshots)
	if completed || len(checkpoints) != 6 {
		t.Fatalf("first phase completed=%t, checkpoints=%d, want false/6", completed, len(checkpoints))
	}
	target := checkpoints[2]
	quiescentEvents := readStreamToHalt(t, t.Context(), stream)
	if hasErrorEvents(quiescentEvents) {
		t.Fatalf("workflow failed while reaching a restore boundary: %v", quiescentEvents)
	}
	if rehydrate {
		if err := stream.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		stream, err = environment.ResumeStreaming(t.Context(), wf, target)
		if err != nil {
			t.Fatal(err)
		}
	} else if err := stream.RestoreCheckpoint(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	prompt = snapshots[target]
	checkpoints, completed = driveSampleGuess(t, stream, replies, &position, &prompt, 0, snapshots)
	if !completed || prompt != "You guessed correctly! You Win!" || position != len(replies) {
		t.Fatalf("restored result=%q, completed=%t, replies=%d", prompt, completed, position)
	}
	if len(checkpoints) < 6 || len(checkpoints) > 7 {
		t.Fatalf("resumed checkpoints=%d, want 6 or 7", len(checkpoints))
	}
}

func TestSample_Step5_InPlaceCheckpointRestore(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) { runSampleGuessCheckpoint(t, environment.env, false, false) })
	}
}
func TestSample_Step5a_RehydrateCheckpointRestore(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) { runSampleGuessCheckpoint(t, environment.env, true, false) })
	}
}
func TestSample_Step5b_JSONCheckpointRestore(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) { runSampleGuessCheckpoint(t, environment.env, true, true) })
	}
}

func sampleEchoAgent(id, name, prefix string) *agent.Agent {
	return agent.New(agent.ProviderConfig{ProviderName: "sample-echo", Run: func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, msg := range messages {
				if msg.Role != message.RoleUser || msg.Contents.Text() == "" {
					continue
				}
				if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, AgentID: id, AuthorName: name, Contents: message.Contents{&message.TextContent{Text: prefix + msg.Contents.Text()}}}, nil) {
					return
				}
			}
		}
	}}, agent.Config{ID: id, Name: name, HistoryProvider: agent.NewHistoryProvider(agent.HistoryProviderConfig{SourceID: "noop-history"})})
}

func sampleGroupChatWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	hello := agent.New(agent.ProviderConfig{ProviderName: "sample-hello", Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, AgentID: "HelloAgent", AuthorName: "HelloAgent", Contents: message.Contents{&message.TextContent{Text: "Hello World!"}}}, nil)
		}
	}}, agent.Config{ID: "HelloAgent", Name: "HelloAgent"})
	wf, err := agentworkflow.NewGroupChatWorkflowBuilder(func(agents []*agent.Agent) *agentworkflow.GroupChatManager {
		return agentworkflow.NewRoundRobinGroupChatManager(agents, agentworkflow.RoundRobinGroupChatOptions{MaximumIterationCount: 2})
	}, hello, sampleEchoAgent("echo", "echo", "You said: ")).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestSample_Step6_GroupChat(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			stream, err := environment.env.RunStreaming(t.Context(), sampleGroupChatWorkflow(t), []*message.Message{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close(t.Context()) }()
			if sent, err := stream.TrySendMessage(t.Context(), workflow.TurnToken{EmitEvents: new(true)}); err != nil || !sent {
				t.Fatalf("send token = %t, %v", sent, err)
			}
			var lines []string
			for event, err := range stream.WatchStream(t.Context()) {
				if err != nil {
					t.Fatal(err)
				}
				if output, ok := event.(workflow.OutputEvent); ok {
					if update, ok := output.Output.(*agent.ResponseUpdate); ok {
						lines = append(lines, fmt.Sprintf("%s: %s", output.ExecutorID, update.String()))
					}
				}
				if failure, ok := event.(workflow.ErrorEvent); ok {
					t.Fatal(failure.Error)
				}
			}
			if len(lines) != 2 || !strings.Contains(lines[0], "HelloAgent: Hello World!") || !strings.Contains(lines[1], "echo: You said: Hello World!") {
				t.Fatalf("group chat lines = %v", lines)
			}
		})
	}
}

type sampleTextRequest struct{ Text, TaskID string }
type sampleTextResult struct {
	TaskID, Text         string
	WordCount, CharCount int
}
type sampleTextState struct {
	Results []sampleTextResult
	Pending map[string]bool
}

func sampleTextProcessingWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	process := workflow.NewExecutor("TextProcessor", func(request sampleTextRequest) sampleTextResult {
		count := 0
		for _, word := range strings.Split(request.Text, " ") {
			if word != "" {
				count++
			}
		}
		return sampleTextResult{TaskID: request.TaskID, Text: request.Text, WordCount: count, CharCount: len(request.Text)}
	}).SetCrossRunShareable(true).Bind()
	child, err := workflow.NewBuilder(process).WithOutputFrom(process).Build()
	if err != nil {
		t.Fatal(err)
	}
	textProcessor := inproc.BindSubworkflowAsExecutor(child, "TextProcessor")
	orchestrator := workflow.BindNewExecutorFunc("TextProcessingOrchestrator", func(_ string, id string) (*workflow.Executor, error) {
		cache := &workflow.StatefulExecutorCache[*sampleTextState]{StateKey: "State", InitialStateFactory: func() *sampleTextState { return &sampleTextState{Pending: map[string]bool{}} }}
		return &workflow.Executor{
			ID: id, ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				pb.SendsMessageType(reflect.TypeFor[sampleTextRequest]())
				pb.YieldsOutputType(reflect.TypeFor[[]sampleTextResult]())
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[[]string](), nil, func(ctx *workflow.Context, value any) (any, error) {
					return nil, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state *sampleTextState) (*sampleTextState, error) {
						for i, text := range value.([]string) {
							request := sampleTextRequest{Text: text, TaskID: fmt.Sprintf("Task%d", i)}
							state.Pending[request.TaskID] = true
							if err := ctx.SendMessage("", request); err != nil {
								return state, err
							}
						}
						return state, nil
					})
				})
				pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleTextResult](), nil, func(ctx *workflow.Context, value any) (any, error) {
					return nil, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state *sampleTextState) (*sampleTextState, error) {
						result := value.(sampleTextResult)
						if state.Pending[result.TaskID] {
							delete(state.Pending, result.TaskID)
							state.Results = append(state.Results, result)
						}
						if len(state.Pending) == 0 {
							return state, ctx.YieldOutput(state.Results)
						}
						return state, nil
					})
				})
				return pb, nil
			},
		}, nil
	})
	orchestrator.SupportsConcurrentSharedExecution = true
	parent, err := workflow.NewBuilder(orchestrator).AddEdge(orchestrator, textProcessor).AddEdge(textProcessor, orchestrator).WithOutputFrom(orchestrator).Build()
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

func TestSample_Step8_TextProcessingSubworkflow(t *testing.T) {
	texts := []string{
		"Hello world! This is a simple test.",
		"Python is a powerful programming language used for many applications.",
		"Short text.",
		"This is a longer text with multiple sentences. It contains more words and characters. We use it to test our text processing workflow.",
		"",
		"   Spaces   around   text   ",
	}
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			run, err := environment.env.Run(t.Context(), sampleTextProcessingWorkflow(t), texts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = run.Close(t.Context()) }()
			events := slices.Collect(run.OutgoingEvents())
			if hasErrorEvents(events) {
				t.Fatalf("workflow errors: %v", events)
			}
			if status, err := run.GetStatus(t.Context()); err != nil || status != inproc.RunStatusIdle {
				t.Fatalf("status = %v, %v", status, err)
			}
			outputs := outputEvents(events)
			if len(outputs) != 1 {
				t.Fatalf("outputs = %v, want one", outputs)
			}
			results, ok := outputs[0].Output.([]sampleTextResult)
			if !ok || len(results) != len(texts) {
				t.Fatalf("results = %#v", outputs[0].Output)
			}
			slices.SortFunc(results, func(a, b sampleTextResult) int { return strings.Compare(a.TaskID, b.TaskID) })
			for i, text := range texts {
				wordCount := 0
				for _, word := range strings.Split(text, " ") {
					if word != "" {
						wordCount++
					}
				}
				expected := sampleTextResult{TaskID: fmt.Sprintf("Task%d", i), Text: text, WordCount: wordCount, CharCount: len(text)}
				if results[i] != expected {
					t.Errorf("result[%d] = %#v, want %#v", i, results[i], expected)
				}
			}
		})
	}
}

func TestSample_Step10_SequentialWorkflowAsAgent(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			wf, err := agentworkflow.NewSequentialWorkflowBuilder(sampleEchoAgent("echo", "Echo", "")).Build()
			if err != nil {
				t.Fatal(err)
			}
			host, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{Config: agent.Config{ID: "echo-workflow", Name: "EchoW"}, Environment: environment.env})
			if err != nil {
				t.Fatal(err)
			}
			session, err := host.CreateSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, input := range []string{"1", "2", "3"} {
				response, err := host.RunText(t.Context(), input, agent.WithSession(session)).Collect()
				if err != nil {
					t.Fatal(err)
				}
				for _, msg := range response.Messages {
					lines = append(lines, fmt.Sprintf("%s: %s", msg.AuthorName, msg.Contents.Text()))
				}
			}
			if !slices.Equal(lines, []string{"Echo: 1", "Echo: 2", "Echo: 3"}) {
				t.Fatalf("lines = %v", lines)
			}
		})
	}
}

func TestSample_Step11_ConcurrentWorkflowAsAgent(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			wf, err := agentworkflow.NewConcurrentWorkflowBuilder(sampleEchoAgent("echo-1", "Echo1", ""), sampleEchoAgent("echo-2", "Echo2", "")).Build()
			if err != nil {
				t.Fatal(err)
			}
			host, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{Config: agent.Config{ID: "echo-workflow", Name: "EchoW"}, Environment: environment.env})
			if err != nil {
				t.Fatal(err)
			}
			session, err := host.CreateSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var lines, expected []string
			for _, input := range []string{"1", "2", "3"} {
				response, err := host.RunText(t.Context(), input, agent.WithSession(session)).Collect()
				if err != nil {
					t.Fatal(err)
				}
				for _, msg := range response.Messages {
					lines = append(lines, fmt.Sprintf("%s: %s", msg.AuthorName, msg.Contents.Text()))
				}
				for i := 1; i <= 2; i++ {
					expected = append(expected, fmt.Sprintf("Echo%d: %s", i, input))
				}
			}
			slices.Sort(lines)
			slices.Sort(expected)
			if !slices.Equal(lines, expected) {
				t.Fatalf("lines = %v, want %v", lines, expected)
			}
		})
	}
}

func sampleEchoSubworkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	executor := &workflow.Executor{ID: "OutputMessagesExecutor", ImplementationID: "sample.OutputMessagesExecutor", CrossRunShareable: true}
	messageworkflow.Configure(executor, &messageworkflow.Options{
		StateKey: "OutputMessagesExecutor.State", StringMessageRole: message.RoleUser,
		TakeTurnHandler: func(ctx *workflow.Context, _ workflow.TurnToken, messages []*message.Message) error {
			return ctx.YieldOutput(messages)
		},
	})
	executor.Extend(&workflow.Executor{ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
		return pb.YieldsOutputType(reflect.TypeFor[[]*message.Message]()), nil
	}})
	binding := executor.Bind()
	child, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatal(err)
	}
	host := inproc.BindSubworkflowAsExecutor(child, "EchoSubworkflow")
	parent, err := workflow.NewBuilder(host).WithOutputFrom(host).Build()
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

func TestSample_Step13_SubworkflowCheckpointResume(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			env := environment.env.WithCheckpointing(checkpoint.NewInMemoryManager())
			var resumeFrom *workflow.CheckpointInfo
			for step := 1; step <= 2; step++ {
				wf := sampleEchoSubworkflow(t)
				input := fmt.Sprintf("[%d] Hello, World!", step)
				var stream *inproc.StreamingRun
				var err error
				if resumeFrom == nil {
					stream, err = env.RunStreaming(t.Context(), wf, input)
				} else {
					stream, err = env.ResumeStreaming(t.Context(), wf, *resumeFrom)
					if err == nil {
						_, err = stream.TrySendMessage(t.Context(), input)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if sent, err := stream.TrySendMessage(t.Context(), workflow.TurnToken{}); err != nil || !sent {
					t.Fatalf("send token = %t, %v", sent, err)
				}
				events := readStreamToHalt(t, t.Context(), stream)
				if err := stream.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if hasErrorEvents(events) {
					t.Fatalf("workflow errors: %v", events)
				}
				var lines []string
				for _, event := range events {
					switch event := event.(type) {
					case workflow.OutputEvent:
						if messages, ok := event.Output.([]*message.Message); ok {
							for _, msg := range messages {
								lines = append(lines, fmt.Sprintf("%s: %s", event.ExecutorID, msg.Contents.Text()))
							}
						}
					case workflow.SuperStepCompletedEvent:
						if event.CompletionInfo != nil && event.CompletionInfo.CheckpointInfo != nil {
							resumeFrom = new(*event.CompletionInfo.CheckpointInfo)
						}
					}
				}
				if len(lines) != 1 || !strings.Contains(lines[0], "EchoSubworkflow: "+input) || resumeFrom == nil {
					t.Fatalf("lines/checkpoint = %v/%v", lines, resumeFrom)
				}
			}
		})
	}
}

func TestSample_Step13a_SubworkflowAsAgentSessionResume(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) {
			var session *agent.Session
			for step := 1; step <= 2; step++ {
				host, err := agentworkflow.NewAgent(sampleEchoSubworkflow(t), agentworkflow.AgentConfig{Config: agent.Config{ID: "echo-workflow", Name: "EchoW"}, Environment: environment.env, IncludeOutputsInResponse: true})
				if err != nil {
					t.Fatal(err)
				}
				if session == nil {
					session, err = host.CreateSession(t.Context())
					if err != nil {
						t.Fatal(err)
					}
				}
				input := fmt.Sprintf("[%d] Hello, World!", step)
				response, err := host.RunText(t.Context(), input, agent.WithSession(session)).Collect()
				if err != nil {
					t.Fatal(err)
				}
				if len(response.Messages) != 1 || !strings.Contains(fmt.Sprintf("%s: %s", response.Messages[0].AuthorName, response.Messages[0].Contents.Text()), ": "+input) {
					t.Fatalf("response = %#v", response)
				}
			}
		})
	}
}

func TestSampleWorkflowInfos_MatchSelfAndDistinguishSamples(t *testing.T) {
	workflows := []*workflow.Workflow{sampleSequentialWorkflow(t), sampleSpamWorkflow(t), sampleGuessLoopWorkflow(t), sampleGuessRequestWorkflow(t), sampleGroupChatWorkflow(t)}
	for _, wf := range workflows {
		info := internalcheckpoint.NewWorkflowInfo(wf)
		if !info.Match(wf) {
			t.Fatal("sample workflow info does not match its workflow")
		}
	}
	info := internalcheckpoint.NewWorkflowInfo(workflows[0])
	if info.Match(workflows[1]) {
		t.Fatal("sequential workflow metadata matched spam workflow")
	}
}

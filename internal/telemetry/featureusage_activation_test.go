// Copyright (c) Microsoft. All rights reserved.

package telemetry_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"iter"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	aguiSSEClient "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/provider/anthropicprovider"
	"github.com/microsoft/agent-framework-go/provider/copilotprovider"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/provider/geminiprovider"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/microsoft/agent-framework-go/tool/shelltool"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"
)

var featureActivationCases = []struct {
	name     string
	features []int
	env      []string
}{
	{"agent", []int{0}, nil},
	{"agent-session", []int{0, 13}, nil},
	{"history-provide", []int{13}, nil},
	{"history-store", []int{13}, nil},
	{"compaction-context", []int{9}, nil},
	{"compaction-history", []int{9}, nil},
	{"compaction-store", []int{9}, nil},
	{"skills", []int{8, 16}, nil},
	{"in-memory-skills", []int{16}, nil},
	{"file-skills", []int{15}, nil},
	{"tool-auto-call", nil, nil},
	{"tool-auto-call-disabled", nil, nil},
	{"tool-auto-call-invalid", nil, nil},
	{"tool-approval", []int{3}, nil},
	{"todo", []int{10}, nil},
	{"agent-mode", []int{11}, nil},
	{"loop", nil, nil},
	{"loop-invalid", nil, nil},
	{"workflow", []int{2}, nil},
	{"workflow-invalid", nil, nil},
	{"sequential", []int{2, 32}, nil},
	{"sequential-invalid", nil, nil},
	{"concurrent", []int{2, 33}, nil},
	{"group-chat", []int{2, 34}, nil},
	{"mcp", []int{14}, nil},
	{"shell", []int{69}, nil},
	{"foundry-memory", []int{50}, nil},
	{"hosting-a2a", []int{73}, nil},
	{"hosting-ag-ui", []int{63}, nil},
	{"a2a", []int{0, 13, 62, 73}, nil},
	{"ag-ui", []int{0, 63}, nil},
	{"anthropic", []int{0, 55}, nil},
	{"gemini", []int{0}, nil},
	{"copilot", []int{0, 57}, nil},
	{"openai-chat", []int{0, 54}, nil},
	{"openai-chat-stream", []int{0, 54}, nil},
	{"openai-responses", []int{0, 54}, nil},
	{"openai-responses-stream", []int{0, 54}, nil},
	{"openai-session", []int{0, 13, 54}, nil},
	{"openai-continuation", []int{0, 54}, nil},
	{"openai-live-mask", []int{0, 54, 127}, nil},
	{"openai-third-party", []int{0, 54}, nil},
	{"openai-same-origin-redirect", []int{0, 54}, nil},
	{"openai-cross-origin-redirect", []int{0, 54}, nil},
	{"openai-mask-disabled", nil, []string{featureMaskDisabledEnvVar + "=true"}},
	{"openai-user-agent-disabled", []int{0, 54}, []string{userAgentTelemetryDisabledEnvVar + "=true"}},
	{"foundry-model", []int{0, 48, 54}, nil},
	{"foundry-server", []int{0, 48, 49, 54}, nil},
	{"foundry-inference", []int{0, 48, 54}, nil},
	{"foundry-openai-origin", []int{0, 48, 54}, nil},
	{"foundry-cross-origin-redirect", []int{0, 48, 54}, nil},
}

func TestFeatureUsageActivation(t *testing.T) {
	for _, tc := range featureActivationCases {
		t.Run(tc.name, func(t *testing.T) {
			got := runHelper(t, "activation:"+tc.name, tc.env...)
			if want := featureComment(tc.features...); got != want {
				t.Fatalf("feature comment = %q, want %q", got, want)
			}
		})
	}
}

func TestAnthropicFeatureUsageNonStreaming(t *testing.T) {
	assertFeatureUsagePort(t, "port-provider-anthropic", 0, 55)
}

func TestAnthropicFeatureUsageStreamingIsCold(t *testing.T) {
	assertFeatureUsagePort(t, "port-provider-anthropic-cold", 0, 55)
}

func TestOpenAIFeatureUsageNonStreaming(t *testing.T) {
	assertFeatureUsagePort(t, "port-provider-openai", 0, 54)
}

func TestOpenAIFeatureUsageStreamingIsCold(t *testing.T) {
	assertFeatureUsagePort(t, "port-provider-openai-cold", 0, 54)
}

func TestCopilotFeatureUsageCancelledRun(t *testing.T) {
	assertFeatureUsagePort(t, "copilot-cancelled-port", 0, 57)
}

func TestCopilotFeatureUsageStreamingIsCold(t *testing.T) {
	assertFeatureUsagePort(t, "copilot-cold-port", 0, 57)
}

func TestShellFeatureUsageRun(t *testing.T) {
	assertFeatureUsagePort(t, "shell", 69)
}

func TestCoreAgentFeatureUsageNonStreaming(t *testing.T) {
	assertFeatureUsagePort(t, "agent", 0)
}

func TestCoreAgentFeatureUsageStreamingIsCold(t *testing.T) {
	assertFeatureUsagePort(t, "agent-cold-port", 0)
}

func TestToolApprovalFeatureUsageOnlyApproval(t *testing.T) {
	assertFeatureUsagePort(t, "tool-approval", 3)
}

func TestWorkflowFeatureUsageInvalidBuild(t *testing.T) {
	assertFeatureUsagePort(t, "workflow-unbound-invalid")
}

func TestFoundryFeatureUsageServerRequestMarksAgentAndClient(t *testing.T) {
	assertFeatureUsagePort(t, "port-foundry-server", 0, 48, 49, 54)
}

func TestFoundryFeatureUsageModelStreamingIsCold(t *testing.T) {
	assertFeatureUsagePort(t, "port-foundry-model-cold", 0, 48, 54)
}

func TestFoundryMemoryFeatureUsageFirstHook(t *testing.T) {
	assertFeatureUsagePort(t, "port-foundry-memory-hook", 50)
}

func assertFeatureUsagePort(t *testing.T, name string, features ...int) {
	t.Helper()
	got := runHelper(t, "activation:"+name)
	if want := featureComment(features...); got != want {
		t.Fatalf("feature comment = %q, want %q", got, want)
	}
}

func TestFeatureUsageRegistryMatchesDocumentation(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "featureusage.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	indexes := map[int]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Feature") {
				continue
			}
			value, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || value.Kind != token.INT {
				t.Fatalf("%s must declare an explicit feature index", name.Name)
			}
			index, err := strconv.Atoi(value.Value)
			if err != nil || index < 0 || index >= 128 {
				t.Fatalf("%s has invalid feature index %q", name.Name, value.Value)
			}
			if previous, ok := indexes[index]; ok {
				t.Fatalf("index %d is shared by %s and %s", index, previous, name.Name)
			}
			indexes[index] = name.Name
		}
		return true
	})
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "feature-usage-telemetry.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| ([0-9]+) \\| `([^`]+)` \\|").FindAllStringSubmatch(string(data), -1)
	if len(rows) != len(indexes) {
		t.Fatalf("registry documents %d indexes, code declares %d", len(rows), len(indexes))
	}
	documented := map[int]bool{}
	ids := map[string]bool{}
	covered := map[int]bool{}
	for _, tc := range featureActivationCases {
		for _, index := range tc.features {
			covered[index] = true
		}
	}
	for _, row := range rows {
		index, err := strconv.Atoi(row[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := indexes[index]; !ok || documented[index] || ids[row[2]] {
			t.Fatalf("invalid or duplicate registry row %v", row)
		}
		if !covered[index] {
			t.Errorf("%s has no public-API activation test", indexes[index])
		}
		documented[index], ids[row[2]] = true, true
	}
}

func featureComment(indexes ...int) string {
	var mask big.Int
	for _, index := range indexes {
		mask.SetBit(&mask, index, 1)
	}
	if mask.Sign() == 0 {
		return ""
	}
	return "(feat=v1." + mask.Text(16) + ")"
}

func assertNoFeatureUsage(t *testing.T) {
	t.Helper()
	if got := telemetry.ApplyToUserAgent("", true); got != "" {
		t.Fatalf("construction marked features: %s", got)
	}
}

func activationRun(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		yield(&agent.ResponseUpdate{
			Role:      message.RoleAssistant,
			MessageID: "message-1",
			Contents:  message.NewText("ok").Contents,
		}, nil)
	}
}

func activationAgent() *agent.Agent {
	return agent.New(agent.ProviderConfig{Run: activationRun}, agent.Config{})
}

type activationCompactionStrategy struct{}

func (activationCompactionStrategy) Compact(context.Context, *compaction.MessageIndex) (bool, error) {
	return false, nil
}

func runFeatureActivation(t *testing.T, name string) {
	t.Helper()
	if strings.HasPrefix(name, "port-provider-") {
		runProviderFeatureUsagePort(t, name)
		return
	}
	if strings.HasPrefix(name, "port-foundry-") {
		runFoundryFeatureUsagePort(t, name)
		return
	}
	if strings.HasPrefix(name, "openai-") || strings.HasPrefix(name, "foundry-") && name != "foundry-memory" {
		runAdapterActivation(t, name)
		return
	}
	ctx := t.Context()
	invoking := agent.InvokingContext{
		Messages: []*message.Message{message.NewText("hello")},
		Options:  []agent.Option{agent.WithSession(new(agent.Session))},
	}
	var activate func() error
	switch name {
	case "agent", "agent-session":
		a := activationAgent()
		activate = func() error {
			var options []agent.Option
			if name == "agent-session" {
				options = append(options, agent.WithSession(new(agent.Session)))
			}
			_, err := a.RunText(ctx, "hello", options...).Collect()
			return err
		}
	case "agent-cold-port":
		calls := 0
		a := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			calls++
			return func(func(*agent.ResponseUpdate, error) bool) {}
		}}, agent.Config{})
		activate = func() error {
			stream := a.RunText(ctx, "hello", agent.Stream(true))
			assertNoFeatureUsage(t)
			if calls != 0 {
				t.Fatal("unconsumed stream invoked the provider")
			}
			for update, err := range stream {
				if err != nil {
					return err
				}
				t.Fatalf("empty stream yielded %#v", update)
			}
			if calls != 1 {
				t.Fatalf("provider calls = %d, want 1", calls)
			}
			return nil
		}
	case "history-provide", "history-store":
		p := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
		activate = func() error {
			if name == "history-store" {
				return p.Invoked(ctx, agent.InvokedContext{RequestMessages: invoking.Messages, Options: invoking.Options})
			}
			_, err := p.Invoking(ctx, invoking)
			return err
		}
	case "compaction-context":
		p := compaction.NewContextProvider(compaction.ContextProviderConfig{Strategy: activationCompactionStrategy{}})
		activate = func() error { _, _, err := p.Invoking(ctx, invoking); return err }
	case "compaction-history", "compaction-store":
		p := compaction.NewHistoryProvider(compaction.HistoryProviderConfig{Strategy: activationCompactionStrategy{}})
		activate = func() error {
			if name == "compaction-store" {
				return p.Invoked(ctx, agent.InvokedContext{RequestMessages: invoking.Messages, Options: invoking.Options})
			}
			_, err := p.Invoking(ctx, invoking)
			return err
		}
	case "skills", "in-memory-skills":
		skill := &skills.Skill{
			Frontmatter: skills.Frontmatter{Name: "demo", Description: "A test skill"},
			GetContent:  func(context.Context) (string, error) { return "Test instructions", nil },
		}
		source := skills.NewInMemorySource(skill)
		if name == "in-memory-skills" {
			activate = func() error { _, err := source.Skills(ctx); return err }
		} else {
			p := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
			activate = func() error { _, _, err := p.Invoking(ctx, invoking); return err }
		}
	case "file-skills":
		source := fsskills.NewSource(fstest.MapFS{})
		activate = func() error { _, err := source.Skills(ctx); return err }
	case "todo":
		p := todo.New(nil)
		activate = func() error { _, _, err := p.Invoking(ctx, invoking); return err }
	case "agent-mode":
		p := agentmode.New(agentmode.Config{})
		activate = func() error { _, _, err := p.Invoking(ctx, invoking); return err }
	case "tool-auto-call", "tool-auto-call-disabled", "tool-auto-call-invalid", "tool-approval", "loop", "loop-invalid":
		var middleware agent.Middleware
		switch name {
		case "tool-auto-call":
			middleware = toolautocall.New(toolautocall.Config{})
		case "tool-auto-call-disabled":
			middleware = toolautocall.New(toolautocall.Config{MaximumIterationsPerRequest: new(0)})
		case "tool-auto-call-invalid":
			middleware = toolautocall.New(toolautocall.Config{MaximumConsecutiveErrorsPerRequest: new(-1)})
		case "tool-approval":
			middleware = toolapproval.New(toolapproval.Config{})
		case "loop":
			middleware = loop.New(loop.Config{Evaluators: []loop.Evaluator{
				loop.EvaluatorFunc(func(context.Context, *loop.Context) (loop.Evaluation, error) { return loop.Stop(), nil }),
			}})
		case "loop-invalid":
			middleware = loop.New(loop.Config{})
		}
		activate = func() error {
			_, err := agent.ResponseStream(middleware.Run(activationRun, ctx, invoking.Messages, invoking.Options...)).Collect()
			return err
		}
	case "workflow", "workflow-invalid":
		builder := workflow.NewBuilder(agentworkflow.New(activationAgent(), agentworkflow.Config{}))
		if name == "workflow-invalid" {
			builder.BindExecutor(agentworkflow.New(activationAgent(), agentworkflow.Config{}))
		}
		activate = func() error { _, err := builder.Build(); return err }
	case "workflow-unbound-invalid":
		builder := workflow.NewBuilder(workflow.ExecutorBinding{ID: "unbound"})
		activate = func() error { _, err := builder.Build(); return err }
	case "sequential", "sequential-invalid":
		var agents []*agent.Agent
		if name != "sequential-invalid" {
			agents = []*agent.Agent{activationAgent()}
		}
		builder := agentworkflow.NewSequentialWorkflowBuilder(agents...)
		activate = func() error { _, err := builder.Build(); return err }
	case "concurrent":
		builder := agentworkflow.NewConcurrentWorkflowBuilder(activationAgent())
		activate = func() error { _, err := builder.Build(); return err }
	case "group-chat":
		builder := agentworkflow.NewGroupChatWorkflowBuilder(func(agents []*agent.Agent) *agentworkflow.GroupChatManager {
			return agentworkflow.NewRoundRobinGroupChatManager(agents, agentworkflow.RoundRobinGroupChatOptions{})
		}, activationAgent())
		activate = func() error { _, err := builder.Build(); return err }
	case "mcp":
		server := mcp.NewServer(&mcp.Implementation{Name: "feature-test", Version: "1"}, nil)
		mcptool.AddTool(server, functool.MustNew(functool.Config{Name: "echo"}, func(context.Context, struct{}) (string, error) {
			return "ok", nil
		}))
		activate = func() error {
			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			serverSession, err := server.Connect(ctx, serverTransport, nil)
			if err != nil {
				return err
			}
			defer func() {
				if err := serverSession.Close(); err != nil {
					t.Error(err)
				}
			}()
			session, err := mcptool.Connect(ctx, clientTransport)
			if err != nil {
				return err
			}
			defer func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			}()
			tools, err := mcptool.ListTools(ctx, session)
			if err != nil {
				return err
			}
			if len(tools) != 1 {
				t.Fatalf("discovered %d tools, want 1", len(tools))
			}
			fn, ok := tools[0].(tool.FuncTool)
			if !ok {
				t.Fatalf("discovered tool is %T", tools[0])
			}
			_, err = fn.Call(ctx, "{}")
			return err
		}
	case "shell":
		local, err := shelltool.NewLocal(shelltool.LocalConfig{Mode: shelltool.ModeStateless})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := local.Close(); err != nil {
				t.Error(err)
			}
		}()
		activate = func() error { _, err := local.Run(ctx, "echo feature-usage"); return err }
	case "foundry-memory":
		wantErr := errors.New("memory request failed")
		client := &http.Client{Transport: activationTransport(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.Header.Get(userAgentKey), "(feat=") {
				t.Error("Projects request received a feature comment")
			}
			return nil, wantErr
		})}
		p := foundryprovider.NewMemoryProvider("https://project.services.ai.azure.com", &azfake.TokenCredential{}, "memory",
			func(*agent.Session) string { return "test-scope" },
			foundryprovider.MemoryProviderConfig{ClientOptions: azcore.ClientOptions{
				Transport: client,
				Retry:     policy.RetryOptions{MaxRetries: -1},
			}})
		activate = func() error {
			err := p.EnsureMemoryStoreCreated(ctx, "chat", "embedding", nil)
			if !errors.Is(err, wantErr) {
				t.Fatalf("provisioning error = %v, want original transport error", err)
			}
			return nil
		}
	case "hosting-a2a":
		executor := a2aprovider.NewExecutor(activationAgent(), a2aprovider.ExecutorConfig{})
		activate = func() error {
			for _, err := range executor.Cancel(ctx, &a2asrv.ExecutorContext{
				TaskID: "task", ContextID: "context", StoredTask: &a2a.Task{ID: "task", ContextID: "context"},
			}) {
				if err != nil {
					return err
				}
			}
			return nil
		}
	case "hosting-ag-ui":
		handler := aguiprovider.NewJSONHTTPHandler(activationAgent(), aguiprovider.HandlerConfig{})
		activate = func() error {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", response.Code)
			}
			return nil
		}
	case "a2a":
		server := httptest.NewServer(a2asrv.NewJSONRPCHandler(a2aprovider.NewHandler(activationAgent(), a2aprovider.ExecutorConfig{})))
		defer server.Close()
		client, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{
			a2a.NewAgentInterface(server.URL, a2a.TransportProtocolJSONRPC),
		})
		if err != nil {
			t.Fatal(err)
		}
		a := a2aprovider.NewAgent(client, a2aprovider.AgentConfig{})
		activate = func() error { _, err := a.RunText(ctx, "hello").Collect(); return err }
	case "ag-ui":
		server := httptest.NewServer(aguiprovider.NewJSONHTTPHandler(activationAgent(), aguiprovider.HandlerConfig{}))
		defer server.Close()
		a := aguiprovider.NewAgent(aguiSSEClient.NewClient(aguiSSEClient.Config{Endpoint: server.URL}), aguiprovider.AgentConfig{})
		activate = func() error { _, err := a.RunText(ctx, "hello").Collect(); return err }
	case "anthropic":
		client := anthropic.NewClient(
			anthropicoption.WithAPIKey("test"),
			anthropicoption.WithHTTPClient(&http.Client{Transport: activationTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.Header.Get(userAgentKey), "(feat=") {
					t.Error("Anthropic request received a feature comment")
				}
				return activationResponse(req, `{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`), nil
			})}),
		)
		a := anthropicprovider.NewAgent(client, anthropicprovider.AgentConfig{Model: "test"})
		activate = func() error { _, err := a.RunText(ctx, "hello").Collect(); return err }
	case "gemini":
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			Backend: genai.BackendGeminiAPI,
			APIKey:  "test",
			HTTPClient: &http.Client{Transport: activationTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.Header.Get(userAgentKey), "(feat=") {
					t.Error("Gemini request received a feature comment")
				}
				return activationResponse(req, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`), nil
			})},
		})
		if err != nil {
			t.Fatal(err)
		}
		a := geminiprovider.NewAgent(client, geminiprovider.AgentConfig{Model: "test"})
		activate = func() error { _, err := a.RunText(ctx, "hello").Collect(); return err }
	case "copilot", "copilot-cancelled-port", "copilot-cold-port":
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		missing := exe + ".missing"
		if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected missing CLI path: %v", err)
		}
		client := copilot.NewClient(&copilot.ClientOptions{
			Connection:      copilot.StdioConnection{Path: missing},
			UseLoggedInUser: new(false),
		})
		a := copilotprovider.NewAgent(client, copilotprovider.AgentConfig{})
		activate = func() error {
			if name == "copilot" {
				if _, err := a.RunText(ctx, "hello").Collect(); err == nil {
					t.Fatal("expected CLI startup failure")
				}
				return nil
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			stream := a.RunText(cancelled, "hello", agent.Stream(name == "copilot-cold-port"))
			if name == "copilot-cold-port" {
				assertNoFeatureUsage(t)
			}
			if _, err := stream.Collect(); !errors.Is(err, context.Canceled) {
				t.Fatalf("run error = %v, want context.Canceled", err)
			}
			return nil
		}
	default:
		t.Fatalf("unknown activation %q", name)
	}
	assertNoFeatureUsage(t)
	err := activate()
	if strings.HasSuffix(name, "-invalid") {
		if err == nil {
			t.Fatal("invalid configuration unexpectedly succeeded")
		}
	} else if err != nil {
		t.Fatal(err)
	}
	fmt.Print(telemetry.ApplyToUserAgent("", true))
}

func runProviderFeatureUsagePort(t *testing.T, name string) {
	t.Helper()
	isAnthropic := strings.Contains(name, "anthropic")
	streaming := strings.HasSuffix(name, "-cold")
	requests := 0
	client := &http.Client{Transport: activationTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		body := `{"id":"chat_test","object":"chat.completion","created":1,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`
		if isAnthropic {
			body = `{"id":"msg_test","type":"message","role":"assistant","model":"test","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
		}
		if streaming {
			body = "data: [DONE]\n\n"
			if isAnthropic {
				body = ""
			}
		}
		response := activationResponse(req, body)
		if streaming {
			response.Header.Set("Content-Type", "text/event-stream")
		}
		return response, nil
	})}
	var a *agent.Agent
	if isAnthropic {
		a = anthropicprovider.NewAgent(anthropic.NewClient(
			anthropicoption.WithAPIKey("test"),
			anthropicoption.WithHTTPClient(client),
			anthropicoption.WithMaxRetries(0),
		), anthropicprovider.AgentConfig{Model: "test-model"})
	} else {
		a = openaiprovider.NewChatCompletionsAgent(openai.NewClient(
			option.WithAPIKey("test"),
			option.WithHTTPClient(client),
			option.WithMaxRetries(0),
		), openaiprovider.AgentConfig{Model: "test-model"})
	}
	assertNoFeatureUsage(t)
	stream := a.RunText(t.Context(), "hello", agent.Stream(streaming))
	if streaming {
		assertNoFeatureUsage(t)
		if requests != 0 {
			t.Fatal("unconsumed stream sent a request")
		}
		for update, err := range stream {
			if err != nil {
				t.Fatal(err)
			}
			t.Fatalf("empty stream yielded %#v", update)
		}
	} else {
		if _, err := stream.Collect(); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	fmt.Print(telemetry.ApplyToUserAgent("", true))
}

func runFoundryFeatureUsagePort(t *testing.T, name string) {
	t.Helper()
	requests := 0
	client := &http.Client{Transport: activationTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if name == "port-foundry-memory-hook" {
			t.Error("empty memory hook sent a request")
			return nil, errors.New("unexpected memory request")
		}
		if name == "port-foundry-server" {
			header := req.Header.Get(userAgentKey)
			want := telemetry.ApplyToUserAgent("", true)
			if want != featureComment(0, 48, 49, 54) || !strings.Contains(header, want) {
				t.Errorf("current request User-Agent = %q, feature comment = %q", header, want)
			}
		}
		body := `{"id":"resp_test","object":"response","created_at":1,"status":"completed","model":"test","output":[]}`
		if name == "port-foundry-model-cold" {
			body = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[]}}\n\n"
		}
		response := activationResponse(req, body)
		if name == "port-foundry-model-cold" {
			response.Header.Set("Content-Type", "text/event-stream")
		}
		return response, nil
	})}
	if name == "port-foundry-memory-hook" {
		p := foundryprovider.NewMemoryProvider("https://test.services.ai.azure.com/api/projects/test",
			&azfake.TokenCredential{}, "memory-store", func(*agent.Session) string { return "scope" },
			foundryprovider.MemoryProviderConfig{ClientOptions: azcore.ClientOptions{Transport: client}})
		assertNoFeatureUsage(t)
		if _, _, err := p.Invoking(t.Context(), agent.InvokingContext{}); err != nil {
			t.Fatal(err)
		}
		if requests != 0 {
			t.Fatalf("memory requests = %d, want 0", requests)
		}
	} else {
		var target foundryprovider.AgentTarget = foundryprovider.ModelDeployment("test-model")
		if name == "port-foundry-server" {
			target = foundryprovider.ServerAgent("test-agent")
		}
		a := foundryprovider.NewAgent("https://test.services.ai.azure.com/api/projects/test", &azfake.TokenCredential{},
			target, foundryprovider.AgentConfig{
				DisableStoreOutput: true,
				OpenAIOptions:      []option.RequestOption{option.WithHTTPClient(client), option.WithMaxRetries(0)},
			})
		assertNoFeatureUsage(t)
		stream := a.RunText(t.Context(), "Hello", agent.Stream(name == "port-foundry-model-cold"))
		if name == "port-foundry-model-cold" {
			assertNoFeatureUsage(t)
			if requests != 0 {
				t.Fatal("unconsumed Foundry stream sent a request")
			}
		}
		if _, err := stream.Collect(); err != nil {
			t.Fatal(err)
		}
		if requests != 1 {
			t.Fatalf("Foundry requests = %d, want 1", requests)
		}
	}
	fmt.Print(telemetry.ApplyToUserAgent("", true))
}

type activationTransport func(*http.Request) (*http.Response, error)

func (transport activationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
	}
	return transport(req)
}

func activationResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func runAdapterActivation(t *testing.T, name string) {
	t.Helper()
	features := []int{0, 54}
	if name == "openai-session" {
		features = append(features, 13)
	}
	isFoundry := strings.HasPrefix(name, "foundry-")
	if isFoundry {
		features = append(features, 48)
		if name == "foundry-server" {
			features = append(features, 49)
		}
	}
	wantComment := featureComment(features...)
	if name == "openai-mask-disabled" {
		wantComment = ""
	}
	emit := name != "openai-third-party" && name != "openai-user-agent-disabled" && name != "foundry-openai-origin" && wantComment != ""
	baseURL := "https://project.openai.azure.com/"
	if isFoundry {
		baseURL = "https://project.services.ai.azure.com/projects/test"
		switch name {
		case "foundry-inference":
			baseURL = "https://model.inference.ai.azure.com/projects/test"
		case "foundry-openai-origin":
			baseURL = "https://project.openai.azure.com/projects/test"
		}
	} else if name == "openai-third-party" {
		baseURL = "https://api.openai.com/"
	}
	redirect := strings.Contains(name, "-redirect")
	crossOrigin := strings.Contains(name, "-cross-origin-")
	chat := name == "openai-chat" || name == "openai-chat-stream"
	streaming := strings.HasSuffix(name, "-stream")
	requests, redirects := 0, 0
	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			redirects++
			return nil
		},
		Transport: new(activationTransport(func(req *http.Request) (*http.Response, error) {
			requests++
			header := req.Header.Get(userAgentKey)
			if emit {
				if !strings.HasSuffix(header, " "+wantComment) || strings.Count(header, "(feat=") != 1 {
					t.Errorf("request User-Agent = %q, want one current %s", header, wantComment)
				}
			} else if strings.Contains(header, "(feat=") {
				t.Errorf("request User-Agent = %q, want no feature comment", header)
			}
			if !strings.Contains(header, "my-app/1.0") {
				t.Errorf("request lost caller User-Agent: %q", header)
			}
			if name == "openai-user-agent-disabled" && strings.Contains(header, "agent-framework-go/") {
				t.Errorf("disabled framework User-Agent was emitted: %q", header)
			}
			if redirect && requests == 1 {
				destination := "/finish"
				if crossOrigin {
					destination = "https://example.test/finish"
				}
				response := activationResponse(req, "")
				response.StatusCode = http.StatusTemporaryRedirect
				response.Header.Set("Location", destination)
				return response, nil
			}
			if streaming {
				body := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"output\":[]}}\n\n"
				if chat {
					body = "data: {\"id\":\"chat_test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				}
				response := activationResponse(req, body)
				response.Header.Set("Content-Type", "text/event-stream")
				return response, nil
			}
			body := `{"id":"resp_test","object":"response","created_at":1,"status":"completed","model":"test","output":[]}`
			if chat {
				body = `{"id":"chat_test","object":"chat.completion","created":1,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
			}
			return activationResponse(req, body), nil
		})),
	}
	originalTransport := httpClient.Transport
	options := []option.RequestOption{
		option.WithHTTPClient(httpClient),
		option.WithHeader(userAgentKey, "my-app/1.0 (feat=v2.AB)"),
		option.WithMaxRetries(0),
	}
	var a *agent.Agent
	if isFoundry {
		var target foundryprovider.AgentTarget = foundryprovider.ModelDeployment("test")
		if name == "foundry-server" {
			target = foundryprovider.ServerAgent("test")
		}
		a = foundryprovider.NewAgent(baseURL, &azfake.TokenCredential{}, target, foundryprovider.AgentConfig{
			OpenAIOptions: options, DisableStoreOutput: true,
		})
	} else {
		options = append(options, option.WithAPIKey("test"))
		client := openai.NewClient(append(options, option.WithBaseURL(baseURL))...)
		config := openaiprovider.AgentConfig{Model: "test", DisableStoreOutput: true}
		if chat {
			a = openaiprovider.NewChatCompletionsAgent(client, config)
		} else {
			a = openaiprovider.NewResponsesAgent(client, config)
		}
	}
	assertNoFeatureUsage(t)
	if name == "openai-continuation" {
		continuation := agenttest.NewContinuationToken(t, `{"response_id":"resp_test"}`)
		if _, err := a.Run(t.Context(), nil, agent.WithContinuationToken(continuation)).Collect(); err != nil {
			t.Fatal(err)
		}
	} else {
		runOptions := []agent.Option{agent.Stream(streaming)}
		if name == "openai-session" {
			runOptions = append(runOptions, agent.WithSession(new(agent.Session)))
		}
		_, err := a.RunText(t.Context(), "hello", runOptions...).Collect()
		if crossOrigin {
			if err == nil || requests != 1 || redirects != 1 {
				t.Fatalf("cross-origin redirect: error=%v, requests=%d, callbacks=%d", err, requests, redirects)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if name == "openai-live-mask" {
		telemetry.MarkUsed(127)
		wantComment = featureComment(append(features, 127)...)
		if _, err := a.RunText(t.Context(), "again").Collect(); err != nil {
			t.Fatal(err)
		}
	}
	wantRequests := 1
	if name == "openai-live-mask" || redirect && !crossOrigin {
		wantRequests = 2
	}
	if requests != wantRequests {
		t.Fatalf("transport requests = %d, want %d", requests, wantRequests)
	}
	if redirect && redirects != 1 {
		t.Fatalf("redirect callbacks = %d, want 1", redirects)
	}
	if httpClient.Transport != originalTransport {
		t.Fatal("caller transport was replaced")
	}
	fmt.Print(telemetry.ApplyToUserAgent("", true))
}

// Copyright (c) Microsoft. All rights reserved.

package shelltool_test

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/shelltool"
)

// --------------------------------------------------------------------------
// Result.FormatForModel
// --------------------------------------------------------------------------

func TestResult_FormatForModel_stdout(t *testing.T) {
	r := shelltool.Result{Stdout: "hello", ExitCode: 0}
	got := r.FormatForModel()
	if !strings.Contains(got, "hello") {
		t.Errorf("expected stdout in output, got %q", got)
	}
	if !strings.Contains(got, "exit_code: 0") {
		t.Errorf("expected exit_code: 0, got %q", got)
	}
}

func TestResult_FormatForModel_stderr(t *testing.T) {
	r := shelltool.Result{Stderr: "err msg", ExitCode: 1}
	got := r.FormatForModel()
	if !strings.Contains(got, "stderr: err msg") {
		t.Errorf("expected stderr prefix, got %q", got)
	}
	if !strings.Contains(got, "exit_code: 1") {
		t.Errorf("expected exit_code: 1, got %q", got)
	}
}

func TestResult_FormatForModel_truncated(t *testing.T) {
	r := shelltool.Result{Stdout: "data", Truncated: true, ExitCode: 0}
	got := r.FormatForModel()
	if !strings.Contains(got, "[stdout truncated]") {
		t.Errorf("expected truncated marker, got %q", got)
	}
}

func TestResult_FormatForModel_timedOut(t *testing.T) {
	r := shelltool.Result{TimedOut: true, ExitCode: 124}
	got := r.FormatForModel()
	if !strings.Contains(got, "[command timed out]") {
		t.Errorf("expected timed out marker, got %q", got)
	}
	if !strings.Contains(got, "exit_code: 124") {
		t.Errorf("expected exit_code: 124, got %q", got)
	}
}

// --------------------------------------------------------------------------
// Policy
// --------------------------------------------------------------------------

func TestPolicy_nil_allowsAll(t *testing.T) {
	var p *shelltool.Policy
	allowed, reason := p.Evaluate("rm -rf /")
	if !allowed {
		t.Errorf("nil policy should allow, got reason %q", reason)
	}
}

func TestPolicy_deny_matchingPattern(t *testing.T) {
	p, err := shelltool.NewPolicy(`rm\s+-rf`)
	if err != nil {
		t.Fatal(err)
	}
	allowed, reason := p.Evaluate("rm -rf /tmp/foo")
	if allowed {
		t.Errorf("expected deny, got allowed")
	}
	if !strings.Contains(reason, "denied by policy") {
		t.Errorf("unexpected reason %q", reason)
	}
}

func TestPolicy_deny_noMatch_allows(t *testing.T) {
	p, err := shelltool.NewPolicy(`rm\s+-rf`)
	if err != nil {
		t.Fatal(err)
	}
	allowed, _ := p.Evaluate("echo hello")
	if !allowed {
		t.Errorf("expected allow for non-matching command")
	}
}

func TestNewPolicy_invalidRegex(t *testing.T) {
	_, err := shelltool.NewPolicy(`[invalid`)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

// --------------------------------------------------------------------------
// Tool construction
// --------------------------------------------------------------------------

func TestNew_returnsApprovalRequired_byDefault(t *testing.T) {
	ft := shelltool.New(shelltool.Options{})
	// ApprovalRequiredFunc wraps the inner tool; verify the tool is usable.
	if ft.Name() != "shell" {
		t.Errorf("expected name 'shell', got %q", ft.Name())
	}
	// The wrapper implements ApprovalRequiredTool.
	if _, ok := ft.(tool.ApprovalRequiredTool); !ok {
		t.Error("expected tool to implement ApprovalRequiredTool")
	}
}

func TestNew_acknowledgeUnsafe_noApproval(t *testing.T) {
	ft := shelltool.New(shelltool.Options{AcknowledgeUnsafe: true})
	if _, ok := ft.(tool.ApprovalRequiredTool); ok {
		t.Error("expected no ApprovalRequiredTool when AcknowledgeUnsafe is true")
	}
}

func TestNew_schema_hasCommandField(t *testing.T) {
	ft := shelltool.New(shelltool.Options{AcknowledgeUnsafe: true})
	schema := ft.Schema()
	m, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("expected map schema, got %T", schema)
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties key")
	}
	if _, ok := props["command"]; !ok {
		t.Error("expected 'command' property in schema")
	}
}

func TestMode_zeroValueIsPersistent(t *testing.T) {
	if shelltool.ModePersistent != 0 {
		t.Fatalf("expected ModePersistent to be the zero value, got %d", shelltool.ModePersistent)
	}
}

// --------------------------------------------------------------------------
// Integration: actual shell execution
// --------------------------------------------------------------------------

// skipIfNotPOSIX skips the test on non-POSIX platforms where shell commands
// like `sleep`, `$VAR` expansion, and `exit N` may not behave as expected.
func skipIfNotPOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping POSIX-only test on Windows")
	}
}

func TestCall_echo_defaultPersistent(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := shelltool.New(shelltool.Options{AcknowledgeUnsafe: true})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := out.(string)
	if !ok {
		t.Fatalf("expected string output, got %T", out)
	}
	if !strings.Contains(s, "hello") {
		t.Errorf("expected 'hello' in output, got %q", s)
	}
	if !strings.Contains(s, "exit_code: 0") {
		t.Errorf("expected exit_code: 0, got %q", s)
	}
}

func TestCall_nonZeroExit(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := shelltool.New(shelltool.Options{AcknowledgeUnsafe: true})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"sh -c 'exit 42'"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.(string), "exit_code: 42") {
		t.Errorf("expected exit_code: 42, got %q", out)
	}
}

func TestCall_emptyCommand_error(t *testing.T) {
	ft := shelltool.New(shelltool.Options{AcknowledgeUnsafe: true})
	ctx := tool.Context{Context: t.Context()}
	_, err := ft.Call(ctx, `{"command":""}`)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestCall_policyDeny(t *testing.T) {
	p, _ := shelltool.NewPolicy(`echo`)
	ft := shelltool.New(shelltool.Options{AcknowledgeUnsafe: true, Policy: p})
	ctx := tool.Context{Context: t.Context()}
	_, err := ft.Call(ctx, `{"command":"echo hello"}`)
	if err == nil {
		t.Error("expected error from policy deny")
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCall_timeout(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := shelltool.New(shelltool.Options{
		AcknowledgeUnsafe: true,
		Timeout:           50 * time.Millisecond,
	})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"sleep 10"}`)
	if err != nil {
		t.Fatalf("unexpected error (timeout should surface in result, not as error): %v", err)
	}
	s := out.(string)
	if !strings.Contains(s, "[command timed out]") {
		t.Errorf("expected timed-out marker, got %q", s)
	}
}

func TestCall_persistent_statePersists(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := shelltool.New(shelltool.Options{
		AcknowledgeUnsafe: true,
	})
	ctx := tool.Context{Context: t.Context()}

	// Set a variable in the first call.
	_, err := ft.Call(ctx, `{"command":"MYVAR=hello_persistent"}`)
	if err != nil {
		t.Fatalf("set var: %v", err)
	}
	// Read it back in the second call.
	out, err := ft.Call(ctx, `{"command":"echo $MYVAR"}`)
	if err != nil {
		t.Fatalf("read var: %v", err)
	}
	if !strings.Contains(out.(string), "hello_persistent") {
		t.Errorf("expected persistent variable, got %q", out)
	}
}

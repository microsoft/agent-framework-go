// Copyright (c) Microsoft. All rights reserved.

package shelltool_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestResult_FormatForModel_emptyStdoutOnlyExitCode(t *testing.T) {
	r := shelltool.Result{ExitCode: 0}
	if got := r.FormatForModel(); got != "exit_code: 0" {
		t.Errorf("got %q, want exact exit code line", got)
	}
}

func TestResult_FormatForModel_truncatedButEmptyStdoutOmitsMarker(t *testing.T) {
	r := shelltool.Result{Stderr: "err\n", Truncated: true, ExitCode: 1}
	got := r.FormatForModel()
	if strings.Contains(got, "[stdout truncated]") {
		t.Errorf("did not expect stdout truncated marker, got %q", got)
	}
	if !strings.Contains(got, "stderr: err") {
		t.Errorf("expected stderr block, got %q", got)
	}
}

// --------------------------------------------------------------------------
// Policy
// --------------------------------------------------------------------------

func TestPolicy_nil_allowsAll(t *testing.T) {
	var p *shelltool.Policy
	allowed, reason := p.Evaluate(shelltool.ShellRequest{Command: "rm -rf /"})
	if !allowed {
		t.Errorf("nil policy should allow, got reason %q", reason)
	}
}

func TestPolicy_deny_matchingPattern(t *testing.T) {
	p, err := shelltool.NewPolicy(shelltool.PolicyConfig{DenyList: []string{`rm\s+-rf`}})
	if err != nil {
		t.Fatal(err)
	}
	allowed, reason := p.Evaluate(shelltool.ShellRequest{Command: "rm -rf /tmp/foo"})
	if allowed {
		t.Errorf("expected deny, got allowed")
	}
	if !strings.Contains(reason, "matched deny pattern") {
		t.Errorf("unexpected reason %q", reason)
	}
}

func TestPolicy_deny_noMatch_allows(t *testing.T) {
	p, err := shelltool.NewPolicy(shelltool.PolicyConfig{DenyList: []string{`rm\s+-rf`}})
	if err != nil {
		t.Fatal(err)
	}
	allowed, _ := p.Evaluate(shelltool.ShellRequest{Command: "echo hello"})
	if !allowed {
		t.Errorf("expected allow for non-matching command")
	}
}

func TestPolicy_allow_shortCircuitsDeny(t *testing.T) {
	p, err := shelltool.NewPolicy(shelltool.PolicyConfig{
		DenyList:  []string{`rm\s+-rf`},
		AllowList: []string{`^rm\s+-rf\s+/tmp/safe$`},
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed, reason := p.Evaluate(shelltool.ShellRequest{Command: "rm -rf /tmp/safe"})
	if !allowed {
		t.Fatalf("expected allow-list match to short-circuit deny-list, got reason %q", reason)
	}
	if !strings.Contains(reason, "matched allow pattern") {
		t.Errorf("unexpected reason %q", reason)
	}
}

func TestPolicy_patternsAreCaseInsensitive(t *testing.T) {
	p, err := shelltool.NewPolicy(shelltool.PolicyConfig{DenyList: []string{`remove-item`}})
	if err != nil {
		t.Fatal(err)
	}
	allowed, reason := p.Evaluate(shelltool.ShellRequest{Command: "Remove-Item -Recurse"})
	if allowed {
		t.Fatalf("expected case-insensitive deny match, got allowed")
	}
	if !strings.Contains(reason, "matched deny pattern") {
		t.Errorf("unexpected reason %q", reason)
	}
}

func TestPolicy_emptyCommand_denies(t *testing.T) {
	p, err := shelltool.NewPolicy(shelltool.PolicyConfig{})
	if err != nil {
		t.Fatal(err)
	}
	allowed, reason := p.Evaluate(shelltool.ShellRequest{Command: "   "})
	if allowed {
		t.Fatal("expected empty command to be denied")
	}
	if reason != "empty command" {
		t.Errorf("unexpected reason %q", reason)
	}
}

func TestNewPolicy_invalidRegex(t *testing.T) {
	_, err := shelltool.NewPolicy(shelltool.PolicyConfig{DenyList: []string{`[invalid`}})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestNewPolicy_invalidAllowRegex(t *testing.T) {
	_, err := shelltool.NewPolicy(shelltool.PolicyConfig{AllowList: []string{`[invalid`}})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

// --------------------------------------------------------------------------
// Tool construction
// --------------------------------------------------------------------------

func TestNewLocal_returnsApprovalRequired_byDefault(t *testing.T) {
	ft := newLocal(t, shelltool.LocalConfig{})
	if ft.Name() != "run_shell" {
		t.Errorf("expected name 'run_shell', got %q", ft.Name())
	}
	if !ft.ApprovalRequired() {
		t.Error("expected tool to require approval")
	}
}

func TestNewLocal_acknowledgeUnsafe_noApproval(t *testing.T) {
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	if ft.ApprovalRequired() {
		t.Error("expected approval to be disabled when AcknowledgeUnsafe is true")
	}
}

func TestNewLocal_schema_hasCommandField(t *testing.T) {
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
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

func TestNewLocal_shellAndShellArgvConflict(t *testing.T) {
	_, err := shelltool.NewLocal(shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Shell:             "sh",
		ShellArgv:         []string{"sh"},
	})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "either Shell or ShellArgv") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewLocal_emptyShellArgvErrors(t *testing.T) {
	_, err := shelltool.NewLocal(shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		ShellArgv:         []string{},
	})
	if err == nil {
		t.Fatal("expected ShellArgv error")
	}
	if !strings.Contains(err.Error(), "ShellArgv must contain") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewLocal_negativeMaxOutputErrors(t *testing.T) {
	_, err := shelltool.NewLocal(shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		MaxOutputBytes:    -1,
	})
	if err == nil {
		t.Fatal("expected MaxOutputBytes error")
	}
	if !strings.Contains(err.Error(), "MaxOutputBytes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefaultTimeout_isThirtySeconds(t *testing.T) {
	if shelltool.DefaultTimeout != 30*time.Second {
		t.Fatalf("DefaultTimeout = %s, want 30s", shelltool.DefaultTimeout)
	}
}

func TestNewLocal_descriptionContainsShellGuidance(t *testing.T) {
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	desc := ft.Description()
	for _, want := range []string{"Execute a single shell command", "Operating system:", "Shell:", "Output is truncated"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %q", want, desc)
		}
	}
}

func TestNewLocal_persistentCmdErrors(t *testing.T) {
	_, err := shelltool.NewLocal(shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Mode:              shelltool.ModePersistent,
		Shell:             "cmd.exe",
	})
	if err == nil {
		t.Fatal("expected persistent cmd.exe error")
	}
	if !strings.Contains(err.Error(), "persistent mode is not supported for cmd.exe") {
		t.Errorf("unexpected error: %v", err)
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

func quotePOSIXTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func newLocal(t *testing.T, cfg shelltool.LocalConfig) *shelltool.Local {
	t.Helper()
	ft, err := shelltool.NewLocal(cfg)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return ft
}

func powershellPathForTest(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell is not available")
	return ""
}

func statelessPlatformConfig(t *testing.T) (shelltool.LocalConfig, func(string) string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return shelltool.LocalConfig{
			AcknowledgeUnsafe: true,
			Mode:              shelltool.ModeStateless,
			Shell:             powershellPathForTest(t),
		}, func(command string) string { return command }
	}
	return shelltool.LocalConfig{AcknowledgeUnsafe: true, Mode: shelltool.ModeStateless}, func(command string) string { return command }
}

func callTool(t *testing.T, ft *shelltool.Local, command string) string {
	t.Helper()
	out, err := ft.Call(tool.Context{Context: t.Context()}, fmt.Sprintf(`{"command":%q}`, command))
	if err != nil {
		t.Fatalf("call %q: %v", command, err)
	}
	text, ok := out.(string)
	if !ok {
		t.Fatalf("expected string output, got %T", out)
	}
	return text
}

func TestNewLocal_initializePersistent(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	if err := ft.Initialize(t.Context()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer ft.Close()
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"echo initialized"}`)
	if err != nil {
		t.Fatalf("call after initialize: %v", err)
	}
	if !strings.Contains(out.(string), "initialized") {
		t.Errorf("expected initialized output, got %q", out)
	}
}

func TestCall_echo_defaultPersistent(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
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

func TestCall_defaultPersistentPowerShellOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping Windows-only PowerShell test")
	}
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"Write-Output hello_windows"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := out.(string)
	if !ok {
		t.Fatalf("expected string output, got %T", out)
	}
	if !strings.Contains(s, "hello_windows") {
		t.Errorf("expected PowerShell output, got %q", s)
	}
	if !strings.Contains(s, "exit_code: 0") {
		t.Errorf("expected exit_code: 0, got %q", s)
	}
}

func TestCall_environmentPowerShellOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping Windows-only PowerShell test")
	}
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Environment: map[string]string{
			"AGFW_TEST_ENV": "hello_env",
		},
	})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"Write-Output $env:AGFW_TEST_ENV"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.(string), "hello_env") {
		t.Errorf("expected environment value, got %q", out)
	}
}

func TestCall_environmentRemovalPowerShellOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping Windows-only PowerShell test")
	}
	t.Setenv("AGFW_REMOVE_ME", "visible")
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		RemoveEnvironment: []string{
			"AGFW_REMOVE_ME",
		},
	})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, `{"command":"if ($env:AGFW_REMOVE_ME) { Write-Output $env:AGFW_REMOVE_ME } else { Write-Output absent }"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.(string), "absent") {
		t.Errorf("expected removed environment value, got %q", out)
	}
}

func TestCall_nonZeroExit(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
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
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	ctx := tool.Context{Context: t.Context()}
	_, err := ft.Call(ctx, `{"command":""}`)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestCall_policyDeny(t *testing.T) {
	p, _ := shelltool.NewPolicy(shelltool.PolicyConfig{DenyList: []string{`echo`}})
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true, Policy: p})
	ctx := tool.Context{Context: t.Context()}
	_, err := ft.Call(ctx, `{"command":"echo hello"}`)
	if err == nil {
		t.Error("expected error from policy deny")
	}
	if !strings.Contains(err.Error(), "matched deny pattern") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCall_timeout(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{
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

func TestCall_statelessOutputTruncationUsesHeadTailFormat(t *testing.T) {
	cfg, _ := statelessPlatformConfig(t)
	cfg.MaxOutputBytes = 2048
	cfg.Timeout = 20 * time.Second
	command := "i=1; while [ $i -le 400 ]; do printf 'line-%s-padding-padding-padding\\n' \"$i\"; i=$((i+1)); done"
	if runtime.GOOS == "windows" {
		command = "1..400 | ForEach-Object { 'line-' + $_ + '-padding-padding-padding' }"
	}

	out := callTool(t, newLocal(t, cfg), command)
	for _, want := range []string{"line-1-", "line-400-", "truncated", "[stdout truncated]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got %q", want, out)
		}
	}
}

func TestCall_statelessStderrContentIsCaptured(t *testing.T) {
	cfg, _ := statelessPlatformConfig(t)
	command := "printf 'err-from-shell\\n' >&2"
	if runtime.GOOS == "windows" {
		command = "[Console]::Error.WriteLine('err-from-shell')"
	}

	out := callTool(t, newLocal(t, cfg), command)
	if !strings.Contains(out, "stderr: err-from-shell") {
		t.Fatalf("expected stderr content, got %q", out)
	}
}

func TestCall_statelessCleanEnvironmentStripsParentVar(t *testing.T) {
	t.Setenv("AF_SHELL_PARENT_VAR", "should-not-leak")
	cfg, _ := statelessPlatformConfig(t)
	cfg.CleanEnvironment = true
	command := "printf '%s' \"$AF_SHELL_PARENT_VAR\""
	if runtime.GOOS == "windows" {
		command = "if ($env:AF_SHELL_PARENT_VAR) { Write-Output $env:AF_SHELL_PARENT_VAR }"
	}

	out := callTool(t, newLocal(t, cfg), command)
	if strings.Contains(out, "should-not-leak") {
		t.Fatalf("expected clean environment to strip parent variable, got %q", out)
	}
	if !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("expected successful command, got %q", out)
	}
}

func TestCall_statelessTimeoutKillsProcessTree(t *testing.T) {
	skipIfNotPOSIX(t)
	marker := filepath.Join(t.TempDir(), "marker")
	command := fmt.Sprintf("sh -c 'sleep 0.4; printf leaked > %s' & wait", quotePOSIXTest(marker))
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Mode:              shelltool.ModeStateless,
		Timeout:           50 * time.Millisecond,
	})
	ctx := tool.Context{Context: t.Context()}
	out, err := ft.Call(ctx, fmt.Sprintf(`{"command":%q}`, command))
	if err != nil {
		t.Fatalf("timeout should surface in result, not as error: %v", err)
	}
	if !strings.Contains(out.(string), "[command timed out]") {
		t.Fatalf("expected timed-out marker, got %q", out)
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("expected process tree to be killed before marker write")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

func TestCall_timeout_preservesPersistentSession(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Timeout:           50 * time.Millisecond,
	})
	ctx := tool.Context{Context: t.Context()}

	if _, err := ft.Call(ctx, `{"command":"MYVAR=survives_timeout"}`); err != nil {
		t.Fatalf("set var: %v", err)
	}
	out, err := ft.Call(ctx, `{"command":"sleep 10"}`)
	if err != nil {
		t.Fatalf("timeout should surface in result, not as error: %v", err)
	}
	if !strings.Contains(out.(string), "[command timed out]") {
		t.Fatalf("expected timed-out marker, got %q", out)
	}

	out, err = ft.Call(ctx, `{"command":"echo $MYVAR"}`)
	if err != nil {
		t.Fatalf("read var after timeout: %v", err)
	}
	if !strings.Contains(out.(string), "survives_timeout") {
		t.Errorf("expected session state to survive timeout interrupt, got %q", out)
	}
}

func TestCall_workingDirectory_reanchorsPersistentCommands(t *testing.T) {
	skipIfNotPOSIX(t)
	dir := t.TempDir()
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		WorkingDirectory:  dir,
	})
	ctx := tool.Context{Context: t.Context()}

	if _, err := ft.Call(ctx, `{"command":"cd /"}`); err != nil {
		t.Fatalf("cd: %v", err)
	}
	out, err := ft.Call(ctx, `{"command":"pwd"}`)
	if err != nil {
		t.Fatalf("pwd: %v", err)
	}
	if got := strings.TrimSpace(strings.Split(out.(string), "exit_code:")[0]); got != dir {
		t.Errorf("expected command to reanchor in %q, got %q", dir, got)
	}
}

func TestCall_workingDirectory_canDisableReanchor(t *testing.T) {
	skipIfNotPOSIX(t)
	dir := t.TempDir()
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe:                  true,
		WorkingDirectory:                   dir,
		DisableWorkingDirectoryConfinement: true,
	})
	ctx := tool.Context{Context: t.Context()}

	if _, err := ft.Call(ctx, `{"command":"cd /"}`); err != nil {
		t.Fatalf("cd: %v", err)
	}
	out, err := ft.Call(ctx, `{"command":"pwd"}`)
	if err != nil {
		t.Fatalf("pwd: %v", err)
	}
	if got := strings.TrimSpace(strings.Split(out.(string), "exit_code:")[0]); got != "/" {
		t.Errorf("expected cwd to remain changed, got %q", got)
	}
}

func TestCall_persistent_statePersists(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{
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

func TestCall_persistent_respawnsAfterUnexpectedExit(t *testing.T) {
	skipIfNotPOSIX(t)
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	ctx := tool.Context{Context: t.Context()}

	_, _ = ft.Call(ctx, `{"command":"exit"}`)
	out, err := ft.Call(ctx, `{"command":"echo after_respawn"}`)
	if err != nil {
		t.Fatalf("call after session exit: %v", err)
	}
	if !strings.Contains(out.(string), "after_respawn") {
		t.Errorf("expected respawned session output, got %q", out)
	}
}

// Copyright (c) Microsoft. All rights reserved.

package shelltool_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
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
// Shell environment provider
// --------------------------------------------------------------------------

func TestShellFamily_zeroValueIsUnknown(t *testing.T) {
	var family shelltool.ShellFamily
	if family != shelltool.ShellFamilyUnknown {
		t.Fatalf("zero ShellFamily = %v, want ShellFamilyUnknown", family)
	}
	if got := family.String(); got != "Unknown" {
		t.Fatalf("zero ShellFamily string = %q, want Unknown", got)
	}
}

func TestDefaultShellEnvironmentInstructions_powerShell(t *testing.T) {
	snapshot := shelltool.ShellEnvironmentSnapshot{
		Family:           shelltool.ShellFamilyPowerShell,
		OSDescription:    "Windows",
		ShellVersion:     "7.5.0",
		WorkingDirectory: `C:\work`,
		ToolVersions: map[string]shelltool.ToolVersion{
			"docker": {},
			"git":    {Version: "git version 2.50.0", Found: true},
		},
	}

	got := shelltool.DefaultShellEnvironmentInstructions(snapshot)
	for _, want := range []string{
		"## Shell environment",
		"PowerShell 7.5.0 session on Windows",
		"Use PowerShell idioms, NOT bash",
		"Working directory: C:\\work",
		"Available CLIs: git (git version 2.50.0)",
		"Not installed: docker",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instructions missing %q:\n%s", want, got)
		}
	}
}

func TestDefaultShellEnvironmentInstructions_posix(t *testing.T) {
	snapshot := shelltool.ShellEnvironmentSnapshot{
		Family:           shelltool.ShellFamilyPOSIX,
		OSDescription:    "Ubuntu 22.04",
		ShellVersion:     "5.2",
		WorkingDirectory: "/home/user/repo",
		ToolVersions: map[string]shelltool.ToolVersion{
			"git": {Version: "git 2.43", Found: true},
		},
	}

	got := shelltool.DefaultShellEnvironmentInstructions(snapshot)
	for _, want := range []string{"POSIX", "export NAME=value", "/home/user/repo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("instructions missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "$env:") {
		t.Fatalf("POSIX instructions should not contain PowerShell env syntax:\n%s", got)
	}
}

func TestEnvironmentProvider_refreshOnHostReportsDefaultFamily(t *testing.T) {
	t.Parallel()

	cfg, _ := statelessPlatformConfig(t)
	ft := newLocal(t, cfg)
	env := shelltool.NewEnvironmentProvider(ft, shelltool.EnvironmentProviderConfig{
		ProbeTools: []string{},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	wantFamily := shelltool.ShellFamilyPOSIX
	if runtime.GOOS == "windows" {
		wantFamily = shelltool.ShellFamilyPowerShell
	}
	if snapshot.Family != wantFamily {
		t.Fatalf("family = %v, want %v", snapshot.Family, wantFamily)
	}
	if snapshot.WorkingDirectory == "" {
		t.Fatalf("expected working directory, got empty snapshot: %+v", snapshot)
	}
	if runtime.GOOS == "windows" && snapshot.ShellVersion == "" {
		t.Fatalf("expected PowerShell version on Windows, got empty snapshot: %+v", snapshot)
	}
}

func TestEnvironmentProvider_missingToolRecordedAsMissing(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{
			{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0},
			{ExitCode: 127},
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"definitely-not-a-real-binary-xyz123"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	version, ok := snapshot.ToolVersions["definitely-not-a-real-binary-xyz123"]
	if !ok {
		t.Fatalf("expected missing tool entry, got %#v", snapshot.ToolVersions)
	}
	if version.Found {
		t.Fatalf("expected missing tool, got %#v", version)
	}
}

func TestEnvironmentProvider_customFormatterOverridesDefault(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0}},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
		InstructionsFormatter: func(snapshot shelltool.ShellEnvironmentSnapshot) string {
			if snapshot.WorkingDirectory != "/tmp" {
				t.Fatalf("working directory = %q, want /tmp", snapshot.WorkingDirectory)
			}
			return "CUSTOM-INSTRUCTIONS"
		},
	})

	_, options, err := env.BeforeRun(t.Context(), nil)
	if err != nil {
		t.Fatalf("provide shell environment: %v", err)
	}
	instructions := slices.Collect(agent.AllOptions(options, agent.WithInstructions))
	if !slices.Equal(instructions, []string{"CUSTOM-INSTRUCTIONS"}) {
		t.Fatalf("instructions = %#v, want custom instructions", instructions)
	}
}

func TestEnvironmentProvider_refreshRecomputesSnapshot(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{
			{Stdout: "VERSION=1.0\nCWD=/a\n", ExitCode: 0},
			{Stdout: "VERSION=2.0\nCWD=/b\n", ExitCode: 0},
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
	})

	first, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first.WorkingDirectory != "/a" {
		t.Fatalf("first working directory = %q, want /a", first.WorkingDirectory)
	}
	probesAfterFirst := fake.runCount

	second, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.WorkingDirectory != "/b" || second.ShellVersion != "2.0" {
		t.Fatalf("second snapshot = %+v, want cwd /b and version 2.0", second)
	}
	if fake.runCount <= probesAfterFirst {
		t.Fatalf("Refresh should re-probe each call, runCount = %d after first %d", fake.runCount, probesAfterFirst)
	}
}

func TestEnvironmentProvider_providesInstructionsAndSnapshot(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0}},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
	})

	_, options, err := env.BeforeRun(t.Context(), nil)
	if err != nil {
		t.Fatalf("provide shell environment: %v", err)
	}
	instructions := slices.Collect(agent.AllOptions(options, agent.WithInstructions))
	if len(instructions) != 1 {
		t.Fatalf("expected one instruction option, got %d", len(instructions))
	}
	for _, want := range []string{"## Shell environment", "Working directory:"} {
		if !strings.Contains(instructions[0], want) {
			t.Fatalf("instructions missing %q:\n%s", want, instructions[0])
		}
	}

	snapshot, ok := env.CurrentSnapshot()
	if !ok {
		t.Fatal("expected current snapshot after provider run")
	}
	if snapshot.WorkingDirectory == "" {
		t.Fatalf("expected probed working directory, got empty snapshot: %+v", snapshot)
	}
	if len(snapshot.ToolVersions) != 0 {
		t.Fatalf("expected no tool probes, got %#v", snapshot.ToolVersions)
	}
}

func TestEnvironmentProvider_currentSnapshotReturnsCopy(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0}},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"bad;name"},
	})
	if _, err := env.Refresh(t.Context()); err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}

	snapshot, ok := env.CurrentSnapshot()
	if !ok {
		t.Fatal("expected current snapshot")
	}
	snapshot.ToolVersions["bad;name"] = shelltool.ToolVersion{Version: "mutated", Found: true}

	again, ok := env.CurrentSnapshot()
	if !ok {
		t.Fatal("expected current snapshot")
	}
	if again.ToolVersions["bad;name"].Found {
		t.Fatalf("expected snapshot map to be defensive copy, got %#v", again.ToolVersions["bad;name"])
	}
}

func TestEnvironmentProvider_failedProvideAllowsRetry(t *testing.T) {
	fake := &environmentTestExecutor{
		run: func(ctx context.Context, command string) (shelltool.Result, error) {
			if err := ctx.Err(); err != nil {
				return shelltool.Result{}, err
			}
			return shelltool.Result{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0}, nil
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
	})

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := env.BeforeRun(canceled, nil); err == nil {
		t.Fatal("expected canceled provider run to fail")
	}
	if _, ok := env.CurrentSnapshot(); ok {
		t.Fatal("did not expect snapshot after failed provider run")
	}

	if _, _, err := env.BeforeRun(t.Context(), nil); err != nil {
		t.Fatalf("retry provider run: %v", err)
	}
	if _, ok := env.CurrentSnapshot(); !ok {
		t.Fatal("expected snapshot after retry")
	}
}

func TestEnvironmentProvider_firstCallFailsNextCallRetriesAndSucceeds(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	fake := &environmentTestExecutor{
		run: func(ctx context.Context, command string) (shelltool.Result, error) {
			calls++
			if calls == 1 {
				return shelltool.Result{}, boom
			}
			return shelltool.Result{Stdout: "VERSION=2.0\nCWD=/tmp\n", ExitCode: 0}, nil
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
	})

	if _, _, err := env.BeforeRun(t.Context(), nil); !errors.Is(err, boom) {
		t.Fatalf("first provider run error = %v, want boom", err)
	}
	if _, ok := env.CurrentSnapshot(); ok {
		t.Fatal("did not expect snapshot after failed provider run")
	}

	if _, _, err := env.BeforeRun(t.Context(), nil); err != nil {
		t.Fatalf("retry provider run: %v", err)
	}
	snapshot, ok := env.CurrentSnapshot()
	if !ok {
		t.Fatal("expected snapshot after retry")
	}
	if snapshot.ShellVersion != "2.0" {
		t.Fatalf("snapshot shell version = %q, want 2.0", snapshot.ShellVersion)
	}
}

func TestEnvironmentProvider_firstCallCanceledNextCallSucceeds(t *testing.T) {
	calls := 0
	fake := &environmentTestExecutor{
		run: func(ctx context.Context, command string) (shelltool.Result, error) {
			calls++
			if calls == 1 {
				return shelltool.Result{}, ctx.Err()
			}
			return shelltool.Result{Stdout: "VERSION=3.0\nCWD=/x\n", ExitCode: 0}, nil
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
	})
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err := env.BeforeRun(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("first provider run error = %v, want context canceled", err)
	}
	if _, ok := env.CurrentSnapshot(); ok {
		t.Fatal("did not expect snapshot after canceled provider run")
	}

	if _, _, err := env.BeforeRun(t.Context(), nil); err != nil {
		t.Fatalf("retry provider run: %v", err)
	}
	snapshot, ok := env.CurrentSnapshot()
	if !ok {
		t.Fatal("expected snapshot after retry")
	}
	if snapshot.ShellVersion != "3.0" {
		t.Fatalf("snapshot shell version = %q, want 3.0", snapshot.ShellVersion)
	}
}

func TestEnvironmentProvider_invalidToolNameRecordedMissingWithoutInvokingExecutor(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{{Stdout: "VERSION=1.0\nCWD=/\n", ExitCode: 0}},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"git; rm -rf /", "echo $PATH", "good-tool && bad"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	if fake.runCount != 1 {
		t.Fatalf("expected only shell/CWD probe to run, runCount = %d", fake.runCount)
	}
	for _, name := range []string{"git; rm -rf /", "echo $PATH", "good-tool && bad"} {
		version, ok := snapshot.ToolVersions[name]
		if !ok || version.Found {
			t.Fatalf("invalid tool %q = %#v, present %v; want missing entry", name, version, ok)
		}
	}
}

func TestEnvironmentProvider_probeToolsDeduplicateCaseInsensitive(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0}},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"bad;name", "BAD;NAME"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	if len(snapshot.ToolVersions) != 1 {
		t.Fatalf("expected one de-duplicated probe entry, got %#v", snapshot.ToolVersions)
	}
	if _, ok := snapshot.ToolVersions["bad;name"]; !ok {
		t.Fatalf("expected first probe name to be preserved, got %#v", snapshot.ToolVersions)
	}
}

func TestEnvironmentProvider_duplicateProbeToolsCaseInsensitiveProbesOnce(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{
			{Stdout: "VERSION=1.0\nCWD=/\n", ExitCode: 0},
			{Stdout: "git 2.46\n", ExitCode: 0},
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"git", "GIT", "Git"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	if fake.runCount != 2 {
		t.Fatalf("expected shell probe plus one tool probe, runCount = %d", fake.runCount)
	}
	if len(snapshot.ToolVersions) != 1 {
		t.Fatalf("expected one de-duplicated probe entry, got %#v", snapshot.ToolVersions)
	}
	if got := snapshot.ToolVersions["git"]; !got.Found || got.Version != "git 2.46" {
		t.Fatalf("git version = %#v, want git 2.46", got)
	}
}

func TestEnvironmentProvider_toolEmitsVersionToStderrFallsBackToStderr(t *testing.T) {
	fake := &environmentTestExecutor{
		results: []shelltool.Result{
			{Stdout: "VERSION=1.0\nCWD=/\n", ExitCode: 0},
			{Stderr: "openjdk 21.0.1 2023-10-17\n", ExitCode: 0},
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"java"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	if got := snapshot.ToolVersions["java"]; !got.Found || got.Version != "openjdk 21.0.1 2023-10-17" {
		t.Fatalf("java version = %#v, want stderr version", got)
	}
}

func TestEnvironmentProvider_callerCancellationPropagates(t *testing.T) {
	fake := &environmentTestExecutor{
		run: func(ctx context.Context, command string) (shelltool.Result, error) {
			return shelltool.Result{}, ctx.Err()
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{},
	})
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := env.Refresh(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh error = %v, want context canceled", err)
	}
}

func TestEnvironmentProvider_probeTimeoutRecordedAsMissingFields(t *testing.T) {
	fake := &environmentTestExecutor{
		run: func(ctx context.Context, command string) (shelltool.Result, error) {
			<-ctx.Done()
			return shelltool.Result{}, ctx.Err()
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTimeout:   time.Millisecond,
		ProbeTools:     []string{"git"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	if snapshot.ShellVersion != "" {
		t.Fatalf("shell version = %q, want missing", snapshot.ShellVersion)
	}
	if got := snapshot.ToolVersions["git"]; got.Found {
		t.Fatalf("git version = %#v, want missing", got)
	}
}

func TestEnvironmentProvider_policyRejectedToolProbeIsMissing(t *testing.T) {
	fake := &environmentTestExecutor{
		run: func(ctx context.Context, command string) (shelltool.Result, error) {
			if command == "git --version" {
				return shelltool.Result{}, rejectedProbeError{}
			}
			return shelltool.Result{Stdout: "VERSION=1.0\nCWD=/tmp\n", ExitCode: 0}, nil
		},
	}
	env := shelltool.NewEnvironmentProvider(fake, shelltool.EnvironmentProviderConfig{
		OverrideFamily: shelltool.ShellFamilyPOSIX,
		ProbeTools:     []string{"git"},
	})

	snapshot, err := env.Refresh(t.Context())
	if err != nil {
		t.Fatalf("refresh shell environment: %v", err)
	}
	if snapshot.ToolVersions["git"].Found {
		t.Fatalf("expected rejected probe to be recorded as missing, got %#v", snapshot.ToolVersions["git"])
	}
}

type environmentTestExecutor struct {
	results []shelltool.Result
	run     func(context.Context, string) (shelltool.Result, error)

	runCount int
}

type rejectedProbeError struct{}

func (rejectedProbeError) Error() string { return "shelltool: command rejected" }

func (rejectedProbeError) Is(target error) bool {
	return target != nil && target.Error() == "shelltool: command rejected"
}

func (e *environmentTestExecutor) Initialize(context.Context) error { return nil }

func (e *environmentTestExecutor) Run(ctx context.Context, command string) (shelltool.Result, error) {
	e.runCount++
	if e.run != nil {
		return e.run(ctx, command)
	}
	if len(e.results) == 0 {
		return shelltool.Result{}, fmt.Errorf("unexpected probe command %q", command)
	}
	result := e.results[0]
	e.results = e.results[1:]
	return result, nil
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

var (
	powershellPathOnce sync.Once
	powershellPath     string
)

func powershellPathForTest(t *testing.T) string {
	t.Helper()
	powershellPathOnce.Do(func() {
		for _, name := range []string{"pwsh", "powershell"} {
			if path, err := exec.LookPath(name); err == nil {
				powershellPath = path
				return
			}
		}
	})
	if powershellPath == "" {
		t.Skip("PowerShell is not available")
	}
	return powershellPath
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
	out, err := ft.Call(t.Context(), fmt.Sprintf(`{"command":%q}`, command))
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
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	if err := ft.Initialize(t.Context()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer func() {
		if err := ft.Close(); err != nil {
			t.Errorf("close shell: %v", err)
		}
	}()
	out, err := ft.Call(t.Context(), `{"command":"echo initialized"}`)
	if err != nil {
		t.Fatalf("call after initialize: %v", err)
	}
	if !strings.Contains(out.(string), "initialized") {
		t.Errorf("expected initialized output, got %q", out)
	}
}

func TestCall_echo_defaultPersistent(t *testing.T) {
	skipIfNotPOSIX(t)
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	out, err := ft.Call(t.Context(), `{"command":"echo hello"}`)
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
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	out, err := ft.Call(t.Context(), `{"command":"Write-Output hello_windows"}`)
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
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Mode:              shelltool.ModeStateless,
		Shell:             powershellPathForTest(t),
		Environment: map[string]string{
			"AGFW_TEST_ENV": "hello_env",
		},
	})
	out, err := ft.Call(t.Context(), `{"command":"Write-Output $env:AGFW_TEST_ENV"}`)
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
		Mode:              shelltool.ModeStateless,
		Shell:             powershellPathForTest(t),
		RemoveEnvironment: []string{
			"AGFW_REMOVE_ME",
		},
	})
	out, err := ft.Call(t.Context(), `{"command":"if ($env:AGFW_REMOVE_ME) { Write-Output $env:AGFW_REMOVE_ME } else { Write-Output absent }"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.(string), "absent") {
		t.Errorf("expected removed environment value, got %q", out)
	}
}

func TestCall_nonZeroExit(t *testing.T) {
	skipIfNotPOSIX(t)
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	out, err := ft.Call(t.Context(), `{"command":"sh -c 'exit 42'"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.(string), "exit_code: 42") {
		t.Errorf("expected exit_code: 42, got %q", out)
	}
}

func TestCall_emptyCommand_error(t *testing.T) {
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	_, err := ft.Call(t.Context(), `{"command":""}`)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestCall_policyDeny(t *testing.T) {
	p, _ := shelltool.NewPolicy(shelltool.PolicyConfig{DenyList: []string{`echo`}})
	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true, Policy: p})
	_, err := ft.Call(t.Context(), `{"command":"echo hello"}`)
	if err == nil {
		t.Error("expected error from policy deny")
	}
	if !strings.Contains(err.Error(), "matched deny pattern") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCall_timeout(t *testing.T) {
	skipIfNotPOSIX(t)
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Timeout:           50 * time.Millisecond,
	})
	out, err := ft.Call(t.Context(), `{"command":"sleep 10"}`)
	if err != nil {
		t.Fatalf("unexpected error (timeout should surface in result, not as error): %v", err)
	}
	s := out.(string)
	if !strings.Contains(s, "[command timed out]") {
		t.Errorf("expected timed-out marker, got %q", s)
	}
}

func TestCall_statelessOutputTruncationUsesHeadTailFormat(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "marker")
	command := fmt.Sprintf("sh -c 'sleep 0.4; printf leaked > %s' & wait", quotePOSIXTest(marker))
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Mode:              shelltool.ModeStateless,
		Timeout:           50 * time.Millisecond,
	})
	out, err := ft.Call(t.Context(), fmt.Sprintf(`{"command":%q}`, command))
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
	if runtime.GOOS != "linux" {
		t.Skip("persistent timeout session preservation requires descendant-only interrupt support")
	}
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		Timeout:           50 * time.Millisecond,
	})
	ctx := t.Context()

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
	t.Parallel()

	dir := t.TempDir()
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
		WorkingDirectory:  dir,
	})
	ctx := t.Context()

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
	t.Parallel()

	dir := t.TempDir()
	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe:                  true,
		WorkingDirectory:                   dir,
		DisableWorkingDirectoryConfinement: true,
	})
	ctx := t.Context()

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
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{
		AcknowledgeUnsafe: true,
	})
	ctx := t.Context()

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
	t.Parallel()

	ft := newLocal(t, shelltool.LocalConfig{AcknowledgeUnsafe: true})
	ctx := t.Context()

	_, _ = ft.Call(ctx, `{"command":"exit"}`)
	out, err := ft.Call(ctx, `{"command":"echo after_respawn"}`)
	if err != nil {
		t.Fatalf("call after session exit: %v", err)
	}
	if !strings.Contains(out.(string), "after_respawn") {
		t.Errorf("expected respawned session output, got %q", out)
	}
}

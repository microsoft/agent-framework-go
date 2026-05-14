// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const (
	defaultShellEnvironmentSourceID = "shell_environment"
	defaultProbeTimeout             = 5 * time.Second
)

var defaultProbeTools = []string{"git", "dotnet", "node", "python", "docker", "go"}

// ShellFamily identifies the shell syntax family in use.
type ShellFamily int

const (
	// ShellFamilyUnknown means no shell family has been selected or detected.
	ShellFamilyUnknown ShellFamily = iota
	// ShellFamilyPOSIX is a POSIX-style shell such as bash, sh, or zsh.
	ShellFamilyPOSIX
	// ShellFamilyPowerShell is PowerShell, including pwsh and Windows PowerShell.
	ShellFamilyPowerShell
)

func (f ShellFamily) String() string {
	switch f {
	case ShellFamilyUnknown:
		return "Unknown"
	case ShellFamilyPOSIX:
		return "POSIX"
	case ShellFamilyPowerShell:
		return "PowerShell"
	default:
		return "Unknown"
	}
}

// ToolVersion reports whether a CLI was found and, when available, the first
// non-empty line from its --version output.
type ToolVersion struct {
	Version string
	Found   bool
}

// Executor runs shell commands for environment probing.
type Executor interface {
	Initialize(context.Context) error
	Run(context.Context, string) (Result, error)
}

// ShellEnvironmentSnapshot is a point-in-time view of the shell environment
// the agent is using.
type ShellEnvironmentSnapshot struct {
	Family           ShellFamily
	OSDescription    string
	ShellVersion     string
	WorkingDirectory string
	ToolVersions     map[string]ToolVersion
}

// EnvironmentProviderConfig configures an [EnvironmentProvider].
type EnvironmentProviderConfig struct {
	// SourceID identifies context injected by this provider.
	// Defaults to "shell_environment".
	SourceID string

	// ProbeTools lists CLI tools whose --version output should be probed.
	// Nil uses a small default list; an empty non-nil slice disables tool probes.
	ProbeTools []string

	// OverrideFamily forces the reported shell family when non-zero.
	// The zero value, [ShellFamilyUnknown], auto-detects the shell family.
	OverrideFamily ShellFamily

	// ProbeTimeout bounds each individual probe. Zero uses a 5 second default.
	ProbeTimeout time.Duration

	// InstructionsFormatter renders a snapshot into agent instructions.
	// Defaults to [DefaultShellEnvironmentInstructions].
	InstructionsFormatter func(ShellEnvironmentSnapshot) string
}

// EnvironmentProvider probes a local shell and injects shell-specific
// instructions through an [agent.ContextProvider].
type EnvironmentProvider struct {
	*agent.ContextProvider

	executor Executor
	config   EnvironmentProviderConfig

	mu           sync.Mutex
	current      *ShellEnvironmentSnapshot
	snapshotCall *environmentProbeCall
}

type environmentProbeCall struct {
	done     chan struct{}
	snapshot ShellEnvironmentSnapshot
	err      error
}

// NewEnvironmentProvider creates a shell environment provider backed by executor.
func NewEnvironmentProvider(executor Executor, config EnvironmentProviderConfig) *EnvironmentProvider {
	if executor == nil {
		panic("shelltool: executor is required")
	}
	p := &EnvironmentProvider{executor: executor, config: config}
	sourceID := strings.TrimSpace(config.SourceID)
	if sourceID == "" {
		sourceID = defaultShellEnvironmentSourceID
	}
	p.ContextProvider = &agent.ContextProvider{
		SourceID: sourceID,
		Provide:  p.provide,
	}
	return p
}

// CurrentSnapshot returns the most recently captured snapshot, if one exists.
func (p *EnvironmentProvider) CurrentSnapshot() (ShellEnvironmentSnapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil {
		return ShellEnvironmentSnapshot{}, false
	}
	return cloneShellEnvironmentSnapshot(*p.current), true
}

// Refresh forces a re-probe and stores the new snapshot.
func (p *EnvironmentProvider) Refresh(ctx context.Context) (ShellEnvironmentSnapshot, error) {
	snapshot, err := p.probe(ctx)
	if err != nil {
		return ShellEnvironmentSnapshot{}, err
	}
	stored := cloneShellEnvironmentSnapshot(snapshot)
	call := completedEnvironmentProbeCall(snapshot)

	p.mu.Lock()
	p.current = &stored
	p.snapshotCall = call
	p.mu.Unlock()

	return cloneShellEnvironmentSnapshot(snapshot), nil
}

func (p *EnvironmentProvider) provide(ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	snapshot, err := p.snapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	formatter := p.config.InstructionsFormatter
	if formatter == nil {
		formatter = DefaultShellEnvironmentInstructions
	}
	instructions := formatter(snapshot)
	return messages, append(options, agent.WithInstructions(instructions)), nil
}

func (p *EnvironmentProvider) snapshot(ctx context.Context) (ShellEnvironmentSnapshot, error) {
	p.mu.Lock()
	call := p.snapshotCall
	if call == nil {
		call = &environmentProbeCall{done: make(chan struct{})}
		p.snapshotCall = call
		p.mu.Unlock()
		p.runSnapshotCall(ctx, call)
	} else {
		p.mu.Unlock()
		<-call.done
	}

	if call.err != nil {
		return ShellEnvironmentSnapshot{}, call.err
	}
	return cloneShellEnvironmentSnapshot(call.snapshot), nil
}

func (p *EnvironmentProvider) runSnapshotCall(ctx context.Context, call *environmentProbeCall) {
	snapshot, err := p.probe(ctx)

	p.mu.Lock()
	if err != nil {
		if p.snapshotCall == call {
			p.snapshotCall = nil
		}
		call.err = err
	} else {
		stored := cloneShellEnvironmentSnapshot(snapshot)
		p.current = &stored
		call.snapshot = cloneShellEnvironmentSnapshot(snapshot)
	}
	p.mu.Unlock()
	close(call.done)
}

func completedEnvironmentProbeCall(snapshot ShellEnvironmentSnapshot) *environmentProbeCall {
	call := &environmentProbeCall{
		done:     make(chan struct{}),
		snapshot: cloneShellEnvironmentSnapshot(snapshot),
	}
	close(call.done)
	return call
}

func (p *EnvironmentProvider) probe(ctx context.Context) (ShellEnvironmentSnapshot, error) {
	if err := p.executor.Initialize(ctx); err != nil {
		return ShellEnvironmentSnapshot{}, err
	}

	family := p.detectFamily()
	shellVersion, workingDir, err := p.probeShellAndCWD(ctx, family)
	if err != nil {
		return ShellEnvironmentSnapshot{}, err
	}

	toolVersions := make(map[string]ToolVersion)
	seenTools := make(map[string]struct{})
	for _, tool := range p.probeTools() {
		key := strings.ToLower(tool)
		if _, ok := seenTools[key]; ok {
			continue
		}
		seenTools[key] = struct{}{}
		version, err := p.probeToolVersion(ctx, tool)
		if err != nil {
			return ShellEnvironmentSnapshot{}, err
		}
		toolVersions[tool] = version
	}

	return ShellEnvironmentSnapshot{
		Family:           family,
		OSDescription:    runtime.GOOS + "/" + runtime.GOARCH,
		ShellVersion:     shellVersion,
		WorkingDirectory: workingDir,
		ToolVersions:     toolVersions,
	}, nil
}

func (p *EnvironmentProvider) detectFamily() ShellFamily {
	if p.config.OverrideFamily != ShellFamilyUnknown {
		return p.config.OverrideFamily
	}
	if runtime.GOOS == "windows" {
		return ShellFamilyPowerShell
	}
	return ShellFamilyPOSIX
}

func (p *EnvironmentProvider) probeTools() []string {
	if p.config.ProbeTools == nil {
		return slices.Clone(defaultProbeTools)
	}
	return slices.Clone(p.config.ProbeTools)
}

func (p *EnvironmentProvider) probeTimeout() time.Duration {
	if p.config.ProbeTimeout > 0 {
		return p.config.ProbeTimeout
	}
	return defaultProbeTimeout
}

func (p *EnvironmentProvider) probeShellAndCWD(ctx context.Context, family ShellFamily) (string, string, error) {
	probe := `echo "VERSION=${BASH_VERSION:-${ZSH_VERSION:-unknown}}"; echo "CWD=$PWD"`
	if family == ShellFamilyPowerShell {
		probe = `Write-Output ("VERSION=" + $PSVersionTable.PSVersion.ToString()); Write-Output ("CWD=" + (Get-Location).Path)`
	}
	result, ok, err := p.runProbe(ctx, probe)
	if err != nil || !ok {
		return "", "", err
	}

	var version, cwd string
	for _, line := range splitNonEmptyLines(result.Stdout) {
		if v, ok := strings.CutPrefix(line, "VERSION="); ok {
			v = strings.TrimSpace(v)
			if v != "" && v != "unknown" {
				version = v
			}
		} else if v, ok := strings.CutPrefix(line, "CWD="); ok {
			cwd = strings.TrimSpace(v)
		}
	}
	return version, cwd, nil
}

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (p *EnvironmentProvider) probeToolVersion(ctx context.Context, name string) (ToolVersion, error) {
	if name == "" || !toolNamePattern.MatchString(name) {
		return ToolVersion{}, nil
	}
	result, ok, err := p.runProbe(ctx, name+" --version")
	if err != nil || !ok || result.ExitCode != 0 {
		return ToolVersion{}, err
	}
	line := firstNonEmptyLine(result.Stdout)
	if line == "" {
		line = firstNonEmptyLine(result.Stderr)
	}
	if line == "" {
		return ToolVersion{}, nil
	}
	return ToolVersion{Version: strings.TrimSpace(line), Found: true}, nil
}

func (p *EnvironmentProvider) runProbe(ctx context.Context, command string) (Result, bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, p.probeTimeout())
	defer cancel()

	result, err := p.executor.Run(probeCtx, command)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, false, err
		}
		if isMissingProbeResult(probeCtx, err) {
			return Result{}, false, nil
		}
		return Result{}, false, err
	}
	if result.TimedOut {
		return Result{}, false, nil
	}
	return result, true, nil
}

func isMissingProbeResult(probeCtx context.Context, err error) bool {
	if probeCtx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return errors.Is(err, errCommandRejected) || errors.Is(err, errCommandIO)
}

func splitNonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' }) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func firstNonEmptyLine(text string) string {
	for _, line := range splitNonEmptyLines(text) {
		return line
	}
	return ""
}

// DefaultShellEnvironmentInstructions renders shell environment guidance for an agent.
func DefaultShellEnvironmentInstructions(snapshot ShellEnvironmentSnapshot) string {
	var sb strings.Builder
	sb.WriteString("## Shell environment\n")

	switch snapshot.Family {
	case ShellFamilyPowerShell:
		version := ""
		if snapshot.ShellVersion != "" {
			version = " " + snapshot.ShellVersion
		}
		fmt.Fprintf(&sb, "You are operating a PowerShell%s session on %s.\n", version, snapshot.OSDescription)
		sb.WriteString("Use PowerShell idioms, NOT bash:\n")
		sb.WriteString("- Set environment variables with `$env:NAME = 'value'` (NOT `NAME=value`).\n")
		sb.WriteString("- Change directory with `Set-Location` or `cd`. Paths use `\\` separators.\n")
		sb.WriteString("- Reference environment variables as `$env:NAME` (NOT `$NAME`).\n")
		sb.WriteString("- The system temp directory is `[System.IO.Path]::GetTempPath()` (NOT `/tmp`).\n")
		sb.WriteString("- Pipe to `Out-Null` to suppress output (NOT `> /dev/null`).\n")
	case ShellFamilyPOSIX:
		version := ""
		if snapshot.ShellVersion != "" {
			version = " " + snapshot.ShellVersion
		}
		fmt.Fprintf(&sb, "You are operating a POSIX shell%s session on %s.\n", version, snapshot.OSDescription)
		sb.WriteString("Use POSIX shell idioms (bash/sh).\n")
		sb.WriteString("- Set environment variables for the next command with `export NAME=value`.\n")
		sb.WriteString("- Reference environment variables as `$NAME` or `${NAME}`.\n")
		sb.WriteString("- Paths use `/` separators.\n")
	default:
		fmt.Fprintf(&sb, "You are operating a shell session on %s.\n", snapshot.OSDescription)
	}

	if snapshot.WorkingDirectory != "" {
		fmt.Fprintf(&sb, "Working directory: %s\n", snapshot.WorkingDirectory)
	}

	installed, missing := formatToolVersions(snapshot.ToolVersions)
	if len(installed) > 0 {
		fmt.Fprintf(&sb, "Available CLIs: %s\n", strings.Join(installed, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&sb, "Not installed: %s\n", strings.Join(missing, ", "))
	}

	return strings.TrimSpace(sb.String())
}

func formatToolVersions(versions map[string]ToolVersion) (installed, missing []string) {
	keys := make([]string, 0, len(versions))
	for name := range versions {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		version := versions[name]
		if version.Found {
			installed = append(installed, fmt.Sprintf("%s (%s)", name, version.Version))
		} else {
			missing = append(missing, name)
		}
	}
	return installed, missing
}

func cloneShellEnvironmentSnapshot(snapshot ShellEnvironmentSnapshot) ShellEnvironmentSnapshot {
	out := snapshot
	if snapshot.ToolVersions != nil {
		out.ToolVersions = make(map[string]ToolVersion, len(snapshot.ToolVersions))
		maps.Copy(out.ToolVersions, snapshot.ToolVersions)
	}
	return out
}

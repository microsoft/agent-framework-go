// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/tool"
)

var (
	errCommandRejected = errors.New("shelltool: command rejected")
	errCommandIO       = errors.New("shelltool: command I/O failure")
)

// defaultMaxOutputBytes is the default cap for captured stdout per command (64 KiB).
const defaultMaxOutputBytes = 64 * 1024

// --------------------------------------------------------------------------
// LocalConfig
// --------------------------------------------------------------------------

// LocalConfig configures the shell tool returned by [NewLocal].
type LocalConfig struct {
	// Shell is an optional override for the shell binary path.  When empty,
	// the AGENT_FRAMEWORK_SHELL environment variable is consulted; if that is
	// also unset, the OS default is used (/bin/bash on POSIX, pwsh/cmd on
	// Windows). Mutually exclusive with [LocalConfig.ShellArgv].
	Shell string

	// ShellArgv overrides the shell launch argv. The first element is the
	// shell binary; remaining elements are passed as a launch-time prefix
	// before the standard -c / -Command / persistent suffix. Mutually
	// exclusive with [LocalConfig.Shell].
	ShellArgv []string

	// Mode selects stateless-per-call or persistent-shell execution.
	// Defaults to [ModePersistent].
	Mode Mode

	// WorkingDirectory is the initial working directory for the shell.
	// Defaults to the current process working directory.
	WorkingDirectory string

	// DisableWorkingDirectoryConfinement allows persistent shell working
	// directory changes to carry across calls. By default, each persistent
	// command is prefixed with a cd/Set-Location back to
	// [LocalConfig.WorkingDirectory]. It has no effect when WorkingDirectory is
	// empty.
	DisableWorkingDirectoryConfinement bool

	// Environment contains extra environment variables for the spawned shell.
	// Nil means no overrides.
	Environment map[string]string

	// RemoveEnvironment contains inherited environment variable names to omit
	// from the spawned shell before [LocalConfig.Environment] is applied.
	RemoveEnvironment []string

	// CleanEnvironment starts the shell with only a small allowlist of
	// inherited variables (PATH, HOME, USER, USERNAME, USERPROFILE,
	// SystemRoot, TEMP, TMP) before applying [LocalConfig.Environment].
	CleanEnvironment bool

	// Timeout is the per-command deadline. Zero (the default) means no
	// timeout. 30s is the recommended value.
	Timeout time.Duration

	// MaxOutputBytes caps each captured output stream per command.
	// Defaults to 64 KiB. Output beyond this limit is
	// silently truncated and [Result.Truncated] is set.
	MaxOutputBytes int

	// Policy is an optional allow/deny filter checked before the command
	// reaches the shell. Nil means allow everything.
	Policy *Policy

	// AcknowledgeUnsafe opts out of the default approval-required gate.
	// When false (the default), the returned tool's ApprovalRequired method
	// reports true so the harness prompts a human before every execution. Set
	// this to true only when you have an independent isolation mechanism and
	// accept the risk.
	AcknowledgeUnsafe bool
}

func (o LocalConfig) maxOutputBytes() int {
	if o.MaxOutputBytes > 0 {
		return o.MaxOutputBytes
	}
	return defaultMaxOutputBytes
}

func (o LocalConfig) validate() error {
	if o.MaxOutputBytes < 0 {
		return fmt.Errorf("shelltool: MaxOutputBytes must be non-negative")
	}
	shell, err := o.resolvedShell()
	if err != nil {
		return err
	}
	if o.Mode == ModePersistent && shell.kind == shellKindCmd {
		return fmt.Errorf("shelltool: persistent mode is not supported for cmd.exe; use pwsh, powershell, or a POSIX shell")
	}
	return nil
}

func (o LocalConfig) confineWorkingDirectory() bool {
	return !o.DisableWorkingDirectoryConfinement
}

// --------------------------------------------------------------------------
// Public constructor
// --------------------------------------------------------------------------

// NewLocal returns a local shell command tool for an agent.
func NewLocal(opts LocalConfig) (*Local, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &Local{exec: &localShellExecutor{opts: opts}}, nil
}

// --------------------------------------------------------------------------
// Local — implements tool.FuncTool
// --------------------------------------------------------------------------

type shellInput struct {
	Command string `json:"command"`
}

// Local runs shell commands on behalf of an agent.
type Local struct {
	exec *localShellExecutor
}

func (t *Local) Name() string { return "run_shell" }

func (t *Local) Description() string {
	return t.exec.opts.defaultDescription()
}

func (t *Local) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *Local) ReturnSchema() any { return nil }

func (t *Local) Call(ctx context.Context, args string) (any, error) {
	var in shellInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return nil, fmt.Errorf("shelltool: invalid arguments: %w", err)
	}
	if in.Command == "" {
		return nil, fmt.Errorf("shelltool: command must not be empty")
	}
	result, err := t.Run(ctx, in.Command)
	if err != nil {
		return nil, err
	}
	return result.FormatForModel(), nil
}

// Run executes command and returns its raw shell result.
func (t *Local) Run(ctx context.Context, command string) (Result, error) {
	if err := t.exec.opts.validate(); err != nil {
		return Result{}, err
	}
	request := ShellRequest{Command: command, WorkingDirectory: t.exec.opts.WorkingDirectory}
	if allowed, reason := t.exec.opts.Policy.Evaluate(request); !allowed {
		return Result{}, fmt.Errorf("%w: %s", errCommandRejected, reason)
	}
	return t.exec.run(ctx, command)
}

// Initialize starts a persistent shell early; it is a no-op in stateless mode.
func (t *Local) Initialize(ctx context.Context) error {
	return t.exec.initialize(ctx)
}

// Close terminates any persistent shell owned by the tool.
func (t *Local) Close() error {
	t.exec.close()
	return nil
}

// ApprovalRequired reports whether calls should require human approval.
func (t *Local) ApprovalRequired() bool { return !t.exec.opts.AcknowledgeUnsafe }

var (
	_ tool.FuncTool             = (*Local)(nil)
	_ tool.ApprovalRequiredTool = (*Local)(nil)
	_ Executor                  = (*Local)(nil)
)

// --------------------------------------------------------------------------
// localShellExecutor — manages shell lifecycle
// --------------------------------------------------------------------------

type localShellExecutor struct {
	opts    LocalConfig
	mu      sync.Mutex
	session *persistentSession
}

func (e *localShellExecutor) run(ctx context.Context, command string) (Result, error) {
	if err := e.opts.validate(); err != nil {
		return Result{}, err
	}
	if e.opts.Mode == ModePersistent {
		return e.runPersistent(ctx, command)
	}
	return runStateless(ctx, e.opts, command)
}

func (e *localShellExecutor) initialize(ctx context.Context) error {
	if err := e.opts.validate(); err != nil {
		return err
	}
	if e.opts.Mode != ModePersistent {
		return nil
	}
	shell, err := e.opts.resolvedShell()
	if err != nil {
		return err
	}
	command := ":"
	if shell.kind == shellKindPowerShell {
		command = "$null"
	}
	_, err = e.runPersistent(ctx, command)
	return err
}

func (e *localShellExecutor) close() {
	e.mu.Lock()
	sess := e.session
	e.session = nil
	e.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
}

func (e *localShellExecutor) runPersistent(ctx context.Context, command string) (Result, error) {
	e.mu.Lock()
	if e.session == nil || !e.session.isAlive() {
		// Replace a dead session (caused by a previous timeout).
		if e.session != nil {
			go e.session.Close()
		}
		shell, err := e.opts.resolvedShell()
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		sess, err := newPersistentSession(persistentSessionConfig{
			shell:                   shell,
			workingDirectory:        e.opts.WorkingDirectory,
			confineWorkingDirectory: e.opts.confineWorkingDirectory(),
			environment:             e.opts.Environment,
			removeEnvironment:       e.opts.RemoveEnvironment,
			cleanEnvironment:        e.opts.CleanEnvironment,
		})
		if err != nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("%w: shelltool: start persistent shell: %w", errCommandIO, err)
		}
		e.session = sess
	}
	sess := e.session
	e.mu.Unlock()

	result, err := sess.run(ctx, command, e.opts.Timeout, e.opts.maxOutputBytes())

	// If the session timed out and marked itself dead, evict it so the next
	// call gets a fresh shell with clean state.
	if !sess.isAlive() {
		e.mu.Lock()
		if e.session == sess {
			go e.session.Close()
			e.session = nil
		}
		e.mu.Unlock()
	}

	return result, err
}

// --------------------------------------------------------------------------
// stateless execution
// --------------------------------------------------------------------------

func runStateless(ctx context.Context, opts LocalConfig, command string) (Result, error) {
	shell, err := opts.resolvedShell()
	if err != nil {
		return Result{}, err
	}
	argv := shell.statelessArgvForCommand(command)

	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	if err := runCtx.Err(); err != nil {
		return Result{}, err
	}

	cmd := exec.Command(shell.binary, argv...)
	cmd.SysProcAttr = newProcessTreeSysProcAttr()
	if opts.WorkingDirectory != "" {
		cmd.Dir = opts.WorkingDirectory
	}
	cmd.Env = commandEnvironment(opts.CleanEnvironment, opts.Environment, opts.RemoveEnvironment)
	if shell.kind == shellKindPowerShell {
		cmd.Env = setEnvironmentListValue(cmd.Env, "PSDefaultParameterValues", "Out-File:Encoding=utf8")
	}

	maxBytes := opts.maxOutputBytes()
	outBuf := &headTailBuffer{cap: maxBytes}
	errBuf := &headTailBuffer{cap: maxBytes}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	start := time.Now()
	tree, err := startProcessTree(cmd)
	if err != nil {
		return Result{}, fmt.Errorf("%w: shelltool: run: %w", errCommandIO, err)
	}
	defer closeProcessTree(tree)
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	timedOut := false
	var runErr error
	select {
	case runErr = <-waitCh:
	case <-runCtx.Done():
		killProcessTree(cmd, tree)
		runErr = <-waitCh
		if runCtx.Err() == context.DeadlineExceeded {
			timedOut = true
		} else {
			return Result{}, fmt.Errorf("%w: shelltool: run: %w", errCommandIO, runCtx.Err())
		}
	}
	dur := time.Since(start)

	exitCode := 0
	if timedOut {
		exitCode = exitCodeTimedOut
	} else if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("%w: shelltool: run: %w", errCommandIO, runErr)
		}
	}

	return Result{
		Stdout:    outBuf.String(),
		Stderr:    errBuf.String(),
		ExitCode:  exitCode,
		Duration:  dur,
		Truncated: outBuf.truncated || errBuf.truncated,
		TimedOut:  timedOut,
	}, nil
}

// --------------------------------------------------------------------------
// headTailBuffer — bounded output accumulator
// --------------------------------------------------------------------------

// headTailBuffer keeps up to cap bytes: the first half as head and the most
// recent half as a rolling tail. When the total exceeds cap, the middle is
// dropped and [Result.Truncated] is set. This mirrors the .NET HeadTailBuffer.
type headTailBuffer struct {
	cap        int
	head       []byte
	tail       [][]byte // queue of complete rune-byte slices
	tailBytes  int
	totalBytes int
	truncated  bool
}

func (b *headTailBuffer) Write(p []byte) (int, error) {
	n := len(p) // original byte count to return
	headCap := b.cap / 2
	tailCap := b.cap - headCap

	for len(p) > 0 {
		r, size := utf8.DecodeRune(p)
		// Preserve invalid UTF-8 bytes as-is: re-encoding RuneError (U+FFFD)
		// needs 3 bytes but an invalid byte has size==1, which would panic in
		// utf8.EncodeRune if we allocated only size bytes.
		var encoded []byte
		if r == utf8.RuneError && size == 1 {
			encoded = []byte{p[0]}
		} else {
			encoded = make([]byte, size)
			utf8.EncodeRune(encoded, r)
		}
		p = p[size:]

		b.totalBytes += size

		if len(b.head)+len(encoded) <= headCap {
			b.head = append(b.head, encoded...)
			continue
		}
		// Head full — append to tail.
		b.tail = append(b.tail, encoded)
		b.tailBytes += len(encoded)
		// Evict oldest rune-chunk from tail until within budget.
		for b.tailBytes > tailCap && len(b.tail) > 0 {
			b.tailBytes -= len(b.tail[0])
			b.tail = b.tail[1:]
			b.truncated = true
		}
	}
	return n, nil
}

func (b *headTailBuffer) String() string {
	if len(b.tail) == 0 {
		return string(b.head)
	}
	var sb strings.Builder
	sb.Write(b.head)
	dropped := b.totalBytes - len(b.head) - b.tailBytes
	if dropped > 0 {
		fmt.Fprintf(&sb, "\n[... truncated %d bytes ...]\n", dropped)
	}
	for _, chunk := range b.tail {
		sb.Write(chunk)
	}
	return sb.String()
}

// --------------------------------------------------------------------------
// Shell resolution helpers
// --------------------------------------------------------------------------

type shellKind int

const (
	shellKindBash shellKind = iota
	shellKindPowerShell
	shellKindCmd
	shellKindSh
)

type resolvedShell struct {
	binary    string
	kind      shellKind
	extraArgv []string
}

// resolveShell returns the shell binary to use. Resolution order:
//  1. explicit override argument
//  2. AGENT_FRAMEWORK_SHELL environment variable
//  3. OS default: /bin/bash → /bin/sh on POSIX; pwsh → cmd.exe on Windows
func resolveShell(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if env := os.Getenv("AGENT_FRAMEWORK_SHELL"); strings.TrimSpace(env) != "" {
		return env
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("pwsh"); err == nil {
			return p
		}
		if p, err := exec.LookPath("powershell"); err == nil {
			return p
		}
		return `C:\Windows\System32\cmd.exe`
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}

func (o LocalConfig) resolvedShell() (resolvedShell, error) {
	if strings.TrimSpace(o.Shell) != "" && o.ShellArgv != nil {
		return resolvedShell{}, fmt.Errorf("shelltool: pass either Shell or ShellArgv, not both")
	}
	if o.ShellArgv != nil {
		if len(o.ShellArgv) == 0 || strings.TrimSpace(o.ShellArgv[0]) == "" {
			return resolvedShell{}, fmt.Errorf("shelltool: ShellArgv must contain a shell binary")
		}
		return resolvedShell{
			binary:    o.ShellArgv[0],
			kind:      classifyShellKind(o.ShellArgv[0]),
			extraArgv: append([]string(nil), o.ShellArgv[1:]...),
		}, nil
	}
	binary := resolveShell(o.Shell)
	return resolvedShell{binary: binary, kind: classifyShellKind(binary)}, nil
}

func (o LocalConfig) defaultDescription() string {
	shell, err := o.resolvedShell()
	if err != nil {
		return "Execute a single shell command on the local machine and return its stdout, stderr, and exit code."
	}

	var sb strings.Builder
	sb.WriteString("Execute a single shell command on the local machine and return its stdout, stderr, and exit code. ")
	sb.WriteString("Operating system: ")
	sb.WriteString(localOSName())
	sb.WriteString(". Shell: ")
	sb.WriteString(shellKindDescription(shell))
	sb.WriteString(". ")

	switch shell.kind {
	case shellKindPowerShell:
		sb.WriteString("Use PowerShell syntax, not bash/sh syntax. ")
		sb.WriteString("Examples: `cd $env:TEMP`, `$env:VAR = 'x'`, `$env:VAR`, `Get-ChildItem`, `Get-Content`, and `Select-String`. ")
	case shellKindBash:
		sb.WriteString("Use POSIX shell syntax. Bash-specific features are available. ")
	case shellKindSh:
		sb.WriteString("Use POSIX shell syntax. Avoid bash-only features like `[[ ... ]]`, arrays, here-strings, and `set -o pipefail`. ")
	}

	if o.Mode == ModePersistent {
		sb.WriteString("PERSISTENT MODE: a single long-lived shell handles every call; directory changes, exported variables, and function definitions persist across calls. Change directory once, then run subsequent commands without re-cd'ing. ")
	} else {
		sb.WriteString("STATELESS MODE: each call runs in a fresh shell; working directory and environment changes do not carry across calls. Combine related steps into one command if state matters. ")
	}
	if o.Timeout > 0 {
		fmt.Fprintf(&sb, "Per-call timeout: %ds. ", int(o.Timeout.Seconds()))
	}
	fmt.Fprintf(&sb, "Output is truncated to %d bytes per stream (head + tail). ", o.maxOutputBytes())
	if o.AcknowledgeUnsafe {
		sb.WriteString("Approval gating has been explicitly disabled.")
	} else {
		sb.WriteString("The user reviews and approves every call.")
	}
	return sb.String()
}

func localOSName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return "POSIX"
	}
}

func shellKindDescription(shell resolvedShell) string {
	switch shell.kind {
	case shellKindPowerShell:
		return "PowerShell (binary: '" + shell.binary + "')"
	case shellKindCmd:
		return "cmd.exe (binary: '" + shell.binary + "')"
	case shellKindBash:
		return "bash (binary: '" + shell.binary + "')"
	default:
		return "POSIX sh (binary: '" + shell.binary + "')"
	}
}

func (s resolvedShell) statelessArgvForCommand(command string) []string {
	var suffix []string
	switch s.kind {
	case shellKindPowerShell:
		suffix = []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-Command", command}
	case shellKindCmd:
		suffix = []string{"/d", "/c", command}
	case shellKindBash:
		suffix = []string{"--noprofile", "--norc", "-c", command}
	default:
		suffix = []string{"-c", command}
	}
	return combineArgv(s.extraArgv, suffix)
}

func (s resolvedShell) persistentArgv() ([]string, error) {
	var suffix []string
	switch s.kind {
	case shellKindPowerShell:
		suffix = []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-Command", "-"}
	case shellKindCmd:
		return nil, fmt.Errorf("persistent mode is not supported for cmd.exe; use pwsh, powershell, or a POSIX shell")
	case shellKindBash:
		suffix = []string{"--noprofile", "--norc"}
	default:
		suffix = nil
	}
	return append([]string{s.binary}, combineArgv(s.extraArgv, suffix)...), nil
}

func combineArgv(extra, suffix []string) []string {
	if len(extra) == 0 {
		return append([]string(nil), suffix...)
	}
	combined := make([]string, 0, len(extra)+len(suffix))
	combined = append(combined, extra...)
	combined = append(combined, suffix...)
	return combined
}

func classifyShellKind(shell string) shellKind {
	base := strings.TrimSuffix(shellBase(shell), ".exe")
	switch base {
	case "pwsh", "powershell":
		return shellKindPowerShell
	case "cmd":
		return shellKindCmd
	case "bash":
		return shellKindBash
	default:
		return shellKindSh
	}
}

func shellBase(shell string) string {
	for i := len(shell) - 1; i >= 0; i-- {
		if shell[i] == '/' || shell[i] == '\\' {
			return strings.ToLower(shell[i+1:])
		}
	}
	return strings.ToLower(shell)
}

var preservedEnvironmentVariables = []string{
	"PATH",
	"HOME",
	"USER",
	"USERNAME",
	"USERPROFILE",
	"SystemRoot",
	"TEMP",
	"TMP",
}

func commandEnvironment(clean bool, overrides map[string]string, removals []string) []string {
	if !clean && len(overrides) == 0 && len(removals) == 0 {
		return nil
	}
	env := make(map[string]string)
	if clean {
		for _, name := range preservedEnvironmentVariables {
			if value, ok := lookupEnvFold(name); ok {
				env[name] = value
			}
		}
	} else {
		for _, entry := range os.Environ() {
			name, value, ok := strings.Cut(entry, "=")
			if ok {
				env[name] = value
			}
		}
	}

	for _, name := range removals {
		deleteEnvironmentValue(env, name)
	}
	for name, value := range overrides {
		setEnvironmentValue(env, name, value)
	}

	keys := make([]string, 0, len(env))
	for name := range env {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, name := range keys {
		result = append(result, name+"="+env[name])
	}
	return result
}

func lookupEnvFold(name string) (string, bool) {
	for _, entry := range os.Environ() {
		envName, envValue, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(envName, name) {
			return envValue, true
		}
	}
	return "", false
}

func deleteEnvironmentValue(env map[string]string, name string) {
	for key := range env {
		if strings.EqualFold(key, name) {
			delete(env, key)
		}
	}
}

func setEnvironmentValue(env map[string]string, name string, value string) {
	for key := range env {
		if strings.EqualFold(key, name) {
			env[key] = value
			return
		}
	}
	env[name] = value
}

func setEnvironmentListValue(env []string, name string, value string) []string {
	if env == nil {
		env = os.Environ()
	}
	entry := name + "=" + value
	for i, item := range env {
		envName, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(envName, name) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

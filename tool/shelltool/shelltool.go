// Copyright (c) Microsoft. All rights reserved.

// Package shelltool provides a shell command execution tool that can be
// registered with an agent. It mirrors the .NET LocalShellTool / LocalShellExecutor
// design: approval-in-the-loop is the default security boundary; a deny-list
// [Policy] offers a best-effort pre-execution guardrail.
//
// # Security
//
// Running agent-generated shell commands is inherently dangerous.  This package
// provides two complementary controls:
//
//   - [Policy]: a deny-list of regular expressions that reject commands before
//     they reach the shell.  The deny-list is a UX guardrail, NOT a security
//     boundary — a determined model can trivially work around regex checks.
//
//   - Approval-in-the-loop: [New] returns a [tool.FuncTool] that also
//     implements [tool.ApprovalRequiredTool] so the harness tool-approval
//     middleware prompts a human before every execution.  This is the primary
//     security control.  Pass [Options.AcknowledgeUnsafe] = true only when
//     you have an independent isolation mechanism (e.g. a Docker container)
//     and understand the risk.
//
// # Usage
//
//	t := shelltool.New(shelltool.Options{})
//	cfg := agent.Config{Tools: []tool.FuncTool{t}}
package shelltool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/tool"
)

// DefaultTimeout is the recommended per-command timeout (30 s). It is not
// applied by default; pass it via [Options.Timeout] to opt in.
const DefaultTimeout = 30 * time.Second

// DefaultMaxOutputBytes is the default cap for captured stdout per command (64 KiB).
const DefaultMaxOutputBytes = 64 * 1024

// --------------------------------------------------------------------------
// Result
// --------------------------------------------------------------------------

// Result is the outcome of a single shell command invocation.
type Result struct {
	// Stdout is the captured standard output, possibly truncated.
	Stdout string
	// Stderr is the captured standard error, possibly truncated.
	Stderr string
	// ExitCode is the exit status reported by the process. -1 if the process
	// did not exit cleanly.
	ExitCode int
	// Duration is how long the command ran end-to-end.
	Duration time.Duration
	// Truncated is true when stdout or stderr was truncated.
	Truncated bool
	// TimedOut is true when the command was killed for exceeding the timeout.
	TimedOut bool
}

// FormatForModel returns a single text block combining stdout, stderr, status
// flags, and the exit code — suitable for returning to the language model.
func (r Result) FormatForModel() string {
	var sb strings.Builder
	if r.Stdout != "" {
		sb.WriteString(r.Stdout)
		if r.Truncated {
			sb.WriteString("\n[stdout truncated]")
		}
		sb.WriteByte('\n')
	}
	if r.Stderr != "" {
		sb.WriteString("stderr: ")
		sb.WriteString(r.Stderr)
		sb.WriteByte('\n')
	}
	if r.TimedOut {
		sb.WriteString("[command timed out]\n")
	}
	sb.WriteString("exit_code: ")
	sb.WriteString(fmt.Sprint(r.ExitCode))
	return sb.String()
}

// --------------------------------------------------------------------------
// Policy
// --------------------------------------------------------------------------

// Policy is a deny-list that rejects commands whose text matches any of the
// configured patterns before they reach the shell.
//
// The deny-list is a UX guardrail, NOT a security boundary. It is intended to
// prevent obviously dangerous commands (e.g. rm -rf /) while the primary
// isolation is approval-in-the-loop or container sandboxing.
type Policy struct {
	deny []*regexp.Regexp
}

// NewPolicy creates a [Policy] that denies any command matching one of the
// provided regular expressions.
func NewPolicy(denyPatterns ...string) (*Policy, error) {
	p := &Policy{}
	for _, pat := range denyPatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("shelltool: invalid deny pattern %q: %w", pat, err)
		}
		p.deny = append(p.deny, re)
	}
	return p, nil
}

// Evaluate returns (true, "") if none of the deny patterns match command, or
// (false, reason) for the first matching pattern.
func (p *Policy) Evaluate(command string) (allowed bool, reason string) {
	if p == nil {
		return true, ""
	}
	for _, re := range p.deny {
		if re.MatchString(command) {
			return false, "denied by policy pattern: " + re.String()
		}
	}
	return true, ""
}

// --------------------------------------------------------------------------
// Options
// --------------------------------------------------------------------------

// Mode controls whether each call spawns a fresh shell or reuses a single
// long-lived shell process.
type Mode int

const (
	// ModeStateless spawns a new shell process for every command. Safe to
	// share across concurrent calls; no state leaks between invocations.
	ModeStateless Mode = iota
	// ModePersistent keeps a single shell alive across calls so that
	// directory changes, exported variables, and history persist. A
	// persistent executor MUST NOT be shared across concurrent users or
	// agent sessions.
	ModePersistent
)

// Options configures the shell tool returned by [New].
type Options struct {
	// Shell is an optional override for the shell binary path.  When empty,
	// the AGENT_FRAMEWORK_SHELL environment variable is consulted; if that is
	// also unset, the OS default is used (/bin/bash on POSIX, pwsh/cmd on
	// Windows).
	Shell string

	// Mode selects stateless-per-call or persistent-shell execution.
	// Defaults to [ModeStateless].
	Mode Mode

	// WorkingDirectory is the initial working directory for the shell.
	// Defaults to the current process working directory.
	WorkingDirectory string

	// Timeout is the per-command deadline. Zero (the default) means no
	// timeout. [DefaultTimeout] (30 s) is the recommended value.
	Timeout time.Duration

	// MaxOutputBytes caps the combined stdout captured per command.
	// Defaults to [DefaultMaxOutputBytes]. Output beyond this limit is
	// silently truncated and [Result.Truncated] is set.
	MaxOutputBytes int

	// Policy is an optional deny-list checked before the command reaches
	// the shell. Nil means allow everything.
	Policy *Policy

	// AcknowledgeUnsafe opts out of the default approval-required gate.
	// When false (the default) the returned tool also implements
	// [tool.ApprovalRequiredTool] so the harness prompts a human before
	// every execution. Set this to true only when you have an independent
	// isolation mechanism and accept the risk.
	AcknowledgeUnsafe bool
}

func (o Options) maxOutputBytes() int {
	if o.MaxOutputBytes > 0 {
		return o.MaxOutputBytes
	}
	return DefaultMaxOutputBytes
}

// --------------------------------------------------------------------------
// Public constructor
// --------------------------------------------------------------------------

// New returns a [tool.FuncTool] that runs shell commands on behalf of an
// agent. By default the tool also implements [tool.ApprovalRequiredTool];
// pass [Options.AcknowledgeUnsafe] = true to opt out.
func New(opts Options) tool.FuncTool {
	e := &shellExecutor{opts: opts}
	t := &shellTool{exec: e}
	if opts.AcknowledgeUnsafe {
		return t
	}
	return tool.ApprovalRequiredFunc(t)
}

// --------------------------------------------------------------------------
// shellTool — implements tool.FuncTool
// --------------------------------------------------------------------------

type shellInput struct {
	Command string `json:"command"`
}

type shellTool struct {
	exec *shellExecutor
}

func (t *shellTool) Name() string { return "shell" }

func (t *shellTool) Description() string {
	shell := resolveShell(t.exec.opts.Shell)
	return fmt.Sprintf(
		"Run a shell command using %s and return its output. "+
			"The result includes stdout, stderr, and the exit code.",
		shell,
	)
}

func (t *shellTool) Schema() any {
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

func (t *shellTool) ReturnSchema() any { return nil }

func (t *shellTool) Call(ctx tool.Context, args string) (any, error) {
	var in shellInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return nil, fmt.Errorf("shelltool: invalid arguments: %w", err)
	}
	if in.Command == "" {
		return nil, fmt.Errorf("shelltool: command must not be empty")
	}
	if allowed, reason := t.exec.opts.Policy.Evaluate(in.Command); !allowed {
		return nil, fmt.Errorf("shelltool: %s", reason)
	}
	result, err := t.exec.run(ctx.Context, in.Command)
	if err != nil {
		return nil, err
	}
	return result.FormatForModel(), nil
}

var _ tool.FuncTool = (*shellTool)(nil)

// --------------------------------------------------------------------------
// shellExecutor — manages shell lifecycle
// --------------------------------------------------------------------------

type shellExecutor struct {
	opts    Options
	mu      sync.Mutex
	session *persistentSession
}

func (e *shellExecutor) run(ctx context.Context, command string) (Result, error) {
	if e.opts.Mode == ModePersistent {
		return e.runPersistent(ctx, command)
	}
	return runStateless(ctx, e.opts, command)
}

func (e *shellExecutor) runPersistent(ctx context.Context, command string) (Result, error) {
	e.mu.Lock()
	if e.session == nil {
		sess, err := newPersistentSession(resolveShell(e.opts.Shell), e.opts.WorkingDirectory)
		if err != nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("shelltool: start persistent shell: %w", err)
		}
		e.session = sess
	}
	sess := e.session
	e.mu.Unlock()
	return sess.run(ctx, command, e.opts.Timeout, e.opts.maxOutputBytes())
}

// --------------------------------------------------------------------------
// stateless execution
// --------------------------------------------------------------------------

func runStateless(ctx context.Context, opts Options, command string) (Result, error) {
	shell := resolveShell(opts.Shell)
	argv := shellArgs(shell, command)

	runCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if opts.WorkingDirectory != "" {
		cmd.Dir = opts.WorkingDirectory
	}

	maxBytes := opts.maxOutputBytes()
	outBuf := &headTailBuffer{cap: maxBytes}
	errBuf := &headTailBuffer{cap: maxBytes / 4}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	timedOut := runCtx.Err() == context.DeadlineExceeded
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(runErr, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = 124
		} else {
			return Result{}, fmt.Errorf("shelltool: run: %w", runErr)
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

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

// --------------------------------------------------------------------------
// persistent shell session (sentinel-protocol)
// --------------------------------------------------------------------------

const sentinelMarker = "__AGENT_FRAMEWORK_DONE__"

// persistentSession wraps a long-lived shell process. A single background
// goroutine scans stdout lines and forwards them on the lines channel.
// Callers MUST hold the mutex for the lifetime of each run call.
type persistentSession struct {
	mu    sync.Mutex
	stdin io.WriteCloser
	lines chan string
}

func newPersistentSession(shell, workdir string) (*persistentSession, error) {
	cmd := exec.Command(shell)
	if workdir != "" {
		cmd.Dir = workdir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	s := &persistentSession{
		stdin: stdin,
		lines: make(chan string, 256),
	}
	// Single background goroutine reads lines for the lifetime of the session.
	go func() {
		scanner := bufio.NewScanner(outPipe)
		for scanner.Scan() {
			s.lines <- scanner.Text()
		}
		close(s.lines)
	}()
	return s, nil
}

// run sends command to the shell and collects output until the sentinel line.
func (s *persistentSession) run(ctx context.Context, command string, timeout time.Duration, maxBytes int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	script := fmt.Sprintf("%s\necho '%s' $?\n", command, sentinelMarker)
	if _, err := io.WriteString(s.stdin, script); err != nil {
		return Result{}, fmt.Errorf("shelltool: write to shell: %w", err)
	}

	outBuf := &headTailBuffer{cap: maxBytes}
	exitCode := 0
	timedOut := false

loop:
	for {
		select {
		case <-runCtx.Done():
			timedOut = true
			exitCode = 124
			break loop
		case line, ok := <-s.lines:
			if !ok {
				break loop
			}
			if strings.HasPrefix(line, sentinelMarker) {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					fmt.Sscan(parts[1], &exitCode) //nolint:errcheck
				}
				break loop
			}
			outBuf.Write([]byte(line + "\n")) //nolint:errcheck
		}
	}

	return Result{
		Stdout:    outBuf.String(),
		ExitCode:  exitCode,
		Duration:  time.Since(start),
		Truncated: outBuf.truncated,
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
		p = p[size:]
		encoded := make([]byte, size)
		utf8.EncodeRune(encoded, r)

		b.totalBytes += size

		if len(b.head)+size <= headCap {
			b.head = append(b.head, encoded...)
			continue
		}
		// Head full — append to tail.
		b.tail = append(b.tail, encoded)
		b.tailBytes += size
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

// resolveShell returns the shell binary to use. Resolution order:
//  1. explicit override argument
//  2. AGENT_FRAMEWORK_SHELL environment variable
//  3. OS default: /bin/bash → /bin/sh on POSIX; pwsh → cmd.exe on Windows
func resolveShell(override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("AGENT_FRAMEWORK_SHELL"); env != "" {
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

// shellArgs returns the argv for spawning the given shell with a one-shot
// command, following the conventions of the .NET ShellResolver.
func shellArgs(shell, command string) []string {
	base := shellBase(shell)
	switch {
	case base == "cmd" || base == "cmd.exe":
		return []string{shell, "/C", command}
	case base == "pwsh" || base == "pwsh.exe" ||
		base == "powershell" || base == "powershell.exe":
		return []string{shell, "-NonInteractive", "-Command", command}
	default: // bash / sh / zsh / …
		return []string{shell, "-c", command}
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

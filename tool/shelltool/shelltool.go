// Copyright (c) Microsoft. All rights reserved.

// Package shelltool provides a shell command execution tool that can be
// registered with an agent. It mirrors the .NET LocalShellTool / LocalShellExecutor
// design: approval-in-the-loop is the default security boundary; an allow/deny
// [Policy] offers a best-effort pre-execution guardrail.
//
// # Security
//
// Running agent-generated shell commands is inherently dangerous.  This package
// provides two complementary controls:
//
//   - [Policy]: an allow/deny list of regular expressions checked before
//     commands reach the shell. The policy is a UX guardrail, NOT a security
//     boundary — a determined model can trivially work around regex checks.
//
//   - Approval-in-the-loop: [NewLocal] returns a [tool.FuncTool] that also
//     implements [tool.ApprovalRequiredTool] so the harness tool-approval
//     middleware prompts a human before every execution.  This is the primary
//     security control.  Pass [LocalConfig.AcknowledgeUnsafe] = true only when
//     you have an independent isolation mechanism (e.g. a Docker container)
//     and understand the risk.
//
// # Usage
//
//	t := shelltool.NewLocal(shelltool.LocalConfig{})
//	cfg := agent.Config{Tools: []tool.Tool{t}}
package shelltool

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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
// applied by default; pass it via [LocalConfig.Timeout] to opt in.
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
	fmt.Fprint(&sb, r.ExitCode)
	return sb.String()
}

// --------------------------------------------------------------------------
// Policy
// --------------------------------------------------------------------------

// ShellRequest is a shell command awaiting a policy decision.
type ShellRequest struct {
	// Command is the full command line that the agent wants to run.
	Command string

	// WorkingDirectory is the optional working directory the command will
	// execute in, if known.
	WorkingDirectory string
}

// Policy is a layered allow/deny pattern filter for shell commands.
//
// The regex filter is a UX guardrail, NOT a security boundary. It is intended
// to fast-fail commands operators would rather reject before execution while
// the primary isolation is approval-in-the-loop or container sandboxing.
//
// A policy constructed with no patterns allows any non-empty command. Allow
// patterns are checked before deny patterns, so an allow match short-circuits
// evaluation and skips the deny list.
type Policy struct {
	denies []policyPattern
	allows []policyPattern
}

// PolicyConfig configures a [Policy].
type PolicyConfig struct {
	// DenyList contains patterns that trigger a deny outcome. Nil or empty
	// disables the deny list.
	DenyList []string

	// AllowList contains explicit-allow patterns. A match here short-circuits
	// the deny list.
	AllowList []string
}

type policyPattern struct {
	pattern string
	re      *regexp.Regexp
}

// NewPolicy creates a [Policy] from cfg. Patterns are matched case-insensitively.
func NewPolicy(cfg PolicyConfig) (*Policy, error) {
	denies, err := compilePolicyPatterns("deny", cfg.DenyList)
	if err != nil {
		return nil, err
	}
	allows, err := compilePolicyPatterns("allow", cfg.AllowList)
	if err != nil {
		return nil, err
	}
	return &Policy{denies: denies, allows: allows}, nil
}

func compilePolicyPatterns(kind string, patterns []string) ([]policyPattern, error) {
	compiled := make([]policyPattern, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile("(?i:" + pattern + ")")
		if err != nil {
			return nil, fmt.Errorf("shelltool: invalid %s pattern %q: %w", kind, pattern, err)
		}
		compiled = append(compiled, policyPattern{pattern: pattern, re: re})
	}
	return compiled, nil
}

// Evaluate returns whether request may run and a human-readable reason when
// one applies. Evaluation order is: empty-command guard, allow patterns, deny
// patterns, default allow.
func (p *Policy) Evaluate(request ShellRequest) (allowed bool, reason string) {
	if p == nil {
		return true, ""
	}
	command := strings.TrimSpace(request.Command)
	if command == "" {
		return false, "empty command"
	}
	for _, allow := range p.allows {
		if allow.re.MatchString(command) {
			return true, "matched allow pattern"
		}
	}
	for _, deny := range p.denies {
		if deny.re.MatchString(command) {
			return false, "matched deny pattern: " + deny.pattern
		}
	}
	return true, ""
}

// --------------------------------------------------------------------------
// LocalConfig
// --------------------------------------------------------------------------

// Mode controls whether each call spawns a fresh shell or reuses a single
// long-lived shell process.
type Mode int

const (
	// ModePersistent keeps a single shell alive across calls so that
	// directory changes, exported variables, and history persist. A
	// persistent executor MUST NOT be shared across concurrent users or
	// agent sessions.
	ModePersistent Mode = iota
	// ModeStateless spawns a new shell process for every command. Safe to
	// share across concurrent calls; no state leaks between invocations.
	ModeStateless
)

// LocalConfig configures the shell tool returned by [NewLocal].
type LocalConfig struct {
	// Shell is an optional override for the shell binary path.  When empty,
	// the AGENT_FRAMEWORK_SHELL environment variable is consulted; if that is
	// also unset, the OS default is used (/bin/bash on POSIX, pwsh/cmd on
	// Windows).
	Shell string

	// Mode selects stateless-per-call or persistent-shell execution.
	// Defaults to [ModePersistent].
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

	// Policy is an optional allow/deny filter checked before the command
	// reaches the shell. Nil means allow everything.
	Policy *Policy

	// AcknowledgeUnsafe opts out of the default approval-required gate.
	// When false (the default) the returned tool also implements
	// [tool.ApprovalRequiredTool] so the harness prompts a human before
	// every execution. Set this to true only when you have an independent
	// isolation mechanism and accept the risk.
	AcknowledgeUnsafe bool
}

func (o LocalConfig) maxOutputBytes() int {
	if o.MaxOutputBytes > 0 {
		return o.MaxOutputBytes
	}
	return DefaultMaxOutputBytes
}

// --------------------------------------------------------------------------
// Public constructor
// --------------------------------------------------------------------------

// NewLocal returns a [tool.FuncTool] that runs shell commands on behalf of an
// agent. By default the tool also implements [tool.ApprovalRequiredTool];
// pass [LocalConfig.AcknowledgeUnsafe] = true to opt out.
func NewLocal(opts LocalConfig) tool.FuncTool {
	e := &localShellExecutor{opts: opts}
	t := &localShellTool{exec: e}
	if opts.AcknowledgeUnsafe {
		return t
	}
	return tool.ApprovalRequiredFunc(t)
}

// --------------------------------------------------------------------------
// localShellTool — implements tool.FuncTool
// --------------------------------------------------------------------------

type shellInput struct {
	Command string `json:"command"`
}

type localShellTool struct {
	exec *localShellExecutor
}

func (t *localShellTool) Name() string { return "shell" }

func (t *localShellTool) Description() string {
	shell := resolveShell(t.exec.opts.Shell)
	return fmt.Sprintf(
		"Run a shell command using %s and return its output. "+
			"The result includes stdout, stderr, and the exit code.",
		shell,
	)
}

func (t *localShellTool) Schema() any {
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

func (t *localShellTool) ReturnSchema() any { return nil }

func (t *localShellTool) Call(ctx tool.Context, args string) (any, error) {
	var in shellInput
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return nil, fmt.Errorf("shelltool: invalid arguments: %w", err)
	}
	if in.Command == "" {
		return nil, fmt.Errorf("shelltool: command must not be empty")
	}
	request := ShellRequest{Command: in.Command, WorkingDirectory: t.exec.opts.WorkingDirectory}
	if allowed, reason := t.exec.opts.Policy.Evaluate(request); !allowed {
		return nil, fmt.Errorf("shelltool: %s", reason)
	}
	result, err := t.exec.run(ctx.Context, in.Command)
	if err != nil {
		return nil, err
	}
	return result.FormatForModel(), nil
}

var _ tool.FuncTool = (*localShellTool)(nil)

// --------------------------------------------------------------------------
// localShellExecutor — manages shell lifecycle
// --------------------------------------------------------------------------

type localShellExecutor struct {
	opts    LocalConfig
	mu      sync.Mutex
	session *persistentSession
}

func (e *localShellExecutor) run(ctx context.Context, command string) (Result, error) {
	if e.opts.Mode == ModePersistent {
		return e.runPersistent(ctx, command)
	}
	return runStateless(ctx, e.opts, command)
}

func (e *localShellExecutor) runPersistent(ctx context.Context, command string) (Result, error) {
	e.mu.Lock()
	if e.session == nil || e.session.dead {
		// Replace a dead session (caused by a previous timeout).
		if e.session != nil {
			go e.session.Close()
		}
		sess, err := newPersistentSession(resolveShell(e.opts.Shell), e.opts.WorkingDirectory)
		if err != nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("shelltool: start persistent shell: %w", err)
		}
		e.session = sess
	}
	sess := e.session
	e.mu.Unlock()

	result, err := sess.run(ctx, command, e.opts.Timeout, e.opts.maxOutputBytes())

	// If the session timed out and marked itself dead, evict it so the next
	// call gets a fresh shell with clean state.
	if sess.dead {
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
		if errors.As(runErr, &exitErr) {
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

// --------------------------------------------------------------------------
// persistent shell session (sentinel-protocol)
// --------------------------------------------------------------------------

// maxScanTokenSize is the maximum line length the scanner can handle (1 MiB).
// This overrides bufio.Scanner's default 64 KiB limit to avoid silent truncation
// of long output lines in persistent mode.
const maxScanTokenSize = 1 << 20

// newSentinelToken generates a unique per-invocation sentinel token using
// crypto/rand. A unique token prevents accidental sentinel matches caused by
// command output that happens to contain a constant marker string.
func newSentinelToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("shelltool: generate sentinel token: %w", err)
	}
	return fmt.Sprintf("__AGFW_%x__", b), nil
}

// persistentSession wraps a long-lived shell process. Two background goroutines
// scan stdout and stderr and forward lines on their respective channels.
// The session's mutex serialises concurrent run calls. The session is marked
// dead on timeout so the executor can replace it.
//
// Only POSIX-compatible shells (bash, sh, zsh, …) are supported in persistent
// mode because the sentinel protocol relies on POSIX $? and printf semantics.
type persistentSession struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	lines     chan string // stdout lines
	errLines  chan string // stderr lines
	dead      bool        // true after a timeout; executor must create a new session
	closeOnce sync.Once
	closeCh   chan struct{} // closed by Close() to unblock goroutines
}

// isPOSIXShell reports whether shell names a POSIX-compatible shell.
// cmd.exe, pwsh, and powershell use incompatible $?/$LASTEXITCODE semantics
// and are not supported by the persistent-session sentinel protocol.
func isPOSIXShell(shell string) bool {
	switch shellBase(shell) {
	case "cmd", "cmd.exe", "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		return false
	}
	return true
}

func newPersistentSession(shell, workdir string) (*persistentSession, error) {
	if !isPOSIXShell(shell) {
		return nil, fmt.Errorf(
			"shelltool: persistent mode requires a POSIX-compatible shell (bash/sh/zsh); %q is not supported",
			shell,
		)
	}

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
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	s := &persistentSession{
		cmd:      cmd,
		stdin:    stdin,
		lines:    make(chan string, 256),
		errLines: make(chan string, 256),
		closeCh:  make(chan struct{}),
	}

	var wg sync.WaitGroup
	wg.Add(2)

	scanPipe := func(pipe io.Reader, ch chan<- string) {
		defer wg.Done()
		defer close(ch)
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, maxScanTokenSize), maxScanTokenSize)
		for sc.Scan() {
			select {
			case ch <- sc.Text():
			case <-s.closeCh:
				return
			}
		}
	}

	go scanPipe(outPipe, s.lines)
	go scanPipe(errPipe, s.errLines)

	// Reap the child process after both pipes have been fully drained.
	go func() {
		wg.Wait()
		_ = cmd.Wait()
	}()

	return s, nil
}

// Close terminates the persistent session and releases all resources.
func (s *persistentSession) Close() {
	s.closeOnce.Do(func() {
		close(s.closeCh)
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	})
}

// run sends command to the shell and collects output until unique sentinel
// lines appear on both stdout and stderr. On timeout the session is marked
// dead so the executor creates a fresh session on the next call.
func (s *persistentSession) run(ctx context.Context, command string, timeout time.Duration, maxBytes int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dead {
		return Result{}, fmt.Errorf("shelltool: persistent session is dead after timeout; retry")
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Generate a unique per-invocation sentinel so command output cannot
	// accidentally contain the marker. A leading printf '\n' guarantees the
	// sentinel starts on its own line even if the command omitted a trailing
	// newline. The sentinel is echoed to both stdout and stderr so we can
	// collect and delimit each stream separately.
	token, err := newSentinelToken()
	if err != nil {
		return Result{}, err
	}
	start := time.Now()

	// The script:
	//   1. Run the command in a subshell (isolates $?).
	//   2. Capture the exit code into _AGFW_CODE.
	//   3. Emit "TOKEN CODE" to stdout then stderr, each preceded by a
	//      newline so the sentinel is always on its own line.
	script := "\n{ " + command + "\n}; _AGFW_CODE=$?\n" +
		"printf '\\n" + token + " %d\\n' \"$_AGFW_CODE\"\n" +
		"printf '\\n" + token + " %d\\n' \"$_AGFW_CODE\" >&2\n"
	if _, err := io.WriteString(s.stdin, script); err != nil {
		return Result{}, fmt.Errorf("shelltool: write to shell: %w", err)
	}

	outBuf := &headTailBuffer{cap: maxBytes}
	// Stderr typically carries short error messages; 1/4 of the stdout
	// budget keeps total memory usage proportional.
	errBuf := &headTailBuffer{cap: maxBytes / 4}
	exitCode := 0
	timedOut := false
	stdoutDone := false
	stderrDone := false

loop:
	for !stdoutDone || !stderrDone {
		select {
		case <-runCtx.Done():
			timedOut = true
			exitCode = 124
			s.dead = true
			break loop

		case line, ok := <-s.lines:
			if !ok {
				stdoutDone = true
				continue
			}
			if stdoutDone {
				// Discard post-sentinel stdout lines (e.g. from background jobs).
				continue
			}
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[0] == token {
				fmt.Sscan(parts[1], &exitCode) //nolint:errcheck
				stdoutDone = true
			} else {
				_, _ = outBuf.Write([]byte(line + "\n"))
			}

		case line, ok := <-s.errLines:
			if !ok {
				stderrDone = true
				continue
			}
			if stderrDone {
				// Discard post-sentinel stderr lines.
				continue
			}
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[0] == token {
				stderrDone = true
			} else {
				_, _ = errBuf.Write([]byte(line + "\n"))
			}
		}
	}

	return Result{
		Stdout:    outBuf.String(),
		Stderr:    errBuf.String(),
		ExitCode:  exitCode,
		Duration:  time.Since(start),
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
	switch base {
	case "cmd", "cmd.exe":
		return []string{shell, "/C", command}
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
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

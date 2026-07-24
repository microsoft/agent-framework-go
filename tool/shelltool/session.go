// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	sessionReadChunk  = 64 * 1024
	shutdownGrace     = 2 * time.Second
	stderrQuiescence  = 50 * time.Millisecond
	interruptGrace    = 500 * time.Millisecond
	exitCodeTimedOut  = 124
	exitCodeUnstarted = -1
)

func newSignal() chan struct{} {
	return make(chan struct{})
}

func newSentinelTag() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("shelltool: generate sentinel tag: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

func newSentinelToken(tag string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("shelltool: generate sentinel token: %w", err)
	}
	return fmt.Sprintf("__AF_END_%s_%x__", tag, b), nil
}

// persistentSession wraps a long-lived shell process. State such as the current
// directory, exported variables, and function definitions is preserved across
// calls. Commands are serialized onto a single stdin/stdout pipe and delimited
// by a sentinel written to stdout, matching the .NET ShellSession protocol.
type persistentSession struct {
	mu                      sync.Mutex
	lifecycleMu             sync.Mutex
	cmd                     *exec.Cmd
	tree                    *processTree
	stdin                   io.WriteCloser
	kind                    shellKind
	workingDirectory        string
	confineWorkingDirectory bool
	sentinelTag             string

	bufferGate   sync.Mutex
	stdoutBuf    []byte
	stderrBuf    []byte
	stdoutSignal chan struct{}
	stdoutClosed bool

	readerWG sync.WaitGroup
	waitCh   chan struct{}

	dead      atomic.Bool
	closeOnce sync.Once
}

type persistentSessionConfig struct {
	shell                   resolvedShell
	workingDirectory        string
	confineWorkingDirectory bool
	environment             map[string]string
	removeEnvironment       []string
	cleanEnvironment        bool
}

func newPersistentSession(cfg persistentSessionConfig) (*persistentSession, error) {
	argv, err := cfg.shell.persistentArgv()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = newSessionSysProcAttr()
	if cfg.workingDirectory != "" {
		cmd.Dir = cfg.workingDirectory
	}
	cmd.Env = commandEnvironment(cfg.cleanEnvironment, cfg.environment, cfg.removeEnvironment)

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

	tag, err := newSentinelTag()
	if err != nil {
		return nil, err
	}

	s := &persistentSession{
		cmd:                     cmd,
		stdin:                   stdin,
		kind:                    cfg.shell.kind,
		workingDirectory:        cfg.workingDirectory,
		confineWorkingDirectory: cfg.confineWorkingDirectory,
		sentinelTag:             tag,
		stdoutBuf:               make([]byte, 0, 4096),
		stderrBuf:               make([]byte, 0, 1024),
		stdoutSignal:            newSignal(),
		waitCh:                  make(chan struct{}),
	}

	tree, err := startProcessTree(cmd)
	if err != nil {
		return nil, err
	}
	s.tree = tree

	s.readerWG.Add(2)
	go s.readLoop(outPipe, &s.stdoutBuf, true)
	go s.readLoop(errPipe, &s.stderrBuf, false)
	go func() {
		s.readerWG.Wait()
		_ = cmd.Wait()
		s.dead.Store(true)
		closeProcessTree(tree)
		close(s.waitCh)
	}()

	if cfg.shell.kind == shellKindPowerShell {
		if err := s.writeRaw("$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false);$ErrorActionPreference = 'Stop'\n"); err != nil {
			s.Close()
			return nil, err
		}
	}

	return s, nil
}

// Close terminates the persistent session and releases process resources.
func (s *persistentSession) Close() {
	s.dead.Store(true)

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.closeOnce.Do(func() {
		if s.stdin != nil {
			_, _ = io.WriteString(s.stdin, "exit\n")
			_ = s.stdin.Close()
		}

		select {
		case <-s.waitCh:
		case <-time.After(shutdownGrace):
			killProcessTree(s.cmd, s.tree)
			select {
			case <-s.waitCh:
			case <-time.After(shutdownGrace):
			}
		}
	})
}

// run sends command to the shell and collects output until the stdout sentinel
// appears. On timeout the session first tries to interrupt the in-flight
// command so the shell can survive; if the sentinel still does not arrive, the
// session is closed and the executor will create a fresh one for the next call.
func (s *persistentSession) run(ctx context.Context, command string, timeout time.Duration, maxBytes int) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isAlive() {
		return Result{}, fmt.Errorf("shelltool: persistent session is dead after timeout; retry")
	}

	token, err := newSentinelToken(s.sentinelTag)
	if err != nil {
		return Result{}, err
	}
	script := s.buildScript(command, token)

	stdoutOffset, stderrOffset := s.snapshotOffsets()
	start := time.Now()
	if err := s.writeRaw(script); err != nil {
		s.dead.Store(true)
		return Result{}, fmt.Errorf("shelltool: write to shell: %w", err)
	}

	hardCap := maxBytes * 4
	if hardCap <= 0 {
		hardCap = defaultMaxOutputBytes * 4
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	sentinelIdx, exitCode, timedOut, overflow, err := s.waitForSentinel(runCtx, []byte(token), stdoutOffset, hardCap)
	if err != nil {
		return Result{}, err
	}

	if timedOut {
		s.interruptCurrentCommand()
		graceCtx, graceCancel := context.WithTimeout(ctx, interruptGrace)
		defer graceCancel()
		postIdx, _, postTimedOut, postOverflow, err := s.waitForSentinel(graceCtx, []byte(token), stdoutOffset, hardCap)
		if err != nil && ctx.Err() != nil {
			return Result{}, err
		}
		if err == nil && !postTimedOut && !postOverflow && postIdx >= 0 {
			return s.resultFromBuffers(ctx, stdoutOffset, postIdx, stderrOffset, exitCodeTimedOut, true, time.Since(start), maxBytes), nil
		}
	}

	if timedOut || overflow {
		s.dead.Store(true)
		s.Close()
		return s.resultFromBuffers(ctx, stdoutOffset, -1, stderrOffset, exitCodeForClosedSession(timedOut), timedOut, time.Since(start), maxBytes), nil
	}

	return s.resultFromBuffers(ctx, stdoutOffset, sentinelIdx, stderrOffset, exitCode, false, time.Since(start), maxBytes), nil
}

func (s *persistentSession) writeRaw(text string) error {
	_, err := io.WriteString(s.stdin, text)
	return err
}

func (s *persistentSession) isAlive() bool {
	if s == nil || s.dead.Load() {
		return false
	}
	select {
	case <-s.waitCh:
		return false
	default:
		return true
	}
}

func (s *persistentSession) snapshotOffsets() (stdoutOffset, stderrOffset int) {
	s.bufferGate.Lock()
	defer s.bufferGate.Unlock()
	stdoutOffset = len(s.stdoutBuf)
	stderrOffset = len(s.stderrBuf)
	s.stdoutSignal = newSignal()
	return stdoutOffset, stderrOffset
}

func (s *persistentSession) waitForSentinel(ctx context.Context, needle []byte, searchFrom, hardCap int) (sentinelIdx, exitCode int, timedOut, overflow bool, err error) {
	for {
		s.bufferGate.Lock()
		idx := indexOf(s.stdoutBuf, needle, searchFrom)
		bufLen := len(s.stdoutBuf)
		closed := s.stdoutClosed
		signal := s.stdoutSignal
		s.bufferGate.Unlock()

		if idx >= 0 {
			rc, err := s.readExitCode(ctx, idx+len(needle))
			return idx, rc, false, false, err
		}
		if bufLen-searchFrom > hardCap {
			return -1, exitCodeUnstarted, false, true, nil
		}
		if closed {
			return -1, exitCodeUnstarted, false, true, nil
		}

		select {
		case <-signal:
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return -1, exitCodeUnstarted, true, false, nil
			}
			return -1, exitCodeUnstarted, false, false, ctx.Err()
		}
	}
}

func (s *persistentSession) readExitCode(ctx context.Context, afterIdx int) (int, error) {
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	for {
		s.bufferGate.Lock()
		lenTail := len(s.stdoutBuf) - afterIdx
		var tail []byte
		if lenTail > 0 {
			tail = snapshotRange(s.stdoutBuf, afterIdx, lenTail)
		}
		signal := s.stdoutSignal
		s.bufferGate.Unlock()

		if nl := bytes.IndexByte(tail, '\n'); nl >= 0 {
			return parseRc(tail, nl), nil
		}

		select {
		case <-signal:
		case <-time.After(100 * time.Millisecond):
		case <-deadline.C:
			return exitCodeUnstarted, nil
		case <-ctx.Done():
			return exitCodeUnstarted, ctx.Err()
		}
	}
}

func parseRc(tail []byte, newlineIdx int) int {
	if newlineIdx == 0 || len(tail) == 0 || tail[0] != '_' {
		return exitCodeUnstarted
	}
	end := newlineIdx
	if cr := bytes.IndexByte(tail[:newlineIdx], '\r'); cr >= 0 {
		end = cr
	}
	rc, err := strconv.Atoi(string(tail[1:end]))
	if err != nil {
		return exitCodeUnstarted
	}
	return rc
}

func (s *persistentSession) resultFromBuffers(ctx context.Context, stdoutOffset, sentinelIdx, stderrOffset int, exitCode int, timedOut bool, duration time.Duration, maxBytes int) Result {
	timer := time.NewTimer(stderrQuiescence)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}

	s.bufferGate.Lock()
	stdoutEnd := sentinelIdx
	if stdoutEnd < stdoutOffset || stdoutEnd > len(s.stdoutBuf) {
		stdoutEnd = len(s.stdoutBuf)
	}
	stdoutRaw := snapshotRange(s.stdoutBuf, stdoutOffset, stdoutEnd-stdoutOffset)
	stderrRaw := snapshotRange(s.stderrBuf, stderrOffset, len(s.stderrBuf)-stderrOffset)
	s.bufferGate.Unlock()

	stdout := strings.TrimRight(string(stdoutRaw), "\r\n")
	stderr := string(stderrRaw)
	stdout, stdoutTruncated := truncateHeadTail(stdout, maxBytes)
	stderr, stderrTruncated := truncateHeadTail(stderr, maxBytes)

	return Result{
		Stdout:    stdout,
		Stderr:    stderr,
		ExitCode:  exitCode,
		Duration:  duration,
		Truncated: stdoutTruncated || stderrTruncated,
		TimedOut:  timedOut,
	}
}

func exitCodeForClosedSession(timedOut bool) int {
	if timedOut {
		return exitCodeTimedOut
	}
	return exitCodeUnstarted
}

func (s *persistentSession) buildScript(command string, sentinel string) string {
	effective := s.maybeReanchor(command)
	if s.kind == shellKindPowerShell {
		encoded := base64.StdEncoding.EncodeToString([]byte(effective))
		return "& {" +
			" $__af_rc = 0;" +
			" try {" +
			"   $__af_cmd = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + encoded + "'));" +
			"   Invoke-Expression $__af_cmd 2>&1 | ForEach-Object {" +
			"     if ($_ -is [System.Management.Automation.ErrorRecord]) {" +
			"       [Console]::Error.WriteLine(($_ | Out-String).TrimEnd());" +
			"     } else {" +
			"       [Console]::WriteLine(($_ | Out-String).TrimEnd());" +
			"     }" +
			"   };" +
			"   [Console]::Out.Flush();" +
			"   if ($LASTEXITCODE -ne $null) { $__af_rc = $LASTEXITCODE }" +
			"   elseif (-not $?) { $__af_rc = 1 }" +
			" } catch {" +
			"   [Console]::Error.WriteLine($_.ToString());" +
			"   $__af_rc = 1" +
			" } finally {" +
			"   [Console]::WriteLine('" + sentinel + "_' + $__af_rc);" +
			"   [Console]::Out.Flush()" +
			" }" +
			" }\n"
	}

	return "{ " + effective + "\n" +
		"}; __af_rc=$?; set +e; " +
		"printf '\\n" + sentinel + "_%s\\n' \"$__af_rc\"\n"
}

func (s *persistentSession) maybeReanchor(command string) string {
	if !s.confineWorkingDirectory || s.workingDirectory == "" {
		return command
	}
	if s.kind == shellKindPowerShell {
		return "Set-Location -LiteralPath " + quotePowerShell(s.workingDirectory) + "\n" + command
	}
	return "cd -- " + quotePosix(s.workingDirectory) + "\n" + command
}

func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quotePosix(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *persistentSession) interruptCurrentCommand() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if s.kind == shellKindPowerShell {
		_, _ = io.WriteString(s.stdin, "\x03")
		return
	}
	_ = interruptSessionProcess(s.cmd)
}

func (s *persistentSession) readLoop(stream io.Reader, buf *[]byte, isStdout bool) {
	defer s.readerWG.Done()
	chunk := make([]byte, sessionReadChunk)
	for {
		n, err := stream.Read(chunk)
		if n > 0 {
			s.bufferGate.Lock()
			*buf = append(*buf, chunk[:n]...)
			if isStdout {
				prev := s.stdoutSignal
				s.stdoutSignal = newSignal()
				close(prev)
			}
			s.bufferGate.Unlock()
		}
		if err != nil {
			break
		}
	}
	if isStdout {
		s.bufferGate.Lock()
		s.stdoutClosed = true
		prev := s.stdoutSignal
		s.stdoutSignal = newSignal()
		close(prev)
		s.bufferGate.Unlock()
	}
}

func snapshotRange(buf []byte, start, length int) []byte {
	if length <= 0 || start >= len(buf) {
		return nil
	}
	if start < 0 {
		length += start
		start = 0
	}
	end := min(start+length, len(buf))
	if end <= start {
		return nil
	}
	result := make([]byte, end-start)
	copy(result, buf[start:end])
	return result
}

func indexOf(buf []byte, needle []byte, from int) int {
	if len(needle) == 0 {
		return from
	}
	if from < 0 {
		from = 0
	}
	idx := bytes.Index(buf[from:], needle)
	if idx < 0 {
		return -1
	}
	return from + idx
}

func truncateHeadTail(data string, capBytes int) (string, bool) {
	if capBytes <= 0 || data == "" {
		return data, false
	}
	if len(data) <= capBytes {
		return data, false
	}

	headCap := capBytes / 2
	tailCap := capBytes - headCap
	head := takePrefixByBytes(data, headCap)
	tail := takeSuffixByBytes(data, tailCap)
	dropped := max(len(data)-len(head)-len(tail), 0)
	return fmt.Sprintf("%s\n[... truncated %d bytes ...]\n%s", head, dropped, tail), true
}

func takePrefixByBytes(data string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var byteCount, end int
	for end < len(data) {
		_, n := utf8.DecodeRuneInString(data[end:])
		if byteCount+n > maxBytes {
			break
		}
		byteCount += n
		end += n
	}
	return data[:end]
}

func takeSuffixByBytes(data string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	totalBytes := len(data)
	if totalBytes <= maxBytes {
		return data
	}
	bytesToSkip := totalBytes - maxBytes
	var skipped, start int
	for start < len(data) {
		_, n := utf8.DecodeRuneInString(data[start:])
		if skipped+n > bytesToSkip {
			break
		}
		skipped += n
		start += n
	}
	return data[start:]
}

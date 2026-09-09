// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPreserveEnvironmentValuesKeepsOnlyAllowlist(t *testing.T) {
	source := []string{
		"PATH=/bin",
		"HOME=/home/test",
		"AF_SHELL_PARENT_VAR=should-not-leak",
		"tmp=/tmp/test",
	}

	env := make(map[string]string)

	preserveEnvironmentValues(env, source)

	if got := env["PATH"]; got != "/bin" {
		t.Fatalf("PATH = %q, want /bin", got)
	}
	if got := env["HOME"]; got != "/home/test" {
		t.Fatalf("HOME = %q, want /home/test", got)
	}
	if got := env["TMP"]; got != "/tmp/test" {
		t.Fatalf("TMP = %q, want /tmp/test", got)
	}
	if _, ok := env["AF_SHELL_PARENT_VAR"]; ok {
		t.Fatal("unexpected non-preserved variable copied")
	}
}

func TestHeadTailBuffer_MultiByteUTF8AtCapPreservesOrder(t *testing.T) {
	t.Parallel()

	const input = "aaaaaaa🔥🔥🔥\n"

	buf := &headTailBuffer{cap: 20}
	if _, err := buf.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}

	if buf.truncated {
		t.Fatalf("truncated = true, want false")
	}
	if got := buf.String(); got != input {
		t.Fatalf("String() = %q, want %q", got, input)
	}
}

func TestHeadTailBuffer_MultiByteUTF8OverflowPreservesHeadTailOrder(t *testing.T) {
	t.Parallel()

	buf := &headTailBuffer{cap: 20}
	if _, err := buf.Write([]byte("aaaaaaa🔥🔥🔥x\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := buf.String()
	if !buf.truncated {
		t.Fatalf("truncated = false, want true")
	}
	if !strings.HasPrefix(got, "aaaaaaa\n") {
		t.Fatalf("String() = %q, want prefix %q", got, "aaaaaaa\\n")
	}
	if !strings.Contains(got, "[... truncated 4 bytes ...]") {
		t.Fatalf("String() = %q, want truncated marker", got)
	}
	if !strings.HasSuffix(got, "🔥🔥x\n") {
		t.Fatalf("String() = %q, want suffix %q", got, "🔥🔥x\\n")
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("String() = %q, should not contain replacement rune", got)
	}
}

// scriptedReader delivers a fixed sequence of chunks, sleeping delay before
// each so successive stdout chunks land on distinct readLoop signals.
type scriptedReader struct {
	chunks [][]byte
	delay  time.Duration
	i      int
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}

// TestReadExitCodeWakesOnLiveStdoutSignal exercises the exit-code read path
// when the sentinel and the exit-code line arrive in two separate stdout
// reads. readExitCode must wake on the live stdout signal for the second read
// rather than waiting out the 100ms fallback poll.
func TestReadExitCodeWakesOnLiveStdoutSignal(t *testing.T) {
	s := &persistentSession{stdoutSignal: newSignal()}
	s.readerWG.Add(1)

	reader := &scriptedReader{
		chunks: [][]byte{
			[]byte("hello\nSENTINEL"),
			[]byte("_0\n"),
		},
		delay: 5 * time.Millisecond,
	}
	go s.readLoop(reader, &s.stdoutBuf, true)

	start := time.Now()
	idx, rc, timedOut, overflow, err := s.waitForSentinel(context.Background(), []byte("SENTINEL"), 0, 1<<20)
	elapsed := time.Since(start)
	s.readerWG.Wait()

	if err != nil {
		t.Fatalf("waitForSentinel: %v", err)
	}
	if timedOut || overflow {
		t.Fatalf("timedOut=%v overflow=%v, want both false", timedOut, overflow)
	}
	if idx < 0 {
		t.Fatalf("sentinel not found: idx=%d", idx)
	}
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	// The exit-code line arrives ~5ms after the sentinel on a live signal.
	// Before the fix readExitCode discarded that signal and only noticed the
	// line on the 100ms fallback poll, so elapsed would be ~100ms.
	if elapsed >= 80*time.Millisecond {
		t.Fatalf("readExitCode gated on 100ms poll: elapsed %v", elapsed)
	}
}

func TestResolvedShellArgvIncludesExtraArgv(t *testing.T) {
	shell := resolvedShell{
		binary:    "/custom/bash",
		kind:      shellKindBash,
		extraArgv: []string{"--login"},
	}

	if got, want := shell.statelessArgvForCommand("echo hi"), []string{"--login", "--noprofile", "--norc", "-c", "echo hi"}; !slices.Equal(got, want) {
		t.Fatalf("statelessArgvForCommand = %v, want %v", got, want)
	}

	got, err := shell.persistentArgv()
	if err != nil {
		t.Fatalf("persistentArgv: %v", err)
	}
	if want := []string{"/custom/bash", "--login", "--noprofile", "--norc"}; !slices.Equal(got, want) {
		t.Fatalf("persistentArgv = %v, want %v", got, want)
	}
}

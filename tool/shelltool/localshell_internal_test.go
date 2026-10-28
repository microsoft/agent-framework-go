// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

func TestHeadTailBuffer_BelowCapRoundTrips(t *testing.T) {
	buf := &headTailBuffer{cap: 1024}
	_, _ = buf.Write([]byte("hello\n"))
	_, _ = buf.Write([]byte("world\n"))
	if got := buf.String(); buf.truncated || got != "hello\nworld\n" {
		t.Fatalf("buffer = %q, truncated=%v", got, buf.truncated)
	}
}

func TestHeadTailBuffer_ManyLinesBoundedWithHeadAndTail(t *testing.T) {
	buf := &headTailBuffer{cap: 4096}
	for i := range 100_000 {
		_, _ = fmt.Fprintf(buf, "line %06d\n", i)
	}
	got := buf.String()
	if !buf.truncated || len(got) > 4096+128 {
		t.Fatalf("output size=%d, truncated=%v", len(got), buf.truncated)
	}
	for _, want := range []string{"line 000000", "[... truncated", "line 099999"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestHeadTailBuffer_HugeSingleLineStaysBounded(t *testing.T) {
	buf := &headTailBuffer{cap: 1024}
	chunk := strings.Repeat("x", 10_000) + "\n"
	for range 100 {
		_, _ = buf.Write([]byte(chunk))
	}
	if got := buf.String(); !buf.truncated || len(got) >= 4096 {
		t.Fatalf("output size=%d, truncated=%v", len(got), buf.truncated)
	}
}

func TestHeadTailBuffer_MultiByteRespectsBudget(t *testing.T) {
	buf := &headTailBuffer{cap: 32}
	for range 200 {
		_, _ = buf.Write([]byte("🔥🔥🔥🔥🔥\n"))
	}
	got := buf.String()
	if !buf.truncated || !utf8.ValidString(got) || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("output = %q, truncated=%v", got, buf.truncated)
	}
	if string([]rune(got)) != got {
		t.Fatalf("UTF-8 round trip changed %q", got)
	}
}

func TestHeadTailBuffer_ExactlyAtEvenCap(t *testing.T) {
	buf := &headTailBuffer{cap: 6}
	_, _ = buf.Write([]byte("ABCDE\n"))
	if got := buf.String(); buf.truncated || got != "ABCDE\n" {
		t.Fatalf("output = %q, truncated=%v", got, buf.truncated)
	}
}

func TestHeadTailBuffer_ExactlyAtOddCap(t *testing.T) {
	buf := &headTailBuffer{cap: 5}
	_, _ = buf.Write([]byte("ABCD\n"))
	if got := buf.String(); buf.truncated || got != "ABCD\n" {
		t.Fatalf("output = %q, truncated=%v", got, buf.truncated)
	}
}

func TestShellResolver_ShStatelessOmitsBashFlags(t *testing.T) {
	for _, binary := range []string{"/bin/sh", "/bin/dash", "/bin/ash", "/usr/bin/busybox", "/usr/bin/zsh", "/bin/ksh"} {
		t.Run(binary, func(t *testing.T) {
			shell, err := (LocalConfig{ShellArgv: []string{binary}}).resolvedShell()
			if err != nil {
				t.Fatal(err)
			}
			got := shell.statelessArgvForCommand("echo hi")
			if !slices.Equal(got, []string{"-c", "echo hi"}) || slices.Contains(got, "--noprofile") || slices.Contains(got, "--norc") {
				t.Fatalf("stateless argv = %v", got)
			}
		})
	}
}

func TestShellResolver_ShPersistentOmitsBashFlags(t *testing.T) {
	for _, binary := range []string{"/bin/sh", "/bin/dash", "/bin/ash", "/usr/bin/busybox", "/usr/bin/zsh", "/bin/ksh"} {
		t.Run(binary, func(t *testing.T) {
			shell, err := (LocalConfig{ShellArgv: []string{binary}}).resolvedShell()
			if err != nil {
				t.Fatal(err)
			}
			got, err := shell.persistentArgv()
			if err != nil || !slices.Equal(got, []string{binary}) {
				t.Fatalf("persistent argv = %v, %v", got, err)
			}
		})
	}
}

func TestShellResolver_BashStatelessIncludesBashFlags(t *testing.T) {
	for _, binary := range []string{"/bin/bash", "/usr/local/bin/bash"} {
		t.Run(binary, func(t *testing.T) {
			shell, err := (LocalConfig{ShellArgv: []string{binary}}).resolvedShell()
			if err != nil {
				t.Fatal(err)
			}
			if got := shell.statelessArgvForCommand("echo hi"); !slices.Equal(got, []string{"--noprofile", "--norc", "-c", "echo hi"}) {
				t.Fatalf("stateless argv = %v", got)
			}
		})
	}
}

func TestShellResolver_BashPersistentIncludesBashFlags(t *testing.T) {
	for _, binary := range []string{"/bin/bash", "/usr/local/bin/bash"} {
		t.Run(binary, func(t *testing.T) {
			shell, err := (LocalConfig{ShellArgv: []string{binary}}).resolvedShell()
			if err != nil {
				t.Fatal(err)
			}

			got, err := shell.persistentArgv()
			if err != nil || !slices.Equal(got, []string{binary, "--noprofile", "--norc"}) {
				t.Fatalf("persistent argv = %v, %v", got, err)
			}
		})
	}
}

func TestQuotePosix_PlainPath(t *testing.T) {
	if got := quotePosix("/tmp/work"); got != "'/tmp/work'" {
		t.Fatalf("quoted path = %q", got)
	}
}

func TestQuotePosix_ExpansionsAreLiteral(t *testing.T) {
	for _, path := range []string{"/tmp/$(touch /pwn)", "/tmp/$VAR", "/tmp/`id`"} {
		if got := quotePosix(path); got != "'"+path+"'" {
			t.Errorf("quotePosix(%q) = %q", path, got)
		}
	}
}

func TestQuotePosix_EmbeddedSingleQuote(t *testing.T) {
	if got := quotePosix("a'b"); got != "'a'\\''b'" {
		t.Fatalf("quoted string = %q", got)
	}
}

func TestQuotePowerShell_ExpansionsAreLiteral(t *testing.T) {
	for _, path := range []string{`C:\$(throw)`, `C:\$env:PATH`} {
		if got := quotePowerShell(path); got != "'"+path+"'" {
			t.Errorf("quotePowerShell(%q) = %q", path, got)
		}
	}
}

func TestQuotePowerShell_EmbeddedSingleQuote(t *testing.T) {
	if got := quotePowerShell("a'b"); got != "'a''b'" {
		t.Fatalf("quoted string = %q", got)
	}
}

func TestTruncateHeadTail_UnderCap(t *testing.T) {
	if got, truncated := truncateHeadTail("short", 1024); got != "short" || truncated {
		t.Fatalf("truncation = %q, %v", got, truncated)
	}
}

func TestTruncateHeadTail_ExactlyAtCap(t *testing.T) {
	input := strings.Repeat("x", 100)
	if got, truncated := truncateHeadTail(input, 100); got != input || truncated {
		t.Fatalf("truncation = %q, %v", got, truncated)
	}
}

func TestTruncateHeadTail_OverCap(t *testing.T) {
	input := "HEAD" + strings.Repeat("x", 1000) + "TAIL"
	got, truncated := truncateHeadTail(input, 20)
	if !truncated || len(got) >= len(input) {
		t.Fatalf("truncation length=%d, truncated=%v", len(got), truncated)
	}
	for _, want := range []string{"[... truncated", "HEAD", "TAIL"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
}

func TestTruncateHeadTail_Empty(t *testing.T) {
	if got, truncated := truncateHeadTail("", 10); got != "" || truncated {
		t.Fatalf("truncation = %q, %v", got, truncated)
	}
}

func TestTruncateHeadTail_MultiByteRuneBoundaries(t *testing.T) {
	input := strings.Repeat("🔥", 50)
	if len(input) != 200 {
		t.Fatal("invalid fixture")
	}
	got, truncated := truncateHeadTail(input, 40)
	if !truncated || !utf8.ValidString(got) || string([]rune(got)) != got {
		t.Fatalf("truncation = %q, %v", got, truncated)
	}
	first, last := strings.IndexByte(got, '\n'), strings.LastIndexByte(got, '\n')
	if first < 0 || last <= first {
		t.Fatalf("missing truncation marker: %q", got)
	}
	preserved := strings.ReplaceAll(got[:first]+got[last:], "\n", "")
	if len(preserved) > 40 {
		t.Fatalf("retained bytes=%d, want <=40", len(preserved))
	}
}

func TestTruncateHeadTail_NonASCIIBoundary(t *testing.T) {
	got, _ := truncateHeadTail("AAAA🔥BBBBCCCC🔥DDDD", 8)
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("replacement rune in %q", got)
	}
}
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
	// Use virtual time: a busy CI host can otherwise exceed the wall-clock
	// bound even when readExitCode correctly wakes on the live signal.
	synctest.Test(t, func(t *testing.T) {
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
		idx, rc, timedOut, overflow, err := s.waitForSentinel(t.Context(), []byte("SENTINEL"), 0, 1<<20)
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
		// The exit-code line arrives 5ms after the sentinel on a live signal.
		// Before the fix readExitCode discarded that signal and only noticed the
		// line on the 100ms fallback poll, so elapsed would be about 100ms.
		if elapsed >= 80*time.Millisecond {
			t.Fatalf("readExitCode gated on 100ms poll: elapsed %v", elapsed)
		}
	})
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

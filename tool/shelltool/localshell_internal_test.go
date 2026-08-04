// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"slices"
	"strings"
	"testing"
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

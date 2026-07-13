// Copyright (c) Microsoft. All rights reserved.

package shelltool

import (
	"slices"
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

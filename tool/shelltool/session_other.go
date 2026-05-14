// Copyright (c) Microsoft. All rights reserved.

//go:build !unix && !windows

package shelltool

import (
	"os/exec"
	"syscall"
)

func newSessionSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func newProcessTreeSysProcAttr() *syscall.SysProcAttr {
	return nil
}

type processTree struct{}

func startProcessTree(cmd *exec.Cmd) (*processTree, error) {
	return &processTree{}, cmd.Start()
}

func closeProcessTree(_ *processTree) {}

func interruptSessionProcess(_ *exec.Cmd) error {
	return nil
}

func killProcessTree(cmd *exec.Cmd, _ *processTree) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

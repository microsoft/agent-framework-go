// Copyright (c) Microsoft. All rights reserved.

//go:build unix

package shelltool

import (
	"os/exec"
	"syscall"
)

func newSessionSysProcAttr() *syscall.SysProcAttr {
	return newProcessTreeSysProcAttr()
}

func newProcessTreeSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

type processTree struct{}

func startProcessTree(cmd *exec.Cmd) (*processTree, error) {
	return &processTree{}, cmd.Start()
}

func closeProcessTree(_ *processTree) {}

func interruptSessionProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

func killProcessTree(cmd *exec.Cmd, _ *processTree) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

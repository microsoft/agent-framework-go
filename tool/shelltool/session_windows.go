// Copyright (c) Microsoft. All rights reserved.

//go:build windows

package shelltool

import (
	"os/exec"
	"syscall"
)

const (
	processSetQuota  = 0x0100
	processTerminate = 0x0001
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

func newSessionSysProcAttr() *syscall.SysProcAttr {
	return nil
}

func newProcessTreeSysProcAttr() *syscall.SysProcAttr {
	return nil
}

type processTree struct {
	job      syscall.Handle
	assigned bool
}

func startProcessTree(cmd *exec.Cmd) (*processTree, error) {
	var tree processTree
	if job, err := createJobObject(); err == nil {
		tree.job = job
	}
	if err := cmd.Start(); err != nil {
		closeProcessTree(&tree)
		return nil, err
	}
	if tree.job != 0 {
		tree.assigned = assignProcessToJob(tree.job, uint32(cmd.Process.Pid)) == nil
	}
	return &tree, nil
}

func closeProcessTree(tree *processTree) {
	if tree == nil || tree.job == 0 {
		return
	}
	_ = syscall.CloseHandle(tree.job)
	tree.job = 0
	tree.assigned = false
}

func interruptSessionProcess(_ *exec.Cmd) error {
	return nil
}

func killProcessTree(cmd *exec.Cmd, tree *processTree) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if tree != nil && tree.job != 0 && tree.assigned {
		result, _, _ := procTerminateJobObject.Call(uintptr(tree.job), 1)
		if result != 0 {
			return
		}
	}
	_ = cmd.Process.Kill()
}

func createJobObject() (syscall.Handle, error) {
	handle, _, err := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		if err != syscall.Errno(0) {
			return 0, err
		}
		return 0, syscall.EINVAL
	}
	return syscall.Handle(handle), nil
}

func assignProcessToJob(job syscall.Handle, pid uint32) error {
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, pid)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	result, _, callErr := procAssignProcessToJobObject.Call(uintptr(job), uintptr(process))
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EINVAL
	}
	return nil
}

// Copyright (c) Microsoft. All rights reserved.

//go:build unix

package shelltool

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
	// Linux exposes each task's direct children through /proc. Prefer sending
	// SIGINT to the shell's descendants so a timed-out persistent command can be
	// interrupted without also signaling the long-lived shell that owns session
	// state such as variables, functions, and the current directory. If process
	// discovery finds no descendants, fall back to process-group SIGINT below.
	if runtime.GOOS == "linux" {
		if descendants := linuxDescendantPIDs(cmd.Process.Pid); len(descendants) > 0 {
			var firstErr error
			for _, pid := range descendants {
				if err := syscall.Kill(pid, syscall.SIGINT); err != nil && err != syscall.ESRCH && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		}
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

// linuxDescendantPIDs returns descendant PIDs in post-order so deeper children
// are signaled before their parents. It relies on Linux's procfs
// /proc/<pid>/task/<tid>/children interface and must not be used on other Unix
// platforms.
func linuxDescendantPIDs(parentPID int) []int {
	seen := map[int]bool{parentPID: true}
	var descendants []int
	collectLinuxDescendantPIDs(parentPID, seen, &descendants)
	return descendants
}

func collectLinuxDescendantPIDs(parentPID int, seen map[int]bool, descendants *[]int) {
	for _, childPID := range linuxChildPIDs(parentPID) {
		if childPID <= 0 || seen[childPID] {
			continue
		}
		seen[childPID] = true
		collectLinuxDescendantPIDs(childPID, seen, descendants)
		*descendants = append(*descendants, childPID)
	}
}

func linuxChildPIDs(parentPID int) []int {
	entries, err := os.ReadDir("/proc/" + strconv.Itoa(parentPID) + "/task")
	if err != nil {
		return nil
	}

	var children []int
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(parentPID), "task", entry.Name(), "children"))
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(data)) {
			childPID, err := strconv.Atoi(field)
			if err == nil {
				children = append(children, childPID)
			}
		}
	}
	return children
}

func killProcessTree(cmd *exec.Cmd, _ *processTree) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

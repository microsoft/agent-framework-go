// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

func runExample(ctx context.Context, projectPath string, timeout time.Duration, build bool, inputs []*string, inputDelay time.Duration) ExampleRunResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"run"}
	if !build {
		args = append(args, "-mod=readonly")
	}
	args = append(args, ".")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = projectPath

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	var stdin io.WriteCloser
	var err error
	if len(inputs) > 0 {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return ExampleRunResult{Stderr: err.Error(), ExitCode: -1}
		}
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ExampleRunResult{Stderr: err.Error(), ExitCode: -1}
	}

	if stdin != nil {
		go feedInputs(ctx, stdin, inputs, inputDelay)
	}

	err = cmd.Wait()
	elapsed := time.Since(start)
	if ctx.Err() == context.DeadlineExceeded {
		return ExampleRunResult{
			Stdout:   stdout.String(),
			Stderr:   fmt.Sprintf("TIMEOUT: Example did not complete within %.0fs.\n%s", timeout.Seconds(), stderr.String()),
			ExitCode: -1,
			Elapsed:  elapsed,
		}
	}

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			if stderr.Len() > 0 {
				stderr.WriteByte('\n')
			}
			stderr.WriteString(err.Error())
		}
	}

	return ExampleRunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Elapsed:  elapsed,
	}
}

func feedInputs(ctx context.Context, stdin io.WriteCloser, inputs []*string, inputDelay time.Duration) {
	defer func() {
		_ = stdin.Close()
	}()
	for _, input := range inputs {
		select {
		case <-ctx.Done():
			return
		case <-time.After(inputDelay):
		}
		if input == nil {
			continue
		}
		_, _ = fmt.Fprintln(stdin, *input)
	}
}

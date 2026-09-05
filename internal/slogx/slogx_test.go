// Copyright (c) Microsoft. All rights reserved.

package slogx_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/internal/slogx"
)

func TestSensitiveDataEnabledRendersRawValue(t *testing.T) {
	var buf bytes.Buffer
	logger := &slogx.Logger{
		Logger:        slog.New(slog.NewTextHandler(&buf, nil)),
		SensitiveData: true,
	}

	logger.Info(context.Background(), "call", slogx.SensitiveData("arguments", "secret-payload"))

	out := buf.String()
	if !strings.Contains(out, "secret-payload") {
		t.Fatalf("expected raw sensitive value in output, got: %q", out)
	}
	if strings.Contains(out, "any:") {
		t.Fatalf("expected wrapper struct to be unwrapped, but output contains wrapper: %q", out)
	}
}

func TestSensitiveDataDisabledRedactsAttr(t *testing.T) {
	var buf bytes.Buffer
	logger := &slogx.Logger{
		Logger:        slog.New(slog.NewTextHandler(&buf, nil)),
		SensitiveData: false,
	}

	logger.Info(context.Background(), "call", slogx.SensitiveData("arguments", "secret-payload"))

	out := buf.String()
	if strings.Contains(out, "secret-payload") {
		t.Fatalf("expected sensitive value to be redacted, got: %q", out)
	}
	if strings.Contains(out, "arguments") {
		t.Fatalf("expected sensitive attr key to be dropped, got: %q", out)
	}
}

// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/messagetest"
	"github.com/microsoft/agent-framework-go/message"
)

func marshal(v any) (string, error) {
	marshaled, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return "", err
	}
	return string(marshaled), nil
}

func MessagesEqual(expected, actual []*message.Message) error {
	return equal(expected, actual)
}

func AgentRunResponseUpdatesEqual(expected, actual []*agent.ResponseUpdate) error {
	return equal(expected, actual)
}

func ResponseUpdatesEqual(got, want []*agent.ResponseUpdate) error {
	var errs []error
	if len(want) != len(got) {
		errs = append(errs, fmt.Errorf("response update count mismatch: expected %d, got %d", len(want), len(got)))
	}
	for i := range want {
		if i >= len(got) {
			break
		}
		if err := ResponseUpdateEqual(got[i], want[i]); err != nil {
			errs = append(errs, fmt.Errorf("response update %d mismatch: %v", i, err))
		}
	}
	return errors.Join(errs...)
}

func ResponseUpdateEqual(got, want *agent.ResponseUpdate) error {
	var errs []error
	if want.MessageID != got.MessageID {
		errs = append(errs, fmt.Errorf("message ID mismatch: expected %s, got %s", want.MessageID, got.MessageID))
	}
	if want.ResponseID != got.ResponseID {
		errs = append(errs, fmt.Errorf("response ID mismatch: expected %s, got %s", want.ResponseID, got.ResponseID))
	}
	if want.FinishReason != got.FinishReason {
		errs = append(errs, fmt.Errorf("finish reason mismatch: expected %s, got %s", want.FinishReason, got.FinishReason))
	}
	if want.Role != got.Role {
		errs = append(errs, fmt.Errorf("role mismatch: expected %s, got %s", want.Role, got.Role))
	}
	if want.CreatedAt != got.CreatedAt {
		errs = append(errs, fmt.Errorf("created at mismatch: expected %v, got %v", want.CreatedAt, got.CreatedAt))
	}
	if want.String() != got.String() {
		errs = append(errs, fmt.Errorf("string representation mismatch:\nexpected: %q\ngot:      %q", want.String(), got.String()))
	}
	errs = append(errs, messagetest.ContentsEqual(got.Contents, want.Contents))
	return errors.Join(errs...)
}

func equal[T any](expected, actual []T) error {
	var diffs []string

	minLen := min(len(actual), len(expected))

	// Compare messages that exist in both slices
	for i := range minLen {
		estr, err := marshal(expected[i])
		if err != nil {
			return fmt.Errorf("failed to marshal at index %d: %w", i, err)
		}
		astr, err := marshal(actual[i])
		if err != nil {
			return fmt.Errorf("failed to marshal at index %d: %w", i, err)
		}
		if estr != astr {
			diffs = append(diffs, fmt.Sprintf("index %d differs:\nexpected: %s\nactual:   %s", i, estr, astr))
		}
	}

	// Report missing messages from actual
	if len(expected) > len(actual) {
		for i := len(actual); i < len(expected); i++ {
			estr, err := marshal(expected[i])
			if err != nil {
				return fmt.Errorf("failed to marshal at index %d: %w", i, err)
			}
			diffs = append(diffs, fmt.Sprintf("index %d missing from actual:\nexpected: %s", i, estr))
		}
	}

	// Report extra messages in actual
	if len(actual) > len(expected) {
		for i := len(expected); i < len(actual); i++ {
			astr, err := marshal(actual[i])
			if err != nil {
				return fmt.Errorf("failed to marshal at index %d: %w", i, err)
			}
			diffs = append(diffs, fmt.Sprintf("index %d unexpected in actual:\nactual:   %s", i, astr))
		}
	}

	if len(diffs) > 0 {
		return fmt.Errorf("slices not equal:\n%s", strings.Join(diffs, "\n\n"))
	}
	return nil
}

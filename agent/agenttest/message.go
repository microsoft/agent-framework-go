// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
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

func AgentRunResponseUpdatesEqual(expected, actual []*agent.RunResponseUpdate) error {
	return equal(expected, actual)
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

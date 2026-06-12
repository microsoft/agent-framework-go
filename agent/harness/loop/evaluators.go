// Copyright (c) Microsoft. All rights reserved.

package loop

import (
	"context"
	"errors"
	"strings"
)

// CompletionMarkerOptions configures a completion-marker evaluator.
type CompletionMarkerOptions struct {
	// FeedbackMessageTemplate is used when the marker is absent. The
	// CompletionMarkerPlaceholder token is replaced when the evaluator is
	// created, and LastResponsePlaceholder is replaced on each evaluation.
	FeedbackMessageTemplate string
}

// CompletionMarkerEvaluator stops the loop once a marker appears in the latest
// response text, otherwise it asks the agent to continue.
type CompletionMarkerEvaluator struct {
	completionMarker        string
	feedbackMessageTemplate string
}

// NewCompletionMarkerEvaluator creates an evaluator that waits for marker in
// the latest response text.
func NewCompletionMarkerEvaluator(marker string, opts *CompletionMarkerOptions) (*CompletionMarkerEvaluator, error) {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return nil, errors.New("loop: completion marker cannot be empty")
	}
	template := DefaultCompletionMarkerFeedbackTemplate
	if opts != nil && opts.FeedbackMessageTemplate != "" {
		template = opts.FeedbackMessageTemplate
	}
	template = strings.ReplaceAll(template, CompletionMarkerPlaceholder, marker)
	return &CompletionMarkerEvaluator{
		completionMarker:        marker,
		feedbackMessageTemplate: template,
	}, nil
}

// MustCompletionMarkerEvaluator is like NewCompletionMarkerEvaluator but panics
// on invalid input.
func MustCompletionMarkerEvaluator(marker string, opts *CompletionMarkerOptions) *CompletionMarkerEvaluator {
	evaluator, err := NewCompletionMarkerEvaluator(marker, opts)
	if err != nil {
		panic(err)
	}
	return evaluator
}

// Evaluate implements Evaluator.
func (e *CompletionMarkerEvaluator) Evaluate(_ context.Context, loop *Context) (Evaluation, error) {
	if loop == nil {
		return Stop(), errors.New("loop: context cannot be nil")
	}
	if loop.LastResponse == nil {
		return Stop(), errors.New("loop: last response cannot be nil")
	}
	if strings.Contains(loop.LastResponse.String(), e.completionMarker) {
		return Stop(), nil
	}
	feedback := strings.ReplaceAll(e.feedbackMessageTemplate, LastResponsePlaceholder, loop.LastResponse.String())
	return Continue(feedback), nil
}

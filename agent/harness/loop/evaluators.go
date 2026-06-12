// Copyright (c) Microsoft. All rights reserved.

package loop

import (
	"context"
	"errors"
	"strings"
)

// CompletionMarkerConfig configures a completion-marker evaluator.
type CompletionMarkerConfig struct {
	// Marker is the completion marker that stops the loop when present in the
	// latest response text.
	Marker string

	// FeedbackMessageTemplate is used when the marker is absent. The
	// completionMarkerPlaceholder token is replaced when the evaluator is
	// created, and lastResponsePlaceholder is replaced on each evaluation.
	FeedbackMessageTemplate string
}

// CompletionMarkerEvaluator stops the loop once a marker appears in the latest
// response text, otherwise it asks the agent to continue.
type CompletionMarkerEvaluator struct {
	completionMarker        string
	feedbackMessageTemplate string
}

// NewCompletionMarkerEvaluator creates an evaluator that waits for the
// configured marker in the latest response text.
func NewCompletionMarkerEvaluator(config CompletionMarkerConfig) *CompletionMarkerEvaluator {
	marker := strings.TrimSpace(config.Marker)
	if marker == "" {
		panic("loop: completion marker cannot be empty")
	}
	template := defaultCompletionMarkerFeedbackTemplate
	if config.FeedbackMessageTemplate != "" {
		template = config.FeedbackMessageTemplate
	}
	template = strings.ReplaceAll(template, completionMarkerPlaceholder, marker)
	return &CompletionMarkerEvaluator{
		completionMarker:        marker,
		feedbackMessageTemplate: template,
	}
}

// Evaluate implements Evaluator.
func (e *CompletionMarkerEvaluator) Evaluate(_ context.Context, loop *Context) (Evaluation, error) {
	if loop == nil {
		return Stop(), errors.New("loop: context cannot be nil")
	}
	if loop.LastResponse == nil {
		return Stop(), errors.New("loop: last response cannot be nil")
	}
	responseText := loop.LastResponse.String()
	if strings.Contains(responseText, e.completionMarker) {
		return Stop(), nil
	}
	feedback := strings.ReplaceAll(e.feedbackMessageTemplate, lastResponsePlaceholder, responseText)
	return Continue(feedback), nil
}
